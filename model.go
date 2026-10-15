package sunspec

import (
	"context"
	"fmt"
	"math"

	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/model"
)

// Model is a model of a device: its header, and its registers as last read, laid out along its definition. It is not
// safe for concurrent use.
//
// A model found by Discover holds no registers, nor points, until its first Read. The registers it holds are a
// snapshot: setters change them, and Write sends what they changed to the device.
type Model struct {
	id      uint16
	address uint16
	length  uint16

	definition *model.Definition
	// device is the device the model is read from and written to, nil for a model from NewModel.
	device *Device

	// registers holds the model's registers, header included, so that registers[0] is ID and registers[1] is L: a
	// point's offset is its index here.
	registers []uint16
	// dirty marks the registers the setters changed since the last Read or Write.
	dirty []bool
	// root is the model's own group, laid out on registers: nil until the first Read.
	root *Group
}

// NewModel returns a model of def, held in memory rather than read from a device: for a simulator, a test, or to encode
// registers. repeats gives each group that repeats a variable number of times its count, by group name, 0 when
// missing: model 160's "module", model 705's "Crv" and "Pt".
//
// Every point holds the value meaning not implemented, except the header and the count points. Its setters change any
// point, writable or not.
func NewModel(def *model.Definition, repeats map[string]int) (*Model, error) {
	for name, n := range repeats {
		if n < 0 || n > math.MaxUint16-1 {
			return nil, fmt.Errorf("%w: model %d repeats %s %d times", ErrOutOfRange, def.ID, name, n)
		}
	}
	n := size(&def.Group, repeats)
	if n > math.MaxUint16 {
		return nil, fmt.Errorf("%w: model %d of %d registers, past 65535", ErrOutOfRange, def.ID, n)
	}

	m := &Model{
		id:         def.ID,
		length:     uint16(n - 2), //nolint:gosec // checked above, and every definition has its header
		definition: def,
		registers:  make([]uint16, n),
		dirty:      make([]bool, n),
	}
	m.root = m.layout(repeats)
	for _, p := range m.Points() {
		setUnimplemented(p.definition.Type, p.registers())
	}
	m.registers[0], m.registers[1] = m.id, m.length
	m.setCounts(m.root, repeats)
	// A client lays the model out from its count points: they must give the layout repeats did, which they do not
	// when groups of different names, counted by the same point, are given different counts.
	if relaid := m.layout(nil); len(relaid.appendPoints(nil)) != len(m.Points()) {
		return nil, fmt.Errorf("%w: model %d: groups counted by the same point repeat differently", ErrOutOfRange, def.ID)
	}

	return m, nil
}

// ID returns the model's ID: 1 for the common model, 103 for a three-phase inverter.
func (m *Model) ID() uint16 {
	return m.id
}

// Address returns the address of the model's first register, ID, among the device's holding registers: 0 for a model
// from NewModel.
func (m *Model) Address() uint16 {
	return m.address
}

// Length returns the model's length, L: how many registers follow its header.
func (m *Model) Length() uint16 {
	return m.length
}

// Definition returns the model's definition, or nil for a model neither SunSpec nor WithDefinitions defines, of which
// only Registers is known.
func (m *Model) Definition() *model.Definition {
	return m.definition
}

// Read reads the model's registers from the device, and lays its points out again, so that points and groups got
// before it are stale: get them again. It discards what the setters changed and Write did not send.
//
// A model longer than WithMaxRegisters, 125 by default, takes several requests, so that a value and its scale factor
// may come from different moments. SunSpec does not say how devices should avoid it; most update their registers far
// less often than they are read.
func (m *Model) Read(ctx context.Context) error {
	d := m.device
	if d == nil {
		return ErrNoDevice
	}
	total := int(m.length) + 2
	registers := make([]uint16, 0, total)
	for offset := 0; offset < total; {
		n := min(total-offset, d.config.maxRead)
		chunk, err := d.client.ReadRegisters(ctx, d.unit, modbus.HoldingRegisters, m.address+uint16(offset), uint16(n)) //nolint:gosec // within the model
		if err != nil {
			return err
		}
		registers = append(registers, chunk...)
		offset += n
	}
	if registers[0] != m.id || registers[1] != m.length {
		return &MapError{Address: m.address, Reason: fmt.Sprintf("model %d of length %d is now model %d of length %d", m.id, m.length, registers[0], registers[1])}
	}

	m.registers = registers
	m.dirty = make([]bool, total)
	// The layout changes only with a count point: a model without one keeps its first, and its points, to save
	// allocating them at each Read.
	if m.definition != nil && (m.root == nil || counted(&m.definition.Group)) {
		m.root = m.layout(nil)
	}

	return nil
}

// Write sends the registers the setters changed since the last Read or Write, each run of them in as few
// WriteMultipleRegisters requests as WithMaxRegisters allows, 123 registers each by default. If a request fails, it
// returns its error, and the registers not sent yet stay changed, for the next Write.
func (m *Model) Write(ctx context.Context) error {
	d := m.device
	if d == nil {
		return ErrNoDevice
	}
	for start := 0; start < len(m.dirty); {
		if !m.dirty[start] {
			start++

			continue
		}
		end := start + 1
		for end < len(m.dirty) && m.dirty[end] && end-start < d.config.maxWrite {
			end++
		}
		if err := d.client.WriteRegisters(ctx, d.unit, m.address+uint16(start), m.registers[start:end]); err != nil {
			return err
		}
		clear(m.dirty[start:end])
		start = end
	}

	return nil
}

// Registers returns a copy of the model's registers, header included, as last read or set: none before a model from
// Discover is read.
func (m *Model) Registers() []uint16 {
	return append([]uint16(nil), m.registers...)
}

// Point returns the model's point named name, among its own group's, or nil if it has none: the definition has no such
// point, the device's version of the model ends before it, or the model has not been read yet. The getters of a nil
// *Point report a value that is not there, so that m.Point("W").Float() needs no check.
func (m *Model) Point(name string) *Point {
	return m.root.Point(name)
}

// Groups returns the repetitions of the model's group named name, among its own group's: model 160's "module".
func (m *Model) Groups(name string) []*Group {
	return m.root.Groups(name)
}

// Points returns every point of the model, its groups' included, in register order.
func (m *Model) Points() []*Point {
	if m.root == nil {
		return nil
	}

	return m.root.appendPoints(nil)
}

// layout lays the model's registers out along its definition, with the counts repeats gives, or else the count points.
func (m *Model) layout(repeats map[string]int) *Group {
	l := layout{m: m, repeats: repeats}

	return l.group(&m.definition.Group, nil, 0)
}

// setCounts sets each count point of g and its groups to the count repeats gives.
func (m *Model) setCounts(g *Group, repeats map[string]int) {
	for i, sub := range g.definition.Groups {
		if sub.CountPoint != "" {
			p := g.find(sub.CountPoint)
			encodeUint(p.registers(), uint64(repeats[sub.Name])) //nolint:gosec // NewModel checked every count
		}
		if i < len(g.groups) {
			for _, repetition := range g.groups[i] {
				m.setCounts(repetition, repeats)
			}
		}
	}
}
