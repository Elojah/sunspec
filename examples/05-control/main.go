// An energy manager limits an inverter's power to half its maximum, through the immediate controls of model 123, which
// device.go simulates: it sets the limit and enables it, then writes both, in the one request Write makes of
// neighbouring registers. A point the model does not let a client write is refused before anything is sent.
//
// `go run ./examples/05-control`.
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
	inverter := newInverter()
	device, err := sunspec.Discover(ctx, modbus.NewClient(modbus.NewLoopback(inverter)), 1)
	if err != nil {
		log.Fatal(err)
	}

	controls := device.Model(123)
	if err := controls.Read(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println(controls.Point("WMaxLimPct"), "|", controls.Point("WMaxLim_Ena"))

	// SetFloat scales by WMaxLimPct_SF, which Read got with it: 50 % is 500 × 10^-1.
	if err := controls.Point("WMaxLimPct").SetFloat(50); err != nil {
		log.Fatal(err)
	}
	if err := controls.Point("WMaxLim_Ena").SetUint(1); err != nil { // ENABLED
		log.Fatal(err)
	}
	if err := controls.Write(ctx); err != nil {
		log.Fatal(err)
	}
	inverter.report()

	// Read back from the device.
	if err := controls.Read(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println(controls.Point("WMaxLimPct"), "|", controls.Point("WMaxLim_Ena"))

	fmt.Println(controls.Point("WMaxLimPct_SF").SetInt(0))
}
