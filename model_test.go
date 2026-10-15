package sunspec_test

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/model"
)

// An inverter's measurements, read through Discover, as the simulator holds them.
func TestModelRead(t *testing.T) {
	t.Parallel()

	common, inverter := newModel(t, 1, nil), newModel(t, 103, nil)
	set(t,
		common.Point("Mn").SetText("Elojah"),
		common.Point("SN").SetText("SN-0001"),
		inverter.Point("A_SF").SetInt(-2),
		inverter.Point("A").SetInt(1234),
		inverter.Point("W_SF").SetInt(1),
		inverter.Point("W").SetInt(420),
		inverter.Point("WH_SF").SetInt(0),
		inverter.Point("WH").SetUint(123456789),
		inverter.Point("St").SetUint(4),
		inverter.Point("Evt1").SetUint(1<<3|1<<7),
	)
	d, _, requests := newDevice(t, common, inverter)

	m := d.Model(103)
	read(t, m)
	if got := requests.sent(); !slices.Equal(got, []request{{modbus.ReadHoldingRegisters, 40070, 52}}) {
		t.Errorf("sent %v, want one read of the 52 registers", got)
	}
	if a, ok := m.Point("A").Float(); !ok || a != 12.34 {
		t.Errorf("A = %v, %t, want 12.34", a, ok)
	}
	if w, ok := m.Point("W").Float(); !ok || w != 4200 {
		t.Errorf("W = %v, %t, want 4200", w, ok)
	}
	if wh, ok := m.Point("WH").Float(); !ok || wh != 123456789 {
		t.Errorf("WH = %v, %t, want 123456789", wh, ok)
	}
	if st, ok := m.Point("St").Symbol(); !ok || st.Name != "MPPT" {
		t.Errorf("St = %v, %t, want MPPT", st, ok)
	}
	flags := make([]string, 0, 2)
	for _, f := range m.Point("Evt1").Flags() {
		flags = append(flags, f.Name)
	}
	if want := []string{"DC_DISCONNECT", "OVER_TEMP"}; !slices.Equal(flags, want) {
		t.Errorf("Evt1's flags = %v, want %v", flags, want)
	}
	// Not implemented, and implemented without its scale factor.
	if v, ok := m.Point("VAr").Float(); ok {
		t.Errorf("VAr = %v, want not implemented", v)
	}
	if v, ok := m.Point("Hz").Float(); ok {
		t.Errorf("Hz = %v, want not implemented", v)
	}

	c := d.Model(1)
	read(t, c)
	if mn, ok := c.Point("Mn").Text(); !ok || mn != "Elojah" {
		t.Errorf("Mn = %q, %t, want Elojah", mn, ok)
	}
	if opt, ok := c.Point("Opt").Text(); ok {
		t.Errorf("Opt = %q, want not implemented", opt)
	}
}

// A model longer than a request reads in several, and its repetitions are laid out from the length or a count point.
func TestModelRead_Repeating(t *testing.T) {
	t.Parallel()

	mppt := newModel(t, 160, map[string]int{"module": 10})
	modules := mppt.Groups("module")
	set(t,
		mppt.Point("DCA_SF").SetInt(-1),
		modules[9].Point("DCA").SetFloat(12.3),
		modules[9].Point("IDStr").SetText("string 10"),
	)
	d, _, requests := newDevice(t, mppt)
	m := d.Model(160)
	read(t, m)

	// 8 registers, then 20 per module, header included.
	if got, want := requests.sent(), []request{{3, 40002, 125}, {3, 40127, 85}}; !slices.Equal(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
	modules = m.Groups("module")
	if len(modules) != 10 {
		t.Fatalf("%d modules, want 10", len(modules))
	}
	last := modules[9]
	if last.Index() != 9 || last.Definition().Name != "module" {
		t.Errorf("last module: index %d, %q", last.Index(), last.Definition().Name)
	}
	if dca, ok := last.Point("DCA").Float(); !ok || dca != 12.3 {
		t.Errorf("module 10's DCA = %v, %t, want 12.3, scaled by the model's DCA_SF", dca, ok)
	}
	if id, _ := last.Point("IDStr").Text(); id != "string 10" {
		t.Errorf("module 10's IDStr = %q", id)
	}
	// After the model's 10 registers, 9 modules, then the module's ID and IDStr.
	if p := last.Point("DCA"); p.Offset() != 10+9*20+9 || p.Address() != 40002+10+9*20+9 {
		t.Errorf("module 10's DCA at offset %d, address %d", p.Offset(), p.Address())
	}
	if points := m.Points(); len(points) != 9+10*10 || points[9].Group() != modules[0] {
		t.Errorf("%d points, want 109, the modules' after the model's", len(points))
	}
}

// Groups counted by points, nested: model 705's curves, each of points.
func TestModelRead_Nested(t *testing.T) {
	t.Parallel()

	voltVar := newModel(t, 705, map[string]int{"Crv": 2, "Pt": 3})
	set(t, voltVar.Point("V_SF").SetInt(-1))
	curves := voltVar.Groups("Crv")
	if len(curves) != 2 || len(curves[1].Groups("Pt")) != 3 {
		t.Fatalf("NewModel laid out %d curves, want 2 of 3 points", len(curves))
	}
	set(t, curves[1].Groups("Pt")[2].Point("V").SetFloat(104.5))
	if n, _ := voltVar.Point("NCrv").Int(); n != 2 {
		t.Errorf("NCrv = %d, want 2", n)
	}

	d, _, _ := newDevice(t, voltVar)
	m := d.Model(705)
	read(t, m)
	pts := m.Groups("Crv")[1].Groups("Pt")
	if len(pts) != 3 {
		t.Fatalf("%d points in curve 2, want 3, as NPt says", len(pts))
	}
	if v, ok := pts[2].Point("V").Float(); !ok || v != 104.5 {
		t.Errorf("curve 2's point 3: V = %v, %t, want 104.5, scaled by the model's V_SF", v, ok)
	}
	if m.Length() != voltVar.Length() {
		t.Errorf("L = %d, want %d", m.Length(), voltVar.Length())
	}
}

// A device's model shorter than its definition, an older version, lacks its last points, and a longer one keeps the
// extra registers.
func TestModelRead_Length(t *testing.T) {
	t.Parallel()

	// Model 1 without Pad, then with 2 registers past it.
	short := memory(40000, append(append([]uint16{0x5375, 0x6E53, 1, 65}, make([]uint16, 65)...), 0xFFFF, 0)...)
	long := memory(40000, append(append([]uint16{0x5375, 0x6E53, 1, 68}, make([]uint16, 68)...), 0xFFFF, 0)...)
	for name, tt := range map[string]struct {
		memory *modbus.Memory
		pad    bool
	}{"short": {short, false}, "long": {long, true}} {
		d, err := sunspec.Discover(t.Context(), modbus.NewClient(modbus.NewLoopback(tt.memory)), 1)
		if err != nil {
			t.Fatal(err)
		}
		m := d.Model(1)
		read(t, m)
		if (m.Point("Pad") != nil) != tt.pad || m.Point("DA") == nil {
			t.Errorf("%s: Pad %v, DA %v", name, m.Point("Pad"), m.Point("DA"))
		}
		if len(m.Registers()) != int(m.Length())+2 {
			t.Errorf("%s: %d registers, want %d", name, len(m.Registers()), m.Length()+2)
		}
	}
}

// A header changed since Discover is an invalid map.
func TestModelRead_Changed(t *testing.T) {
	t.Parallel()

	mem := memory(40000, append(append([]uint16{0x5375, 0x6E53, 1, 66}, make([]uint16, 66)...), 0xFFFF, 0)...)
	d, err := sunspec.Discover(t.Context(), modbus.NewClient(modbus.NewLoopback(mem)), 1)
	if err != nil {
		t.Fatal(err)
	}
	mem.Update(func(tables *modbus.Tables) { tables.HoldingRegisters[40002] = 2 })
	err = d.Model(1).Read(t.Context())
	if !errors.Is(err, sunspec.ErrInvalidMap) {
		t.Fatalf("got %v, want ErrInvalidMap", err)
	}
	if want := "sunspec: invalid map at 40002: model 1 of length 66 is now model 2 of length 66"; err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

// Write sends the runs of registers the setters changed, and nothing else.
func TestModelWrite(t *testing.T) {
	t.Parallel()

	controls := newModel(t, 123, nil)
	set(t, controls.Point("WMaxLimPct_SF").SetInt(-1))
	d, sim, requests := newDevice(t, controls)
	m := d.Model(123)
	read(t, m)
	requests.reset()

	set(t,
		m.Point("WMaxLimPct").SetFloat(55.5),
		m.Point("WMaxLim_Ena").SetUint(1),
		m.Point("Conn").SetUint(1),
	)
	if err := m.Write(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Conn and WMaxLimPct at offsets 4 and 5, in one request, then WMaxLim_Ena at 9.
	want := []request{{modbus.WriteMultipleRegisters, 40006, 2}, {modbus.WriteMultipleRegisters, 40011, 1}}
	if got := requests.sent(); !slices.Equal(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
	err := sim.Update(func() error {
		if pct, _ := controls.Point("WMaxLimPct").Float(); pct != 55.5 {
			t.Errorf("the device's WMaxLimPct = %v, want 55.5", pct)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	requests.reset()
	if err := m.Write(t.Context()); err != nil || len(requests.sent()) != 0 {
		t.Errorf("second Write: %v, sent %v, want nothing", err, requests.sent())
	}
	if err := m.Point("WMaxLimPct_SF").SetInt(0); !errors.Is(err, sunspec.ErrReadOnly) {
		t.Errorf("setting a read-only point: got %v, want ErrReadOnly", err)
	}
}

// Contiguous changes go in one request, as long as WithMaxRegisters allows.
func TestModelWrite_Runs(t *testing.T) {
	t.Parallel()

	_, transport := newSimulator(t, newModel(t, 705, map[string]int{"Crv": 1, "Pt": 4}))
	d, err := sunspec.Discover(t.Context(), modbus.NewClient(transport), 1, sunspec.WithMaxRegisters(3))
	if err != nil {
		t.Fatal(err)
	}
	m := d.Model(705)
	read(t, m)
	transport.reset()
	for _, pt := range m.Groups("Crv")[0].Groups("Pt") {
		set(t, pt.Point("V").SetRaw([]uint16{1000}), pt.Point("Var").SetRaw([]uint16{0xFFF0}))
	}
	if err := m.Write(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := m.Groups("Crv")[0].Groups("Pt")[0].Point("V").Address()
	want := []request{{16, first, 3}, {16, first + 3, 3}, {16, first + 6, 2}}
	if got := transport.sent(); !slices.Equal(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
}

// A failed Write leaves what it did not send changed, for the next.
func TestModelWrite_Error(t *testing.T) {
	t.Parallel()

	d, _, requests := newDevice(t, newModel(t, 123, nil))
	m := d.Model(123)
	read(t, m)
	set(t, m.Point("Conn").SetUint(1), m.Point("WMaxLim_Ena").SetUint(1))

	requests.fail(errInjected)
	if err := m.Write(t.Context()); !errors.Is(err, errInjected) {
		t.Fatalf("got %v, want the transport's error", err)
	}
	requests.fail(nil)
	requests.reset()
	if err := m.Write(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, want := requests.sent(), []request{{16, 40006, 1}, {16, 40011, 1}}; !slices.Equal(got, want) {
		t.Errorf("sent %v, want %v", got, want)
	}
}

func TestModel_NoDevice(t *testing.T) {
	t.Parallel()

	m := newModel(t, 1, nil)
	if err := m.Read(t.Context()); !errors.Is(err, sunspec.ErrNoDevice) {
		t.Errorf("Read: got %v, want ErrNoDevice", err)
	}
	if err := m.Write(t.Context()); !errors.Is(err, sunspec.ErrNoDevice) {
		t.Errorf("Write: got %v, want ErrNoDevice", err)
	}
}

func TestNewModel(t *testing.T) {
	t.Parallel()

	m := newModel(t, 1, nil)
	if m.ID() != 1 || m.Length() != 66 || m.Address() != 0 || m.Definition() != model.Lookup(1) {
		t.Errorf("model %d of length %d at %d", m.ID(), m.Length(), m.Address())
	}
	registers := m.Registers()
	if registers[0] != 1 || registers[1] != 66 {
		t.Errorf("header %v, want [1 66]", registers[:2])
	}
	for _, p := range m.Points()[2:] {
		if p.Implemented() {
			t.Errorf("%s is implemented", p.Name())
		}
	}
	// DA, a uint16, and Pad.
	if got := registers[66:]; !slices.Equal(got, []uint16{0xFFFF, 0}) {
		t.Errorf("DA and Pad = %04X, want FFFF 0000", got)
	}

	if _, err := sunspec.NewModel(model.Lookup(160), map[string]int{"module": -1}); !errors.Is(err, sunspec.ErrOutOfRange) {
		t.Errorf("negative repeats: got %v, want ErrOutOfRange", err)
	}
	if _, err := sunspec.NewModel(model.Lookup(160), map[string]int{"module": math.MaxUint16/20 + 1}); !errors.Is(err, sunspec.ErrOutOfRange) {
		t.Errorf("past 65535 registers: got %v, want ErrOutOfRange", err)
	}
}
