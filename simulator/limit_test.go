package simulator_test

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// wMax is the simulated inverter's maximum power, in watts.
const wMax = 5000

// newLimited returns a client's view of a 5000 W inverter, model 103, and its immediate controls, model 123, whose
// panels give what available returns, with the controls' times set as given, the limit disabled.
func newLimited(t *testing.T, available func(time.Time) float64, ramp, revert uint64) (*sunspec.Model, *sunspec.Model) {
	t.Helper()

	inverter, controls := newModel(t, model.Lookup(103)), newModel(t, model.Lookup(123))
	for _, err := range []error{
		inverter.Point("W_SF").SetInt(0),
		controls.Point("WMaxLimPct_SF").SetInt(-1),
		controls.Point("WMaxLimPct").SetFloat(100),
		controls.Point("WMaxLim_Ena").SetUint(0), // DISABLED
		controls.Point("WMaxLimPct_RmpTms").SetUint(ramp),
		controls.Point("WMaxLimPct_RvrtTms").SetUint(revert),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	limit, err := simulator.NewLimit(controls, inverter, wMax, available)
	if err != nil {
		t.Fatal(err)
	}
	sim, c := newSimulator(t, inverter, controls)
	sim.OnWrite(limit.Write)
	sim.OnRead(limit.Read)

	d, err := sunspec.Discover(t.Context(), c, 1)
	if err != nil {
		t.Fatal(err)
	}

	return d.Model(103), d.Model(123)
}

// setLimit writes a limit of pct percent, enabled or not, as an energy manager does.
func setLimit(t *testing.T, controls *sunspec.Model, pct float64, enable bool) error {
	t.Helper()

	if err := controls.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	ena := uint64(0)
	if enable {
		ena = 1
	}
	if err := controls.Point("WMaxLimPct").SetFloat(pct); err != nil {
		t.Fatal(err)
	}
	if err := controls.Point("WMaxLim_Ena").SetUint(ena); err != nil {
		t.Fatal(err)
	}

	return controls.Write(t.Context())
}

// readW returns the inverter's W, as a client reads it.
func readW(t *testing.T, inverter *sunspec.Model) float64 {
	t.Helper()

	if err := inverter.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	w, ok := inverter.Point("W").Float()
	if !ok {
		t.Fatal("W not implemented")
	}

	return w
}

func sunny(time.Time) float64 { return 4000 }

// W is what the panels give, capped while the limit is enabled, and a limit past 100 % is refused.
func TestLimit(t *testing.T) {
	t.Parallel()

	inverter, controls := newLimited(t, sunny, 0, 0)
	if w := readW(t, inverter); w != 4000 {
		t.Errorf("no limit: W = %v, want 4000", w)
	}
	if err := setLimit(t, controls, 50, true); err != nil {
		t.Fatal(err)
	}
	if w := readW(t, inverter); w != 2500 {
		t.Errorf("50 %%: W = %v, want 2500", w)
	}
	if err := setLimit(t, controls, 120, true); !errors.Is(err, modbus.ErrIllegalDataValue) {
		t.Errorf("120 %%: got %v, want ErrIllegalDataValue", err)
	}
	if err := controls.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	if pct, _ := controls.Point("WMaxLimPct").Float(); pct != 50 {
		t.Errorf("WMaxLimPct = %v after the refused write, want 50", pct)
	}
	if err := setLimit(t, controls, 50, false); err != nil {
		t.Fatal(err)
	}
	if w := readW(t, inverter); w != 4000 {
		t.Errorf("disabled: W = %v, want 4000", w)
	}
}

// A change of the limit ramps over WMaxLimPct_RmpTms, while W follows what the panels give.
func TestLimit_Ramp(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		// The panels give their full 5000 W, then 1000 W from 30s on, as a cloud passes.
		cloudy := func(now time.Time) float64 {
			if now.Sub(start) >= 30*time.Second {
				return 1000
			}

			return wMax
		}
		inverter, controls := newLimited(t, cloudy, 10, 0)
		if err := setLimit(t, controls, 50, true); err != nil {
			t.Fatal(err)
		}
		for _, step := range []struct {
			at   time.Duration
			want float64
		}{
			{0, 5000},
			{5 * time.Second, 3750},
			{10 * time.Second, 2500},
			{30 * time.Second, 1000},
		} {
			time.Sleep(time.Until(start.Add(step.at)))
			if w := readW(t, inverter); w != step.want {
				t.Errorf("at %v: W = %v, want %v", step.at, w, step.want)
			}
		}
	})
}

// A limit reverts WMaxLimPct_RvrtTms after its last write, which a write of the same value starts again.
func TestLimit_Revert(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		inverter, controls := newLimited(t, sunny, 0, 30)
		if err := setLimit(t, controls, 50, true); err != nil {
			t.Fatal(err)
		}
		for _, step := range []struct {
			at      time.Duration
			refresh bool
			want    float64
		}{
			{at: 29 * time.Second, want: 2500},
			{at: 29 * time.Second, refresh: true, want: 2500},
			{at: 58 * time.Second, want: 2500},
			{at: 59 * time.Second, want: 4000},
		} {
			time.Sleep(time.Until(start.Add(step.at)))
			if step.refresh {
				if err := setLimit(t, controls, 50, true); err != nil {
					t.Fatal(err)
				}
			}
			if w := readW(t, inverter); w != step.want {
				t.Errorf("at %v: W = %v, want %v", step.at, w, step.want)
			}
		}
		if err := controls.Read(t.Context()); err != nil {
			t.Fatal(err)
		}
		if ena, _ := controls.Point("WMaxLim_Ena").Uint(); ena != 0 {
			t.Errorf("WMaxLim_Ena = %d after the revert, want 0, DISABLED", ena)
		}
	})
}

func TestNewLimit_MissingPoint(t *testing.T) {
	t.Parallel()

	common, controls := newModel(t, model.Lookup(1)), newModel(t, model.Lookup(123))
	if _, err := simulator.NewLimit(controls, common, wMax, nil); !errors.Is(err, simulator.ErrMissingPoint) {
		t.Errorf("no W: got %v, want ErrMissingPoint", err)
	}
	if _, err := simulator.NewLimit(common, controls, wMax, nil); !errors.Is(err, simulator.ErrMissingPoint) {
		t.Errorf("no controls: got %v, want ErrMissingPoint", err)
	}
}
