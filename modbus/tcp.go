package modbus

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
)

// Over TCP, a frame is an MBAP header and a PDU. TCP is a stream, so the header says where the frame ends:
//
//	transaction ID  2 bytes  chosen by the client, echoed by the server, to pair a response with its request
//	protocol ID     2 bytes  0 for Modbus
//	length          2 bytes  how many bytes follow: the unit and the PDU
//	unit            1 byte   the device behind a gateway, or the device itself
//	PDU             up to 253 bytes
const (
	mbapHeaderLen = 7
	// mbapMaxLength is the largest length field: the unit and a full PDU.
	mbapMaxLength = 1 + maxPDU
)

// mbapFrame is a decoded Modbus TCP frame.
type mbapFrame struct {
	id   uint16
	unit uint8
	pdu  PDU
}

// appendFrame appends f, header first.
func appendFrame(b []byte, f mbapFrame) []byte {
	b = appendUint16s(b, f.id, 0, uint16(2+len(f.pdu.Data))) //nolint:gosec // a PDU is at most 253 bytes
	b = append(b, f.unit, byte(f.pdu.Function))

	return append(b, f.pdu.Data...)
}

// readFrame reads one frame from r. A protocol ID other than 0, or a length that cannot hold a function code or goes
// past a full PDU, wraps ErrInvalidResponse: the stream cannot be trusted to resynchronise after it.
func readFrame(r io.Reader) (mbapFrame, error) {
	var header [mbapHeaderLen]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return mbapFrame{}, err
	}
	protocol, length := binary.BigEndian.Uint16(header[2:]), binary.BigEndian.Uint16(header[4:])
	if protocol != 0 || length < 2 || length > mbapMaxLength {
		return mbapFrame{}, fmt.Errorf("%w: MBAP header % X", ErrInvalidResponse, header)
	}
	// The length counts the unit, already read with the header.
	pdu := make([]byte, length-1)
	if _, err := io.ReadFull(r, pdu); err != nil {
		return mbapFrame{}, err
	}

	return mbapFrame{
		id:   binary.BigEndian.Uint16(header[0:]),
		unit: header[6],
		pdu:  PDU{Function: FunctionCode(pdu[0]), Data: pdu[1:]},
	}, nil
}

// TCPTransport is a Transport over Modbus TCP, to one server. It dials on the first request, and again on the next
// request after any failure. It never retries a request, since a write may not be safe to repeat.
//
// It sends one request at a time, and waits for its response before the next one: Modbus TCP allows several in
// flight, but many devices answer only one per connection at a time, or mix up the others.
type TCPTransport struct {
	address string
	config  config

	// sem holds a token while a request is in flight. Waiting for it selects on ctx, which a mutex cannot.
	sem chan struct{}

	// mu guards conn and closed, which Close changes while a request may be in flight.
	mu     sync.Mutex
	conn   net.Conn
	closed bool

	// id is the last request's transaction ID. Only the holder of sem uses it.
	id uint16
}

// NewTCPTransport returns a TCPTransport to address, as host:port. Modbus TCP servers listen on port 502.
func NewTCPTransport(address string, options ...Option) *TCPTransport {
	return &TCPTransport{
		address: address,
		config:  newConfig(options),
		sem:     make(chan struct{}, 1),
	}
}

// Send sends request to unit, once every request before it is answered, and returns the response. If ctx is done, or
// WithTimeout's timeout runs out, before the response, it returns ctx's error.
//
// Any failure closes the connection, which the next request dials again. Once a request has been given up on, its
// response may still come, or half of it, and a new connection is the only sure way not to read it as the next
// request's response. The transaction ID would tell them apart, but not a response cut in the middle.
func (t *TCPTransport) Send(ctx context.Context, unit uint8, request PDU) (PDU, error) {
	b := newBound(ctx, t.config.timeout)
	if err := acquire(t.sem, b); err != nil {
		return PDU{}, err
	}
	defer func() { <-t.sem }()

	conn, err := t.connect(b)
	if err != nil {
		return PDU{}, err
	}
	response, err := t.roundTrip(b, conn, unit, request)
	if err != nil {
		t.drop(conn)

		return PDU{}, err
	}

	return response, nil
}

// Close closes the connection, failing a request in flight, and every later Send with ErrClosed. Every Close after the
// first returns ErrClosed.
func (t *TCPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrClosed
	}
	t.closed = true
	if t.conn == nil {
		return nil
	}
	err := t.conn.Close()
	t.conn = nil

	return err
}

// connect returns the connection, dialing it if there is none.
func (t *TCPTransport) connect(b bound) (net.Conn, error) {
	t.mu.Lock()
	conn, closed := t.conn, t.closed
	t.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	if conn != nil {
		return conn, nil
	}

	// Outside the lock, so that Close never waits for a dial.
	dialer := net.Dialer{Deadline: b.deadline}
	conn, err := dialer.DialContext(b.ctx, "tcp", t.address)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		_ = conn.Close()

		return nil, ErrClosed
	}
	t.conn = conn

	return conn, nil
}

// drop closes conn, and forgets it unless Close already did.
func (t *TCPTransport) drop(conn net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.conn == conn {
		t.conn = nil
	}
	_ = conn.Close()
}

// roundTrip writes request and reads its response on conn.
func (t *TCPTransport) roundTrip(b bound, conn net.Conn, unit uint8, request PDU) (PDU, error) {
	stop := watch(b, conn)
	defer stop()

	t.id++
	frame := appendFrame(make([]byte, 0, mbapHeaderLen+1+len(request.Data)), mbapFrame{id: t.id, unit: unit, pdu: request})
	if _, err := conn.Write(frame); err != nil {
		return PDU{}, ioError(b, err)
	}
	response, err := readFrame(conn)
	if err != nil {
		return PDU{}, ioError(b, err)
	}
	if response.id != t.id || response.unit != unit {
		return PDU{}, invalidResponse(request.Function, "transaction %d for unit %d, want %d for unit %d", response.id, response.unit, t.id, unit)
	}

	return response.pdu, nil
}

// Serve serves h over Modbus TCP on l, with a goroutine per connection, which serves its requests one at a time, in
// order. Pass a listener on port 502 for clients to find it, or on 127.0.0.1:0 in a test.
//
// Once ctx is done, it closes l and every connection, waits for their goroutines, and returns nil. If l.Accept fails
// first, it closes the connections too, then returns that error. A connection sending a frame it cannot parse is
// closed, since its stream cannot be trusted to resynchronise.
func Serve(ctx context.Context, l net.Listener, h Handler) error {
	// Canceled on return, whatever the reason, which closes l and every connection through their AfterFuncs. The
	// deferred calls run in reverse: cancel, then wait.
	connCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	context.AfterFunc(connCtx, func() { _ = l.Close() })

	for {
		conn, err := l.Accept()
		if err != nil {
			return stopped(ctx, err)
		}
		wg.Go(func() { serveConn(connCtx, conn, h) })
	}
}

// serveConn serves conn's requests until it fails, or ctx is done.
func serveConn(ctx context.Context, conn net.Conn, h Handler) {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()

	for {
		// A client may stay idle for long between requests, so no deadline.
		request, err := readFrame(conn)
		if err != nil {
			return
		}
		response := mbapFrame{id: request.id, unit: request.unit, pdu: serve(ctx, h, request.unit, request.pdu)}
		if _, err := conn.Write(appendFrame(make([]byte, 0, mbapHeaderLen+1+len(response.pdu.Data)), response)); err != nil {
			return
		}
	}
}

// stopped returns err, or nil once ctx is done: Serve and ServeRTU end without an error then, whatever their last I/O
// returned.
func stopped(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil //nolint:nilerr // a done ctx is how a server is stopped
	}

	return err
}
