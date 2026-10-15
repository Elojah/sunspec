package modbus

import (
	"context"
	"errors"
	"os"
	"time"
)

// readDeadliner is a port whose reads take a deadline: a net.Conn, or an os.File opened on a tty, which the runtime
// polls. Writes may take one too (writeDeadliner).
type readDeadliner interface {
	SetReadDeadline(t time.Time) error
}

// writeDeadliner is a port whose writes take a deadline.
type writeDeadliner interface {
	SetWriteDeadline(t time.Time) error
}

// expired is a deadline in the past, which fails I/O at once.
var expired = time.Unix(1, 0)

// setDeadline sets t as the deadline of port's reads, and of its writes if it takes one.
func setDeadline(port readDeadliner, t time.Time) {
	// Errors are ignored: a port that cannot take a deadline is waited for as it is, as a port without one.
	_ = port.SetReadDeadline(t)
	if w, ok := port.(writeDeadliner); ok {
		_ = w.SetWriteDeadline(t)
	}
}

// watch makes port's I/O fail once ctx is done, which Read and Write do not see on their own.
//
// It sets ctx's deadline on port, which covers a timeout. A cancel has no deadline, so context.AfterFunc sets an
// expired one then, which unblocks a Read already waiting. The returned stop ends the watch: once it returns, the
// AfterFunc has either not run and never will, or finished, so the caller's next deadline cannot be overwritten by a
// late one. context.AfterFunc's own stop does not wait, hence fired.
func watch(ctx context.Context, port readDeadliner) func() {
	deadline, _ := ctx.Deadline() // zero, so no deadline, if ctx has none
	setDeadline(port, deadline)

	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		setDeadline(port, expired)
		close(fired)
	})

	return func() {
		if !stop() {
			<-fired
		}
	}
}

// ioError returns ctx's error for an I/O error caused by watch's deadlines, so that a timeout reads as
// context.DeadlineExceeded and a cancel as context.Canceled, whatever the port; and err as it is otherwise.
func ioError(ctx context.Context, err error) error {
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	// The port's deadline may pass a moment before ctx's timer fires.
	return context.DeadlineExceeded
}
