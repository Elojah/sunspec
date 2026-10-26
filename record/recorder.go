package record

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/elojah/sunspec/modbus"
)

// Recorder is a modbus.Handler forwarding each request to a device through a Client, and handing save an Entry of it.
// It answers as the device did: what it read, or its exception, and nothing, modbus.ErrNoResponse, for a request the
// device did not answer, or whose response was lost or garbled, which the Entry records as NoResponse.
//
// A Handler cannot tell a write of one register, or one coil, from a write of several: the Recorder forwards every
// write with WriteMultipleRegisters or WriteMultipleCoils, as sunspec.Model.Write does.
type Recorder struct {
	device *modbus.Client
	save   func(Entry)

	// mu hands save one Entry at a time, although Serve serves each connection from its own goroutine.
	mu sync.Mutex
}

// NewRecorder returns a Recorder forwarding requests to device, and handing each to save, one at a time, in the
// goroutine serving it, once the device answered.
func NewRecorder(device *modbus.Client, save func(Entry)) *Recorder {
	return &Recorder{device: device, save: save}
}

// ReadBits implements modbus.Handler.
func (r *Recorder) ReadBits(ctx context.Context, unit uint8, table modbus.Table, address, quantity uint16) ([]bool, error) {
	start, e := time.Now(), Entry{Unit: unit, Table: table, Address: address, Quantity: quantity}
	bits, err := r.device.ReadBits(ctx, unit, table, address, quantity)
	for _, bit := range bits {
		e.Values = append(e.Values, fromBit(bit))
	}

	return bits, r.record(start, e, err)
}

// WriteBits implements modbus.Handler.
func (r *Recorder) WriteBits(ctx context.Context, unit uint8, address uint16, values []bool) error {
	start, e := time.Now(), Entry{Unit: unit, Table: modbus.Coils, Address: address, Quantity: count(values), Write: true}
	for _, bit := range values {
		e.Values = append(e.Values, fromBit(bit))
	}

	return r.record(start, e, r.device.WriteCoils(ctx, unit, address, values))
}

// ReadRegisters implements modbus.Handler.
func (r *Recorder) ReadRegisters(ctx context.Context, unit uint8, table modbus.Table, address, quantity uint16) ([]uint16, error) {
	start, e := time.Now(), Entry{Unit: unit, Table: table, Address: address, Quantity: quantity}
	registers, err := r.device.ReadRegisters(ctx, unit, table, address, quantity)
	e.Values = registers

	return registers, r.record(start, e, err)
}

// WriteRegisters implements modbus.Handler.
func (r *Recorder) WriteRegisters(ctx context.Context, unit uint8, address uint16, values []uint16) error {
	start, e := time.Now(), Entry{Unit: unit, Table: modbus.HoldingRegisters, Address: address, Quantity: count(values), Write: true}
	e.Values = values

	return r.record(start, e, r.device.WriteRegisters(ctx, unit, address, values))
}

// record completes e, sent at start, with the device's answer, err, saves it, and returns what to answer the client.
func (r *Recorder) record(start time.Time, e Entry, err error) error {
	// At without its monotonic reading, which an Entry read back has not either.
	e.At, e.Took = start.Round(0), time.Since(start)
	var answer error
	switch exception, ok := errors.AsType[*modbus.ExceptionError](err); {
	case err == nil:
		e.Outcome = Answered
	case ok:
		e.Outcome, e.Code = Exception, exception.Code
		answer = exception
	default:
		e.Outcome = NoResponse
		answer = modbus.ErrNoResponse
	}
	if e.Outcome != Answered && !e.Write {
		e.Values = nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.save(e)

	return answer
}

func fromBit(bit bool) uint16 {
	if bit {
		return 1
	}

	return 0
}

// count returns how many values a write holds, which the server bounded by the protocol's limits.
func count[T any](values []T) uint16 {
	return uint16(len(values)) //nolint:gosec // at most 123 registers or 1968 bits
}
