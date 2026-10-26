package simulator_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// A vendor's model, with writable points of 1 and 2 registers, and a read-only one.
const vendor = `{"id": 64990, "group": {"name": "vendor", "points": [
	{"name": "ID", "type": "uint16", "size": 1}, {"name": "L", "type": "uint16", "size": 1},
	{"name": "Small", "type": "uint16", "size": 1, "access": "RW"},
	{"name": "Big", "type": "uint32", "size": 2, "access": "RW"},
	{"name": "RO", "type": "uint16", "size": 1}
]}}`

func newModel(t *testing.T, def *model.Definition) *sunspec.Model {
	t.Helper()

	m, err := sunspec.NewModel(def, nil)
	if err != nil {
		t.Fatal(err)
	}

	return m
}

func newVendor(t *testing.T) *sunspec.Model {
	t.Helper()

	def, err := model.Parse(strings.NewReader(vendor))
	if err != nil {
		t.Fatal(err)
	}

	return newModel(t, def)
}

// newSimulator returns a simulator serving models from 40000, and a client to it.
func newSimulator(t *testing.T, models ...*sunspec.Model) (*simulator.Simulator, *modbus.Client) {
	t.Helper()

	sim, err := simulator.New(40000, models...)
	if err != nil {
		t.Fatal(err)
	}

	return sim, modbus.NewClient(modbus.NewLoopback(sim))
}

// The marker, each model, then the end header, and nothing around them.
func TestSimulator_Layout(t *testing.T) {
	t.Parallel()

	_, c := newSimulator(t, newVendor(t), newVendor(t))
	got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 40000, 16)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{
		0x5375, 0x6E53,
		// The header, then Small, Big and RO not implemented.
		64990, 4, 0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF,
		64990, 4, 0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF,
		0xFFFF, 0,
	}
	if !slices.Equal(got, want) {
		t.Errorf("registers %04X, want %04X", got, want)
	}

	for _, r := range []struct{ address, quantity uint16 }{{39999, 2}, {40014, 3}, {40016, 1}} {
		if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, r.address, r.quantity); !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Errorf("read %d from %d: got %v, want ErrIllegalDataAddress", r.quantity, r.address, err)
		}
	}
	if _, err := c.ReadRegisters(t.Context(), 1, modbus.InputRegisters, 40000, 2); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("read input registers: got %v, want ErrIllegalDataAddress", err)
	}
	if _, err := c.ReadBits(t.Context(), 1, modbus.Coils, 0, 1); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("read coils: got %v, want ErrIllegalDataAddress", err)
	}
	if err := c.WriteCoil(t.Context(), 1, 0, true); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("write a coil: got %v, want ErrIllegalDataAddress", err)
	}
}

// A write of whole writable points of one model goes through, and any other changes nothing.
func TestSimulator_Write(t *testing.T) {
	t.Parallel()

	first, second := newVendor(t), newVendor(t)
	sim, c := newSimulator(t, first, second)
	// first's Small at 40004, Big at 40005 and 40006, RO at 40007; second's header at 40008.
	if err := c.WriteRegisters(t.Context(), 1, 40004, []uint16{1, 0, 2}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteRegister(t.Context(), 1, 40004, 3); err != nil {
		t.Fatal(err)
	}
	err := sim.Update(func() error {
		small, _ := first.Point("Small").Uint()
		big, _ := first.Point("Big").Uint()
		if small != 3 || big != 2 {
			t.Errorf("Small = %d and Big = %d, want 3 and 2", small, big)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	refused := []struct {
		name    string
		address uint16
		values  []uint16
	}{
		{"header", 40002, []uint16{64990}},
		{"read-only", 40007, []uint16{1}},
		{"half a uint32", 40005, []uint16{1}},
		{"the other half", 40006, []uint16{1}},
		{"into the read-only", 40005, []uint16{0, 1, 1}},
		{"across models", 40007, []uint16{1, 64990}},
		{"marker", 40000, []uint16{0}},
		{"end header", 40014, []uint16{0}},
	}
	for _, r := range refused {
		if err := c.WriteRegisters(t.Context(), 1, r.address, r.values); !errors.Is(err, modbus.ErrIllegalDataAddress) {
			t.Errorf("%s: got %v, want ErrIllegalDataAddress", r.name, err)
		}
	}
	got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 40004, 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{3, 0, 2, 0xFFFF}; !slices.Equal(got, want) {
		t.Errorf("first's points = %v, want %v, unchanged by the refused writes", got, want)
	}
}

// A client reading while Update changes a value and its scale factor together never sees one without the other.
func TestSimulator_Update(t *testing.T) {
	t.Parallel()

	inverter := newModel(t, model.Lookup(103))
	sim, c := newSimulator(t, inverter)
	d, err := sunspec.Discover(t.Context(), c, 1)
	if err != nil {
		t.Fatal(err)
	}
	m := d.Model(103)

	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 200 {
			// 1000 W, as 1000 × 10^0, 100 × 10^1 or 10 × 10^2.
			sf := int64(i % 3)
			err := sim.Update(func() error {
				if err := inverter.Point("W_SF").SetInt(sf); err != nil {
					return err
				}

				return inverter.Point("W").SetInt(1000 / int64([]int{1, 10, 100}[sf]))
			})
			if err != nil {
				t.Error(err)

				return
			}
		}
	})
	for range 200 {
		if err := m.Read(t.Context()); err != nil {
			t.Fatal(err)
		}
		if w, ok := m.Point("W").Float(); ok && w != 1000 {
			t.Fatalf("W = %v, want 1000", w)
		}
	}
	wg.Wait()

	if err := sim.Update(func() error { return errUpdate }); !errors.Is(err, errUpdate) {
		t.Errorf("Update returned %v, want f's error", err)
	}
}

var errUpdate = errors.New("update failed")

func TestNew_TooLarge(t *testing.T) {
	t.Parallel()

	if _, err := simulator.New(65500, newModel(t, model.Lookup(1))); !errors.Is(err, simulator.ErrTooLarge) {
		t.Errorf("got %v, want ErrTooLarge", err)
	}
}

// OnWrite's hooks run in order after a write, with the model and the points written, and one failing undoes the write,
// and what the hooks changed, and answers its error.
func TestSimulator_OnWrite(t *testing.T) {
	t.Parallel()

	first, second := newVendor(t), newVendor(t)
	sim, c := newSimulator(t, first, second)
	var calls []string
	sim.OnWrite(func(m *sunspec.Model, written []*sunspec.Point) error {
		names := make([]string, 0, len(written))
		for _, p := range written {
			names = append(names, p.Name())
		}
		calls = append(calls, strings.Join(names, ","))
		if m != first {
			t.Errorf("hook got model %p, want first's", m)
		}

		// RO follows Small, as a measurement follows its setting.
		small, _ := m.Point("Small").Uint()

		return m.Point("RO").SetUint(small * 10)
	})
	sim.OnWrite(func(m *sunspec.Model, _ []*sunspec.Point) error {
		calls = append(calls, "second hook")
		if small, _ := m.Point("Small").Uint(); small > 100 {
			return modbus.ErrIllegalDataValue
		}

		return nil
	})

	// first's Small at 40004, Big at 40005 and 40006, RO at 40007.
	if err := c.WriteRegisters(t.Context(), 1, 40004, []uint16{5, 0, 2}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteRegisters(t.Context(), 1, 40004, []uint16{101}); !errors.Is(err, modbus.ErrIllegalDataValue) {
		t.Errorf("refused write: got %v, want ErrIllegalDataValue", err)
	}
	if want := []string{"Small,Big", "second hook", "Small", "second hook"}; !slices.Equal(calls, want) {
		t.Errorf("calls %q, want %q", calls, want)
	}
	got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 40004, 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{5, 0, 2, 50}; !slices.Equal(got, want) {
		t.Errorf("first's points = %v, want %v, as before the refused write", got, want)
	}
}

// OnRead's hooks run before each read, which answers the first error one returns.
func TestSimulator_OnRead(t *testing.T) {
	t.Parallel()

	m := newVendor(t)
	sim, c := newSimulator(t, m)
	var (
		reads int
		fail  error
	)
	sim.OnRead(func() error {
		reads++

		return m.Point("RO").SetUint(uint64(reads)) //nolint:gosec // a few reads
	})
	sim.OnRead(func() error { return fail })

	for want := range uint16(3) {
		got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 40007, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] != want+1 {
			t.Errorf("read %d: RO = %d, want %d", want, got[0], want+1)
		}
	}

	fail = modbus.ErrServerDeviceBusy
	if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 40007, 1); !errors.Is(err, modbus.ErrServerDeviceBusy) {
		t.Errorf("got %v, want ErrServerDeviceBusy", err)
	}

	fail = modbus.ErrNoResponse
	silent := modbus.NewClient(modbus.NewLoopback(sim, modbus.WithTimeout(10*time.Millisecond)))
	if _, err := silent.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 40007, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want context.DeadlineExceeded", err)
	}
}
