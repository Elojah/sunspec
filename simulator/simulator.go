// Package simulator simulates a SunSpec device: models from sunspec.NewModel, served as holding registers by any
// modbus server, as a real device lays them out. Test code reading SunSpec devices against it, without hardware:
//
//	inverter, _ := sunspec.NewModel(model.Lookup(103), nil)
//	sim, _ := simulator.New(40000, common, inverter)
//	_ = sim.Update(func() error { return inverter.Point("W").SetFloat(4200) })
//	go modbus.Serve(ctx, listener, sim)
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
type Simulator struct {
	// mu guards the models' registers, which Update changes and requests read and write.
	mu     sync.Mutex
	base   uint16
	models []placed
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
	for _, p := range points {
		offset := p.Offset() - start
		if err := p.SetRaw(values[offset : offset+int(p.Definition().Size)]); err != nil {
			return err
		}
	}

	return nil
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
