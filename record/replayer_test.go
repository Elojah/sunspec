package record_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/record"
)

var t0 = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

// deviceLog is what a device did over a minute, as a Recorder writes it: registers 0 and 1 moving, an outage from
// 20s to 30s, a read past its registers, a write applied and one refused, and its coils.
func deviceLog() []record.Entry {
	holding, coils := modbus.HoldingRegisters, modbus.Coils
	ms5 := 5 * time.Millisecond

	return []record.Entry{
		{At: t0, Took: ms5, Unit: 1, Table: holding, Address: 0, Quantity: 2, Values: []uint16{10, 20}},
		{At: t0.Add(10 * time.Second), Took: ms5, Unit: 1, Table: holding, Address: 0, Quantity: 2, Values: []uint16{11, 21}},
		{At: t0.Add(20 * time.Second), Took: time.Second, Unit: 1, Table: holding, Address: 0, Quantity: 2, Outcome: record.NoResponse},
		{At: t0.Add(30 * time.Second), Took: ms5, Unit: 1, Table: holding, Address: 0, Quantity: 2, Values: []uint16{12, 22}},
		{At: t0.Add(30 * time.Second), Took: ms5, Unit: 1, Table: holding, Address: 100, Quantity: 1, Outcome: record.Exception, Code: modbus.IllegalDataAddress},
		{At: t0.Add(40 * time.Second), Took: ms5, Unit: 1, Table: holding, Address: 1, Quantity: 1, Write: true, Values: []uint16{99}},
		{
			At: t0.Add(45 * time.Second), Took: ms5, Unit: 1, Table: holding, Address: 0, Quantity: 1, Write: true,
			Outcome: record.Exception, Code: modbus.IllegalDataValue, Values: []uint16{500},
		},
		{At: t0.Add(50 * time.Second), Took: ms5, Unit: 1, Table: coils, Address: 0, Quantity: 2, Values: []uint16{0, 1}},
	}
}

// check is a request to a replay, and what it answers: values, or an error.
type check struct {
	at      time.Duration // since the replay started
	read    func(ctx context.Context, c *modbus.Client) ([]uint16, error)
	want    []uint16
	wantErr error
}

func registers(unit uint8, address, quantity uint16) func(context.Context, *modbus.Client) ([]uint16, error) {
	return func(ctx context.Context, c *modbus.Client) ([]uint16, error) {
		return c.ReadRegisters(ctx, unit, modbus.HoldingRegisters, address, quantity)
	}
}

func coils(address, quantity uint16) func(context.Context, *modbus.Client) ([]uint16, error) {
	return func(ctx context.Context, c *modbus.Client) ([]uint16, error) {
		bits, err := c.ReadBits(ctx, 1, modbus.Coils, address, quantity)
		values := make([]uint16, len(bits))
		for i, bit := range bits {
			if bit {
				values[i] = 1
			}
		}

		return values, err
	}
}

func write(address, value uint16) func(context.Context, *modbus.Client) ([]uint16, error) {
	return func(ctx context.Context, c *modbus.Client) ([]uint16, error) {
		return nil, c.WriteRegister(ctx, 1, address, value)
	}
}

// replay runs checks against r, each once its time has come, through a Loopback timing out after 1s.
func replay(t *testing.T, r *record.Replayer, checks []check) {
	t.Helper()

	c := modbus.NewClient(modbus.NewLoopback(r))
	start := time.Now()
	for _, check := range checks {
		time.Sleep(time.Until(start.Add(check.at)))
		got, err := check.read(t.Context(), c)
		if !errors.Is(err, check.wantErr) || !slices.Equal(got, check.want) {
			t.Errorf("at %v: %v, %v, want %v, %v", check.at, got, err, check.want, check.wantErr)
		}
	}
}

// A replay answers each request with the device's state at its time in the log, whatever the order of the entries.
func TestReplayer(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		entries := deviceLog()
		slices.Reverse(entries)
		backwards := slices.Clone(entries)
		r := record.NewReplayer(entries)
		if !slices.EqualFunc(entries, backwards, equal) {
			t.Error("NewReplayer changed the entries")
		}

		replay(t, r, []check{
			{at: 0, read: registers(1, 0, 2), want: []uint16{10, 20}},
			{at: 5 * time.Second, read: registers(1, 0, 2), want: []uint16{10, 20}},
			{at: 10 * time.Second, read: registers(1, 0, 2), want: []uint16{11, 21}},
			// Coils as first read, at 50s.
			{at: 11 * time.Second, read: coils(0, 2), want: []uint16{0, 1}},
			{at: 20 * time.Second, read: registers(1, 0, 2), want: []uint16{}, wantErr: context.DeadlineExceeded},
			{at: 25 * time.Second, read: registers(1, 0, 2), want: []uint16{}, wantErr: context.DeadlineExceeded},
			{at: 30 * time.Second, read: registers(1, 0, 2), want: []uint16{12, 22}},
			{at: 30 * time.Second, read: registers(1, 100, 1), want: []uint16{}, wantErr: modbus.ErrIllegalDataAddress},
			// Never read, so not the device's.
			{at: 30 * time.Second, read: registers(1, 200, 1), want: []uint16{}, wantErr: modbus.ErrIllegalDataAddress},
			{at: 30 * time.Second, read: registers(1, 0, 3), want: []uint16{}, wantErr: modbus.ErrIllegalDataAddress},
			{at: 31 * time.Second, read: write(0, 7)},
			{at: 31 * time.Second, read: registers(1, 0, 2), want: []uint16{7, 22}},
			// The log writes register 1, not 0.
			{at: 40 * time.Second, read: registers(1, 0, 2), want: []uint16{7, 99}},
			// The device refused the write of register 0.
			{at: 45 * time.Second, read: registers(1, 0, 1), want: []uint16{7}},
			// The log ended: the device stays as it was.
			{at: time.Hour, read: registers(1, 0, 2), want: []uint16{7, 99}},
			// A unit not in the log answers nothing.
			{at: time.Hour, read: registers(2, 0, 2), want: []uint16{}, wantErr: context.DeadlineExceeded},
		})

		// The first answer came as late as the device's, 5ms.
		c := modbus.NewClient(modbus.NewLoopback(record.NewReplayer(deviceLog())))
		start := time.Now()
		if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 2); err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(start); waited != 5*time.Millisecond {
			t.Errorf("answered in %v, want the log's 5ms", waited)
		}
	})
}

// WithFrom replays from a time of the log, as the device was then.
func TestReplayer_WithFrom(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		replay(t, record.NewReplayer(deviceLog(), record.WithFrom(t0.Add(30*time.Second))), []check{
			{at: 0, read: registers(1, 0, 2), want: []uint16{12, 22}},
		})
		replay(t, record.NewReplayer(deviceLog(), record.WithFrom(t0.Add(25*time.Second))), []check{
			{at: 0, read: registers(1, 0, 2), want: []uint16{}, wantErr: context.DeadlineExceeded},
			{at: 5 * time.Second, read: registers(1, 0, 2), want: []uint16{12, 22}},
		})
		// Before the log, the device is as its first entry has it.
		replay(t, record.NewReplayer(deviceLog(), record.WithFrom(t0.Add(-time.Hour))), []check{
			{at: 0, read: registers(1, 0, 2), want: []uint16{10, 20}},
		})
	})
}

// Without entries, a replay answers nothing.
func TestReplayer_Empty(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		replay(t, record.NewReplayer(nil), []check{
			{at: 0, read: registers(1, 0, 1), want: []uint16{}, wantErr: context.DeadlineExceeded},
		})
	})
}
