package modbus

import "context"

// Loopback is a Transport serving each request with a Handler, in process: a Client on a Loopback talks to the
// Handler as over a wire, PDUs encoded and decoded the same, without the framing. It tests code against a simulated
// device without a socket, under TinyGo too.
type Loopback struct {
	handler Handler
}

// NewLoopback returns a Loopback serving requests with h.
func NewLoopback(h Handler) *Loopback {
	return &Loopback{handler: h}
}

// Send serves request with the Handler, in the caller's goroutine, and returns its response. It fails only once ctx is
// done.
func (l *Loopback) Send(ctx context.Context, unit uint8, request PDU) (PDU, error) {
	if err := ctx.Err(); err != nil {
		return PDU{}, err
	}

	return serve(ctx, l.handler, unit, request), nil
}

// Close does nothing: a Loopback holds no resource.
func (l *Loopback) Close() error {
	return nil
}
