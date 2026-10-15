package sunspec

import "github.com/elojah/sunspec/model"

// Group is a group of a model laid out on its registers: the model's own group, or one repetition of a group in it,
// such as one of model 160's MPPT modules. It is valid until the model's next Read.
type Group struct {
	definition *model.Group
	model      *Model
	parent     *Group
	index      int
	points     []*Point
	// groups holds the repetitions of each of the definition's groups, in its order. Past the model's end, a group has
	// fewer, or none, and the groups after it no entry.
	groups [][]*Group
}

// Definition returns the group's definition.
func (g *Group) Definition() *model.Group {
	return g.definition
}

// Index returns which repetition of its definition the group is, from 0.
func (g *Group) Index() int {
	return g.index
}

// Point returns the group's point named name, or nil if the group has none: the definition has no such point, or the
// model ends before it. The getters of a nil *Point report a value that is not there, so that
// g.Point("DCA").Float() needs no check.
func (g *Group) Point(name string) *Point {
	if g == nil {
		return nil
	}
	for _, p := range g.points {
		if p.definition.Name == name {
			return p
		}
	}

	return nil
}

// Points returns the group's points, in register order, without its groups'.
func (g *Group) Points() []*Point {
	if g == nil {
		return nil
	}

	return append([]*Point(nil), g.points...)
}

// Groups returns the repetitions of the group's group named name, in register order, or none if it has no such group.
func (g *Group) Groups(name string) []*Group {
	if g == nil {
		return nil
	}
	for i, def := range g.definition.Groups {
		if def.Name == name && i < len(g.groups) {
			return append([]*Group(nil), g.groups[i]...)
		}
	}

	return nil
}

// find returns the point named name in g or its nearest ancestor that has one, as scale factors and counts are
// resolved: model 705's Crv[i].Pt[j].V scales with V_SF, of the model's own group.
func (g *Group) find(name string) *Point {
	for ; g != nil; g = g.parent {
		if p := g.Point(name); p != nil {
			return p
		}
	}

	return nil
}

// appendPoints appends g's points, then its groups' in turn, which is register order: a group's points come before its
// groups.
func (g *Group) appendPoints(points []*Point) []*Point {
	points = append(points, g.points...)
	for _, repetitions := range g.groups {
		for _, sub := range repetitions {
			points = sub.appendPoints(points)
		}
	}

	return points
}

// layout lays a model's definition out on its registers, which may be fewer or more than the definition takes.
//
// Points go one after the other, each taking its size, then the groups, each as many times as it repeats. How many is
// fixed by the definition, held by a count point laid out before, or, for a group that repeats until the end of the
// model, as many as the registers left leave room for. A model shorter than its definition, an older version of it,
// ends at the last point that fits: the points after are not there. Registers left after the last group are not laid
// out, but Registers has them.
type layout struct {
	m      *Model
	offset int
	// end is set once a point does not fit: the layout stops there.
	end bool
	// repeats, when not nil, gives the counts by group name instead of the count points: see NewModel.
	repeats map[string]int
}

// group lays out def, the index-th repetition of a group of parent, or the model's own group if parent is nil.
func (l *layout) group(def *model.Group, parent *Group, index int) *Group {
	g := &Group{definition: def, model: l.m, parent: parent, index: index}
	for i := range def.Points {
		p := &def.Points[i]
		if l.offset+int(p.Size) > len(l.m.registers) {
			l.end = true

			break
		}
		g.points = append(g.points, &Point{definition: p, group: g, offset: l.offset})
		l.offset += int(p.Size)
	}
	// Once every point of g is laid out, since a scale factor may come after the points it scales, as A_SF after A in
	// model 103.
	for _, p := range g.points {
		if name := p.definition.SF.Point; name != "" {
			p.sf = g.find(name)
		}
	}

	for i := range def.Groups {
		if l.end {
			break
		}
		sub := &def.Groups[i]
		// Every repetition takes a register at least, so no more fit than registers are left.
		n := min(l.count(sub, g), len(l.m.registers)-l.offset)
		repetitions := make([]*Group, 0, max(n, 0))
		for j := 0; j < n && !l.end; j++ {
			repetitions = append(repetitions, l.group(sub, g, j))
		}
		g.groups = append(g.groups, repetitions)
	}

	return g
}

// count returns how many times sub repeats in g.
func (l *layout) count(sub *model.Group, g *Group) int {
	switch {
	case sub.CountPoint != "" && l.repeats != nil:
		return l.repeats[sub.Name]
	case sub.CountPoint != "":
		// Not implemented, or past the model's end, is none.
		n, _ := g.find(sub.CountPoint).Int()

		return int(n)
	case sub.Count == 0:
		// model.Parse made sure such a group is the model's last, without groups of its own: each repetition takes the
		// same registers, so the rest of the model holds a whole number of them, and maybe a few registers more.
		return (len(l.m.registers) - l.offset) / size(sub, nil)
	default:
		return sub.Count
	}
}

// size returns how many registers def takes, each group repeating as repeats says, by group name, when its count is
// not fixed.
func size(def *model.Group, repeats map[string]int) int {
	n := 0
	for _, p := range def.Points {
		n += int(p.Size)
	}
	for i := range def.Groups {
		sub := &def.Groups[i]
		count := sub.Count
		if sub.CountPoint != "" || sub.Count == 0 {
			count = repeats[sub.Name]
		}
		n += count * size(sub, repeats)
	}

	return n
}

// counted reports whether def, or any of its groups, has a group counted by a point.
func counted(def *model.Group) bool {
	for i := range def.Groups {
		if def.Groups[i].CountPoint != "" || counted(&def.Groups[i]) {
			return true
		}
	}

	return false
}
