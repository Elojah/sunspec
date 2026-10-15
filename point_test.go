package sunspec_test

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/model"
)

// A model with a point of each type.
const allTypes = `{"id": 64998, "group": {"name": "types", "points": [
	{"name": "ID", "type": "uint16", "size": 1}, {"name": "L", "type": "uint16", "size": 1},
	{"name": "SF", "type": "sunssf", "size": 1},
	{"name": "I16", "type": "int16", "size": 1, "sf": "SF", "units": "W"},
	{"name": "I32", "type": "int32", "size": 2},
	{"name": "I64", "type": "int64", "size": 4},
	{"name": "U16", "type": "uint16", "size": 1},
	{"name": "U32", "type": "uint32", "size": 2, "sf": -2},
	{"name": "U64", "type": "uint64", "size": 4},
	{"name": "A16", "type": "acc16", "size": 1},
	{"name": "A32", "type": "acc32", "size": 2},
	{"name": "A64", "type": "acc64", "size": 4},
	{"name": "C", "type": "count", "size": 1},
	{"name": "E16", "type": "enum16", "size": 1, "symbols": [{"name": "OFF", "value": 0}, {"name": "ON", "value": 1}]},
	{"name": "E32", "type": "enum32", "size": 2},
	{"name": "B16", "type": "bitfield16", "size": 1, "symbols": [{"name": "A", "value": 0}, {"name": "C", "value": 2}]},
	{"name": "B32", "type": "bitfield32", "size": 2, "symbols": [{"name": "HIGH", "value": 31}]},
	{"name": "F32", "type": "float32", "size": 2},
	{"name": "F64", "type": "float64", "size": 4},
	{"name": "S", "type": "string", "size": 4},
	{"name": "P", "type": "pad", "size": 1},
	{"name": "IP", "type": "ipaddr", "size": 2},
	{"name": "IP6", "type": "ipv6addr", "size": 8},
	{"name": "MAC", "type": "eui48", "size": 4}
]}}`

func newAllTypes(t *testing.T) *sunspec.Model {
	t.Helper()

	def, err := model.Parse(strings.NewReader(allTypes))
	if err != nil {
		t.Fatal(err)
	}
	m, err := sunspec.NewModel(def, nil)
	if err != nil {
		t.Fatal(err)
	}

	return m
}

// Each type has its value meaning not implemented, which no getter reports as a value.
func TestPoint_Unimplemented(t *testing.T) {
	t.Parallel()

	want := map[string][]uint16{
		"SF":  {0x8000},
		"I16": {0x8000},
		"I32": {0x8000, 0},
		"I64": {0x8000, 0, 0, 0},
		"U16": {0xFFFF},
		"U32": {0xFFFF, 0xFFFF},
		"U64": {0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF},
		"A16": {0},
		"A32": {0, 0},
		"A64": {0, 0, 0, 0},
		"C":   {0xFFFF},
		"E16": {0xFFFF},
		"E32": {0xFFFF, 0xFFFF},
		"B16": {0xFFFF},
		"B32": {0xFFFF, 0xFFFF},
		"F32": {0x7FC0, 0},
		"F64": {0x7FF8, 0, 0, 0},
		"S":   {0, 0, 0, 0},
		"P":   {0},
		"IP":  {0, 0},
		"IP6": {0, 0, 0, 0, 0, 0, 0, 0},
		"MAC": {0, 0xFFFF, 0xFFFF, 0xFFFF},
	}
	m := newAllTypes(t)
	for name, raw := range want {
		p := m.Point(name)
		if got := p.Raw(); !slices.Equal(got, raw) {
			t.Errorf("%s = %04X, want %04X", name, got, raw)
		}
		if p.Implemented() {
			t.Errorf("%s is implemented", name)
		}
		if _, ok := p.Int(); ok {
			t.Errorf("%s has an Int", name)
		}
		if _, ok := p.Float(); ok {
			t.Errorf("%s has a Float", name)
		}
		if _, ok := p.Text(); ok {
			t.Errorf("%s has a Text", name)
		}
		if p.String() != name+": not implemented" {
			t.Errorf("%s.String() = %q", name, p)
		}
	}
}

// Each type encodes and decodes as SunSpec lays it out: high word first, strings and addresses high byte first.
func TestPoint_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		point string
		set   func(p *sunspec.Point) error
		raw   []uint16
		// want is the point's String, which reads it with the getter its type has.
		want string
	}{
		{"SF", func(p *sunspec.Point) error { return p.SetInt(-3) }, []uint16{0xFFFD}, "SF: -3"},
		{"I32", func(p *sunspec.Point) error { return p.SetInt(-70000) }, []uint16{0xFFFE, 0xEE90}, "I32: -70000"},
		{"I64", func(p *sunspec.Point) error { return p.SetInt(-1) }, []uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF}, "I64: -1"},
		{"U16", func(p *sunspec.Point) error { return p.SetUint(65534) }, []uint16{0xFFFE}, "U16: 65534"},
		{"U32", func(p *sunspec.Point) error { return p.SetUint(0x12345678) }, []uint16{0x1234, 0x5678}, "U32: 3054198.96"},
		{"U64", func(p *sunspec.Point) error { return p.SetUint(1 << 63) }, []uint16{0x8000, 0, 0, 0}, "U64: 9223372036854775808"},
		{"A16", func(p *sunspec.Point) error { return p.SetUint(7) }, []uint16{7}, "A16: 7"},
		{"A32", func(p *sunspec.Point) error { return p.SetUint(0) }, []uint16{0, 0}, "A32: not implemented"},
		{"C", func(p *sunspec.Point) error { return p.SetInt(3) }, []uint16{3}, "C: 3"},
		{"E16", func(p *sunspec.Point) error { return p.SetUint(1) }, []uint16{1}, "E16: ON"},
		{"E32", func(p *sunspec.Point) error { return p.SetUint(7) }, []uint16{0, 7}, "E32: 7"},
		{"B16", func(p *sunspec.Point) error { return p.SetUint(0b111) }, []uint16{7}, "B16: [A C]"},
		{"B32", func(p *sunspec.Point) error { return p.SetUint(1 << 31) }, []uint16{0x8000, 0}, "B32: [HIGH]"},
		{"F32", func(p *sunspec.Point) error { return p.SetFloat(1.5) }, []uint16{0x3FC0, 0}, "F32: 1.5"},
		{"F64", func(p *sunspec.Point) error { return p.SetFloat(-2.25) }, []uint16{0xC002, 0, 0, 0}, "F64: -2.25"},
		{"S", func(p *sunspec.Point) error { return p.SetText("abcdefg") }, []uint16{0x6162, 0x6364, 0x6566, 0x6700}, `S: "abcdefg"`},
		{"S", func(p *sunspec.Point) error { return p.SetRaw([]uint16{0x0041, 0x4243, 0, 0}) }, []uint16{0x0041, 0x4243, 0, 0}, "S: not implemented"},
		{"S", func(p *sunspec.Point) error { return p.SetRaw([]uint16{0x4100, 0x4243, 0, 0}) }, []uint16{0x4100, 0x4243, 0, 0}, `S: "A"`},
		{"IP", func(p *sunspec.Point) error { return p.SetText("192.168.1.10") }, []uint16{0xC0A8, 0x010A}, `IP: "192.168.1.10"`},
		{"IP6", func(p *sunspec.Point) error { return p.SetText("fe80::1") }, []uint16{0xFE80, 0, 0, 0, 0, 0, 0, 1}, `IP6: "fe80::1"`},
		{"MAC", func(p *sunspec.Point) error { return p.SetText("00-11-22-aa-bb-cc") }, []uint16{0, 0x0011, 0x22AA, 0xBBCC}, `MAC: "00:11:22:AA:BB:CC"`},
		{"P", func(p *sunspec.Point) error { return p.SetRaw([]uint16{0x1234}) }, []uint16{0x1234}, "P: not implemented"},
	}
	for _, tt := range tests {
		t.Run(tt.point, func(t *testing.T) {
			t.Parallel()

			p := newAllTypes(t).Point(tt.point)
			if err := tt.set(p); err != nil {
				t.Fatal(err)
			}
			if got := p.Raw(); !slices.Equal(got, tt.raw) {
				t.Errorf("registers %04X, want %04X", got, tt.raw)
			}
			if got := p.String(); got != tt.want {
				t.Errorf("reads back as %q, want %q", got, tt.want)
			}
			if err := p.SetUnimplemented(); err != nil || p.Implemented() {
				t.Errorf("SetUnimplemented: %v, still implemented: %t", err, p.Implemented())
			}
		})
	}
}

// Each getter reads only the types it is for.
func TestPoint_Getters(t *testing.T) {
	t.Parallel()

	m := newAllTypes(t)
	set(t, m.Point("U64").SetUint(1<<63), m.Point("E16").SetUint(1), m.Point("E32").SetUint(7), m.Point("I32").SetInt(-1), m.Point("S").SetText("a"))
	if _, ok := m.Point("U64").Int(); ok {
		t.Error("Int of a uint64 past math.MaxInt64")
	}
	if _, ok := m.Point("E16").Float(); ok {
		t.Error("Float of an enum")
	}
	if _, ok := m.Point("E32").Symbol(); ok {
		t.Error("Symbol of a value without one")
	}
	if _, ok := m.Point("I32").Uint(); ok {
		t.Error("Uint of an int32")
	}
	if _, ok := m.Point("S").Int(); ok {
		t.Error("Int of a string")
	}
	if _, ok := m.Point("I32").Text(); ok {
		t.Error("Text of an int32")
	}
	if _, ok := m.Point("I32").Symbol(); ok {
		t.Error("Symbol of an int32")
	}
	if m.Point("E16").Flags() != nil {
		t.Error("Flags of an enum")
	}
}

// A setter refuses a value its point's type cannot hold, or of the wrong kind, and changes nothing.
func TestPoint_SetErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		point string
		set   func(p *sunspec.Point) error
		want  error
	}{
		{"int16's not implemented", "I16", func(p *sunspec.Point) error { return p.SetInt(-32768) }, sunspec.ErrOutOfRange},
		{"int16 overflow", "I16", func(p *sunspec.Point) error { return p.SetInt(32768) }, sunspec.ErrOutOfRange},
		{"int32 overflow", "I32", func(p *sunspec.Point) error { return p.SetInt(1 << 31) }, sunspec.ErrOutOfRange},
		{"uint16's not implemented", "U16", func(p *sunspec.Point) error { return p.SetUint(65535) }, sunspec.ErrOutOfRange},
		{"negative uint16", "U16", func(p *sunspec.Point) error { return p.SetInt(-1) }, sunspec.ErrOutOfRange},
		{"uint32 overflow", "U32", func(p *sunspec.Point) error { return p.SetUint(1 << 32) }, sunspec.ErrOutOfRange},
		{"scale factor", "SF", func(p *sunspec.Point) error { return p.SetInt(11) }, sunspec.ErrOutOfRange},
		{"enum's not implemented", "E16", func(p *sunspec.Point) error { return p.SetUint(0xFFFF) }, sunspec.ErrOutOfRange},
		{"NaN", "F32", func(p *sunspec.Point) error { return p.SetFloat(math.NaN()) }, sunspec.ErrOutOfRange},
		{"float32 overflow", "F32", func(p *sunspec.Point) error { return p.SetFloat(1e39) }, sunspec.ErrOutOfRange},
		{"scaled overflow", "U32", func(p *sunspec.Point) error { return p.SetFloat(1e9) }, sunspec.ErrOutOfRange},
		{"negative scaled uint", "U32", func(p *sunspec.Point) error { return p.SetFloat(-1) }, sunspec.ErrOutOfRange},
		{"long string", "S", func(p *sunspec.Point) error { return p.SetText("abcdefghi") }, sunspec.ErrOutOfRange},
		{"IPv6 for IPv4", "IP", func(p *sunspec.Point) error { return p.SetText("fe80::1") }, sunspec.ErrOutOfRange},
		{"IPv4 for IPv6", "IP6", func(p *sunspec.Point) error { return p.SetText("10.0.0.1") }, sunspec.ErrOutOfRange},
		{"short EUI-48", "MAC", func(p *sunspec.Point) error { return p.SetText("00:11") }, sunspec.ErrOutOfRange},
		{"bad EUI-48", "MAC", func(p *sunspec.Point) error { return p.SetText("00:11:22:33:44:5G") }, sunspec.ErrOutOfRange},
		{"raw length", "I32", func(p *sunspec.Point) error { return p.SetRaw([]uint16{1}) }, sunspec.ErrOutOfRange},
		{"text for an int", "I16", func(p *sunspec.Point) error { return p.SetText("1") }, sunspec.ErrType},
		{"int for a string", "S", func(p *sunspec.Point) error { return p.SetInt(1) }, sunspec.ErrType},
		{"int for a float", "F32", func(p *sunspec.Point) error { return p.SetInt(1) }, sunspec.ErrType},
		{"uint for an int", "I16", func(p *sunspec.Point) error { return p.SetUint(1) }, sunspec.ErrType},
		{"float for an enum", "E16", func(p *sunspec.Point) error { return p.SetFloat(1) }, sunspec.ErrType},
		{"float for a string", "S", func(p *sunspec.Point) error { return p.SetFloat(1) }, sunspec.ErrType},
		{"scale factor not implemented", "I16", func(p *sunspec.Point) error { return p.SetFloat(1) }, sunspec.ErrNotImplemented},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := newAllTypes(t).Point(tt.point)
			before := p.Raw()
			err := tt.set(p)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if e, ok := errors.AsType[*sunspec.PointError](err); !ok || e.Model != 64998 || e.Point != tt.point {
				t.Errorf("got %#v, want a *PointError for %s of model 64998", err, tt.point)
			}
			if want := tt.want.Error() + ", for point " + tt.point + " of model 64998"; err.Error() != want {
				t.Errorf("got %q, want %q", err, want)
			}
			if !slices.Equal(p.Raw(), before) {
				t.Errorf("registers changed from %04X to %04X", before, p.Raw())
			}
		})
	}
}

// Values scale by their scale factor, dividing for a negative one so that the float is the nearest to the value meant,
// and setting rounds half away from zero.
func TestPoint_ScaleFactor(t *testing.T) {
	t.Parallel()

	m := newAllTypes(t)
	sf, i16 := m.Point("SF"), m.Point("I16")
	set(t, sf.SetInt(-1), i16.SetInt(2301))
	if v, _ := i16.Float(); v != 230.1 {
		t.Errorf("2301 with a scale factor of -1 = %v, want 230.1", v)
	}

	tests := []struct {
		sf   int64
		set  float64
		raw  int64
		read float64
	}{
		{-1, 0.25, 3, 0.3},
		{-1, -0.25, -3, -0.3},
		{-1, 230.15, 2302, 230.2},
		{-2, 12.34, 1234, 12.34},
		{2, 12345, 123, 12300},
		{0, 0.5, 1, 1},
	}
	for _, tt := range tests {
		set(t, sf.SetInt(tt.sf), i16.SetFloat(tt.set))
		raw, _ := i16.Int()
		read, _ := i16.Float()
		if raw != tt.raw || read != tt.read {
			t.Errorf("%v with a scale factor of %d: %d, reading %v, want %d, reading %v", tt.set, tt.sf, raw, read, tt.raw, tt.read)
		}
	}

	if got, ok := m.Point("U32").ScaleFactor(); !ok || got != -2 {
		t.Errorf("U32's fixed scale factor = %d, %t, want -2", got, ok)
	}
	set(t, sf.SetRaw([]uint16{11}))
	if v, ok := i16.Float(); ok {
		t.Errorf("with a scale factor of 11, out of range: %v", v)
	}
	set(t, sf.SetUnimplemented())
	if v, ok := i16.Float(); ok {
		t.Errorf("with a scale factor not implemented: %v", v)
	}
	if v, ok := i16.Int(); !ok || v != 1 {
		t.Errorf("Int, unscaled, without the scale factor = %d, %t, want 1", v, ok)
	}
}

// A nil *Point, which a model returns for a point it lacks, reads as not there.
func TestPoint_Nil(t *testing.T) {
	t.Parallel()

	p := newAllTypes(t).Point("Nope")
	if p != nil {
		t.Fatalf("Point(Nope) = %v", p)
	}
	if p.Implemented() || p.Raw() != nil || p.Name() != "" || p.Definition() != nil || p.Group() != nil || p.Flags() != nil ||
		p.Offset() != 0 || p.Address() != 0 || p.String() != "<no point>" {
		t.Error("a nil *Point reads as there")
	}
	if _, ok := p.Int(); ok {
		t.Error("a nil *Point has an Int")
	}
	if _, ok := p.Uint(); ok {
		t.Error("a nil *Point has a Uint")
	}
	if _, ok := p.Float(); ok {
		t.Error("a nil *Point has a Float")
	}
	if _, ok := p.ScaleFactor(); ok {
		t.Error("a nil *Point has a ScaleFactor")
	}
	if _, ok := p.Text(); ok {
		t.Error("a nil *Point has a Text")
	}
	if _, ok := p.Symbol(); ok {
		t.Error("a nil *Point has a Symbol")
	}
	for _, err := range []error{p.SetInt(1), p.SetUint(1), p.SetFloat(1), p.SetText(""), p.SetRaw(nil), p.SetUnimplemented()} {
		if !errors.Is(err, sunspec.ErrNoPoint) {
			t.Errorf("got %v, want ErrNoPoint", err)
		}
	}
	var g *sunspec.Group
	if g.Point("A") != nil || g.Points() != nil || g.Groups("A") != nil {
		t.Error("a nil *Group has points or groups")
	}
}

func TestPoint_String(t *testing.T) {
	t.Parallel()

	m := newAllTypes(t)
	set(t,
		m.Point("SF").SetInt(-1),
		m.Point("I16").SetInt(42),
		m.Point("E16").SetUint(1),
		m.Point("S").SetText("hi"),
		m.Point("B16").SetUint(0b101),
		m.Point("E32").SetUint(9),
	)
	tests := map[string]string{
		"I16": "I16: 4.2 W",
		"E16": "E16: ON",
		"S":   `S: "hi"`,
		"B16": "B16: [A C]",
		"E32": "E32: 9",
		"SF":  "SF: -1",
	}
	for name, want := range tests {
		if got := m.Point(name).String(); got != want {
			t.Errorf("%s.String() = %q, want %q", name, got, want)
		}
	}
}
