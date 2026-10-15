// A Modbus TCP client talks to a PLC, which a Memory simulates, served in process: it reads temperatures, writes
// setpoints and a coil, and reads past the registers, which the PLC answers with an exception.
//
// `go run ./examples/01-registers`.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"

	"github.com/elojah/sunspec/modbus"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Temperatures in tenths of a degree, two setpoints, and eight relays.
	plc := modbus.NewMemory(modbus.Tables{
		Coils:            make([]bool, 8),
		InputRegisters:   []uint16{215, 232},
		HoldingRegisters: make([]uint16, 2),
	})
	addr, stop := serve(ctx, plc)
	defer stop()

	client := modbus.NewClient(modbus.NewTCPTransport(addr))
	defer func() { _ = client.Close() }()

	temperatures, err := client.ReadRegisters(ctx, 1, modbus.InputRegisters, 0, 2)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("temperatures: %.1f°C, %.1f°C\n", float64(temperatures[0])/10, float64(temperatures[1])/10)

	if err := client.WriteRegisters(ctx, 1, 0, []uint16{300, 290}); err != nil {
		log.Fatal(err)
	}
	if err := client.WriteCoil(ctx, 1, 3, true); err != nil {
		log.Fatal(err)
	}
	coils, err := client.ReadBits(ctx, 1, modbus.Coils, 0, 8)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("relays:", coils)
	plc.Update(func(tables *modbus.Tables) {
		fmt.Println("setpoints:", tables.HoldingRegisters)
	})

	_, err = client.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 1, 2)
	fmt.Println("past the registers:", err)
	fmt.Println("illegal data address:", errors.Is(err, modbus.ErrIllegalDataAddress))
}

// serve serves h over Modbus TCP on a free local port, and returns its address and a func that stops it.
func serve(ctx context.Context, h modbus.Handler) (string, func()) {
	l, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	served := make(chan error, 1)
	go func() { served <- modbus.Serve(ctx, l, h) }()

	return l.Addr().String(), func() {
		cancel()
		if err := <-served; err != nil {
			log.Fatal(err)
		}
	}
}
