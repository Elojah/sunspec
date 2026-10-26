package record_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elojah/sunspec/fault"
	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/record"
)

// A gateway reads a device through a Recorder, which answers as the device did, its exceptions and silence included,
// and records each request with what the device answered and when.
func TestRecorder(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		memory := modbus.NewMemory(modbus.Tables{HoldingRegisters: []uint16{10, 20, 30}, Coils: []bool{true, false}})
		var device fault.Device // what the device does, which the requests below change as they go
		faulty := fault.NewHandler(memory, func(fault.Request) fault.Device { return device })
		var entries []record.Entry
		recorder := record.NewRecorder(
			modbus.NewClient(modbus.NewLoopback(faulty, modbus.WithTimeout(100*time.Millisecond))),
			func(e record.Entry) { entries = append(entries, e) },
		)
		gateway := modbus.NewClient(modbus.NewLoopback(recorder))
		ctx, start := t.Context(), time.Now()

		if got, err := gateway.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 0, 2); err != nil || !slices.Equal(got, []uint16{10, 20}) {
			t.Errorf("read: %v, %v, want [10 20]", got, err)
		}
		time.Sleep(time.Second)
		if err := gateway.WriteCoils(ctx, 1, 0, []bool{false, true}); err != nil {
			t.Errorf("write coils: %v", err)
		}
		device = fault.Device{Exception: modbus.ServerDeviceBusy}
		if _, err := gateway.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 0, 2); !errors.Is(err, modbus.ErrServerDeviceBusy) {
			t.Errorf("busy: got %v, want ErrServerDeviceBusy", err)
		}
		device = fault.Device{Delay: 20 * time.Millisecond}
		if err := gateway.WriteRegister(ctx, 1, 2, 7); err != nil {
			t.Errorf("write: %v", err)
		}
		// The device times out after 100ms, the gateway after its own 1s.
		device = fault.Device{Silent: true}
		if _, err := gateway.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 0, 2); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("silent: got %v, want context.DeadlineExceeded", err)
		}
		device = fault.Device{}
		if got, err := gateway.ReadBits(ctx, 1, modbus.Coils, 0, 2); err != nil || !slices.Equal(got, []bool{false, true}) {
			t.Errorf("read coils: %v, %v, want [false true]", got, err)
		}

		holding, coils := modbus.HoldingRegisters, modbus.Coils
		second := start.Add(time.Second)
		want := []record.Entry{
			{At: start, Unit: 1, Table: holding, Address: 0, Quantity: 2, Values: []uint16{10, 20}},
			{At: second, Unit: 1, Table: coils, Address: 0, Quantity: 2, Write: true, Values: []uint16{0, 1}},
			{At: second, Unit: 1, Table: holding, Address: 0, Quantity: 2, Outcome: record.Exception, Code: modbus.ServerDeviceBusy},
			{At: second, Took: 20 * time.Millisecond, Unit: 1, Table: holding, Address: 2, Quantity: 1, Write: true, Values: []uint16{7}},
			{At: second.Add(20 * time.Millisecond), Took: 100 * time.Millisecond, Unit: 1, Table: holding, Address: 0, Quantity: 2, Outcome: record.NoResponse},
			{At: second.Add(1020 * time.Millisecond), Unit: 1, Table: coils, Address: 0, Quantity: 2, Values: []uint16{0, 1}},
		}
		if len(entries) != len(want) {
			t.Fatalf("%d entries, want %d: %+v", len(entries), len(want), entries)
		}
		for i := range want {
			if !equal(entries[i], want[i]) {
				t.Errorf("entry %d:\n%+v\nwant\n%+v", i, entries[i], want[i])
			}
		}
	})
}
