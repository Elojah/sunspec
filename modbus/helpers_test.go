package modbus_test

import (
	"context"
	"errors"
	"sync"

	"github.com/elojah/sunspec/modbus"
)

// recorder is a Transport that records each request and answers the next of responses, or err.
type recorder struct {
	mu        sync.Mutex
	requests  []modbus.PDU
	responses []modbus.PDU
	err       error
}

func (r *recorder) Send(_ context.Context, _ uint8, request modbus.PDU) (modbus.PDU, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.requests = append(r.requests, request)
	if r.err != nil {
		return modbus.PDU{}, r.err
	}
	if len(r.responses) == 0 {
		return modbus.PDU{}, errNoResponse
	}
	response := r.responses[0]
	r.responses = r.responses[1:]

	return response, nil
}

func (r *recorder) Close() error {
	return nil
}

var errNoResponse = errors.New("recorder: no response left")

// newMemory returns a Memory with 100 of each: coil and discrete input i is on when i%3 == 0, input register i holds
// 1000+i and holding register i holds i.
func newMemory() *modbus.Memory {
	var tables modbus.Tables
	for i := range 100 {
		tables.Coils = append(tables.Coils, i%3 == 0)
		tables.DiscreteInputs = append(tables.DiscreteInputs, i%3 == 0)
		tables.InputRegisters = append(tables.InputRegisters, uint16(1000+i))
		tables.HoldingRegisters = append(tables.HoldingRegisters, uint16(i))
	}

	return modbus.NewMemory(tables)
}

// blocking is a Memory whose reads of holding register 1 wait for release, or for their ctx.
type blocking struct {
	*modbus.Memory

	release chan struct{}
}

func (b blocking) ReadRegisters(ctx context.Context, unit uint8, table modbus.Table, address, quantity uint16) ([]uint16, error) {
	if address == 1 {
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return b.Memory.ReadRegisters(ctx, unit, table, address, quantity)
}

// uart is one end of a simulated serial line, whose Read returns at once, with 0 bytes when none came, as TinyGo's
// machine.UART does. Neither end takes deadlines.
type uart struct {
	line *uartLine
	in   *[]byte
	out  *[]byte
}

type uartLine struct {
	mu   sync.Mutex
	a, b []byte
	// corrupt, when set, flips a bit of the next frame written from end a.
	corrupt bool
}

// newUARTs returns both ends of a line: a's writes are b's reads, and the other way round.
func newUARTs() (*uart, *uart, *uartLine) {
	l := &uartLine{}

	return &uart{line: l, in: &l.a, out: &l.b}, &uart{line: l, in: &l.b, out: &l.a}, l
}

func (u *uart) Read(p []byte) (int, error) {
	u.line.mu.Lock()
	defer u.line.mu.Unlock()

	n := copy(p, *u.in)
	*u.in = (*u.in)[n:]

	return n, nil
}

func (u *uart) Write(p []byte) (int, error) {
	u.line.mu.Lock()
	defer u.line.mu.Unlock()

	frame := append([]byte(nil), p...)
	if u.out == &u.line.b && u.line.corrupt {
		frame[len(frame)-1] ^= 0x01
		u.line.corrupt = false
	}
	*u.out = append(*u.out, frame...)

	return len(p), nil
}
