package sunspec

import (
	"bytes"
	"encoding/hex"
	"math"
	"net/netip"
	"strconv"
	"strings"

	"github.com/elojah/sunspec/model"
)

// Point is a point of a model laid out on its registers: its getters decode the registers the model last read, its
// setters encode into them, and Model.Write sends what they changed. It is valid until the model's next Read.
//
// A nil *Point is a point the model lacks, which Model.Point and Group.Point return: its getters report a value that
// is not there, as for a point that is not implemented, and its setters return ErrNoPoint.
type Point struct {
	definition *model.Point
	group      *Group
	// offset is the index of the point's first register in its model's.
	offset int
	// sf is the point the scale factor names, nil without one, or when the model ends before it.
	sf *Point
}

// Definition returns the point's definition, or nil for a nil *Point.
func (p *Point) Definition() *model.Point {
	if p == nil {
		return nil
	}

	return p.definition
}

// Name returns the point's name, or "" for a nil *Point.
func (p *Point) Name() string {
	if p == nil {
		return ""
	}

	return p.definition.Name
}

// Group returns the group the point belongs to.
func (p *Point) Group() *Group {
	if p == nil {
		return nil
	}

	return p.group
}

// Offset returns how many registers the point is from its model's first, ID: 0 for ID, 2 for the first point after
// the header.
func (p *Point) Offset() int {
	if p == nil {
		return 0
	}

	return p.offset
}

// Address returns the address of the point's first register among the device's holding registers.
func (p *Point) Address() uint16 {
	if p == nil {
		return 0
	}

	return p.group.model.address + uint16(p.offset) //nolint:gosec // within the model
}

// Raw returns a copy of the point's registers.
func (p *Point) Raw() []uint16 {
	if p == nil {
		return nil
	}

	return append([]uint16(nil), p.registers()...)
}

// Implemented reports whether the point holds a value, rather than the one its type reserves to mean not implemented:
// 0x8000 for an int16, 0xFFFF for a uint16, NaN for a float32, all NULs for a string. A pad is never implemented.
func (p *Point) Implemented() bool {
	return p != nil && implemented(p.definition.Type, p.registers())
}

// Int returns the value of an integer point: any type but floats, strings, addresses and pads. It is not scaled: see
// Float. ok is false if the point is not implemented, or is a uint64 or acc64 past math.MaxInt64.
func (p *Point) Int() (int64, bool) {
	if !p.Implemented() {
		return 0, false
	}
	i, registers := info(p.definition.Type), p.registers()
	if i.class != classInteger {
		return 0, false
	}
	v := decodeUint(registers)
	if i.signed {
		return signExtend(v, bits(registers)), true
	}
	if v > math.MaxInt64 {
		return 0, false
	}

	return int64(v), true
}

// Uint returns the value of an unsigned integer point, unscaled. ok is false if the point is not implemented, or is
// signed.
func (p *Point) Uint() (uint64, bool) {
	if !p.Implemented() {
		return 0, false
	}
	i := info(p.definition.Type)
	if i.class != classInteger || i.signed {
		return 0, false
	}

	return decodeUint(p.registers()), true
}

// Float returns the value of a measurement, scaled: an integer times 10 to the power of its scale factor, or a float.
// ok is false if the point or its scale factor is not implemented, or the point is no measurement: an enum, a
// bitfield, a scale factor, a string, an address.
func (p *Point) Float() (float64, bool) {
	if !p.Implemented() {
		return 0, false
	}
	i, registers := info(p.definition.Type), p.registers()
	var v float64
	switch {
	case i.class == classFloat:
		v = decodeFloat(registers)
	case i.class == classInteger && i.measurement && i.signed:
		v = float64(signExtend(decodeUint(registers), bits(registers)))
	case i.class == classInteger && i.measurement:
		v = float64(decodeUint(registers))
	default:
		return 0, false
	}
	sf, ok := p.ScaleFactor()
	if !ok {
		return 0, false
	}

	return scale(v, sf), true
}

// ScaleFactor returns the power of ten the point's value is scaled by: 0 without a scale factor, a fixed one, or the
// value of its sunssf point. ok is false if that point is not implemented, or past the model's end, or holds a value
// out of the range SunSpec allows, [-10, 10].
func (p *Point) ScaleFactor() (int, bool) {
	if p == nil {
		return 0, false
	}
	if p.definition.SF.Point == "" {
		return int(p.definition.SF.Value), true
	}
	sf, ok := p.sf.Int()
	if !ok || sf < -10 || sf > 10 {
		return 0, false
	}

	return int(sf), true
}

// Text returns the value of a string, up to its first NUL, or of an address: an IPv4 or IPv6 one as net/netip formats
// it, an EUI-48 as six pairs of hex digits, colon-separated. ok is false if the point is not implemented, or holds no
// text.
func (p *Point) Text() (string, bool) {
	if !p.Implemented() {
		return "", false
	}
	t, b := p.definition.Type, decodeBytes(p.registers())
	switch info(t).class {
	case classString:
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}

		return string(b), true
	case classAddress:
		switch len(b) {
		case 4:
			return netip.AddrFrom4([4]byte(b)).String(), true
		case 16:
			return netip.AddrFrom16([16]byte(b)).String(), true
		default:
			// An EUI-48's 6 bytes, after 2 bytes of padding.
			pairs := make([]string, 0, 6)
			for _, c := range b[2:] {
				pairs = append(pairs, strings.ToUpper(hex.EncodeToString([]byte{c})))
			}

			return strings.Join(pairs, ":"), true
		}
	case classInteger, classFloat, classPad:
		return "", false
	default:
		return "", false
	}
}

// Symbol returns the symbol of an enum's value: model 103's St at 4 is MPPT. ok is false if the point is not
// implemented, is no enum, or its value has no symbol, such as a vendor's: Int still has it.
func (p *Point) Symbol() (model.Symbol, bool) {
	if p == nil || (p.definition.Type != model.Enum16 && p.definition.Type != model.Enum32) {
		return model.Symbol{}, false
	}
	v, ok := p.Uint()
	if !ok {
		return model.Symbol{}, false
	}
	for _, s := range p.definition.Symbols {
		if uint64(s.Value) == v {
			return s, true
		}
	}

	return model.Symbol{}, false
}

// Flags returns the symbols of a bitfield's bits that are set, from bit 0: a bit without a symbol is left out, but Uint
// has it. It returns nil if the point is not implemented, or is no bitfield.
func (p *Point) Flags() []model.Symbol {
	if p == nil || (p.definition.Type != model.Bitfield16 && p.definition.Type != model.Bitfield32) {
		return nil
	}
	v, ok := p.Uint()
	if !ok {
		return nil
	}
	var flags []model.Symbol
	for _, s := range p.definition.Symbols {
		if s.Value < 64 && v&(1<<s.Value) != 0 {
			flags = append(flags, s)
		}
	}

	return flags
}

// String returns the point's name and value, for display: a number, scaled, with its units, an enum's symbol, a
// bitfield's set flags, text quoted, or "not implemented".
func (p *Point) String() string {
	if p == nil {
		return "<no point>"
	}

	return p.definition.Name + ": " + p.format()
}

// SetInt sets an integer point, unscaled: see SetFloat. The value must fit the type, but for the value meaning not
// implemented, which SetUnimplemented sets: an int16 is in [-32767, 32767], a uint16 in [0, 65534], a scale factor in
// [-10, 10]. Accumulators take any value, 0 included, which is both a count and not implemented.
func (p *Point) SetInt(v int64) error {
	return p.set(func(t model.Type, registers []uint16) error {
		i := info(t)
		if i.class != classInteger {
			return ErrType
		}
		if i.signed {
			return encodeInt(t, registers, v)
		}
		if v < 0 {
			return ErrOutOfRange
		}

		return encodeUnsigned(i, registers, uint64(v))
	})
}

// SetUint sets an unsigned integer point, unscaled, as SetInt does.
func (p *Point) SetUint(v uint64) error {
	return p.set(func(t model.Type, registers []uint16) error {
		i := info(t)
		if i.class != classInteger || i.signed {
			return ErrType
		}

		return encodeUnsigned(i, registers, v)
	})
}

// SetFloat sets a measurement, scaled by its scale factor as the model holds it: v divided by 10^sf, rounded half away
// from zero, so that 230.15 V with a scale factor of -1 sets 2302. The scale factor must be implemented, and the
// rounded value fit the type as SetInt requires. A float point takes v as it is.
func (p *Point) SetFloat(v float64) error {
	sf, sfOK := p.ScaleFactor()

	return p.set(func(t model.Type, registers []uint16) error {
		i := info(t)
		switch {
		case math.IsNaN(v):
			// NaN means not implemented: SetUnimplemented says so.
			return ErrOutOfRange
		case i.class == classFloat:
			if len(registers) == 2 && !math.IsInf(v, 0) && math.Abs(v) > math.MaxFloat32 {
				return ErrOutOfRange
			}
			encodeFloat(registers, v)

			return nil
		case i.class != classInteger || !i.measurement:
			return ErrType
		case !sfOK:
			return ErrNotImplemented
		}

		raw := math.Round(unscale(v, sf))
		// Past ±2^63, no int64 holds it, and float64(math.MaxInt64) rounds up to 2^63.
		if i.signed {
			if raw < math.MinInt64 || raw >= math.MaxInt64 {
				return ErrOutOfRange
			}

			return encodeInt(t, registers, int64(raw))
		}
		if raw < 0 || raw >= math.MaxUint64 {
			return ErrOutOfRange
		}

		return encodeUnsigned(i, registers, uint64(raw))
	})
}

// SetText sets a string, padded with NULs, which must fit the point's registers, 2 bytes each; or an address: an IPv4
// or IPv6 one as net/netip parses it, an EUI-48 as six pairs of hex digits, separated by colons or dashes. An empty
// string is all NULs, which means not implemented.
func (p *Point) SetText(s string) error {
	return p.set(func(t model.Type, registers []uint16) error {
		var b []byte
		switch info(t).class {
		case classString:
			if len(s) > 2*len(registers) {
				return ErrOutOfRange
			}
			b = []byte(s)
		case classAddress:
			var ok bool
			if b, ok = parseAddress(t, s); !ok {
				return ErrOutOfRange
			}
		case classInteger, classFloat, classPad:
			return ErrType
		default:
			return ErrType
		}
		encodeBytes(registers, b)

		return nil
	})
}

// SetRaw sets the point's registers, as many as its size, whatever its type.
func (p *Point) SetRaw(registers []uint16) error {
	return p.set(func(_ model.Type, dst []uint16) error {
		if len(registers) != len(dst) {
			return ErrOutOfRange
		}
		copy(dst, registers)

		return nil
	})
}

// SetUnimplemented sets the value meaning not implemented.
func (p *Point) SetUnimplemented() error {
	return p.set(func(t model.Type, registers []uint16) error {
		setUnimplemented(t, registers)

		return nil
	})
}

// format formats the point's value for String.
func (p *Point) format() string {
	if !p.Implemented() {
		return "not implemented"
	}
	if s, ok := p.Symbol(); ok {
		return s.Name
	}
	if flags := p.Flags(); flags != nil {
		names := make([]string, 0, len(flags))
		for _, f := range flags {
			names = append(names, f.Name)
		}

		return "[" + strings.Join(names, " ") + "]"
	}
	if t, ok := p.Text(); ok {
		return strconv.Quote(t)
	}
	value, ok := p.number()
	if !ok {
		return "scale factor not implemented"
	}
	if p.definition.Units != "" {
		value += " " + p.definition.Units
	}

	return value
}

// number formats a number: an integer as it is when unscaled, since a float64 does not hold every int64, and else
// scaled, as a float.
func (p *Point) number() (string, bool) {
	if sf, ok := p.ScaleFactor(); ok && sf == 0 {
		if v, ok := p.Int(); ok {
			return strconv.FormatInt(v, 10), true
		}
		if v, ok := p.Uint(); ok {
			return strconv.FormatUint(v, 10), true
		}
	}
	if f, ok := p.Float(); ok {
		return strconv.FormatFloat(f, 'f', -1, 64), true
	}

	return "", false
}

// registers returns the point's registers, in its model's: writing them writes the model's.
func (p *Point) registers() []uint16 {
	return p.group.model.registers[p.offset : p.offset+int(p.definition.Size)]
}

// set encodes a value with encode, into registers as many as the point's, and copies them to the model's only if it
// succeeds, then marks them changed for Model.Write. Only a model read from a device refuses to set a point that is
// not writable: a simulator sets them all.
func (p *Point) set(encode func(t model.Type, registers []uint16) error) error {
	if p == nil {
		return ErrNoPoint
	}
	m := p.group.model
	if m.device != nil && !p.definition.Writable {
		return &PointError{Model: m.id, Point: p.definition.Name, Err: ErrReadOnly}
	}
	registers := make([]uint16, p.definition.Size)
	if err := encode(p.definition.Type, registers); err != nil {
		return &PointError{Model: m.id, Point: p.definition.Name, Err: err}
	}
	copy(p.registers(), registers)
	for i := range registers {
		m.dirty[p.offset+i] = true
	}

	return nil
}

// encodeInt encodes v, of the signed type t, after checking it fits.
func encodeInt(t model.Type, registers []uint16, v int64) error {
	n := bits(registers)
	lo, hi := intRange(n)
	if t == model.SunSSF {
		lo, hi = -10, 10
	}
	if v < lo || v > hi {
		return ErrOutOfRange
	}
	encodeUint(registers, truncate(v, n))

	return nil
}

// encodeUnsigned encodes v, of an unsigned type, after checking it fits, and is not the value meaning not implemented,
// but for accumulators.
func encodeUnsigned(i typeInfo, registers []uint16, v uint64) error {
	if v > mask(bits(registers)) || (!i.accumulator && v == i.unimplemented) {
		return ErrOutOfRange
	}
	encodeUint(registers, v)

	return nil
}

// parseAddress returns the bytes of the address s, of type t: IPAddr, IPv6Addr or EUI48.
func parseAddress(t model.Type, s string) ([]byte, bool) {
	if t == model.EUI48 {
		mac, ok := parseEUI48(s)

		// After 2 bytes of padding.
		return append([]byte{0, 0}, mac...), ok
	}
	addr, err := netip.ParseAddr(s)
	if err != nil || (t == model.IPAddr) != addr.Is4() {
		return nil, false
	}

	return addr.AsSlice(), true
}

// parseEUI48 parses six pairs of hex digits, separated by colons or dashes.
func parseEUI48(s string) ([]byte, bool) {
	pairs := strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == '-' })
	if len(pairs) != 6 {
		return nil, false
	}
	mac := make([]byte, 0, 6)
	for _, pair := range pairs {
		b, err := hex.DecodeString(pair)
		if err != nil || len(b) != 1 {
			return nil, false
		}
		mac = append(mac, b[0])
	}

	return mac, true
}
