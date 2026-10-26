package modbus

import (
	"context"
	"time"
)

// Loopback is a Transport serving each request with a Handler, in process: a Client on a Loopback talks to the
// Handler as over a wire, PDUs encoded and decoded the same, without the framing. It tests code against a simulated
// device without a socket, under TinyGo too.
//
// It times out as the other transports do, after WithTimeout's 1s by default: a Handler answering ErrNoResponse is
// waited for until then, and a response that comes later is dropped.
type Loopback struct {
	handler Handler
	config  config
}

// NewLoopback returns a Loopback serving requests with h.
func NewLoopback(h Handler, options ...Option) *Loopback {
	return &Loopback{handler: h, config: newConfig(options)}
}

// Send serves request with the Handler, in the caller's goroutine, and returns its response. It fails once ctx is
// done, or the timeout runs out, with ctx's error. The Handler gets ctx as it is: one slower than the timeout is
// waited for, and its response dropped.
func (l *Loopback) Send(ctx context.Context, unit uint8, request PDU) (PDU, error) {
	if err := ctx.Err(); err != nil {
		return PDU{}, err
	}
	// The clock is read only for a timeout: a bound would read it once more, and ctx's deadline, on every request.
	var start time.Time
	if l.config.timeout > 0 {
		start = time.Now()
	}

	response, ok := serve(ctx, l.handler, unit, request)
	if !ok {
		// Nothing comes back: wait as a client on a wire does.
		return PDU{}, wait(newBoundFrom(ctx, start, l.config.timeout))
	}
	if err := ctx.Err(); err != nil {
		return PDU{}, err
	}
	if l.config.timeout > 0 && time.Since(start) >= l.config.timeout {
		return PDU{}, context.DeadlineExceeded
	}

	return response, nil
}

// Close does nothing: a Loopback holds no resource.
func (l *Loopback) Close() error {
	return nil
}
