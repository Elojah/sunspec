package fault

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/elojah/sunspec/modbus"
)

// Request is what a Handler's decide sees of a request: the unit, the table, and the range read or written. A
// ReadWriteMultipleRegisters request is two, the write first.
type Request struct {
	Unit     uint8
	Table    modbus.Table
	Address  uint16
	Quantity uint16
	Write    bool
}

// Device is how a device misbehaves for a request. The zero Device serves it as it is. Delay comes first, then the
// first of the others set, in their order here.
type Device struct {
	// Delay holds the response back, as a busy device or a slow line does, unless the server's ctx is done first.
	Delay time.Duration
	// Silent answers nothing, as a device that is off or rebooting: the client times out.
	Silent bool
	// Exception answers it rather than serve the request, such as modbus.ServerDeviceBusy while a device boots.
	Exception modbus.ExceptionCode
	// Frozen answers a read with each register or bit as it was first read since the unit froze, as a device that
	// answers but stopped updating its measurements. The unit thaws at its first request that is not Frozen.
	Frozen bool
	// Ignored acknowledges a write without applying it.
	Ignored bool
}

// Handler is a modbus.Handler serving through another, but for the faults decide returns. decide is called once per
// request, in the server's goroutine, so it must be safe for concurrent use with Serve, which serves each connection
// from its own.
type Handler struct {
	next   modbus.Handler
	decide func(Request) Device

	// mu guards frozen.
	mu sync.Mutex
	// frozen holds, for each unit frozen, every register and bit read since it froze, bits as 0 and 1.
	frozen map[uint8]map[cell]uint16
}

// cell is the address of a register or a bit.
type cell struct {
	table   modbus.Table
	address uint16
}

// NewHandler returns a Handler serving through next, with the faults decide returns for each request.
func NewHandler(next modbus.Handler, decide func(Request) Device) *Handler {
	return &Handler{next: next, decide: decide, frozen: map[uint8]map[cell]uint16{}}
}

// ReadBits implements modbus.Handler.
func (h *Handler) ReadBits(ctx context.Context, unit uint8, table modbus.Table, address, quantity uint16) ([]bool, error) {
	r := Request{Unit: unit, Table: table, Address: address, Quantity: quantity}
	f, err := h.fault(ctx, r)
	if err != nil {
		return nil, err
	}
	bits, err := h.next.ReadBits(ctx, unit, table, address, quantity)
	if err != nil || !f.Frozen {
		return bits, err
	}

	values := make([]uint16, len(bits))
	for i, bit := range bits {
		if bit {
			values[i] = 1
		}
	}
	h.freeze(r, values)
	frozen := make([]bool, len(values))
	for i, v := range values {
		frozen[i] = v == 1
	}

	return frozen, nil
}

// WriteBits implements modbus.Handler.
func (h *Handler) WriteBits(ctx context.Context, unit uint8, address uint16, values []bool) error {
	f, err := h.fault(ctx, Request{Unit: unit, Table: modbus.Coils, Address: address, Quantity: count(values), Write: true})
	if err != nil || f.Ignored {
		return err
	}

	return h.next.WriteBits(ctx, unit, address, values)
}

// ReadRegisters implements modbus.Handler.
func (h *Handler) ReadRegisters(ctx context.Context, unit uint8, table modbus.Table, address, quantity uint16) ([]uint16, error) {
	r := Request{Unit: unit, Table: table, Address: address, Quantity: quantity}
	f, err := h.fault(ctx, r)
	if err != nil {
		return nil, err
	}
	registers, err := h.next.ReadRegisters(ctx, unit, table, address, quantity)
	if err != nil || !f.Frozen {
		return registers, err
	}

	// A handler may return its own slice: overlay a copy.
	registers = slices.Clone(registers)
	h.freeze(r, registers)

	return registers, nil
}

// WriteRegisters implements modbus.Handler.
func (h *Handler) WriteRegisters(ctx context.Context, unit uint8, address uint16, values []uint16) error {
	r := Request{Unit: unit, Table: modbus.HoldingRegisters, Address: address, Quantity: count(values), Write: true}
	f, err := h.fault(ctx, r)
	if err != nil || f.Ignored {
		return err
	}

	return h.next.WriteRegisters(ctx, unit, address, values)
}

// fault decides r's fault, thaws its unit unless Frozen, waits its Delay, and returns the error it answers, if any.
func (h *Handler) fault(ctx context.Context, r Request) (Device, error) {
	f := h.decide(r)
	if !f.Frozen {
		h.mu.Lock()
		delete(h.frozen, r.Unit)
		h.mu.Unlock()
	}
	if err := sleep(ctx, f.Delay); err != nil {
		return f, err
	}

	switch {
	case f.Silent:
		return f, modbus.ErrNoResponse
	case f.Exception != 0:
		return f, &modbus.ExceptionError{Code: f.Exception}
	default:
		return f, nil
	}
}

// freeze sets each of values, read for r, to the value its cell had when first read since r's unit froze, and keeps
// those read for the first time.
func (h *Handler) freeze(r Request, values []uint16) {
	h.mu.Lock()
	defer h.mu.Unlock()

	cells := h.frozen[r.Unit]
	if cells == nil {
		cells = map[cell]uint16{}
		h.frozen[r.Unit] = cells
	}
	// address wraps past the last value only: the server checked the range ends by 65535.
	address := r.Address
	for i := range values {
		c := cell{table: r.Table, address: address}
		if v, ok := cells[c]; ok {
			values[i] = v
		} else {
			cells[c] = values[i]
		}
		address++
	}
}

// count returns how many values a write holds, which the server bounded by the protocol's limits.
func count[T any](values []T) uint16 {
	return uint16(len(values)) //nolint:gosec // at most 123 registers or 1968 bits
}

// sleep waits for d, or returns ctx's error once it is done first.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
