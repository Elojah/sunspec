package main

import (
	"log"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// newInverter simulates a three-phase inverter producing 4.2 kW from two strings.
func newInverter() *simulator.Simulator {
	inverter := newModel(103, nil)
	set(
		// Each measurement is an integer, and its scale factor a power of ten: W is 420 × 10^1.
		inverter.Point("W_SF").SetInt(1),
		inverter.Point("W").SetFloat(4200),
		inverter.Point("A_SF").SetInt(-2),
		inverter.Point("A").SetFloat(6.08),
		inverter.Point("V_SF").SetInt(-1),
		inverter.Point("PhVphA").SetFloat(230.1),
		inverter.Point("Hz_SF").SetInt(-2),
		inverter.Point("Hz").SetFloat(50.02),
		inverter.Point("WH_SF").SetInt(0),
		inverter.Point("WH").SetFloat(12345678),
		inverter.Point("St").SetUint(4),      // MPPT
		inverter.Point("Evt1").SetUint(1<<7), // OVER_TEMP
	)

	mppt := newModel(160, map[string]int{"module": 2})
	set(
		mppt.Point("DCV_SF").SetInt(-1),
		mppt.Point("DCA_SF").SetInt(-2),
		mppt.Point("DCW_SF").SetInt(0),
	)
	for i, module := range mppt.Groups("module") {
		v, a := []float64{412.3, 398.7}[i], []float64{5.1, 5.3}[i]
		set(
			module.Point("IDStr").SetText([]string{"roof east", "roof west"}[i]),
			module.Point("DCV").SetFloat(v),
			module.Point("DCA").SetFloat(a),
			module.Point("DCW").SetFloat(v*a),
		)
	}

	sim, err := simulator.New(40000, newModel(1, nil), inverter, mppt)
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
