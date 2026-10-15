// Discover finds the models of a SunSpec inverter, which device.go simulates, then reads its nameplate from the common
// model. The simulator is reached in process, through a modbus.Loopback: a real device's client would be
// modbus.NewClient(modbus.NewTCPTransport("192.168.1.10:502")).
//
// `go run ./examples/03-discover`.
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
	client := modbus.NewClient(modbus.NewLoopback(newInverter()))

	device, err := sunspec.Discover(ctx, client, 1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("SunSpec marker at", device.Base())
	for _, m := range device.Models() {
		label := "unknown"
		if d := m.Definition(); d != nil {
			label = d.Group.Label
		}
		fmt.Printf("model %d at %d, %d registers: %s\n", m.ID(), m.Address(), m.Length(), label)
	}

	common := device.Model(1)
	if err := common.Read(ctx); err != nil {
		log.Fatal(err)
	}
	for _, name := range []string{"Mn", "Md", "Vr", "SN", "Opt"} {
		fmt.Println(common.Point(name))
	}
}
