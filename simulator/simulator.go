// Package simulator simulates a SunSpec device: models from sunspec.NewModel, served as holding registers by any
// modbus server, as a real device lays them out. Test code reading SunSpec devices against it, without hardware:
//
//	inverter, _ := sunspec.NewModel(model.Lookup(103), nil)
//	sim, _ := simulator.New(40000, common, inverter)
//	_ = sim.Update(func() error { return inverter.Point("W").SetFloat(4200) })
//	go modbus.Serve(ctx, listener, sim)
//
// Hooks make it behave as a device does: OnWrite reacts to what a client writes, OnRead sets what a client reads at
// the time it does. Limit is a pair of them, model 123's power limit on an inverter's output.
package simulator

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
)

// ErrTooLarge is returned by New for models that do not fit between the base address and address 65535.
var ErrTooLarge = errors.New("simulator: models past address 65535")

// marker is "SunS", which Discover looks for at the base address.
var marker = [2]uint16{0x5375, 0x6E53}

// endID is the ID of the header that ends the models, whose length is 0.
const endID = 0xFFFF

// Simulator is a modbus.Handler serving SunSpec models as holding registers, from a base address: the marker "SunS",
// each model, then the end header. It serves no other table. It is safe for concurrent use.
//
// A client writes the writable points of one model at a time, each whole: a write that touches another register, such
// as a header, a read-only point, or half of an int32, is refused with IllegalDataAddress, and changes nothing.
//
// Its models hold what Update sets, unless hooks make them behave as a device does: OnWrite reacts to a client's
// writes, OnRead sets what a read returns at the time it comes.
type Simulator struct {
	// mu guards the models' registers, which Update changes and requests read and write, and the hooks, which run
	// under it.
	mu      sync.Mutex
	base    uint16
	models  []placed
	onWrite []func(m *sunspec.Model, written []*sunspec.Point) error
	onRead  []func() error
	// end is the address after the end header.
	end int
}

// placed is a model, and the address of its first register.
type placed struct {
	model   *sunspec.Model
	address int
}

// New returns a Simulator serving models from base, in order. It takes the models over: change them only inside
// Update.
func New(base uint16, models ...*sunspec.Model) (*Simulator, error) {
	s := &Simulator{base: base}
	address := int(base) + len(marker)
	for _, m := range models {
		s.models = append(s.models, placed{model: m, address: address})
		address += 2 + int(m.Length())
	}
	s.end = address + 2
	if s.end > 1<<16 {
		return nil, fmt.Errorf("%w: from %d, %d registers", ErrTooLarge, base, s.end-int(base))
	}

	return s, nil
}

// Update calls f, which may change the models, under the lock requests are served under, so that a client never
// reads half an update, then returns f's error.
func (s *Simulator) Update(f func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return f()
}

// OnWrite adds f, which the Simulator calls after each write of a client, under the lock, after the hooks added before,
// with the model written and its points written, in order, even with the values they held: a device reacts to its
// controls, such as an inverter applying a power limit to its output. If f fails, the write is undone, every model
// back to what it held before, and the client is answered f's error: modbus.ErrIllegalDataValue refuses a value, as a
// device does.
func (s *Simulator) OnWrite(f func(m *sunspec.Model, written []*sunspec.Point) error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.onWrite = append(s.onWrite, f)
}

// OnRead adds f, which the Simulator calls before each read of a client, under the lock, after the hooks added before,
// so that f sets what the read returns, such as a measurement at the time it is read. If f fails, the read is
// answered f's error, such as modbus.ErrServerDeviceBusy, or modbus.ErrNoResponse for none.
func (s *Simulator) OnRead(f func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.onRead = append(s.onRead, f)
}

// ReadBits implements modbus.Handler: a SunSpec device has no bits.
func (s *Simulator) ReadBits(context.Context, uint8, modbus.Table, uint16, uint16) ([]bool, error) {
	return nil, modbus.ErrIllegalDataAddress
}

// WriteBits implements modbus.Handler: a SunSpec device has no bits.
func (s *Simulator) WriteBits(context.Context, uint8, uint16, []bool) error {
	return modbus.ErrIllegalDataAddress
}

// ReadRegisters implements modbus.Handler: it serves holding registers from the base address to the end header, and
// IllegalDataAddress for any other, as a device does.
func (s *Simulator) ReadRegisters(_ context.Context, _ uint8, table modbus.Table, address, quantity uint16) ([]uint16, error) {
	start, end := int(address), int(address)+int(quantity)
	if table != modbus.HoldingRegisters || start < int(s.base) || end > s.end {
		return nil, modbus.ErrIllegalDataAddress
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, f := range s.onRead {
		if err := f(); err != nil {
			return nil, err
		}
	}

	// The whole map, then the range: a model's registers are copied anyway.
	registers := append(make([]uint16, 0, s.end-int(s.base)), marker[:]...)
	for _, p := range s.models {
		registers = append(registers, p.model.Registers()...)
	}
	registers = append(registers, endID, 0)

	return registers[start-int(s.base) : end-int(s.base)], nil
}

// WriteRegisters implements modbus.Handler: it writes whole writable points of one model, or nothing.
func (s *Simulator) WriteRegisters(_ context.Context, _ uint8, address uint16, values []uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, start := s.locate(int(address))
	if m == nil {
		return modbus.ErrIllegalDataAddress
	}
	end := start + len(values)

	// Every register written must belong to a writable point written whole: checked before any is, so that a refused
	// write changes nothing.
	var (
		points  []*sunspec.Point
		covered int
	)
	for _, p := range m.Points() {
		offset, size := p.Offset(), int(p.Definition().Size)
		if offset+size <= start || offset >= end {
			continue
		}
		if offset < start || offset+size > end || !p.Definition().Writable {
			return modbus.ErrIllegalDataAddress
		}
		points = append(points, p)
		covered += size
	}
	if covered != len(values) {
		return modbus.ErrIllegalDataAddress
	}

	var before [][]uint16
	if len(s.onWrite) > 0 {
		before = s.registers()
	}
	for _, p := range points {
		offset := p.Offset() - start
		if err := p.SetRaw(values[offset : offset+int(p.Definition().Size)]); err != nil {
			return err
		}
	}
	for _, f := range s.onWrite {
		if err := f(m, points); err != nil {
			s.restore(before)

			return err
		}
	}

	return nil
}

// registers returns a copy of each model's registers, in order.
func (s *Simulator) registers() [][]uint16 {
	registers := make([][]uint16, len(s.models))
	for i, p := range s.models {
		registers[i] = p.model.Registers()
	}

	return registers
}

// restore sets every point of each model back to registers, as registers returned them. Headers never change.
func (s *Simulator) restore(registers [][]uint16) {
	for i, placed := range s.models {
		for _, p := range placed.model.Points() {
			offset := p.Offset()
			_ = p.SetRaw(registers[i][offset : offset+int(p.Definition().Size)]) // the size is the point's
		}
	}
}

// locate returns the model holding address, and address's offset from the model's first register, or nil.
func (s *Simulator) locate(address int) (*sunspec.Model, int) {
	for _, p := range s.models {
		if address >= p.address && address < p.address+2+int(p.model.Length()) {
			return p.model, address - p.address
		}
	}

	return nil, 0
}
