// An installer records an inverter, which device.go simulates, through a record.Recorder put in front of it: a
// gateway polls it every 100ms, through an outage at 100ms. The log is kept as text, a line per request, then
// replayed by a record.Replayer to another gateway, polling halfway between: it reads the inverter as it was at each
// time, outage included.
//
// In the field, modbus.Serve serves the Recorder on the gateway's network, and its Client reaches the real inverter.
// Here, Loopbacks stand for both.
//
// `go run ./examples/09-replay`.
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/fault"
	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/record"
)

// timeout is how long a gateway waits for an answer.
const timeout = 20 * time.Millisecond

func main() {
	ctx := context.Background()

	// Record: the log is written as the gateway polls.
	inverter := newInverter()
	var off bool
	device := fault.NewHandler(inverter, func(fault.Request) fault.Device { return fault.Device{Silent: off} })
	var file bytes.Buffer
	recorder := record.NewRecorder(modbus.NewClient(modbus.NewLoopback(device, modbus.WithTimeout(timeout))), func(e record.Entry) {
		line, err := e.MarshalText()
		if err != nil {
			log.Fatal(err)
		}
		file.Write(append(line, '\n'))
	})
	fmt.Println("record:")
	poll(ctx, recorder, []int{0, 100, 200}, func(at int) {
		switch at {
		case 100:
			off = true
		case 200:
			off = false
			inverter.produce(3900)
		}
	})

	// The log, without the times and the values: the unit, what was asked, and how the inverter answered.
	fmt.Println("log:")
	var entries []record.Entry
	lines := bufio.NewScanner(&file)
	for lines.Scan() {
		var e record.Entry
		if err := e.UnmarshalText(lines.Bytes()); err != nil {
			log.Fatal(err)
		}
		entries = append(entries, e)
		fmt.Println(" ", strings.Join(strings.Fields(lines.Text())[2:8], " "))
	}

	// Replay, halfway between the polls recorded.
	fmt.Println("replay:")
	poll(ctx, record.NewReplayer(entries), []int{50, 150, 250}, func(int) {})
}

// poll discovers the inverter h serves, then reads its power at each time, in milliseconds from now, calling
// before(at) first.
func poll(ctx context.Context, h modbus.Handler, times []int, before func(at int)) {
	start := time.Now()
	device, err := sunspec.Discover(ctx, modbus.NewClient(modbus.NewLoopback(h, modbus.WithTimeout(timeout))), 1)
	if err != nil {
		log.Fatal(err)
	}
	measurements := device.Model(103)
	for _, ms := range times {
		time.Sleep(time.Until(start.Add(time.Duration(ms) * time.Millisecond)))
		before(ms)
		if err := measurements.Read(ctx); err != nil {
			fmt.Printf("  %3dms: %v\n", ms, err)

			continue
		}
		fmt.Printf("  %3dms: %v\n", ms, measurements.Point("W"))
	}
}
