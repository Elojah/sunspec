package main

import (
	"log"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// newInverter simulates a three-phase inverter with two MPPT inputs, whose nameplate leaves out its options.
func newInverter() *simulator.Simulator {
	common := newModel(1, nil)
	set(
		common.Point("Mn").SetText("Elojah"),
		common.Point("Md").SetText("SPV-5000-3P"),
		common.Point("Vr").SetText("1.4.2"),
		common.Point("SN").SetText("SPV5K0042"),
	)
	sim, err := simulator.New(40000, common, newModel(103, nil), newModel(160, map[string]int{"module": 2}))
	if err != nil {
		log.Fatal(err)
	}

	return sim
}

func newModel(id uint16, repeats map[string]int) *sunspec.Model {
	m, err := sunspec.NewModel(model.Lookup(id), repeats)
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
