package record

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elojah/sunspec/modbus"
)

// ErrInvalid is matched by *EntryError.
var ErrInvalid = errors.New("record: invalid entry")

// EntryError is an Entry that MarshalText cannot write, or a line UnmarshalText cannot read: Field holds what is
// wrong, Value its text. It matches ErrInvalid.
type EntryError struct {
	Field string
	Value string
}

func (e *EntryError) Error() string {
	return fmt.Sprintf("record: invalid %s %q", e.Field, e.Value)
}

func (e *EntryError) Is(target error) bool {
	return target == ErrInvalid
}

// Outcome is how a device answered a request.
type Outcome uint8

const (
	// Answered is a response: the values read, or the write acknowledged.
	Answered Outcome = iota
	// Exception is an exception response, whose code the Entry holds.
	Exception
	// NoResponse is a request the device did not answer in time, or whose response the link lost or garbled.
	NoResponse
)

// Entry is a request a device was sent, and how it answered.
type Entry struct {
	// At is when the request was sent, and Took how long until its response, or until it failed.
	At   time.Time
	Took time.Duration

	Unit     uint8
	Table    modbus.Table
	Address  uint16
	Quantity uint16
	Write    bool

	Outcome Outcome
	// Code is the exception's, for the Exception outcome only.
	Code modbus.ExceptionCode
	// Values are the registers or bits read, a bit as 0 or 1, for an answered read, or written, for any write: as
	// many as Quantity, or none.
	Values []uint16
}

// The tables, as an Entry's text names them.
var tables = [...]struct {
	table modbus.Table
	name  string
}{
	{modbus.Coils, "coils"},
	{modbus.DiscreteInputs, "discrete"},
	{modbus.InputRegisters, "input"},
	{modbus.HoldingRegisters, "holding"},
}

// MarshalText writes e as one line, its fields separated by a space: the time in RFC 3339 with nanoseconds, the time
// taken as Go writes durations, the unit, read or write, the table (coils, discrete, input or holding), the address
// and the quantity in decimal, the outcome (ok, silent, or x and the exception code in hex, such as x02), then the
// values, registers in 4 hex digits and bits as 0 or 1:
//
//	2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 ok 5375 6E53
//	2026-10-26T12:00:01Z 1s 1 read holding 40002 2 silent
func (e Entry) MarshalText() ([]byte, error) {
	if err := e.check(); err != nil {
		return nil, err
	}

	op := "read"
	if e.Write {
		op = "write"
	}
	b := fmt.Appendf(nil, "%s %s %d %s %s %d %d %s",
		e.At.Format(time.RFC3339Nano), e.Took, e.Unit, op, tableName(e.Table), e.Address, e.Quantity, e.outcome())
	bits := e.Table == modbus.Coils || e.Table == modbus.DiscreteInputs
	for _, v := range e.Values {
		if bits {
			b = fmt.Appendf(b, " %d", v)
		} else {
			b = fmt.Appendf(b, " %04X", v)
		}
	}

	return b, nil
}

// UnmarshalText reads a line MarshalText wrote, hex digits in either case.
func (e *Entry) UnmarshalText(text []byte) error {
	fields := strings.Fields(string(text))
	if len(fields) < 8 {
		return &EntryError{Field: "line", Value: string(text)}
	}

	var (
		parsed Entry
		err    error
	)
	if parsed.At, err = time.Parse(time.RFC3339Nano, fields[0]); err != nil {
		return &EntryError{Field: "time", Value: fields[0]}
	}
	if parsed.Took, err = time.ParseDuration(fields[1]); err != nil || parsed.Took < 0 {
		return &EntryError{Field: "time taken", Value: fields[1]}
	}
	unit, err := strconv.ParseUint(fields[2], 10, 8)
	if err != nil {
		return &EntryError{Field: "unit", Value: fields[2]}
	}
	parsed.Unit = uint8(unit)
	switch fields[3] {
	case "read":
	case "write":
		parsed.Write = true
	default:
		return &EntryError{Field: "operation", Value: fields[3]}
	}
	if parsed.Table = tableNamed(fields[4]); parsed.Table == 0 {
		return &EntryError{Field: "table", Value: fields[4]}
	}
	if parsed.Address, err = parseUint16(fields[5], 10); err != nil {
		return &EntryError{Field: "address", Value: fields[5]}
	}
	if parsed.Quantity, err = parseUint16(fields[6], 10); err != nil {
		return &EntryError{Field: "quantity", Value: fields[6]}
	}
	if err := parsed.parseOutcome(fields[7]); err != nil {
		return err
	}
	for _, v := range fields[8:] {
		value, err := parseUint16(v, 16)
		if err != nil {
			return &EntryError{Field: "value", Value: v}
		}
		parsed.Values = append(parsed.Values, value)
	}
	if err := parsed.check(); err != nil {
		return err
	}
	*e = parsed

	return nil
}

// check returns an *EntryError for an Entry whose fields disagree.
func (e Entry) check() error {
	write := e.Write && (e.Table == modbus.Coils || e.Table == modbus.HoldingRegisters)
	switch {
	case tableName(e.Table) == "":
		return &EntryError{Field: "table", Value: e.Table.String()}
	case e.Write && !write:
		return &EntryError{Field: "operation", Value: "write of " + e.Table.String()}
	case e.Quantity == 0 || int(e.Address)+int(e.Quantity) > 1<<16:
		return &EntryError{Field: "range", Value: fmt.Sprintf("%d from %d", e.Quantity, e.Address)}
	case e.Outcome > NoResponse:
		return &EntryError{Field: "outcome", Value: strconv.Itoa(int(e.Outcome))}
	case (e.Outcome == Exception) != (e.Code != 0):
		return &EntryError{Field: "exception code", Value: e.outcome()}
	}

	// Values are those written, or read once answered.
	want := 0
	if e.Write || e.Outcome == Answered {
		want = int(e.Quantity)
	}
	if len(e.Values) != want {
		return &EntryError{Field: "values", Value: fmt.Sprintf("%d, for %d", len(e.Values), want)}
	}
	if e.Table == modbus.Coils || e.Table == modbus.DiscreteInputs {
		for _, v := range e.Values {
			if v > 1 {
				return &EntryError{Field: "bit", Value: strconv.Itoa(int(v))}
			}
		}
	}

	return nil
}

// outcome returns the outcome's text.
func (e Entry) outcome() string {
	switch e.Outcome {
	case Answered:
		return "ok"
	case Exception:
		return fmt.Sprintf("x%02X", uint8(e.Code))
	case NoResponse:
		return "silent"
	default:
		return fmt.Sprintf("outcome %d", e.Outcome)
	}
}

// parseOutcome sets the outcome, and the code, that s holds.
func (e *Entry) parseOutcome(s string) error {
	switch {
	case s == "ok":
		e.Outcome = Answered
	case s == "silent":
		e.Outcome = NoResponse
	case strings.HasPrefix(s, "x"):
		code, err := strconv.ParseUint(s[1:], 16, 8)
		if err != nil || code == 0 {
			return &EntryError{Field: "outcome", Value: s}
		}
		e.Outcome, e.Code = Exception, modbus.ExceptionCode(code)
	default:
		return &EntryError{Field: "outcome", Value: s}
	}

	return nil
}

// tableName returns t's name in an Entry's text, or "" for none.
func tableName(t modbus.Table) string {
	for _, x := range tables {
		if x.table == t {
			return x.name
		}
	}

	return ""
}

// tableNamed returns the table named name in an Entry's text, or 0 for none.
func tableNamed(name string) modbus.Table {
	for _, x := range tables {
		if x.name == name {
			return x.table
		}
	}

	return 0
}

// parseUint16 parses s, digits only, in base.
func parseUint16(s string, base int) (uint16, error) {
	// ParseUint takes a sign, and an underscore or a prefix with base 0, neither of which MarshalText writes.
	if s == "" || s[0] == '+' {
		return 0, ErrInvalid
	}
	v, err := strconv.ParseUint(s, base, 16)

	return uint16(v), err
}
