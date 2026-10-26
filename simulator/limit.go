package simulator

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
)

// ErrMissingPoint is returned by NewLimit for a model that lacks a point it needs.
var ErrMissingPoint = errors.New("simulator: model lacks a point")

// WMaxLim_Ena's values.
const (
	disabled = 0
	enabled  = 1
)

// Limit simulates the active power limit of model 123, the immediate controls, on an inverter's W: what the panels
// give, available, capped while WMaxLim_Ena is ENABLED by WMaxLimPct percent of the inverter's maximum power.
//
// A client's write of WMaxLimPct or WMaxLim_Ena, even with the values they held, changes the cap: it ramps from the
// cap at that time over WMaxLimPct_RmpTms seconds, and reverts after WMaxLimPct_RvrtTms seconds unless written again:
// WMaxLim_Ena goes back to DISABLED, and the cap ramps back to the maximum. A time of 0 is at once, or never for a
// revert. A WMaxLimPct outside 0 to 100 is refused with modbus.ErrIllegalDataValue, as is a WMaxLim_Ena other than
// DISABLED or ENABLED. WMaxLimPct_WinTms, a random delay, is not simulated: a change applies at once.
//
// Its methods are hooks, for Simulator.OnWrite and Simulator.OnRead, and run under the Simulator's lock:
//
//	limit, err := simulator.NewLimit(controls, inverter, 5000, sun)
//	...
//	sim.OnWrite(limit.Write)
//	sim.OnRead(limit.Read)
type Limit struct {
	controls, inverter *sunspec.Model
	wMax               float64
	available          func(now time.Time) float64

	// The cap ramps from the value from, at the time at, to the value to, over ramp.
	from, to float64
	at       time.Time
	ramp     time.Duration
	// revert is when the limit reverts, zero for never.
	revert time.Time
}

// NewLimit returns a Limit of controls, a model 123, on inverter's W, of at most wMax watts, from what available
// returns at the time of each read, in watts, or wMax always if available is nil. It reads the models, so call it
// before the Simulator serves them, or inside Update.
func NewLimit(controls, inverter *sunspec.Model, wMax float64, available func(now time.Time) float64) (*Limit, error) {
	for _, name := range []string{"WMaxLimPct", "WMaxLim_Ena", "WMaxLimPct_RmpTms", "WMaxLimPct_RvrtTms"} {
		if controls.Point(name) == nil {
			return nil, fmt.Errorf("%w: model %d has no %s", ErrMissingPoint, controls.ID(), name)
		}
	}
	if inverter.Point("W") == nil {
		return nil, fmt.Errorf("%w: model %d has no W", ErrMissingPoint, inverter.ID())
	}
	if available == nil {
		available = func(time.Time) float64 { return wMax }
	}
	l := &Limit{controls: controls, inverter: inverter, wMax: wMax, available: available}
	if to, _, err := l.setting(); err == nil {
		l.to = to
	} else {
		l.to = wMax // a setting a device would refuse is no limit
	}

	return l, nil
}

// Write is a hook for Simulator.OnWrite: it changes the cap once a client writes WMaxLimPct or WMaxLim_Ena.
func (l *Limit) Write(m *sunspec.Model, written []*sunspec.Point) error {
	if m != l.controls || !slices.ContainsFunc(written, func(p *sunspec.Point) bool {
		return p.Name() == "WMaxLimPct" || p.Name() == "WMaxLim_Ena"
	}) {
		return nil
	}
	to, on, err := l.setting()
	if err != nil {
		return err
	}

	now := time.Now()
	l.change(now, to)
	l.revert = time.Time{}
	if seconds, ok := l.controls.Point("WMaxLimPct_RvrtTms").Uint(); ok && seconds > 0 && on {
		l.revert = now.Add(time.Duration(seconds) * time.Second) //nolint:gosec // a uint16
	}

	return nil
}

// Read is a hook for Simulator.OnRead: it reverts the limit once its time has come, and sets W to what the panels give
// now, capped.
func (l *Limit) Read() error {
	now := time.Now()
	if !l.revert.IsZero() && !now.Before(l.revert) {
		if err := l.controls.Point("WMaxLim_Ena").SetUint(disabled); err != nil {
			return err
		}
		l.change(l.revert, l.wMax)
		l.revert = time.Time{}
	}

	return l.inverter.Point("W").SetFloat(max(0, min(l.available(now), l.cap(now))))
}

// setting returns the cap the controls hold, and whether the limit is enabled, or modbus.ErrIllegalDataValue for a
// setting a device refuses. A WMaxLim_Ena not implemented is DISABLED.
func (l *Limit) setting() (float64, bool, error) {
	pct, pctOK := l.controls.Point("WMaxLimPct").Float()
	if pctOK && (pct < 0 || pct > 100) {
		return 0, false, modbus.ErrIllegalDataValue
	}
	ena, enaOK := l.controls.Point("WMaxLim_Ena").Uint()
	switch {
	case !enaOK || ena == disabled:
		return l.wMax, false, nil
	case ena == enabled && pctOK:
		return l.wMax * pct / 100, true, nil
	default:
		// Neither DISABLED nor ENABLED, or enabled without a percentage.
		return 0, false, modbus.ErrIllegalDataValue
	}
}

// change makes the cap ramp from what it is at to to, over WMaxLimPct_RmpTms.
func (l *Limit) change(at time.Time, to float64) {
	seconds, _ := l.controls.Point("WMaxLimPct_RmpTms").Uint() // 0 when not implemented: at once
	l.from, l.to, l.at = l.cap(at), to, at
	l.ramp = time.Duration(seconds) * time.Second //nolint:gosec // a uint16
}

// cap returns the cap at now, along its ramp.
func (l *Limit) cap(now time.Time) float64 {
	elapsed := now.Sub(l.at)
	if l.ramp <= 0 || elapsed >= l.ramp {
		return l.to
	}

	return l.from + (l.to-l.from)*float64(max(0, elapsed))/float64(l.ramp)
}
