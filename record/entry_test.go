package record_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/record"
)

var at = time.Date(2026, 10, 26, 12, 0, 0, 500_000_000, time.UTC)

// equal reports whether a and b hold the same entry, their times the same instant.
func equal(a, b record.Entry) bool {
	return a.At.Equal(b.At) && a.Took == b.Took && a.Unit == b.Unit && a.Table == b.Table && a.Address == b.Address &&
		a.Quantity == b.Quantity && a.Write == b.Write && a.Outcome == b.Outcome && a.Code == b.Code &&
		slices.Equal(a.Values, b.Values)
}

// Each kind of entry, as a line and back.
func TestEntry_Text(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		entry record.Entry
		text  string
	}{
		{
			record.Entry{At: at, Took: 85 * time.Millisecond, Unit: 1, Table: modbus.HoldingRegisters, Address: 40000, Quantity: 2, Values: []uint16{0x5375, 0x6E53}},
			"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 ok 5375 6E53",
		},
		{
			record.Entry{At: at, Took: time.Second, Unit: 1, Table: modbus.HoldingRegisters, Address: 40002, Quantity: 2, Outcome: record.NoResponse},
			"2026-10-26T12:00:00.5Z 1s 1 read holding 40002 2 silent",
		},
		{
			record.Entry{At: at, Took: time.Millisecond, Unit: 2, Table: modbus.InputRegisters, Address: 0, Quantity: 3, Outcome: record.Exception, Code: modbus.IllegalDataAddress},
			"2026-10-26T12:00:00.5Z 1ms 2 read input 0 3 x02",
		},
		{
			record.Entry{At: at, Unit: 247, Table: modbus.Coils, Address: 3, Quantity: 2, Write: true, Values: []uint16{1, 0}},
			"2026-10-26T12:00:00.5Z 0s 247 write coils 3 2 ok 1 0",
		},
		{
			// A write refused keeps the values written.
			record.Entry{
				At: at, Unit: 1, Table: modbus.HoldingRegisters, Address: 40100, Quantity: 1, Write: true,
				Outcome: record.Exception, Code: modbus.IllegalDataValue, Values: []uint16{1200},
			},
			"2026-10-26T12:00:00.5Z 0s 1 write holding 40100 1 x03 04B0",
		},
		{
			record.Entry{At: at, Unit: 1, Table: modbus.DiscreteInputs, Address: 0, Quantity: 1, Values: []uint16{1}},
			"2026-10-26T12:00:00.5Z 0s 1 read discrete 0 1 ok 1",
		},
	} {
		text, err := tt.entry.MarshalText()
		if err != nil {
			t.Errorf("%s: %v", tt.text, err)

			continue
		}
		if string(text) != tt.text {
			t.Errorf("MarshalText = %q, want %q", text, tt.text)
		}
		var got record.Entry
		if err := got.UnmarshalText([]byte(tt.text)); err != nil {
			t.Errorf("%s: %v", tt.text, err)

			continue
		}
		if !equal(got, tt.entry) {
			t.Errorf("%s: read back %+v, want %+v", tt.text, got, tt.entry)
		}
	}
}

// A line that is not an entry's, or an entry whose fields disagree, is an *EntryError naming what is wrong.
func TestEntry_Invalid(t *testing.T) {
	t.Parallel()

	const ok = "2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 ok 5375 6E53"
	for _, tt := range []struct{ text, field string }{
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000", "line"},
		{"yesterday 85ms 1 read holding 40000 2 ok 1 2", "time"},
		{"2026-10-26T12:00:00.5Z -1ms 1 read holding 40000 2 ok 1 2", "time taken"},
		{"2026-10-26T12:00:00.5Z 85ms 256 read holding 40000 2 ok 1 2", "unit"},
		{"2026-10-26T12:00:00.5Z 85ms 1 poll holding 40000 2 ok 1 2", "operation"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read registers 40000 2 ok 1 2", "table"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding +40000 2 ok 1 2", "address"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 two ok 1 2", "quantity"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 fine 1 2", "outcome"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 x00", "outcome"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 ok 1 G", "value"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 ok 1", "values"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 silent 1 2", "values"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 0 ok", "range"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read holding 65535 2 ok 1 2", "range"},
		{"2026-10-26T12:00:00.5Z 85ms 1 write input 0 1 ok 1", "operation"},
		{"2026-10-26T12:00:00.5Z 85ms 1 read coils 0 1 ok 2", "bit"},
	} {
		got := record.Entry{Unit: 9}
		err := got.UnmarshalText([]byte(tt.text))
		entryErr, isEntryErr := errors.AsType[*record.EntryError](err)
		switch {
		case !errors.Is(err, record.ErrInvalid) || !isEntryErr:
			t.Errorf("%q: got %v, want an *EntryError", tt.text, err)
		case entryErr.Field != tt.field:
			t.Errorf("%q: field %q, want %q", tt.text, entryErr.Field, tt.field)
		case got.Unit != 9:
			t.Errorf("%q: the entry changed", tt.text)
		}
	}

	var e record.Entry
	if err := e.UnmarshalText([]byte(ok)); err != nil {
		t.Errorf("%q: %v", ok, err)
	}
	e.Code = modbus.ServerDeviceBusy
	if _, err := e.MarshalText(); !errors.Is(err, record.ErrInvalid) {
		t.Errorf("an answer with an exception code: got %v, want ErrInvalid", err)
	}
}

// Any line read writes back as a line that reads the same entry, and writes back the same.
func FuzzEntry(f *testing.F) {
	for _, s := range []string{
		"2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 ok 5375 6E53",
		"2026-10-26T12:00:00.5Z 1s 1 read holding 40002 2 silent",
		"2026-10-26T12:00:00+02:00 1ms 2 read input 0 3 x02",
		"2026-10-26T12:00:00.5Z 0s 247 write coils 3 2 ok 1 0",
		"2026-10-26T12:00:00.5Z 1h2m 1 write holding 40100 1 x03 04b0",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var e record.Entry
		if e.UnmarshalText([]byte(s)) != nil {
			return
		}
		text, err := e.MarshalText()
		if err != nil {
			t.Fatalf("%q read, but does not write: %v", s, err)
		}
		var again record.Entry
		if err := again.UnmarshalText(text); err != nil {
			t.Fatalf("%q, written as %q, does not read: %v", s, text, err)
		}
		if !equal(again, e) {
			t.Fatalf("%q read as %+v, written as %q, read back as %+v", s, e, text, again)
		}
		if text2, _ := again.MarshalText(); string(text2) != string(text) {
			t.Fatalf("%q written as %q, then %q", s, text, text2)
		}
	})
}
