package model

import (
	"encoding/binary"
	"fmt"
)

// A definition's binary form, which MarshalBinary writes and Lookup reads SunSpec's from. Every integer is a uvarint,
// and every string its length, then its bytes:
//
//	definition  ID, then the group of the model
//	group       name, label, count point, count; how many points, each; how many groups, each
//	point       name, label, units, scale factor point, type, size, scale factor value, flags; how many symbols, each
//	symbol      name, label, value
//
// A scale factor value, from -10 to 10, is stored plus 128. flags holds Writable, Mandatory and Static, from bit 0.
const (
	flagWritable = 1 << iota
	flagMandatory
	flagStatic
)

// MarshalBinary returns d in a compact binary form, which UnmarshalBinary reads back: tools/modelgen stores SunSpec's
// models so, which a microcontroller keeps in flash rather than RAM. It never fails.
func (d *Definition) MarshalBinary() ([]byte, error) {
	b := binary.AppendUvarint(nil, uint64(d.ID))

	return appendGroup(b, &d.Group), nil
}

// UnmarshalBinary reads d from data, in MarshalBinary's form, and checks it as Parse does.
func (d *Definition) UnmarshalBinary(data []byte) error {
	r := reader{s: string(data)}
	decoded := r.definition()
	if r.err != nil {
		return r.err
	}
	if err := check(&decoded); err != nil {
		return err
	}
	*d = decoded

	return nil
}

func appendGroup(b []byte, g *Group) []byte {
	b = appendString(b, g.Name)
	b = appendString(b, g.Label)
	b = appendString(b, g.CountPoint)
	b = binary.AppendUvarint(b, uint64(g.Count)) //nolint:gosec // Parse refuses a negative count
	b = binary.AppendUvarint(b, uint64(len(g.Points)))
	for i := range g.Points {
		b = appendPoint(b, &g.Points[i])
	}
	b = binary.AppendUvarint(b, uint64(len(g.Groups)))
	for i := range g.Groups {
		b = appendGroup(b, &g.Groups[i])
	}

	return b
}

func appendPoint(b []byte, p *Point) []byte {
	b = appendString(b, p.Name)
	b = appendString(b, p.Label)
	b = appendString(b, p.Units)
	b = appendString(b, p.SF.Point)
	b = binary.AppendUvarint(b, uint64(p.Type))
	b = binary.AppendUvarint(b, uint64(p.Size))
	b = binary.AppendUvarint(b, uint64(int(p.SF.Value)+128)) //nolint:gosec // a scale factor is in [-10, 10]
	var flags uint64
	for _, f := range []struct {
		set  bool
		flag uint64
	}{{p.Writable, flagWritable}, {p.Mandatory, flagMandatory}, {p.Static, flagStatic}} {
		if f.set {
			flags |= f.flag
		}
	}
	b = binary.AppendUvarint(b, flags)
	b = binary.AppendUvarint(b, uint64(len(p.Symbols)))
	for _, s := range p.Symbols {
		b = appendString(b, s.Name)
		b = appendString(b, s.Label)
		b = binary.AppendUvarint(b, uint64(s.Value))
	}

	return b
}

func appendString(b []byte, s string) []byte {
	return append(binary.AppendUvarint(b, uint64(len(s))), s...)
}

// reader reads the binary form from a string, so that the strings it returns are slices of it: of a constant, kept
// in flash, they take no RAM. The first error sticks, and every read after it returns zero values.
type reader struct {
	s   string
	err error
}

func (r *reader) definition() Definition {
	id := r.uint(1<<16 - 1)

	return Definition{ID: uint16(id), Group: r.group()} //nolint:gosec // r.uint bounds it
}

func (r *reader) group() Group {
	g := Group{Name: r.string(), Label: r.string(), CountPoint: r.string()}
	g.Count = int(r.uint(1<<16 - 1)) //nolint:gosec // r.uint bounds it
	if n := r.uint(1<<16 - 1); n > 0 {
		g.Points = make([]Point, 0, min(n, uint64(len(r.s))))
		for range n {
			g.Points = append(g.Points, r.point())
		}
	}
	if n := r.uint(1<<16 - 1); n > 0 {
		g.Groups = make([]Group, 0, min(n, uint64(len(r.s))))
		for range n {
			g.Groups = append(g.Groups, r.group())
		}
	}

	return g
}

//nolint:gosec // r.uint bounds every conversion
func (r *reader) point() Point {
	p := Point{Name: r.string(), Label: r.string(), Units: r.string(), SF: ScaleFactor{Point: r.string()}}
	p.Type = Type(r.uint(uint64(len(types) - 1)))
	p.Size = uint16(r.uint(1<<16 - 1))
	p.SF.Value = int8(int(r.uint(255)) - 128)
	flags := r.uint(flagWritable | flagMandatory | flagStatic)
	p.Writable, p.Mandatory, p.Static = flags&flagWritable != 0, flags&flagMandatory != 0, flags&flagStatic != 0
	if n := r.uint(1<<16 - 1); n > 0 {
		p.Symbols = make([]Symbol, 0, min(n, uint64(len(r.s))))
		for range n {
			p.Symbols = append(p.Symbols, Symbol{Name: r.string(), Label: r.string(), Value: uint32(r.uint(1<<32 - 1))})
		}
	}

	return p
}

// uint reads a uvarint, which must not be past limit.
func (r *reader) uint(limit uint64) uint64 {
	if r.err != nil {
		return 0
	}
	var v uint64
	for shift := 0; shift < 64; shift += 7 {
		if len(r.s) == 0 {
			break
		}
		c := r.s[0]
		r.s = r.s[1:]
		v |= uint64(c&0x7F) << shift
		if c < 0x80 {
			if v > limit {
				r.err = fmt.Errorf("%w: %d past %d", ErrInvalidDefinition, v, limit)

				return 0
			}

			return v
		}
	}
	r.err = fmt.Errorf("%w: truncated", ErrInvalidDefinition)

	return 0
}

// string reads a string, as a slice of r.s.
func (r *reader) string() string {
	n := r.uint(1<<32 - 1)
	if r.err == nil && n > uint64(len(r.s)) {
		r.err = fmt.Errorf("%w: truncated", ErrInvalidDefinition)
	}
	if r.err != nil {
		return ""
	}
	s := r.s[:n]
	r.s = r.s[n:]

	return s
}
