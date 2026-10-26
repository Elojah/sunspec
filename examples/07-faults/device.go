package main

import (
	"fmt"
	"log"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// inverter simulates a three-phase inverter producing 4.2 kW, with its immediate controls.
type inverter struct {
	*simulator.Simulator

	measurements, controls *sunspec.Model
}

func newInverter() inverter {
	measurements := newModel(103)
	set(
		measurements.Point("W_SF").SetInt(0),
		measurements.Point("W").SetFloat(4200),
		measurements.Point("WH_SF").SetInt(0),
		measurements.Point("WH").SetFloat(12345678),
	)
	controls := newModel(123)
	set(
		controls.Point("WMaxLimPct_SF").SetInt(0),
		controls.Point("WMaxLimPct").SetFloat(100),
		controls.Point("WMaxLim_Ena").SetUint(0), // DISABLED
	)
	sim, err := simulator.New(40000, newModel(1), measurements, controls)
	if err != nil {
		log.Fatal(err)
	}

	return inverter{Simulator: sim, measurements: measurements, controls: controls}
}

// reboot resets the energy counter, as some inverters do when they restart.
func (i inverter) reboot() {
	set(i.Update(func() error { return i.measurements.Point("WH").SetFloat(0) }))
}

// report prints the limit as the inverter holds it.
func (i inverter) report() {
	set(i.Update(func() error {
		fmt.Println("inverter:", i.controls.Point("WMaxLimPct"), "|", i.controls.Point("WMaxLim_Ena"))

		return nil
	}))
}

func newModel(id uint16) *sunspec.Model {
	m, err := sunspec.NewModel(model.Lookup(id), nil)
	if err != nil {
		log.Fatal(err)
	}

	return m
}

func set(errs ...error) {
	for _, err := range errs {
		if err != nil {
			log.Fatal(err)
		}
	}
}
