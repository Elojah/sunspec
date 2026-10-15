// A gateway reads a three-phase inverter, which device.go simulates: its AC measurements, each scaled by its scale
// factor, its state, its events, and each of its MPPT inputs, repetitions of a group of model 160. The inverter does
// not measure reactive power, which reads as not implemented.
//
// `go run ./examples/04-inverter`.
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

	inverter := device.Model(103)
	if err := inverter.Read(ctx); err != nil {
		log.Fatal(err)
	}
	for _, name := range []string{"W", "A", "PhVphA", "Hz", "WH", "VAr", "St", "Evt1"} {
		fmt.Println(inverter.Point(name))
	}
	// A value as a number, for a time series: ok is false when it is not implemented.
	if w, ok := inverter.Point("W").Float(); ok {
		fmt.Printf("%.1f kW\n", w/1000)
	}

	mppt := device.Model(160)
	if err := mppt.Read(ctx); err != nil {
		log.Fatal(err)
	}
	for _, module := range mppt.Groups("module") {
		id, _ := module.Point("IDStr").Text()
		v, _ := module.Point("DCV").Float()
		a, _ := module.Point("DCA").Float()
		w, _ := module.Point("DCW").Float()
		fmt.Printf("input %d (%s): %g V, %g A, %g W\n", module.Index()+1, id, v, a, w)
	}
}
