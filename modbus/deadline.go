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

// bound is what ends a request: its ctx, or its deadline, the earlier of ctx's and WithTimeout's. The timeout stays a
// time rather than a ctx derived with context.WithTimeout, whose timer and allocations every request would pay for:
// the port's deadline enforces it during I/O, and a timer only once a wait blocks (acquire, sleep).
type bound struct {
	ctx context.Context //nolint:containedctx // passed down one request's calls, by value, never kept
	// deadline is zero for none.
	deadline time.Time
}

// newBound returns the bound of a request on ctx, ending timeout from now at the latest, or with ctx alone for a
// timeout of 0 or less.
func newBound(ctx context.Context, timeout time.Duration) bound {
	deadline, _ := ctx.Deadline() // zero, so no deadline, if ctx has none
	if timeout > 0 {
		if d := time.Now().Add(timeout); deadline.IsZero() || d.Before(deadline) {
			deadline = d
		}
	}

	return bound{ctx: ctx, deadline: deadline}
}

// err returns ctx's error, or context.DeadlineExceeded once the deadline has passed, or nil.
func (b bound) err() error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if !b.deadline.IsZero() && !time.Now().Before(b.deadline) {
		return context.DeadlineExceeded
	}

	return nil
}

// acquire takes sem's token, once it is free, or returns b's error first. A request that ended already is never sent.
func acquire(sem chan struct{}, b bound) error {
	if err := b.err(); err != nil {
		return err
	}
	select {
	case sem <- struct{}{}:
		return nil
	default:
	}

	// Another request is in flight: wait for it, as long as b allows. A nil channel never receives.
	var timeout <-chan time.Time
	if !b.deadline.IsZero() {
		timer := time.NewTimer(time.Until(b.deadline))
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case sem <- struct{}{}:
		return nil
	case <-b.ctx.Done():
		return b.ctx.Err()
	case <-timeout:
		return context.DeadlineExceeded
	}
}

// sleep waits for d, or returns b's error once b ends first.
func sleep(b bound, d time.Duration) error {
	var err error // what waiting d returns: nil, or DeadlineExceeded when the deadline comes first
	if !b.deadline.IsZero() {
		if left := time.Until(b.deadline); left <= d {
			d, err = left, context.DeadlineExceeded
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return err
	case <-b.ctx.Done():
		return b.ctx.Err()
	}
}

// watch makes port's I/O fail once b ends, which Read and Write do not see on their own.
//
// It sets b's deadline on port, which covers a timeout. A cancel has no deadline, so context.AfterFunc sets an expired
// one then, which unblocks a Read already waiting. A ctx that is never canceled, such as context.Background(), needs no
// AfterFunc. The returned stop ends the watch: once it returns, the AfterFunc has either not run and never will, or
// finished, so the caller's next deadline cannot be overwritten by a late one. context.AfterFunc's own stop does not
// wait, hence fired.
func watch(b bound, port readDeadliner) func() {
	setDeadline(port, b.deadline)
	if b.ctx.Done() == nil {
		return func() {}
	}

	fired := make(chan struct{})
	stop := context.AfterFunc(b.ctx, func() {
		setDeadline(port, expired)
		close(fired)
	})

	return func() {
		if !stop() {
			<-fired
		}
	}
}

// ioError returns b's error for an I/O error caused by watch's deadlines, so that a timeout reads as
// context.DeadlineExceeded and a cancel as context.Canceled, whatever the port; and err as it is otherwise.
func ioError(b bound, err error) error {
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		return err
	}
	if ctxErr := b.ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	// b's deadline, or ctx's, which the port's may pass a moment before ctx's timer fires.
	return context.DeadlineExceeded
}
