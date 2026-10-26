package main

import (
	"log"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// inverter simulates a three-phase inverter, which stands for the real one an installer would record.
type inverter struct {
	*simulator.Simulator

	measurements *sunspec.Model
}

func newInverter() inverter {
	measurements := newModel(103)
	set(measurements.Point("W_SF").SetInt(0), measurements.Point("W").SetFloat(4200))
	sim, err := simulator.New(40000, newModel(1), measurements)
	if err != nil {
		log.Fatal(err)
	}

	return inverter{Simulator: sim, measurements: measurements}
}

// produce sets the power the inverter produces, in watts.
func (i inverter) produce(w float64) {
	set(i.Update(func() error { return i.measurements.Point("W").SetFloat(w) }))
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
