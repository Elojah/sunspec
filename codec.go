package sunspec

import (
	"math"

	"github.com/elojah/sunspec/model"
)

// class groups the point types by how their registers decode.
type class uint8

const (
	// classInteger holds a two's complement or an unsigned integer, high word first: 32- and 64-bit values span 2 and
	// 4 registers, and SunSpec puts the most significant first, whatever the device's own byte order.
	classInteger class = iota + 1
	// classFloat holds an IEEE 754 float, high word first.
	classFloat
	// classString holds bytes, 2 per register, high byte first, padded with NULs.
	classString
	// classAddress holds an IPv4, IPv6 or EUI-48 address, as bytes.
	classAddress
	// classPad holds nothing: it keeps the next point aligned.
	classPad
)

// typeInfo is how a point type decodes.
type typeInfo struct {
	class class
	// signed is whether an integer is two's complement.
	signed bool
	// measurement is whether an integer is a quantity, which Float scales: not an enum, a bitfield or a scale factor.
	measurement bool
	// accumulator is whether an integer is an accumulator, which counts up and wraps, for which 0 means not
	// implemented, and is a value too.
	accumulator bool
	// unimplemented is the integer that means not implemented, which every type reserves: the most negative value of a
	// signed one, all ones for most unsigned ones, 0 for accumulators.
	unimplemented uint64
}

// infos holds each type's typeInfo. SunSpec's information model fixes the values meaning not implemented, so that a
// device can tell it lacks a point the model defines: these are pysunspec2's, SunSpec's reference implementation.
var infos = [...]typeInfo{
	model.Int16:      {class: classInteger, signed: true, measurement: true, unimplemented: 0x8000},
	model.Int32:      {class: classInteger, signed: true, measurement: true, unimplemented: 0x8000_0000},
	model.Int64:      {class: classInteger, signed: true, measurement: true, unimplemented: 0x8000_0000_0000_0000},
	model.Uint16:     {class: classInteger, measurement: true, unimplemented: 0xFFFF},
	model.Uint32:     {class: classInteger, measurement: true, unimplemented: 0xFFFF_FFFF},
	model.Uint64:     {class: classInteger, measurement: true, unimplemented: math.MaxUint64},
	model.Acc16:      {class: classInteger, measurement: true, accumulator: true},
	model.Acc32:      {class: classInteger, measurement: true, accumulator: true},
	model.Acc64:      {class: classInteger, measurement: true, accumulator: true},
	model.Count:      {class: classInteger, measurement: true, unimplemented: 0xFFFF},
	model.Enum16:     {class: classInteger, unimplemented: 0xFFFF},
	model.Enum32:     {class: classInteger, unimplemented: 0xFFFF_FFFF},
	model.Bitfield16: {class: classInteger, unimplemented: 0xFFFF},
	model.Bitfield32: {class: classInteger, unimplemented: 0xFFFF_FFFF},
	model.Float32:    {class: classFloat},
	model.Float64:    {class: classFloat},
	model.String:     {class: classString},
	model.SunSSF:     {class: classInteger, signed: true, unimplemented: 0x8000},
	model.Pad:        {class: classPad},
	model.IPAddr:     {class: classAddress},
	model.IPv6Addr:   {class: classAddress},
	model.EUI48:      {class: classAddress},
}

// info returns t's typeInfo, or the zero one, of no class, for an unknown type.
func info(t model.Type) typeInfo {
	if int(t) >= len(infos) {
		return typeInfo{}
	}

	return infos[t]
}

// bits returns how many bits registers hold.
func bits(registers []uint16) int {
	return 16 * len(registers)
}

// decodeUint returns registers as one unsigned integer, high word first.
func decodeUint(registers []uint16) uint64 {
	var v uint64
	for _, r := range registers {
		v = v<<16 | uint64(r)
	}

	return v
}

// encodeUint writes the low bits(registers) bits of v to registers, high word first.
func encodeUint(registers []uint16, v uint64) {
	for i := len(registers) - 1; i >= 0; i-- {
		registers[i] = uint16(v & 0xFFFF)
		v >>= 16
	}
}

// signExtend returns v, a two's complement integer of n bits, as an int64: 0xFFFF of 16 bits is -1.
func signExtend(v uint64, n int) int64 {
	shift := 64 - n

	return int64(v<<shift) >> shift //nolint:gosec // reinterprets two's complement bits
}

// truncate returns the low n bits of v's two's complement: -1 of 16 bits is 0xFFFF.
func truncate(v int64, n int) uint64 {
	return uint64(v) & mask(n) //nolint:gosec // reinterprets two's complement bits
}

// mask returns n ones, in the low bits.
func mask(n int) uint64 {
	if n >= 64 {
		return math.MaxUint64
	}

	return 1<<n - 1
}

// intRange returns the integers a signed type of n bits holds, but its most negative value, which means not
// implemented: [-32767, 32767] for int16.
func intRange(n int) (int64, int64) {
	maximum := int64(1)<<(n-1) - 1

	return -maximum, maximum
}

// pow10 returns 10^n, for the scale factors' range, [-10, 10].
func pow10(n int) float64 {
	return math.Pow10(n)
}

// scale returns v times 10^sf. A negative sf divides by 10^-sf rather than multiplying by 10^sf, which no float holds
// exactly: 2301 times 0.1 is 230.10000000000002, while 2301 divided by 10 is 230.1, the float nearest the value meant.
func scale(v float64, sf int) float64 {
	if sf < 0 {
		return v / pow10(-sf)
	}

	return v * pow10(sf)
}

// unscale is scale's inverse: the registers' value for v, before rounding.
func unscale(v float64, sf int) float64 {
	if sf < 0 {
		return v * pow10(-sf)
	}

	return v / pow10(sf)
}

// implemented reports whether registers, of type t, hold a value rather than the one meaning not implemented.
func implemented(t model.Type, registers []uint16) bool {
	i := info(t)
	switch i.class {
	case classInteger:
		return decodeUint(registers) != i.unimplemented
	case classFloat:
		return !math.IsNaN(decodeFloat(registers))
	case classString:
		// An empty string, which pysunspec2 tells by its first byte.
		return registers[0]>>8 != 0
	case classAddress:
		if t == model.EUI48 {
			// The 6 bytes after the 2 of padding all 0xFF.
			return decodeUint(registers)&mask(48) != mask(48)
		}

		return !allZero(registers)
	case classPad:
		// A pad holds no value.
		return false
	default:
		return false
	}
}

// setUnimplemented writes to registers, of type t, the value meaning not implemented.
func setUnimplemented(t model.Type, registers []uint16) {
	i := info(t)
	switch i.class {
	case classInteger, classPad:
		encodeUint(registers, i.unimplemented)
	case classFloat:
		// The quiet NaNs SunSpec gives, rather than math.NaN's, 0x7FF8000000000001.
		if len(registers) == 2 {
			encodeUint(registers, 0x7FC0_0000)
		} else {
			encodeUint(registers, 0x7FF8_0000_0000_0000)
		}
	case classAddress:
		clear(registers)
		if t == model.EUI48 {
			encodeUint(registers, mask(48))
		}
	case classString:
		clear(registers)
	default:
		clear(registers)
	}
}

// decodeFloat returns registers as a float32 or a float64, by their count.
func decodeFloat(registers []uint16) float64 {
	if len(registers) == 2 {
		return float64(math.Float32frombits(uint32(registers[0])<<16 | uint32(registers[1])))
	}

	return math.Float64frombits(decodeUint(registers))
}

// encodeFloat writes v to registers, as a float32 or a float64 by their count.
func encodeFloat(registers []uint16, v float64) {
	if len(registers) == 2 {
		encodeUint(registers, uint64(math.Float32bits(float32(v))))

		return
	}
	encodeUint(registers, math.Float64bits(v))
}

func allZero(registers []uint16) bool {
	for _, r := range registers {
		if r != 0 {
			return false
		}
	}

	return true
}

// decodeBytes returns registers as bytes, high byte first.
func decodeBytes(registers []uint16) []byte {
	b := make([]byte, 0, 2*len(registers))
	for _, r := range registers {
		b = append(b, byte(r>>8), byte(r&0xFF))
	}

	return b
}

// encodeBytes writes b to registers, high byte first, padded with zeros. b must fit.
func encodeBytes(registers []uint16, b []byte) {
	clear(registers)
	for i, c := range b {
		registers[i/2] |= uint16(c) << (8 * (1 - i%2))
	}
}
