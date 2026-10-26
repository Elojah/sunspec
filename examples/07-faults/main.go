// A gateway polls an inverter, which device.go simulates, as it fails in the field. fault.Handler makes the inverter
// reboot: silent for a poll, busy for the next while it starts, then back with its energy counter reset to 0, which
// SunSpec reads as not implemented, an accumulator's value for it. fault.Transport makes the line garble a response,
// then lose the one to a write the inverter applied, so that the gateway cannot tell it was, and writes again.
//
// The faults change between polls here. A test or a harness would decide them from the time, a schedule or a random
// draw, in the decide functions.
//
// `go run ./examples/07-faults`.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/fault"
	"github.com/elojah/sunspec/modbus"
)

func main() {
	ctx := context.Background()
	inverter := newInverter()

	var (
		device fault.Device
		line   fault.Link
	)
	faulty := fault.NewHandler(inverter, func(fault.Request) fault.Device { return device })
	// A short timeout, for the silent inverter not to hold the example up.
	loopback := modbus.NewLoopback(faulty, modbus.WithTimeout(10*time.Millisecond))
	client := modbus.NewClient(fault.NewTransport(loopback, func(uint8, modbus.PDU) fault.Link { return line }))

	d, err := sunspec.Discover(ctx, client, 1)
	if err != nil {
		log.Fatal(err)
	}
	measurements := d.Model(103)
	poll := func(what string) {
		if err := measurements.Read(ctx); err != nil {
			fmt.Printf("%s: %v\n", what, err)

			return
		}
		fmt.Printf("%s: %v, %v\n", what, measurements.Point("W"), measurements.Point("WH"))
	}

	poll("up")
	device = fault.Device{Silent: true}
	poll("off")
	device = fault.Device{Exception: modbus.ServerDeviceBusy}
	poll("starting")
	device = fault.Device{}
	inverter.reboot()
	poll("up again")

	line = fault.Link{Err: modbus.ErrCRC, Sent: true}
	poll("noise")
	line = fault.Link{}

	// The limit and its enable are two runs of registers, so two requests: the first is applied, its response lost,
	// and Write stops there.
	controls := d.Model(123)
	if err := controls.Read(ctx); err != nil {
		log.Fatal(err)
	}
	set(controls.Point("WMaxLimPct").SetFloat(50), controls.Point("WMaxLim_Ena").SetUint(1)) // ENABLED
	line = fault.Link{Err: context.DeadlineExceeded, Sent: true}
	fmt.Println("limit:", controls.Write(ctx))
	inverter.report()

	// What was not acknowledged is sent again: writing the limit twice is safe.
	line = fault.Link{}
	fmt.Println("limit again:", controls.Write(ctx))
	inverter.report()
}
