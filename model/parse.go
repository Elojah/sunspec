package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
)

// ErrInvalidDefinition is matched by *DefinitionError.
var ErrInvalidDefinition = errors.New("model: invalid definition")

// DefinitionError is returned by Parse for a definition that cannot be laid out, such as a scale factor naming no
// sunssf point. It matches ErrInvalidDefinition.
type DefinitionError struct {
	ID uint16
	// Path is where in the definition: groups and point, from the model's group, separated by dots.
	Path   string
	Reason string
}

func (e *DefinitionError) Error() string {
	return fmt.Sprintf("model %d: %s: %s", e.ID, e.Path, e.Reason)
}

func (e *DefinitionError) Is(target error) bool {
	return target == ErrInvalidDefinition
}

// Parse reads a model definition in SunSpec's JSON format, as github.com/sunspec/models has them, and checks that it can
// be laid out: every point has a known type and a size that fits it, every scale factor and count names a point it can
// read before, and the header is there. It keeps what a device's registers need, leaving out descriptions and
// comments.
//
// The definitions Lookup returns are generated with it. Parse is for those it does not know, such as a vendor's.
func Parse(r io.Reader) (*Definition, error) {
	var m jsonModel
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}

	d := &Definition{ID: m.ID}
	group, err := m.Group.definition(d.ID, m.Group.Name)
	if err != nil {
		return nil, err
	}
	group.Count = 1
	d.Group = group
	if err := check(d); err != nil {
		return nil, err
	}

	return d, nil
}

// The JSON format. Only what Definition keeps is decoded.
type (
	jsonModel struct {
		ID    uint16    `json:"id"`
		Group jsonGroup `json:"group"`
	}

	jsonGroup struct {
		Name   string      `json:"name"`
		Label  string      `json:"label"`
		Count  nameOrInt   `json:"count"`
		Points []jsonPoint `json:"points"`
		Groups []jsonGroup `json:"groups"`
	}

	jsonPoint struct {
		Name      string       `json:"name"`
		Label     string       `json:"label"`
		Units     string       `json:"units"`
		Type      string       `json:"type"`
		Size      uint16       `json:"size"`
		SF        nameOrInt    `json:"sf"`
		Access    string       `json:"access"`
		Mandatory string       `json:"mandatory"`
		Static    string       `json:"static"`
		Symbols   []jsonSymbol `json:"symbols"`
	}

	jsonSymbol struct {
		Name  string `json:"name"`
		Label string `json:"label"`
		Value uint32 `json:"value"`
	}
)

// nameOrInt is a JSON value that is either a point's name or a number: a scale factor or a count.
type nameOrInt struct {
	set  bool
	name string
	n    int
}

func (v *nameOrInt) UnmarshalJSON(data []byte) error {
	v.set = true
	if bytes.HasPrefix(data, []byte(`"`)) {
		return json.Unmarshal(data, &v.name)
	}
	n, err := strconv.Atoi(string(data))
	if err != nil {
		return fmt.Errorf("%w: want a name or an integer, got %s", ErrInvalidDefinition, data)
	}
	v.n = n

	return nil
}

func (g jsonGroup) definition(id uint16, path string) (Group, error) {
	group := Group{Name: g.Name, Label: g.Label, Count: 1}
	if g.Count.set {
		group.Count, group.CountPoint = g.Count.n, g.Count.name
		if group.CountPoint != "" {
			group.Count = 0
		}
	}
	for _, p := range g.Points {
		point, err := p.definition(id, path+"."+p.Name)
		if err != nil {
			return Group{}, err
		}
		group.Points = append(group.Points, point)
	}
	for _, sub := range g.Groups {
		subgroup, err := sub.definition(id, path+"."+sub.Name)
		if err != nil {
			return Group{}, err
		}
		group.Groups = append(group.Groups, subgroup)
	}

	return group, nil
}

func (p jsonPoint) definition(id uint16, path string) (Point, error) {
	t, ok := ParseType(p.Type)
	if !ok {
		return Point{}, &DefinitionError{ID: id, Path: path, Reason: fmt.Sprintf("unknown type %q", p.Type)}
	}
	point := Point{
		Name:      p.Name,
		Label:     p.Label,
		Units:     p.Units,
		Type:      t,
		Size:      p.Size,
		Writable:  p.Access == "RW",
		Mandatory: p.Mandatory == "M",
		Static:    p.Static == "S",
	}
	if p.SF.set {
		if p.SF.name == "" && (p.SF.n < -10 || p.SF.n > 10) {
			return Point{}, &DefinitionError{ID: id, Path: path, Reason: fmt.Sprintf("scale factor %d out of [-10, 10]", p.SF.n)}
		}
		point.SF = ScaleFactor{Point: p.SF.name, Value: int8(p.SF.n)} //nolint:gosec // checked just above
	}
	for _, s := range p.Symbols {
		point.Symbols = append(point.Symbols, Symbol(s))
	}

	return point, nil
}

// check returns a *DefinitionError if d cannot be laid out.
func check(d *Definition) error {
	points := d.Group.Points
	if len(points) < 2 || points[0].Name != "ID" || points[1].Name != "L" || points[0].Type != Uint16 || points[1].Type != Uint16 {
		return &DefinitionError{ID: d.ID, Path: d.Group.Name, Reason: "the first points are not the header, ID and L"}
	}

	return checkGroup(d.ID, d.Group, d.Group.Name, nil, true)
}

// checkGroup checks g, at path, whose ancestors' points are scope, nearest last. root is whether g is the model's own
// group.
func checkGroup(id uint16, g Group, path string, scope [][]Point, root bool) error {
	if len(g.Points) == 0 {
		// So that every repetition takes registers, and the layout of a model always ends.
		return &DefinitionError{ID: id, Path: path, Reason: "group without points"}
	}
	scope = append(scope, g.Points)
	names := make(map[string]bool, len(g.Points)+len(g.Groups))
	for _, p := range g.Points {
		if names[p.Name] {
			return &DefinitionError{ID: id, Path: path + "." + p.Name, Reason: "duplicate name"}
		}
		names[p.Name] = true
		if err := checkPoint(id, p, path+"."+p.Name, scope); err != nil {
			return err
		}
	}
	for i, sub := range g.Groups {
		subpath := path + "." + sub.Name
		fail := func(reason string) error {
			return &DefinitionError{ID: id, Path: subpath, Reason: reason}
		}
		if names[sub.Name] {
			return fail("duplicate name")
		}
		names[sub.Name] = true
		switch {
		case sub.CountPoint != "":
			if p, ok := find(scope, sub.CountPoint); !ok || (p.Type != Uint16 && p.Type != Count) {
				return fail(fmt.Sprintf("count %q names no uint16 or count point before it", sub.CountPoint))
			}
		case sub.Count == 0:
			// Its count comes from the model's length, so it must take the rest of the model, with a fixed size.
			if !root || i != len(g.Groups)-1 || len(sub.Groups) > 0 {
				return fail("only the model's last group may repeat until the end of the model, without groups of its own")
			}
		case sub.Count < 0:
			return fail(fmt.Sprintf("count %d", sub.Count))
		}
		if err := checkGroup(id, sub, subpath, scope, false); err != nil {
			return err
		}
	}

	return nil
}

// checkPoint checks p, at path, whose group's points and its ancestors' are scope.
func checkPoint(id uint16, p Point, path string, scope [][]Point) error {
	fail := func(format string, args ...any) error {
		return &DefinitionError{ID: id, Path: path, Reason: fmt.Sprintf(format, args...)}
	}
	if want := p.Type.Size(); p.Size == 0 || (want != 0 && p.Size != want) {
		return fail("size %d for a %s", p.Size, p.Type)
	}
	if name := p.SF.Point; name != "" {
		if sf, ok := find(scope, name); !ok || sf.Type != SunSSF {
			return fail("scale factor %q names no sunssf point", name)
		}
	}

	return nil
}

// find returns the point named name in scope, nearest group first.
func find(scope [][]Point, name string) (Point, bool) {
	for i := len(scope) - 1; i >= 0; i-- {
		if j := slices.IndexFunc(scope[i], func(p Point) bool { return p.Name == name }); j >= 0 {
			return scope[i][j], true
		}
	}

	return Point{}, false
}
