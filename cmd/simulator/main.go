// Command simulator serves a simulated SunSpec three-phase inverter over Modbus TCP, to test a client against without
// hardware, until SIGINT or SIGTERM:
//
//	go run github.com/elojah/sunspec/cmd/simulator@latest -address :1502 -day 10m
//
// It serves the common model (1) and a three-phase inverter's (103) as holding registers from 40000, to any unit,
// through a simulator.Simulator. Its output follows a sun rising at 06:00 UTC and setting at 18:00 UTC, of a day lasting
// -day: W peaks at -w-max at noon, A follows it, WH sums it, and St is MPPT by day, SLEEPING by night.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/elojah/sunspec/modbus"
)

func main() {
	address := flag.String("address", ":502", "the host and port to listen on")
	wMax := flag.Float64("w-max", 5000, "the output at noon, in watts, from 0 to 32767")
	day := flag.Duration("day", 24*time.Hour, "how long a day lasts, such as 10m for a day's output in minutes")
	flag.Parse()

	if err := run(*address, *wMax, *day); err != nil {
		slog.Error("simulator stopped", "error", err)
		os.Exit(1)
	}
}

// run serves the inverter on address until signalled.
func run(address string, wMax float64, day time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sim, err := newSimulator(wMax, day)
	if err != nil {
		return err
	}
	l, err := new(net.ListenConfig).Listen(ctx, "tcp", address)
	if err != nil {
		return err
	}
	slog.Info("simulator listening", "address", l.Addr().String(), "w_max", wMax, "day", day)

	return modbus.Serve(ctx, l, sim)
}
