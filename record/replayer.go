package record

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/elojah/sunspec/modbus"
)

// Option configures a Replayer.
type Option func(config *config)

// config is what Options set.
type config struct {
	from    time.Time
	hasFrom bool
}

// WithFrom replays from from, a time of the log, rather than from its first entry, such as a few minutes before an
// incident.
func WithFrom(from time.Time) Option {
	return func(config *config) {
		config.from, config.hasFrom = from, true
	}
}

// Replayer is a modbus.Handler answering as a device did, from the entries a Recorder made of it: the log's time runs
// from its first entry, or WithFrom's, as the clock does from NewReplayer, and each request is answered with the
// device's state at that time, not with the entry of a request like it. So the code it serves may poll differently.
//
// The state is each register's and bit's last value read or written in the log, or else its first, or the exception
// a read of it was last answered: a request touching one answers that exception, and one touching a register the log
// never read answers IllegalDataAddress. A unit answers nothing while its last request had no response, nor at
// all if the log has none of it. An answer comes as late as the unit's last answer did. A write is acknowledged, and
// sets the registers or bits written, until the log next sets them.
type Replayer struct {
	entries []Entry
	from    time.Time
	start   time.Time

	// mu guards the state, which requests advance along the log.
	mu    sync.Mutex
	at    time.Time // the log's time the state is at, which never goes back
	next  int       // the first entry not applied yet
	cells map[key]cell
	units map[uint8]unit
}

// key is a register's or a bit's address.
type key struct {
	unit    uint8
	table   modbus.Table
	address uint16
}

// cell is a register's or a bit's state: its value, or the exception a read of it is answered.
type cell struct {
	value uint16
	code  modbus.ExceptionCode
}

// unit is a unit's state: silent while its last request had no response, and how long its last answer took.
type unit struct {
	silent bool
	took   time.Duration
}

// NewReplayer returns a Replayer of entries, in the order of their times, which it does not change, starting now.
func NewReplayer(entries []Entry, options ...Option) *Replayer {
	var config config
	for _, option := range options {
		option(&config)
	}

	entries = slices.Clone(entries)
	slices.SortStableFunc(entries, func(a, b Entry) int { return a.At.Compare(b.At) })
	r := &Replayer{entries: entries, cells: map[key]cell{}, units: map[uint8]unit{}}
	switch {
	case config.hasFrom:
		r.from = config.from
	case len(entries) > 0:
		r.from = entries[0].At
	}

	// Before its first entry, a register is as the first one has it, and so is a unit: seed them with the entries
	// backwards, the first ones last.
	for i := len(entries) - 1; i >= 0; i-- {
		r.apply(entries[i])
	}
	r.at = r.from
	r.advance(r.from)
	r.start = time.Now()

	return r
}

// ReadBits implements modbus.Handler.
func (r *Replayer) ReadBits(ctx context.Context, unit uint8, table modbus.Table, address, quantity uint16) ([]bool, error) {
	values, err := r.read(ctx, unit, table, address, quantity)
	if err != nil {
		return nil, err
	}
	bits := make([]bool, len(values))
	for i, v := range values {
		bits[i] = v != 0
	}

	return bits, nil
}

// WriteBits implements modbus.Handler.
func (r *Replayer) WriteBits(ctx context.Context, unit uint8, address uint16, values []bool) error {
	registers := make([]uint16, len(values))
	for i, bit := range values {
		registers[i] = fromBit(bit)
	}

	return r.write(ctx, unit, modbus.Coils, address, registers)
}

// ReadRegisters implements modbus.Handler.
func (r *Replayer) ReadRegisters(ctx context.Context, unit uint8, table modbus.Table, address, quantity uint16) ([]uint16, error) {
	return r.read(ctx, unit, table, address, quantity)
}

// WriteRegisters implements modbus.Handler.
func (r *Replayer) WriteRegisters(ctx context.Context, unit uint8, address uint16, values []uint16) error {
	return r.write(ctx, unit, modbus.HoldingRegisters, address, values)
}

// read answers a read as the unit would now, once its time to answer has gone by.
func (r *Replayer) read(ctx context.Context, unit uint8, table modbus.Table, address, quantity uint16) ([]uint16, error) {
	values, took, err := r.state(unit, table, address, quantity)
	if sleepErr := sleep(ctx, took); sleepErr != nil {
		return nil, sleepErr
	}

	return values, err
}

// state returns the registers or bits the unit holds now, and how long it takes to answer, or the error it answers:
// at once for none.
func (r *Replayer) state(unitID uint8, table modbus.Table, address, quantity uint16) ([]uint16, time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.advance(r.now())
	u, ok := r.units[unitID]
	if !ok || u.silent {
		return nil, 0, modbus.ErrNoResponse
	}
	values := make([]uint16, quantity)
	k := key{unit: unitID, table: table, address: address}
	for i := range values {
		c, ok := r.cells[k]
		switch {
		case !ok:
			return nil, u.took, modbus.ErrIllegalDataAddress
		case c.code != 0:
			return nil, u.took, &modbus.ExceptionError{Code: c.code}
		}
		values[i] = c.value
		k.address++ // wraps past the last value only: the server checked the range ends by 65535
	}

	return values, u.took, nil
}

// write acknowledges a write, as the unit would now, once its time to answer has gone by, and sets what it writes.
func (r *Replayer) write(ctx context.Context, unitID uint8, table modbus.Table, address uint16, values []uint16) error {
	took, err := r.set(unitID, table, address, values)
	if err != nil {
		return err
	}

	return sleep(ctx, took)
}

// set sets values from address, unless the unit answers nothing now, and returns how long it takes to answer.
func (r *Replayer) set(unitID uint8, table modbus.Table, address uint16, values []uint16) (time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.advance(r.now())
	u, ok := r.units[unitID]
	if !ok || u.silent {
		return 0, modbus.ErrNoResponse
	}
	k := key{unit: unitID, table: table, address: address}
	for _, v := range values {
		r.cells[k] = cell{value: v}
		k.address++
	}

	return u.took, nil
}

// now returns the log's time the clock is at.
func (r *Replayer) now() time.Time {
	return r.from.Add(time.Since(r.start))
}

// advance applies the entries up to at, the log's time, unless the state is past it already.
func (r *Replayer) advance(at time.Time) {
	if at.Before(r.at) {
		return
	}
	r.at = at
	for r.next < len(r.entries) && !r.entries[r.next].At.After(at) {
		r.apply(r.entries[r.next])
		r.next++
	}
}

// apply sets the state as e leaves it: the values answered, a read's exception, its unit silent or not. A write the
// device refused, or did not answer, changed nothing that the log knows of.
func (r *Replayer) apply(e Entry) {
	u := r.units[e.Unit]
	u.silent = e.Outcome == NoResponse
	if !u.silent {
		u.took = e.Took
	}
	r.units[e.Unit] = u

	k := key{unit: e.Unit, table: e.Table, address: e.Address}
	switch {
	case e.Outcome == Answered:
		for _, v := range e.Values {
			r.cells[k] = cell{value: v}
			k.address++
		}
	case e.Outcome == Exception && !e.Write:
		for range e.Quantity {
			r.cells[k] = cell{code: e.Code}
			k.address++
		}
	}
}

// sleep waits for d, or returns ctx's error once it is done first.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
