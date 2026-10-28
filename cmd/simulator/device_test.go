package main

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
)

// midnight is a day's start, for a day of 24h as for one of 10 minutes.
var midnight = time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)

// TestNewSimulator: a client discovers the common model and the inverter's, which measures at the time of the read.
func TestNewSimulator(t *testing.T) {
	t.Parallel()

	sim, err := newSimulator(5000, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	device, err := sunspec.Discover(ctx, modbus.NewClient(modbus.NewLoopback(sim)), 1)
	if err != nil {
		t.Fatal(err)
	}
	common := device.Model(1)
	if err := common.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if mn, _ := common.Point("Mn").Text(); mn != "elojah" {
		t.Errorf("Mn = %q, want elojah", mn)
	}
	measurements := device.Model(103)
	if err := measurements.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if v, ok := measurements.Point("PhVphA").Float(); !ok || v != volts {
		t.Errorf("PhVphA = %v, %v, want %v", v, ok, volts)
	}
	want := 5000 * (&inverter{wMax: 5000, day: 24 * time.Hour}).sun(time.Now())
	if w, ok := measurements.Point("W").Float(); !ok || math.Abs(w-want) > 1 {
		t.Errorf("W = %v, %v, want about %v", w, ok, want)
	}
}

// TestNewInverter_Flags: a power past W's int16, or a day not positive, is refused.
func TestNewInverter_Flags(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		wMax float64
		day  time.Duration
	}{{-1, time.Minute}, {40000, time.Minute}, {5000, 0}} {
		if _, err := newInverter(c.wMax, c.day, midnight); !errors.Is(err, errFlag) {
			t.Errorf("newInverter(%v, %v) = %v, want errFlag", c.wMax, c.day, err)
		}
	}
}

// TestMeasure: W peaks at noon, is 0 at night, when the inverter sleeps, and WH sums it.
func TestMeasure(t *testing.T) {
	t.Parallel()

	inv, err := newInverter(5000, 24*time.Hour, midnight)
	if err != nil {
		t.Fatal(err)
	}
	check(t, inv, 0, "SLEEPING", 0)

	noon := midnight.Add(12 * time.Hour)
	inv.at = noon // nothing to sum before
	if err := inv.measure(noon); err != nil {
		t.Fatal(err)
	}
	check(t, inv, 5000, "MPPT", 0)

	// 13:00 is 15° past noon: W is 5000·cos(15°), summed over the hour.
	w := 5000 * math.Cos(math.Pi/12)
	if err := inv.measure(noon.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	check(t, inv, w, "MPPT", w)

	if err := inv.measure(midnight.Add(20 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	check(t, inv, 0, "SLEEPING", w)
}

// TestMeasure_Sunrise: a sixth of a second past 06:00, the sun gives under half a watt: W reads 0, and the inverter
// still sleeps.
func TestMeasure_Sunrise(t *testing.T) {
	t.Parallel()

	inv, err := newInverter(5000, 24*time.Hour, midnight.Add(6*time.Hour+time.Second/6))
	if err != nil {
		t.Fatal(err)
	}
	check(t, inv, 0, "SLEEPING", 0)
}

// TestMeasure_Day: a day of 10 minutes peaks 5 minutes past each tenth of an hour, and sums a simulated hour in 25s.
func TestMeasure_Day(t *testing.T) {
	t.Parallel()

	noon := midnight.Add(5 * time.Minute)
	inv, err := newInverter(5000, 10*time.Minute, noon)
	if err != nil {
		t.Fatal(err)
	}
	check(t, inv, 5000, "MPPT", 0)

	w := 5000 * math.Cos(math.Pi/12)
	if err := inv.measure(noon.Add(25 * time.Second)); err != nil {
		t.Fatal(err)
	}
	check(t, inv, w, "MPPT", w)
}

// check reports inv's W, St and WH other than w, st and wh, rounded as its scale factors do.
func check(t *testing.T, inv *inverter, w float64, st string, wh float64) {
	t.Helper()

	if got, ok := inv.model.Point("W").Float(); !ok || got != math.Round(w) {
		t.Errorf("W = %v, %v, want %v", got, ok, math.Round(w))
	}
	if got, ok := inv.model.Point("A").Float(); !ok || math.Abs(got-w/volts) > 0.01 {
		t.Errorf("A = %v, %v, want %.2f", got, ok, w/volts)
	}
	if got, ok := inv.model.Point("St").Symbol(); !ok || got.Name != st {
		t.Errorf("St = %v, %v, want %s", got.Name, ok, st)
	}
	// An accumulator at 0 is not implemented: Float reports 0.
	if got, _ := inv.model.Point("WH").Float(); got != math.Round(wh) {
		t.Errorf("WH = %v, want %v", got, math.Round(wh))
	}
}
