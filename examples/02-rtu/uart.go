package main

import "sync"

// uart is one end of a serial line. Read returns at once, with what came since the last Read, or nothing, as TinyGo's
// machine.UART does: RTUTransport and ServeRTU poll it.
type uart struct {
	mu  *sync.Mutex
	in  *[]byte
	out *[]byte
}

// newLine returns both ends of a serial line.
func newLine() (*uart, *uart) {
	var (
		mu   sync.Mutex
		a, b []byte
	)

	return &uart{mu: &mu, in: &a, out: &b}, &uart{mu: &mu, in: &b, out: &a}
}

func (u *uart) Read(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	n := copy(p, *u.in)
	*u.in = (*u.in)[n:]

	return n, nil
}

func (u *uart) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	*u.out = append(*u.out, p...)

	return len(p), nil
}
