package sunspec_test

import (
	"testing"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// Reading a three-phase inverter, in process: one request, then the layout.
func BenchmarkModelRead(b *testing.B) {
	inverter, err := sunspec.NewModel(model.Lookup(103), nil)
	if err != nil {
		b.Fatal(err)
	}
	sim, err := simulator.New(40000, inverter)
	if err != nil {
		b.Fatal(err)
	}
	d, err := sunspec.Discover(b.Context(), modbus.NewClient(modbus.NewLoopback(sim)), 1)
	if err != nil {
		b.Fatal(err)
	}
	m := d.Model(103)
	for b.Loop() {
		if err := m.Read(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

// Every point of a three-phase inverter, scaled.
func BenchmarkPointFloat(b *testing.B) {
	inverter, err := sunspec.NewModel(model.Lookup(103), nil)
	if err != nil {
		b.Fatal(err)
	}
	points := inverter.Points()
	for b.Loop() {
		for _, p := range points {
			p.Float()
		}
	}
}
