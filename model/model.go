package model

import "fmt"

// Definition is a model's definition: its ID, and its group, whose first points are always ID and L, the model's
// header. Definitions are shared, by Lookup among others: never modify one.
type Definition struct {
	ID    uint16
	Group Group
}

// Group is a sequence of points, then of groups, each laid out in turn: a group's registers are its points', then each
// of its groups' as many times as it repeats.
type Group struct {
	Name  string
	Label string

	// Count is how many times the group repeats in its parent: 1, or 0 for a group that repeats until the end of the
	// model, as many times as the model's length L leaves room for. Only the model's last group may, without groups of
	// its own. CountPoint, when set, names the point that holds the count instead, and Count is 0: a point of the
	// parent, or of an ancestor, laid out before the group.
	Count      int
	CountPoint string

	Points []Point
	Groups []Group
}

// Point is a value, held in Size registers.
type Point struct {
	Name  string
	Label string
	Units string
	Type  Type
	Size  uint16

	// SF is the point's scale factor: its value is the registers' times 10^SF.
	SF ScaleFactor

	// Writable is whether the point can be written: access RW. Mandatory is whether a device must implement it, and
	// Static whether its value never changes, such as a serial number.
	Writable  bool
	Mandatory bool
	Static    bool

	// Symbols names the values of an enum, or the bits of a bitfield, from bit 0.
	Symbols []Symbol
}

// ScaleFactor is a point's scale factor: the value of the sunssf point named Point, in the point's group or an
// ancestor, read with it; or Value, fixed. The zero value is a factor of 10^0, so none.
type ScaleFactor struct {
	Point string
	Value int8
}

// Symbol names a value of an enum, or a bit of a bitfield.
type Symbol struct {
	Name  string
	Label string
	Value uint32
}

// Type is a point's type: it sets how the point's registers decode, and which value means not implemented.
type Type uint8

// The point types of the SunSpec information model.
const (
	Int16 Type = iota + 1
	Int32
	Int64
	Uint16
	Uint32
	Uint64
	Acc16
	Acc32
	Acc64
	Count
	Enum16
	Enum32
	Bitfield16
	Bitfield32
	Float32
	Float64
	String
	SunSSF
	Pad
	IPAddr
	IPv6Addr
	EUI48
)

// types holds each type's name in SunSpec's definitions, and size in registers: 0 for a string, whose size is each
// point's.
var types = [...]struct {
	name string
	size uint16
}{
	Int16:      {"int16", 1},
	Int32:      {"int32", 2},
	Int64:      {"int64", 4},
	Uint16:     {"uint16", 1},
	Uint32:     {"uint32", 2},
	Uint64:     {"uint64", 4},
	Acc16:      {"acc16", 1},
	Acc32:      {"acc32", 2},
	Acc64:      {"acc64", 4},
	Count:      {"count", 1},
	Enum16:     {"enum16", 1},
	Enum32:     {"enum32", 2},
	Bitfield16: {"bitfield16", 1},
	Bitfield32: {"bitfield32", 2},
	Float32:    {"float32", 2},
	Float64:    {"float64", 4},
	String:     {"string", 0},
	SunSSF:     {"sunssf", 1},
	Pad:        {"pad", 1},
	IPAddr:     {"ipaddr", 2},
	IPv6Addr:   {"ipv6addr", 8},
	// 6 bytes in 4 registers: the first 2 bytes are padding.
	EUI48: {"eui48", 4},
}

// ParseType returns the type SunSpec's definitions name name, such as "uint16".
func ParseType(name string) (Type, bool) {
	for t := Int16; t.valid(); t++ {
		if types[t].name == name {
			return t, true
		}
	}

	return 0, false
}

// String returns the type's name in SunSpec's definitions, such as "uint16".
func (t Type) String() string {
	if !t.valid() {
		return fmt.Sprintf("type %d", uint8(t))
	}

	return types[t].name
}

// Size returns the type's size in registers, or 0 for String, whose points each have their own size.
func (t Type) Size() uint16 {
	if !t.valid() {
		return 0
	}

	return types[t].size
}

func (t Type) valid() bool {
	return t >= Int16 && int(t) < len(types)
}
