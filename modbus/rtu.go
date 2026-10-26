package modbus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// Over a serial line, an RTU frame is the unit's address, the PDU, and a CRC of both, low byte first:
//
//	address  1 byte   the device, from 1 to 247, or 0 to broadcast a write to every device
//	PDU      up to 253 bytes
//	CRC      2 bytes
//
// Nothing in a frame says how long it is. The specification delimits frames by silence: 3.5 characters of it before a
// new frame (t3.5), at most 1.5 between two characters of one (t1.5). Timing a gap that short is unreliable from user
// space, and USB adapters deliver bytes in bursts every few milliseconds anyway. So this package delimits frames by
// length instead, which the function code and, for variable lengths, a byte count give within the first bytes of
// every frame it defines, and keeps silence for what it is reliable for: waiting t3.5 before sending, and telling
// that the rest of a broken frame has passed.
const (
	// rtuMaxFrame is the largest frame: the address, a full PDU and the CRC.
	rtuMaxFrame = 256

	// rtuCharBits is how many bits a character takes on the line: a start bit, 8 data bits, a parity bit or a second
	// stop bit, and a stop bit.
	rtuCharBits = 11

	// rtuFastBaudRate is the rate above which the specification fixes t3.5 at rtuFastSilence, rather than letting it
	// shrink with the rate.
	rtuFastBaudRate = 19200
	rtuFastSilence  = 1750 * time.Microsecond

	// rtuOverTCPQuiet is how long a line without timing, such as RTU over TCP, must stay quiet for a broken frame to
	// have passed: network latency, not characters, sets it.
	rtuOverTCPQuiet = 20 * time.Millisecond

	// rtuFrameSlack is added to the time a full frame takes on the line, to bound how long the rest of a frame may
	// take once its first byte came: adapters that buffer, such as USB ones, deliver bytes late.
	rtuFrameSlack = 100 * time.Millisecond

	// rtuDrainLimit bounds a drain, should the line never go quiet.
	rtuDrainLimit = time.Second

	// rtuMinPoll and rtuMaxPoll bound how long a read waits between reads that return nothing, on a port without
	// deadlines: a character's time, so that a byte waits little in the port's buffer.
	rtuMinPoll = 100 * time.Microsecond
	rtuMaxPoll = time.Millisecond
)

var (
	// errBrokenFrame is a frame that cannot be read whole: its length is impossible, or its end never came.
	errBrokenFrame = errors.New("modbus: broken RTU frame")

	// errUnknownFunction is a frame for a function this package does not define, whose length it cannot tell.
	errUnknownFunction = errors.New("modbus: RTU frame for an unknown function")
)

// line is one end of a serial line: it reads and writes RTU frames on a port, honouring ctx and the line's timing.
//
// A port's Read either takes deadlines, as a net.Conn or an os.File opened on a tty do, and blocks until data comes,
// or returns at once, with 0 bytes when none came, as TinyGo's machine.UART does, or a serial port given a short read
// timeout: line then polls it. A Read that blocks without taking deadlines would hold a request past its ctx.
type line struct {
	port io.ReadWriter
	// deadlines is port, if its reads take deadlines, or else nil.
	deadlines readDeadliner

	// charTime is how long a character takes on the line, and silence is t3.5. Both are 0 without line timing.
	charTime time.Duration
	silence  time.Duration
	// quiet is how long the line must stay quiet for a broken frame to have passed, and frameTimeout how long the rest
	// of a frame may take once its first byte came.
	quiet        time.Duration
	frameTimeout time.Duration
	// poll is how long a read waits between reads that return nothing, on a port without deadlines.
	poll time.Duration

	// idleAt is when the line went idle: when the last byte read came, or when the last frame written has left.
	idleAt time.Time
}

func newLine(port io.ReadWriter, baudRate int) *line {
	l := &line{port: port, quiet: rtuOverTCPQuiet, poll: rtuMaxPoll}
	if d, ok := port.(readDeadliner); ok {
		l.deadlines = d
	}
	if baudRate > 0 {
		l.charTime = rtuCharBits * time.Second / time.Duration(baudRate)
		l.silence = 7 * l.charTime / 2
		if baudRate > rtuFastBaudRate {
			l.silence = rtuFastSilence
		}
		l.quiet = l.silence
		l.poll = min(max(l.charTime, rtuMinPoll), rtuMaxPoll)
	}
	l.frameTimeout = rtuMaxFrame*l.charTime + rtuFrameSlack

	return l
}

// write waits until the line has been silent for t3.5, since a device takes a frame starting earlier as part of the
// last one, then writes frame.
func (l *line) write(b bound, frame []byte) error {
	if wait := time.Until(l.idleAt.Add(l.silence)); wait > 0 {
		if err := sleep(b, wait); err != nil {
			return err
		}
	}
	if l.deadlines != nil {
		stop := watch(b, l.deadlines)
		defer stop()
	}
	if _, err := l.port.Write(frame); err != nil {
		return ioError(b, err)
	}
	// Write returns once the frame is in the port's buffer, before its last character has left: 8 bytes take 9 ms
	// more at 9600 baud.
	l.busyUntil(time.Now().Add(time.Duration(len(frame)) * l.charTime))

	return nil
}

// busyUntil records that the line carried a character until t, unless it is known to have carried one later: a
// response read at once, from a port that buffered it, must not make the line idle before the request has left.
func (l *line) busyUntil(t time.Time) {
	if t.After(l.idleAt) {
		l.idleAt = t
	}
}

// readFull reads len(p) bytes, or fails once b ends.
func (l *line) readFull(b bound, p []byte) error {
	if l.deadlines != nil {
		stop := watch(b, l.deadlines)
		defer stop()
		if _, err := io.ReadFull(l.port, p); err != nil {
			return ioError(b, err)
		}
		l.busyUntil(time.Now())

		return nil
	}

	for n := 0; n < len(p); {
		m, err := l.port.Read(p[n:])
		n += m
		if err != nil {
			return err
		}
		if m == 0 {
			if err := sleep(b, l.poll); err != nil {
				return err
			}
		}
	}
	l.busyUntil(time.Now())

	return nil
}

// readFrame reads the rest of a frame, which holds its first bytes, and returns it whole, CRC included. length works
// out the frame's length from its first bytes (responseLength, requestLength), or how many it needs to tell: readFrame
// reads that many, and asks again, until length returns len(frame).
func (l *line) readFrame(b bound, frame []byte, length func(frame []byte) int) ([]byte, error) {
	for {
		n := length(frame)
		switch {
		case n < 0:
			return frame, errUnknownFunction
		case n < len(frame) || n > rtuMaxFrame:
			return frame, fmt.Errorf("%w: % X", errBrokenFrame, frame)
		case n == len(frame):
			if !checkCRC(frame) {
				return frame, fmt.Errorf("%w: frame % X", ErrCRC, frame)
			}

			return frame, nil
		}
		m := len(frame)
		frame = append(frame, make([]byte, n-m)...)
		if err := l.readFull(b, frame[m:]); err != nil {
			return frame[:m], err
		}
	}
}

// drain reads until the line has been quiet for long enough that the rest of a broken frame, or a response given up
// on, has passed, and returns what it read. Else the next read would take it for the start of the next frame. It
// stops early once b ends, or after rtuDrainLimit.
func (l *line) drain(b bound) []byte {
	var (
		drained []byte
		buf     [rtuMaxFrame]byte
		start   = time.Now()
		last    = start
	)
	for b.err() == nil && time.Since(start) < rtuDrainLimit {
		if l.deadlines != nil {
			// Each Read fails after quiet without a byte, which ends the drain.
			_ = l.deadlines.SetReadDeadline(time.Now().Add(l.quiet))
		}
		n, err := l.port.Read(buf[:])
		drained = append(drained, buf[:n]...)
		switch {
		case n > 0:
			last = time.Now()
		case err != nil || time.Since(last) >= l.quiet:
			l.busyUntil(last)

			return drained
		default:
			if sleep(b, l.poll) != nil {
				return drained
			}
		}
		if err != nil {
			return drained
		}
	}

	return drained
}

// responseLength returns the length of a response frame, CRC included, from its first 3 bytes, or -1 for a function
// this package does not define.
func responseLength(frame []byte) int {
	function := FunctionCode(frame[1])
	if function&exceptionBit != 0 {
		// The address, the function, the exception code and the CRC.
		return 5
	}
	switch function {
	case ReadCoils, ReadDiscreteInputs, ReadHoldingRegisters, ReadInputRegisters, ReadWriteMultipleRegisters:
		// The address, the function, a byte count, that many bytes and the CRC.
		return 3 + int(frame[2]) + 2
	case WriteSingleCoil, WriteSingleRegister, WriteMultipleCoils, WriteMultipleRegisters:
		// The address, the function, the echoed address and value or quantity, and the CRC.
		return 8
	default:
		return -1
	}
}

// requestLength returns the length of a request frame, CRC included, from its first bytes, at least 2. When it needs
// more bytes to tell, it returns how many, more than len(frame). It returns -1 for a function this package does not
// define.
func requestLength(frame []byte) int {
	switch FunctionCode(frame[1]) {
	case ReadCoils, ReadDiscreteInputs, ReadHoldingRegisters, ReadInputRegisters, WriteSingleCoil, WriteSingleRegister:
		// The address, the function, two 16-bit fields and the CRC.
		return 8
	case WriteMultipleCoils, WriteMultipleRegisters:
		// The address, the function, the address and quantity, a byte count, that many bytes and the CRC.
		if len(frame) < 7 {
			return 7
		}

		return 7 + int(frame[6]) + 2
	case ReadWriteMultipleRegisters:
		// The same, after the read's address and quantity.
		if len(frame) < 11 {
			return 11
		}

		return 11 + int(frame[10]) + 2
	default:
		return -1
	}
}

// RTUTransport is a Transport over Modbus RTU: frames on a serial line, RS-485 or RS-232, through any port that reads
// and writes bytes. It also carries RTU over TCP, to a serial-to-Ethernet gateway that forwards frames as they are:
// give it a dialed net.Conn, and a baud rate of 0.
//
// The port's Read must either take deadlines, as a net.Conn or an os.File opened on a tty do, or return at once,
// with 0 bytes when none came, as TinyGo's machine.UART does, or a serial port given a short read timeout. A Read
// that blocks without taking deadlines holds a request past its ctx. The port's settings, such as the baud rate and
// the parity, are the caller's to set.
//
// It sends one request at a time, as a serial line carries one frame at a time.
type RTUTransport struct {
	line   *line
	config config

	// sem holds a token while a request is in flight. Waiting for it selects on ctx, which a mutex cannot.
	sem    chan struct{}
	closed atomic.Bool

	// dirty is set once a request fails: the line may still carry the rest of its response, which the next request
	// drains first. Only the holder of sem uses it.
	dirty bool
}

// NewRTUTransport returns an RTUTransport on port, whose line runs at baudRate, which sets the line's timing. A
// baudRate of 0 means a line without timing, such as RTU over TCP.
func NewRTUTransport(port io.ReadWriter, baudRate int, options ...Option) *RTUTransport {
	return &RTUTransport{
		line:   newLine(port, baudRate),
		config: newConfig(options),
		sem:    make(chan struct{}, 1),
	}
}

// Send sends request to unit, once every request before it is answered, and returns the response. If ctx is done, or
// WithTimeout's timeout runs out, before the response, it returns ctx's error.
//
// Unit 0 broadcasts a write to every device on the line, and no device answers: Send returns once the frame is
// written, with the response a device would have given. Devices may need a moment after a broadcast before they
// answer the next request. RTU frames only the functions this package defines.
func (t *RTUTransport) Send(ctx context.Context, unit uint8, request PDU) (PDU, error) {
	if err := checkRTURequest(unit, request); err != nil {
		return PDU{}, err
	}

	b := newBound(ctx, t.config.timeout)
	if err := acquire(t.sem, b); err != nil {
		return PDU{}, err
	}
	defer func() { <-t.sem }()

	if t.closed.Load() {
		return PDU{}, ErrClosed
	}
	if t.dirty {
		t.line.drain(b)
		t.dirty = false
	}
	response, err := t.roundTrip(b, unit, request)
	if err != nil {
		t.dirty = true

		return PDU{}, err
	}

	return response, nil
}

// Close closes the port if it is an io.Closer, which fails a request in flight, and fails every later Send with
// ErrClosed. Every Close after the first returns ErrClosed.
func (t *RTUTransport) Close() error {
	if t.closed.Swap(true) {
		return ErrClosed
	}
	if c, ok := t.line.port.(io.Closer); ok {
		return c.Close()
	}

	return nil
}

// roundTrip writes request and reads its response.
func (t *RTUTransport) roundTrip(b bound, unit uint8, request PDU) (PDU, error) {
	frame := appendCRC(append([]byte{unit, byte(request.Function)}, request.Data...))
	if err := t.line.write(b, frame); err != nil {
		return PDU{}, err
	}
	if unit == 0 {
		// A write's response echoes the first 4 bytes of its request, which checkRTURequest checked are there.
		return PDU{Function: request.Function, Data: request.Data[:4]}, nil
	}

	// 3 bytes, the shortest any response's length can be told from, and shorter than any response.
	response := make([]byte, 3, rtuMaxFrame)
	if err := t.line.readFull(b, response); err != nil {
		return PDU{}, err
	}
	response, err := t.line.readFrame(b, response, responseLength)
	if errors.Is(err, errBrokenFrame) || errors.Is(err, errUnknownFunction) {
		return PDU{}, invalidResponse(request.Function, "unexpected frame % X", response)
	}
	if err != nil {
		return PDU{}, err
	}
	if response[0] != unit {
		return PDU{}, invalidResponse(request.Function, "from unit %d, want %d", response[0], unit)
	}

	return PDU{Function: FunctionCode(response[1]), Data: response[2 : len(response)-2]}, nil
}

// checkRTURequest returns a *RequestError for a request RTU cannot frame: a function this package does not define,
// whose response's length it cannot tell, or a broadcast that is not a write.
func checkRTURequest(unit uint8, request PDU) error {
	if request.Function&exceptionBit != 0 || responseLength([]byte{unit, byte(request.Function), 0}) < 0 {
		return &RequestError{Function: request.Function, Reason: "RTU cannot frame this function"}
	}
	if len(request.Data) > maxPDU-1 {
		return &RequestError{Function: request.Function, Reason: fmt.Sprintf("PDU of %d bytes, past %d", len(request.Data)+1, maxPDU)}
	}
	// A write's data starts with the 4 bytes its response echoes, which roundTrip answers a broadcast with.
	if unit == 0 && (!isWrite(request.Function) || len(request.Data) < 4) {
		return &RequestError{Function: request.Function, Reason: "unit 0 broadcasts only writes, which no device answers"}
	}

	return nil
}

// isWrite reports whether f writes and reads nothing back, so that it can be broadcast.
func isWrite(f FunctionCode) bool {
	switch f {
	case WriteSingleCoil, WriteSingleRegister, WriteMultipleCoils, WriteMultipleRegisters:
		return true
	case ReadCoils, ReadDiscreteInputs, ReadHoldingRegisters, ReadInputRegisters, ReadWriteMultipleRegisters:
		return false
	default:
		return false
	}
}

// ServeRTU serves h over Modbus RTU on port as the device at unit, from 1 to 247, until ctx is done, then returns
// nil. It returns the port's error if a read or a write fails. port's Read must behave as RTUTransport requires, and
// baudRate is the line's, or 0 for a line without timing.
//
// It answers only the requests for unit, since other devices may share the line. It serves broadcasts, unit 0, without
// answering, as it does a request h answers ErrNoResponse, and drops a frame whose CRC does not match, as the
// specification requires. A frame for a function this package does not define is answered IllegalFunction once the
// line goes quiet, since its length is unknown.
func ServeRTU(ctx context.Context, port io.ReadWriter, baudRate int, unit uint8, h Handler) error {
	l := newLine(port, baudRate)
	server := newBound(ctx, 0)
	for {
		frame, err := readRequest(server, l)
		if err != nil {
			if ctx.Err() == nil && (errors.Is(err, errBrokenFrame) || errors.Is(err, ErrCRC)) {
				l.drain(server)

				continue
			}

			return stopped(ctx, err)
		}

		address, function := frame[0], FunctionCode(frame[1])
		if address != unit && (address != 0 || !isWrite(function)) {
			continue
		}
		response, ok := serve(ctx, h, address, PDU{Function: function, Data: frame[2 : len(frame)-2]})
		if address == 0 || !ok {
			continue
		}
		if err := l.write(server, appendCRC(append([]byte{address, byte(response.Function)}, response.Data...))); err != nil {
			return stopped(ctx, err)
		}
	}
}

// readRequest reads the next request frame on l. It waits as long as it takes for the first byte, then gives the rest
// frameTimeout: a frame cut short is an errBrokenFrame. A frame for a function requestLength cannot frame ends once
// the line goes quiet.
func readRequest(server bound, l *line) ([]byte, error) {
	frame := make([]byte, 1, rtuMaxFrame)
	if err := l.readFull(server, frame); err != nil {
		return nil, err
	}

	rest := newBound(server.ctx, l.frameTimeout)
	frame, err := l.readFrame(rest, frame, func(frame []byte) int {
		if len(frame) < 2 {
			return 2
		}

		return requestLength(frame)
	})
	switch {
	case errors.Is(err, errUnknownFunction):
		frame = append(frame, l.drain(server)...)
		// The address, the function and the CRC at least, so that the PDU is never shorter than nothing.
		if len(frame) < 4 || !checkCRC(frame) {
			return frame, fmt.Errorf("%w: frame % X", ErrCRC, frame)
		}

		return frame, nil
	case err != nil && server.err() == nil && rest.err() != nil:
		return frame, fmt.Errorf("%w: % X cut short", errBrokenFrame, frame)
	default:
		return frame, err
	}
}
