package sunspec_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/model"
)

// Discover finds each model's header after the marker, without reading the models.
func TestDiscover(t *testing.T) {
	t.Parallel()

	d, _, requests := newDevice(t, newModel(t, 1, nil), newModel(t, 103, nil), newModel(t, 160, map[string]int{"module": 2}))
	if d.Base() != 40000 {
		t.Errorf("Base() = %d, want 40000", d.Base())
	}
	type header struct{ id, address, length uint16 }
	got := make([]header, 0, len(d.Models()))
	for _, m := range d.Models() {
		got = append(got, header{m.ID(), m.Address(), m.Length()})
	}
	// The marker takes 40000 and 40001, model 1 66 registers after its header, model 103 50, model 160 8 then 20 per
	// module.
	want := []header{{1, 40002, 66}, {103, 40070, 50}, {160, 40122, 48}}
	if !slices.Equal(got, want) {
		t.Errorf("models %v, want %v", got, want)
	}
	if m := d.Model(103); m == nil || m.Address() != 40070 || m.Definition() != model.Lookup(103) {
		t.Errorf("Model(103) = %v", m)
	}
	if m := d.Model(999); m != nil {
		t.Errorf("Model(999) = %v, want nil", m)
	}
	if m := d.Model(1); m.Point("Mn") != nil || m.Registers() != nil {
		t.Error("a model has points or registers before its first Read")
	}
	if sent := requests.sent(); len(sent) != 0 {
		t.Errorf("Discover's requests were not reset: %v", sent)
	}
}

// Discover tries the base addresses in order, moving on from one answering an exception or no marker.
func TestDiscover_Bases(t *testing.T) {
	t.Parallel()

	for _, base := range []uint16{40000, 50000, 0} {
		marked := memory(base, 0x5375, 0x6E53, 0xFFFF, 0)
		d, err := sunspec.Discover(t.Context(), modbus.NewClient(modbus.NewLoopback(marked)), 1)
		if err != nil {
			t.Fatalf("base %d: %v", base, err)
		}
		if d.Base() != base || len(d.Models()) != 0 {
			t.Errorf("base %d: found base %d and %d models", base, d.Base(), len(d.Models()))
		}
	}

	// Registers up to 40001 only, so that 50000 answers IllegalDataAddress.
	short := modbus.NewMemory(modbus.Tables{HoldingRegisters: make([]uint16, 40002)})
	short.Update(func(tables *modbus.Tables) { tables.HoldingRegisters[0], tables.HoldingRegisters[1] = 0x5375, 0x6E53 })
	d, err := sunspec.Discover(t.Context(), modbus.NewClient(modbus.NewLoopback(short)), 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Base() != 0 {
		t.Errorf("found base %d, want 0, past 40000 without the marker and 50000 answering an exception", d.Base())
	}

	_, err = sunspec.Discover(t.Context(), modbus.NewClient(modbus.NewLoopback(memory(0))), 1, sunspec.WithBases(1, 2))
	if !errors.Is(err, sunspec.ErrNotSunSpec) || !strings.Contains(err.Error(), "[1 2]") {
		t.Errorf("without a marker: got %v, want ErrNotSunSpec naming the bases", err)
	}
}

// A transport error stops Discover at once.
func TestDiscover_TransportError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	transport := &counter{Transport: modbus.NewLoopback(memory(0))}
	if _, err := sunspec.Discover(ctx, modbus.NewClient(transport), 1); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if n := len(transport.sent()); n != 1 {
		t.Errorf("sent %d requests, want 1", n)
	}
}

// An exception where a header should be ends the models, as some devices answer past their last.
func TestDiscover_ExceptionEnd(t *testing.T) {
	t.Parallel()

	// The marker, then model 1 whose 66 registers end the holding registers.
	registers := make([]uint16, 40000+2+2+66)
	copy(registers[40000:], []uint16{0x5375, 0x6E53, 1, 66})
	m := modbus.NewMemory(modbus.Tables{HoldingRegisters: registers})
	d, err := sunspec.Discover(t.Context(), modbus.NewClient(modbus.NewLoopback(m)), 1)
	if err != nil {
		t.Fatal(err)
	}
	if models := d.Models(); len(models) != 1 || models[0].ID() != 1 {
		t.Errorf("found %d models, want model 1", len(models))
	}
}

// A model past address 65535 is an invalid map.
func TestDiscover_PastEnd(t *testing.T) {
	t.Parallel()

	tests := map[string]*modbus.Memory{
		"model":         memory(60000, 0x5375, 0x6E53, 1, 6000),
		"no end header": memory(65530, 0x5375, 0x6E53, 1, 0, 1, 0),
	}
	for name, m := range tests {
		_, err := sunspec.Discover(t.Context(), modbus.NewClient(modbus.NewLoopback(m)), 1, sunspec.WithBases(60000, 65530))
		if !errors.Is(err, sunspec.ErrInvalidMap) {
			t.Errorf("%s: got %v, want ErrInvalidMap", name, err)
		}
	}
}

// A model SunSpec does not define has registers, but no points, unless WithDefinitions defines it.
func TestDiscover_UnknownModel(t *testing.T) {
	t.Parallel()

	m := memory(40000, 0x5375, 0x6E53, 64999, 3, 1, 2, 3, 0xFFFF, 0)
	d, err := sunspec.Discover(t.Context(), modbus.NewClient(modbus.NewLoopback(m)), 1)
	if err != nil {
		t.Fatal(err)
	}
	unknown := d.Model(64999)
	read(t, unknown)
	if unknown.Definition() != nil || unknown.Point("ID") != nil || unknown.Points() != nil {
		t.Errorf("an unknown model has a definition or points")
	}
	if got, want := unknown.Registers(), []uint16{64999, 3, 1, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("Registers() = %v, want %v", got, want)
	}

	def, err := model.Parse(strings.NewReader(`{"id": 64999, "group": {"name": "vendor", "points": [
		{"name": "ID", "type": "uint16", "size": 1}, {"name": "L", "type": "uint16", "size": 1},
		{"name": "A", "type": "uint16", "size": 1}, {"name": "B", "type": "uint32", "size": 2}
	]}}`))
	if err != nil {
		t.Fatal(err)
	}
	d, err = sunspec.Discover(t.Context(), modbus.NewClient(modbus.NewLoopback(m)), 1, sunspec.WithDefinitions(def))
	if err != nil {
		t.Fatal(err)
	}
	vendor := d.Model(64999)
	read(t, vendor)
	if b, ok := vendor.Point("B").Uint(); !ok || b != 2<<16|3 {
		t.Errorf("B = %d, %t, want %d", b, ok, 2<<16|3)
	}
}

func TestWithMaxRegisters(t *testing.T) {
	t.Parallel()

	_, transport := newSimulator(t, newModel(t, 103, nil))
	d, err := sunspec.Discover(t.Context(), modbus.NewClient(transport), 1, sunspec.WithMaxRegisters(20))
	if err != nil {
		t.Fatal(err)
	}
	transport.reset()
	read(t, d.Model(103))
	// 52 registers, header included, 20 at a time.
	want := []request{{3, 40002, 20}, {3, 40022, 20}, {3, 40042, 12}}
	if got := transport.sent(); !slices.Equal(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
}
