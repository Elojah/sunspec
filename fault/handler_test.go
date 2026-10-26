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

// timeout is the Loopback's in the tests.
const timeout = 100 * time.Millisecond

// newMemory returns a Memory whose 10 holding registers hold 100+i, and whose 10 coils are on at even addresses.
func newMemory() *modbus.Memory {
	var tables modbus.Tables
	for i := range 10 {
		tables.HoldingRegisters = append(tables.HoldingRegisters, uint16(100+i))
		tables.Coils = append(tables.Coils, i%2 == 0)
	}

	return modbus.NewMemory(tables)
}

// decision decides the fault it was last set to, and records the requests it decides for.
type decision struct {
	mu       sync.Mutex
	device   fault.Device
	requests []fault.Request
}

func (d *decision) set(f fault.Device) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.device = f
}

func (d *decision) decide(r fault.Request) fault.Device {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.requests = append(d.requests, r)

	return d.device
}

// newHandler returns a Memory behind a Handler deciding with the returned decision, and a client to it.
func newHandler() (*modbus.Memory, *decision, *modbus.Client) {
	m, d := newMemory(), &decision{}

	return m, d, modbus.NewClient(modbus.NewLoopback(fault.NewHandler(m, d.decide), modbus.WithTimeout(timeout)))
}

func readRegisters(t *testing.T, c *modbus.Client, address, quantity uint16) []uint16 {
	t.Helper()

	got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, address, quantity)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// decide sees each request, a ReadWriteMultipleRegisters as its write then its read.
func TestHandler_Request(t *testing.T) {
	t.Parallel()

	_, d, c := newHandler()
	ctx := t.Context()
	if _, err := c.ReadRegisters(ctx, 3, modbus.HoldingRegisters, 2, 3); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteRegisters(ctx, 1, 4, []uint16{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadBits(ctx, 1, modbus.Coils, 0, 5); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteCoils(ctx, 1, 1, []bool{true, false, true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadWriteRegisters(ctx, 1, 0, 2, 5, []uint16{7}); err != nil {
		t.Fatal(err)
	}

	want := []fault.Request{
		{Unit: 3, Table: modbus.HoldingRegisters, Address: 2, Quantity: 3},
		{Unit: 1, Table: modbus.HoldingRegisters, Address: 4, Quantity: 2, Write: true},
		{Unit: 1, Table: modbus.Coils, Address: 0, Quantity: 5},
		{Unit: 1, Table: modbus.Coils, Address: 1, Quantity: 3, Write: true},
		{Unit: 1, Table: modbus.HoldingRegisters, Address: 5, Quantity: 1, Write: true},
		{Unit: 1, Table: modbus.HoldingRegisters, Address: 0, Quantity: 2},
	}
	if !slices.Equal(d.requests, want) {
		t.Errorf("requests\n%+v\nwant\n%+v", d.requests, want)
	}
}

// A Delay holds the response back, then the first fault set applies: Silent before Exception.
func TestHandler_Faults(t *testing.T) {
	t.Parallel()

	const delay = 30 * time.Millisecond
	for _, tt := range []struct {
		name    string
		device  fault.Device
		wantErr error
		want    time.Duration
	}{
		{name: "none"},
		{name: "silent", device: fault.Device{Silent: true}, wantErr: context.DeadlineExceeded, want: timeout},
		{name: "exception", device: fault.Device{Exception: modbus.ServerDeviceBusy}, wantErr: modbus.ErrServerDeviceBusy},
		{name: "silent first", device: fault.Device{Silent: true, Exception: modbus.ServerDeviceBusy}, wantErr: context.DeadlineExceeded, want: timeout},
		{name: "delay", device: fault.Device{Delay: delay}, want: delay},
		{name: "delay then exception", device: fault.Device{Delay: delay, Exception: modbus.ServerDeviceBusy}, wantErr: modbus.ErrServerDeviceBusy, want: delay},
		// The Loopback waits for the response, and drops it.
		{name: "delay past the timeout", device: fault.Device{Delay: 3 * timeout}, wantErr: context.DeadlineExceeded, want: 3 * timeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				_, d, c := newHandler()
				d.set(tt.device)

				start := time.Now()
				if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); !errors.Is(err, tt.wantErr) {
					t.Errorf("got %v, want %v", err, tt.wantErr)
				}
				if waited := time.Since(start); waited != tt.want {
					t.Errorf("waited %v, want %v", waited, tt.want)
				}
			})
		})
	}
}

// A frozen unit answers each register and bit as first read since it froze, and live ones once it thaws.
func TestHandler_Frozen(t *testing.T) {
	t.Parallel()

	m, d, c := newHandler()
	update := func(f func(tables *modbus.Tables)) { m.Update(f) }

	d.set(fault.Device{Frozen: true})
	if got, want := readRegisters(t, c, 0, 2), []uint16{100, 101}; !slices.Equal(got, want) {
		t.Errorf("frozen: %v, want %v", got, want)
	}
	update(func(tables *modbus.Tables) {
		copy(tables.HoldingRegisters, []uint16{500, 501, 502})
		tables.Coils[0] = false
	})
	// Register 2 is first read now, frozen at what it holds.
	if got, want := readRegisters(t, c, 0, 3), []uint16{100, 101, 502}; !slices.Equal(got, want) {
		t.Errorf("frozen after an update: %v, want %v", got, want)
	}
	update(func(tables *modbus.Tables) { tables.HoldingRegisters[2] = 999 })
	if got, want := readRegisters(t, c, 1, 2), []uint16{101, 502}; !slices.Equal(got, want) {
		t.Errorf("frozen, register 2 updated: %v, want %v", got, want)
	}
	bits, err := c.ReadBits(t.Context(), 1, modbus.Coils, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []bool{false, false}; !slices.Equal(bits, want) {
		t.Errorf("coils first read frozen: %v, want %v", bits, want)
	}
	update(func(tables *modbus.Tables) { tables.Coils[0] = true })
	if bits, err = c.ReadBits(t.Context(), 1, modbus.Coils, 0, 1); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(bits, []bool{false}) {
		t.Errorf("coil 0 frozen: %v, want [false]", bits)
	}

	d.set(fault.Device{})
	if got, want := readRegisters(t, c, 0, 3), []uint16{500, 501, 999}; !slices.Equal(got, want) {
		t.Errorf("thawed: %v, want %v", got, want)
	}

	// Frozen again, from what is read then.
	d.set(fault.Device{Frozen: true})
	update(func(tables *modbus.Tables) { tables.HoldingRegisters[0] = 7 })
	if got := readRegisters(t, c, 0, 1); got[0] != 7 {
		t.Errorf("frozen again: %v, want [7]", got)
	}
	update(func(tables *modbus.Tables) { tables.HoldingRegisters[0] = 8 })
	if got := readRegisters(t, c, 0, 1); got[0] != 7 {
		t.Errorf("frozen again, updated: %v, want [7]", got)
	}
}

// An Ignored write is acknowledged, and changes nothing.
func TestHandler_Ignored(t *testing.T) {
	t.Parallel()

	_, d, c := newHandler()
	d.set(fault.Device{Ignored: true})
	if err := c.WriteRegister(t.Context(), 1, 0, 9); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteCoil(t.Context(), 1, 1, true); err != nil {
		t.Fatal(err)
	}

	d.set(fault.Device{})
	if got := readRegisters(t, c, 0, 1); got[0] != 100 {
		t.Errorf("register 0 = %d, want 100: the write was applied", got[0])
	}
	bits, err := c.ReadBits(t.Context(), 1, modbus.Coils, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bits[0] {
		t.Error("coil 1 on: the write was applied")
	}
}
