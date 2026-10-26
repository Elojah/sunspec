package fault_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elojah/sunspec/fault"
	"github.com/elojah/sunspec/modbus"
)

// link decides the fault it was last set to, and records the functions of the requests it decides for.
type link struct {
	mu        sync.Mutex
	link      fault.Link
	units     []uint8
	functions []modbus.FunctionCode
}

func (l *link) set(f fault.Link) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.link = f
}

func (l *link) decide(unit uint8, request modbus.PDU) fault.Link {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.units = append(l.units, unit)
	l.functions = append(l.functions, request.Function)

	return l.link
}

// newTransport returns a client to a Memory through a Transport deciding with the returned link, and a client to the
// Memory directly.
func newTransport() (*link, *modbus.Client, *modbus.Client) {
	m, l := newMemory(), &link{}

	return l, modbus.NewClient(fault.NewTransport(modbus.NewLoopback(m), l.decide)), modbus.NewClient(modbus.NewLoopback(m))
}

// Err fails a request, sent or not: a write sent is applied although it failed.
func TestTransport_Err(t *testing.T) {
	t.Parallel()

	l, c, direct := newTransport()
	if err := c.WriteRegister(t.Context(), 2, 0, 1); err != nil {
		t.Fatal(err)
	}

	l.set(fault.Link{Err: modbus.ErrCRC})
	if err := c.WriteRegister(t.Context(), 1, 0, 2); !errors.Is(err, modbus.ErrCRC) {
		t.Errorf("not sent: got %v, want ErrCRC", err)
	}
	if got := readRegisters(t, direct, 0, 1); got[0] != 1 {
		t.Errorf("not sent: register 0 = %d, want 1", got[0])
	}

	l.set(fault.Link{Err: context.DeadlineExceeded, Sent: true})
	if err := c.WriteRegister(t.Context(), 1, 0, 3); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("sent: got %v, want context.DeadlineExceeded", err)
	}
	if got := readRegisters(t, direct, 0, 1); got[0] != 3 {
		t.Errorf("sent: register 0 = %d, want 3, the write applied", got[0])
	}

	if want := []uint8{2, 1, 1}; !slices.Equal(l.units, want) {
		t.Errorf("units %v, want %v", l.units, want)
	}
	for i, f := range l.functions {
		if f != modbus.WriteSingleRegister {
			t.Errorf("request %d: function %v, want WriteSingleRegister", i, f)
		}
	}
}

// A Delay holds the response, or the error, back, unless the ctx is done first.
func TestTransport_Delay(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		l, c, _ := newTransport()
		l.set(fault.Link{Delay: 50 * time.Millisecond})

		start := time.Now()
		if got := readRegisters(t, c, 0, 1); got[0] != 100 {
			t.Errorf("register 0 = %d, want 100", got[0])
		}
		if waited := time.Since(start); waited != 50*time.Millisecond {
			t.Errorf("waited %v, want 50ms", waited)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		start = time.Now()
		if _, err := c.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 0, 1); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v, want context.DeadlineExceeded", err)
		}
		if waited := time.Since(start); waited != 20*time.Millisecond {
			t.Errorf("waited %v, want the ctx's 20ms", waited)
		}
	})
}
