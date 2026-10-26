package fault

import (
	"context"
	"time"

	"github.com/elojah/sunspec/modbus"
)

// Link is how the link to a device misbehaves for a request. The zero Link carries it as it is.
type Link struct {
	// Delay holds the response, or Err, back, unless the request's ctx is done first.
	Delay time.Duration
	// Err is returned instead of the response, such as modbus.ErrCRC for a line with noise, an error wrapping
	// modbus.ErrInvalidResponse, context.DeadlineExceeded, or a dial's error.
	Err error
	// Sent sends the request before Err is returned: the device serves it, a write applied, and only its response is
	// lost, so that the client cannot tell whether the write was.
	Sent bool
}

// Transport is a modbus.Transport sending through another, but for the faults decide returns. decide is called once
// per request, from the goroutine sending it, so it must be safe for concurrent use with the Client's callers.
type Transport struct {
	next   modbus.Transport
	decide func(unit uint8, request modbus.PDU) Link
}

// NewTransport returns a Transport sending through next, with the faults decide returns for each request.
func NewTransport(next modbus.Transport, decide func(unit uint8, request modbus.PDU) Link) *Transport {
	return &Transport{next: next, decide: decide}
}

// Send implements modbus.Transport.
func (t *Transport) Send(ctx context.Context, unit uint8, request modbus.PDU) (modbus.PDU, error) {
	f := t.decide(unit, request)

	var (
		response modbus.PDU
		err      = f.Err
	)
	switch {
	case f.Err == nil:
		response, err = t.next.Send(ctx, unit, request)
	case f.Sent:
		_, _ = t.next.Send(ctx, unit, request) // its response is lost
	}
	if waitErr := sleep(ctx, f.Delay); waitErr != nil {
		return modbus.PDU{}, waitErr
	}

	return response, err
}

// Close implements modbus.Transport: it closes next.
func (t *Transport) Close() error {
	return t.next.Close()
}
