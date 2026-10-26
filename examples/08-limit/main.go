// An energy manager limits a 5 kW inverter, which device.go simulates, to half its power: the inverter refuses a limit
// past 100 %, as a device does, applies one of 50 % to its output, and gives what the sun does once it is disabled.
//
// `go run ./examples/08-limit`.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
)

func main() {
	ctx := context.Background()
	device, err := sunspec.Discover(ctx, modbus.NewClient(modbus.NewLoopback(newInverter())), 1)
	if err != nil {
		log.Fatal(err)
	}
	measurements, controls := device.Model(103), device.Model(123)

	output := func(what string) {
		if err := measurements.Read(ctx); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: %v\n", what, measurements.Point("W"))
	}
	limit := func(pct float64, enable uint64) error {
		// Read first, so that Write sends only what is set here, and the scale factor is known.
		if err := controls.Read(ctx); err != nil {
			log.Fatal(err)
		}
		set(controls.Point("WMaxLimPct").SetFloat(pct), controls.Point("WMaxLim_Ena").SetUint(enable))

		return controls.Write(ctx)
	}

	output("no limit")
	fmt.Println("120 %:", limit(120, 1)) // ENABLED
	if err := limit(50, 1); err != nil {
		log.Fatal(err)
	}
	output("50 %")
	if err := limit(50, 0); err != nil { // DISABLED
		log.Fatal(err)
	}
	output("disabled")
}
