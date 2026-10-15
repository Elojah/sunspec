package model_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/elojah/sunspec/model"
)

func TestLookup(t *testing.T) {
	t.Parallel()

	common := model.Lookup(1)
	if common == nil || common.ID != 1 || common.Group.Name != "common" {
		t.Fatalf("Lookup(1) = %+v, want the common model", common)
	}
	if mn := common.Group.Points[2]; mn.Name != "Mn" || mn.Type != model.String || mn.Size != 16 {
		t.Errorf("common's third point is %+v, want Mn, a string of 16 registers", mn)
	}
	for _, id := range []uint16{0, 999, 0xFFFF} {
		if d := model.Lookup(id); d != nil {
			t.Errorf("Lookup(%d) = model %d, want nil", id, d.ID)
		}
	}
}

func TestIDs(t *testing.T) {
	t.Parallel()

	ids := model.IDs()
	if !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != len(ids) {
		t.Errorf("IDs() = %v, want sorted and unique", ids)
	}
	for _, id := range []uint16{1, 101, 103, 160, 203, 701, 705, 802} {
		if _, found := slices.BinarySearch(ids, id); !found {
			t.Errorf("IDs() lacks %d", id)
		}
	}
	for _, id := range ids {
		if d := model.Lookup(id); d == nil || d.ID != id {
			t.Errorf("Lookup(%d) = %v", id, d)
		}
	}
}

// The registers each model takes after its header, without repeating groups, are those the SunSpec specification
// gives for L.
func TestDefinitions_Length(t *testing.T) {
	t.Parallel()

	tests := map[uint16]int{
		1:   66, // 65 without Pad, which older devices lack
		101: 50,
		102: 50,
		103: 50,
		111: 60,
		112: 60,
		113: 60,
		120: 26,
		121: 30,
		122: 44,
		123: 24,
		124: 24,
		160: 8, // and 20 per module
		201: 105,
		203: 105,
		211: 124,
		213: 124,
		701: 153,
		702: 50,
		703: 17,
		704: 65,
		713: 7,
		715: 7,
	}
	for id, want := range tests {
		d := model.Lookup(id)
		if got := fixedLength(d.Group) - 2; got != want {
			t.Errorf("model %d: %d registers after the header, want %d", id, got, want)
		}
	}
	module := model.Lookup(160).Group.Groups[0]
	if got := fixedLength(module); module.Count != 0 || got != 20 {
		t.Errorf("model 160's module: count %d and %d registers, want 0 and 20", module.Count, got)
	}
}

// fixedLength returns how many registers g takes, without its groups that repeat a variable number of times.
func fixedLength(g model.Group) int {
	n := 0
	for _, p := range g.Points {
		n += int(p.Size)
	}
	for _, sub := range g.Groups {
		if sub.CountPoint == "" && sub.Count > 0 {
			n += sub.Count * fixedLength(sub)
		}
	}

	return n
}

// Every generated definition can be laid out, as Parse checks: each check is made again here, on what the generator
// wrote.
func TestDefinitions(t *testing.T) {
	t.Parallel()

	for _, id := range model.IDs() {
		d := model.Lookup(id)
		points := d.Group.Points
		if len(points) < 2 || points[0].Name != "ID" || points[1].Name != "L" {
			t.Errorf("model %d does not start with ID and L", id)

			continue
		}
		checkGroup(t, id, d.Group, nil)
	}
}

func checkGroup(t *testing.T, id uint16, g model.Group, scope [][]model.Point) {
	t.Helper()

	scope = append(scope, g.Points)
	for _, p := range g.Points {
		if want := p.Type.Size(); p.Size == 0 || (want != 0 && p.Size != want) {
			t.Errorf("model %d: %s.%s: size %d for a %s", id, g.Name, p.Name, p.Size, p.Type)
		}
		if p.SF.Point != "" {
			if sf, ok := find(scope, p.SF.Point); !ok || sf.Type != model.SunSSF {
				t.Errorf("model %d: %s.%s: scale factor %q is no sunssf point", id, g.Name, p.Name, p.SF.Point)
			}
		}
	}
	for _, sub := range g.Groups {
		if sub.CountPoint != "" {
			if _, ok := find(scope, sub.CountPoint); !ok {
				t.Errorf("model %d: %s: count %q is no point", id, sub.Name, sub.CountPoint)
			}
		}
		checkGroup(t, id, sub, scope)
	}
}

func find(scope [][]model.Point, name string) (model.Point, bool) {
	for i := len(scope) - 1; i >= 0; i-- {
		for _, p := range scope[i] {
			if p.Name == name {
				return p, true
			}
		}
	}

	return model.Point{}, false
}

// A vendor's model, with a scale factor of each kind and a group of each count.
const vendorModel = `{
	"id": 64999,
	"group": {
		"name": "vendor",
		"label": "Vendor",
		"desc": "Left out.",
		"points": [
			{"name": "ID", "type": "uint16", "size": 1, "value": 64999, "mandatory": "M", "static": "S"},
			{"name": "L", "type": "uint16", "size": 1, "mandatory": "M", "static": "S"},
			{"name": "W_SF", "type": "sunssf", "size": 1},
			{"name": "W", "type": "int16", "size": 1, "sf": "W_SF", "units": "W", "access": "RW"},
			{"name": "Tmp", "type": "int16", "size": 1, "sf": -1, "units": "C"},
			{"name": "St", "type": "enum16", "size": 1, "symbols": [{"name": "OFF", "value": 1, "label": "Off"}, {"name": "ON", "value": 2}]},
			{"name": "N", "type": "uint16", "size": 1}
		],
		"groups": [
			{"name": "fixed", "points": [{"name": "Id", "type": "string", "size": 4}]},
			{"name": "counted", "count": "N", "points": [{"name": "V", "type": "uint16", "size": 1, "sf": "W_SF"}]},
			{"name": "rest", "count": 0, "points": [{"name": "A", "type": "float32", "size": 2}]}
		]
	}
}`

func TestParse(t *testing.T) {
	t.Parallel()

	d, err := model.Parse(strings.NewReader(vendorModel))
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != 64999 || d.Group.Name != "vendor" || d.Group.Label != "Vendor" || d.Group.Count != 1 {
		t.Errorf("model %d, group %q %q, count %d", d.ID, d.Group.Name, d.Group.Label, d.Group.Count)
	}
	w, tmp, st := d.Group.Points[3], d.Group.Points[4], d.Group.Points[5]
	if w.SF != (model.ScaleFactor{Point: "W_SF"}) || !w.Writable || w.Units != "W" || w.Type != model.Int16 {
		t.Errorf("W = %+v", w)
	}
	if tmp.SF != (model.ScaleFactor{Value: -1}) || tmp.Writable {
		t.Errorf("Tmp = %+v", tmp)
	}
	if want := []model.Symbol{{Name: "OFF", Label: "Off", Value: 1}, {Name: "ON", Value: 2}}; !slices.Equal(st.Symbols, want) {
		t.Errorf("St's symbols = %+v, want %+v", st.Symbols, want)
	}
	if id := d.Group.Points[0]; !id.Mandatory || !id.Static {
		t.Errorf("ID = %+v, want mandatory and static", id)
	}
	groups := d.Group.Groups
	if groups[0].Count != 1 || groups[0].CountPoint != "" {
		t.Errorf("fixed: count %d, %q, want 1", groups[0].Count, groups[0].CountPoint)
	}
	if groups[1].Count != 0 || groups[1].CountPoint != "N" {
		t.Errorf("counted: count %d, %q, want N", groups[1].Count, groups[1].CountPoint)
	}
	if groups[2].Count != 0 || groups[2].CountPoint != "" {
		t.Errorf("rest: count %d, %q, want 0", groups[2].Count, groups[2].CountPoint)
	}
}

// Parse refuses a definition it cannot lay out.
func TestParse_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, old, new string
	}{
		{"unknown type", `"type": "int16", "size": 1, "sf": -1`, `"type": "int12", "size": 1, "sf": -1`},
		{"size", `{"name": "N", "type": "uint16", "size": 1}`, `{"name": "N", "type": "uint16", "size": 2}`},
		{"no header", `{"name": "L", "type": "uint16", "size": 1, "mandatory": "M", "static": "S"},`, ``},
		{"scale factor names nothing", `"sf": "W_SF", "units"`, `"sf": "X_SF", "units"`},
		{"scale factor is no sunssf", `"sf": "W_SF", "units"`, `"sf": "N", "units"`},
		{"scale factor out of range", `"sf": -1`, `"sf": -11`},
		{"count names nothing", `"count": "N"`, `"count": "M"`},
		{"repeating group not last", `{"name": "fixed",`, `{"name": "fixed", "count": 0,`},
		{"duplicate point", `{"name": "N", "type"`, `{"name": "W", "type"`},
		{"duplicate group", `{"name": "rest"`, `{"name": "fixed"`},
		{"group without points", `"points": [{"name": "A", "type": "float32", "size": 2}]`, `"points": []`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(vendorModel, tt.old) {
				t.Fatalf("the vendor model has no %s", tt.old)
			}
			_, err := model.Parse(strings.NewReader(strings.Replace(vendorModel, tt.old, tt.new, 1)))
			if !errors.Is(err, model.ErrInvalidDefinition) {
				t.Errorf("got %v, want an ErrInvalidDefinition", err)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "model 64999: vendor") {
				t.Errorf("got %q, want it to name the model and where", err)
			}
		})
	}

	if _, err := model.Parse(strings.NewReader(`{"id": 1, "group": {"points": [`)); err == nil {
		t.Error("Parse accepted truncated JSON")
	}
	if _, err := model.Parse(strings.NewReader(`{"id": 1, "group": {"count": 1.5}}`)); err == nil {
		t.Error("Parse accepted a count of 1.5")
	}
}

func TestType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		t    model.Type
		name string
		size uint16
	}{
		{model.Int16, "int16", 1},
		{model.Uint64, "uint64", 4},
		{model.Bitfield32, "bitfield32", 2},
		{model.String, "string", 0},
		{model.SunSSF, "sunssf", 1},
		{model.IPv6Addr, "ipv6addr", 8},
		{model.EUI48, "eui48", 4},
	}
	for _, tt := range tests {
		if tt.t.String() != tt.name || tt.t.Size() != tt.size {
			t.Errorf("%d: %q of size %d, want %q of size %d", tt.t, tt.t, tt.t.Size(), tt.name, tt.size)
		}
		if got, ok := model.ParseType(tt.name); !ok || got != tt.t {
			t.Errorf("ParseType(%q) = %v, %t", tt.name, got, ok)
		}
	}
	if _, ok := model.ParseType("int12"); ok {
		t.Error("ParseType(int12) succeeded")
	}
	if invalid := model.Type(0); invalid.String() != "type 0" || invalid.Size() != 0 {
		t.Errorf("type 0: %q of size %d", invalid, invalid.Size())
	}
}

// Every definition reads back from its binary form as it was, so that Lookup returns what Parse read.
func TestDefinition_MarshalBinary(t *testing.T) {
	t.Parallel()

	vendor, err := model.Parse(strings.NewReader(vendorModel))
	if err != nil {
		t.Fatal(err)
	}
	definitions := make([]*model.Definition, 0, 1+len(model.IDs()))
	definitions = append(definitions, vendor)
	for _, id := range model.IDs() {
		definitions = append(definitions, model.Lookup(id))
	}
	for _, d := range definitions {
		data, err := d.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		var got model.Definition
		if err := got.UnmarshalBinary(data); err != nil {
			t.Fatalf("model %d: %v", d.ID, err)
		}
		if !reflect.DeepEqual(&got, d) {
			t.Errorf("model %d reads back as %+v, want %+v", d.ID, got, *d)
		}
	}
}

// UnmarshalBinary refuses truncated data, and a definition Parse would refuse.
func TestDefinition_UnmarshalBinary(t *testing.T) {
	t.Parallel()

	data, err := model.Lookup(1).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var d model.Definition
	for _, n := range []int{0, 1, 5, len(data) / 2, len(data) - 1} {
		if err := d.UnmarshalBinary(data[:n]); !errors.Is(err, model.ErrInvalidDefinition) {
			t.Errorf("%d bytes of %d: got %v, want ErrInvalidDefinition", n, len(data), err)
		}
	}

	// Model 1 without its header's L.
	headless := *model.Lookup(1)
	headless.Group.Points = slices.Delete(slices.Clone(headless.Group.Points), 1, 2)
	data, err = headless.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.UnmarshalBinary(data); !errors.Is(err, model.ErrInvalidDefinition) {
		t.Errorf("without L: got %v, want ErrInvalidDefinition", err)
	}
	if d.ID != 0 {
		t.Errorf("a refused definition was read: model %d", d.ID)
	}
}
