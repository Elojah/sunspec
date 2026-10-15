package main

import (
	"log"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// newBattery simulates a battery of 4 cells, balancing at 24.5°C.
func newBattery(definition *model.Definition) *simulator.Simulator {
	cells, err := sunspec.NewModel(definition, map[string]int{"cell": 4})
	if err != nil {
		log.Fatal(err)
	}
	set(
		cells.Point("Tmp_SF").SetInt(-1),
		cells.Point("Tmp").SetFloat(24.5),
		cells.Point("Bal").SetUint(1),
	)
	for i, cell := range cells.Groups("cell") {
		set(cell.Point("V").SetFloat([]float64{3.301, 3.298, 3.312, 3.305}[i]))
	}
	common, err := sunspec.NewModel(model.Lookup(1), nil)
	if err != nil {
		log.Fatal(err)
	}
	sim, err := simulator.New(40000, common, cells)
	if err != nil {
		log.Fatal(err)
	}

	return sim
}

func set(errs ...error) {
	for _, err := range errs {
		if err != nil {
			log.Fatal(err)
		}
	}
}
