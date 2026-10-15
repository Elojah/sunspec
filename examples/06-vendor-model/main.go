// A battery whose vendor adds a model of its own, 64900, which SunSpec does not define: Discover finds it, but without
// a definition it holds registers only. model.Parse reads the vendor's definition, in SunSpec's JSON format, and
// WithDefinitions hands it to Discover, which then lays its points out as any other model's.
//
// `go run ./examples/06-vendor-model`.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/model"
)

// cells is the vendor's definition: cell voltages in mV, with a fixed scale factor of -3 to read volts, a temperature
// scaled by a point, and a balancing state.
const cells = `{
	"id": 64900,
	"group": {
		"name": "cells",
		"label": "Cell Monitor",
		"points": [
			{"name": "ID", "type": "uint16", "size": 1, "mandatory": "M", "static": "S"},
			{"name": "L", "type": "uint16", "size": 1, "mandatory": "M", "static": "S"},
			{"name": "Tmp_SF", "type": "sunssf", "size": 1},
			{"name": "Tmp", "type": "int16", "size": 1, "sf": "Tmp_SF", "units": "C"},
			{"name": "Bal", "type": "enum16", "size": 1, "symbols": [{"name": "IDLE", "value": 0}, {"name": "BALANCING", "value": 1}]},
			{"name": "N", "type": "uint16", "size": 1}
		],
		"groups": [
			{"name": "cell", "count": "N", "points": [{"name": "V", "type": "uint16", "size": 1, "sf": -3, "units": "V"}]}
		]
	}
}`

func main() {
	ctx := context.Background()
	definition, err := model.Parse(strings.NewReader(cells))
	if err != nil {
		log.Fatal(err)
	}
	client := modbus.NewClient(modbus.NewLoopback(newBattery(definition)))

	device, err := sunspec.Discover(ctx, client, 1)
	if err != nil {
		log.Fatal(err)
	}
	unknown := device.Model(64900)
	if err := unknown.Read(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println("without its definition:", unknown.Definition() == nil, unknown.Registers())

	device, err = sunspec.Discover(ctx, client, 1, sunspec.WithDefinitions(definition))
	if err != nil {
		log.Fatal(err)
	}
	m := device.Model(64900)
	if err := m.Read(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println(m.Definition().Group.Label)
	fmt.Println(m.Point("Tmp"), "|", m.Point("Bal"))
	for _, cell := range m.Groups("cell") {
		fmt.Printf("cell %d: %v\n", cell.Index()+1, cell.Point("V"))
	}
}
