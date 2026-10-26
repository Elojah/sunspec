package main

import (
	"log"
	"time"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// wMax is the inverter's maximum power, in watts, which WMaxLimPct is a percentage of.
const wMax = 5000

// sun is what the panels give, in watts, before any limit. A simulation would read it from irradiance data at now.
func sun(time.Time) float64 {
	return 4000
}

// newInverter simulates a 5 kW inverter whose output follows the sun, and the limit its immediate controls set: a
// simulator.Limit hooked to the simulator, which applies a limit once written, at once and for good, since the ramp
// and revert times are 0.
func newInverter() *simulator.Simulator {
	measurements := newModel(103)
	set(measurements.Point("W_SF").SetInt(0))
	controls := newModel(123)
	set(
		controls.Point("WMaxLimPct_SF").SetInt(-1),
		controls.Point("WMaxLimPct").SetFloat(100),
		controls.Point("WMaxLim_Ena").SetUint(0), // DISABLED
		controls.Point("WMaxLimPct_RmpTms").SetUint(0),
		controls.Point("WMaxLimPct_RvrtTms").SetUint(0),
	)
	limit, err := simulator.NewLimit(controls, measurements, wMax, sun)
	if err != nil {
		log.Fatal(err)
	}

	sim, err := simulator.New(40000, newModel(1), measurements, controls)
	if err != nil {
		log.Fatal(err)
	}
	sim.OnWrite(limit.Write)
	sim.OnRead(limit.Read)

	return sim
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
