package main

import (
	"fmt"
	"log"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// inverter simulates an inverter's immediate controls, at full power.
type inverter struct {
	*simulator.Simulator

	controls *sunspec.Model
}

func newInverter() inverter {
	controls, err := sunspec.NewModel(model.Lookup(123), nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, err := range []error{
		controls.Point("WMaxLimPct_SF").SetInt(-1),
		controls.Point("WMaxLimPct").SetFloat(100),
		controls.Point("WMaxLim_Ena").SetUint(0), // DISABLED
	} {
		if err != nil {
			log.Fatal(err)
		}
	}
	common, err := sunspec.NewModel(model.Lookup(1), nil)
	if err != nil {
		log.Fatal(err)
	}
	sim, err := simulator.New(40000, common, controls)
	if err != nil {
		log.Fatal(err)
	}

	return inverter{Simulator: sim, controls: controls}
}

// report prints the limit as the inverter holds it, raw and scaled.
func (i inverter) report() {
	err := i.Update(func() error {
		limit := i.controls.Point("WMaxLimPct")
		raw, _ := limit.Int()
		fmt.Printf("inverter: limit register %d, %v\n", raw, limit)

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}
