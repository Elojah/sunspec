package main

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// errFlag is returned for a flag the inverter cannot run with.
var errFlag = errors.New("invalid flag")

const (
	// base is where the models start, as on most devices.
	base = 40000
	// volts is each phase's voltage, and hertz the grid's frequency.
	volts = 230.1
	hertz = 50
)

// St's values, of model 103.
const (
	sleeping = 2
	mppt     = 4
)

// inverter is a three-phase inverter's model 103, measuring an output of at most wMax watts at noon, under a sun
// rising at 06:00 UTC and setting at 18:00 UTC, of a day lasting day.
type inverter struct {
	model *sunspec.Model
	wMax  float64
	day   time.Duration
	// at is the time of the last measure, which WH sums W up to, and wh is WH unrounded.
	at time.Time
	wh float64
}

// newSimulator returns a Simulator serving the common model and an inverter, measured at each read.
func newSimulator(wMax float64, day time.Duration) (*simulator.Simulator, error) {
	common, err := newModel(1)
	if err != nil {
		return nil, err
	}
	if err := errors.Join(
		common.Point("Mn").SetText("elojah"),
		common.Point("Md").SetText("simulator"),
		common.Point("SN").SetText("1"),
		common.Point("DA").SetUint(1),
	); err != nil {
		return nil, err
	}
	inv, err := newInverter(wMax, day, time.Now())
	if err != nil {
		return nil, err
	}
	sim, err := simulator.New(base, common, inv.model)
	if err != nil {
		return nil, err
	}
	sim.OnRead(func() error { return inv.measure(time.Now()) })

	return sim, nil
}

// newInverter returns an inverter, measured at now.
func newInverter(wMax float64, day time.Duration, now time.Time) (*inverter, error) {
	// W_SF is 0: W is an int16 of watts.
	if wMax < 0 || wMax > math.MaxInt16 {
		return nil, fmt.Errorf("%w: -w-max %g, want 0 to %d", errFlag, wMax, math.MaxInt16)
	}
	if day <= 0 {
		return nil, fmt.Errorf("%w: -day %s, want more than 0", errFlag, day)
	}
	m, err := newModel(103)
	if err != nil {
		return nil, err
	}
	if err := errors.Join(
		m.Point("A_SF").SetInt(-2),
		m.Point("V_SF").SetInt(-1),
		m.Point("W_SF").SetInt(0),
		m.Point("Hz_SF").SetInt(-2),
		m.Point("WH_SF").SetInt(0),
		m.Point("PhVphA").SetFloat(volts),
		m.Point("PhVphB").SetFloat(volts),
		m.Point("PhVphC").SetFloat(volts),
		m.Point("Hz").SetFloat(hertz),
	); err != nil {
		return nil, err
	}
	inv := &inverter{model: m, wMax: wMax, day: day, at: now}

	return inv, inv.measure(now)
}

// measure sets what the inverter measures at now: W follows the sun, each phase carrying a third of it, WH sums W since
// the last measure, in hours of the simulated day, and St is MPPT while the sun shines.
func (i *inverter) measure(now time.Time) error {
	w := i.wMax * i.sun(now)
	if now.After(i.at) {
		i.wh += w * now.Sub(i.at).Hours() * float64(24*time.Hour) / float64(i.day)
		i.at = now
	}
	// From W as read, so that a sun under half a watt is no MPPT at 0 W.
	st := uint64(sleeping)
	if math.Round(w) > 0 {
		st = mppt
	}

	return errors.Join(
		i.model.Point("W").SetFloat(w),
		i.model.Point("A").SetFloat(w/volts),
		i.model.Point("AphA").SetFloat(w/volts/3),
		i.model.Point("AphB").SetFloat(w/volts/3),
		i.model.Point("AphC").SetFloat(w/volts/3),
		i.model.Point("WH").SetFloat(i.wh),
		i.model.Point("St").SetUint(st),
	)
}

// sun returns the sun's share of its noon at now: a half-sine from a quarter of the day to three quarters, 0 at night.
// Days start at midnight UTC, the Unix epoch's.
func (i *inverter) sun(now time.Time) float64 {
	f := float64(now.UnixNano()%int64(i.day)) / float64(i.day)
	if f <= 0.25 || f >= 0.75 {
		return 0
	}

	return math.Sin(math.Pi * (f - 0.25) / 0.5)
}

// newModel returns SunSpec's model id, laid out without repetitions.
func newModel(id uint16) (*sunspec.Model, error) {
	return sunspec.NewModel(model.Lookup(id), nil)
}
