// A Modbus RTU client and an energy meter share a serial line, which uart.go simulates as a microcontroller's UART
// does: reads return at once, with nothing when no byte came. The client reads the meter, broadcasts a write, which no
// device answers, then asks a device that is not on the line, until its timeout.
//
// `go run ./examples/02-rtu`, or `tinygo run ./examples/02-rtu`.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/elojah/sunspec/modbus"
)

const (
	// The line's speed, which sets the silence between frames: 4 ms at 9600 baud.
	baudRate = 9600
	// The meter's address on the line.
	meterUnit = 1
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientPort, meterPort := newLine()
	// Energy in Wh, as a 32-bit value over two registers, high word first, then a tariff.
	meter := modbus.NewMemory(modbus.Tables{HoldingRegisters: []uint16{0x0001, 0xE240, 1}})
	served := make(chan error, 1)
	go func() { served <- modbus.ServeRTU(ctx, meterPort, baudRate, meterUnit, meter) }()

	client := modbus.NewClient(modbus.NewRTUTransport(clientPort, baudRate, modbus.WithTimeout(100*time.Millisecond)))
	energy, err := client.ReadRegisters(ctx, meterUnit, modbus.HoldingRegisters, 0, 2)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("energy: %d Wh\n", uint32(energy[0])<<16|uint32(energy[1]))

	// Unit 0 reaches every device on the line, and none answers.
	if err := client.WriteRegister(ctx, 0, 2, 2); err != nil {
		log.Fatal(err)
	}
	tariff, err := client.ReadRegisters(ctx, meterUnit, modbus.HoldingRegisters, 2, 1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("tariff after the broadcast:", tariff[0])

	_, err = client.ReadRegisters(ctx, 2, modbus.HoldingRegisters, 0, 1)
	fmt.Println("unit 2:", err)

	cancel()
	if err := <-served; err != nil {
		log.Fatal(err)
	}
}
