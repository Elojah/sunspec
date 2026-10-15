package sunspec

import (
	"context"
	"errors"
	"fmt"

	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/model"
)

// marker is "SunS", in the two registers at a SunSpec device's base address: 0x5375 is "Su", 0x6E53 "nS".
var marker = [2]uint16{0x5375, 0x6E53}

// endID is the ID of the header that ends a device's models, whose length is 0.
const endID = 0xFFFF

// Option configures Discover.
type Option func(config *config)

// config is what Options set.
type config struct {
	bases       []uint16
	definitions map[uint16]*model.Definition
	maxRead     int
	maxWrite    int
}

// WithBases sets the addresses Discover looks for the SunSpec marker at, in order. It is 40000, 50000 then 0 by
// default, the three the specification allows, the first being by far the most common. Pass a device's own when its
// documentation gives it, to save the requests the others would take.
func WithBases(bases ...uint16) Option {
	return func(config *config) {
		config.bases = bases
	}
}

// WithDefinitions adds definitions for models Lookup does not know, such as a vendor's, read with model.Parse. They
// replace SunSpec's for the same ID.
func WithDefinitions(definitions ...*model.Definition) Option {
	return func(config *config) {
		for _, d := range definitions {
			config.definitions[d.ID] = d
		}
	}
}

// WithMaxRegisters caps how many registers a request reads or writes: the protocol allows 125 per read and 123 per
// write, but some devices fewer.
func WithMaxRegisters(n int) Option {
	return func(config *config) {
		config.maxRead = min(max(n, 1), modbus.MaxReadRegisters)
		config.maxWrite = min(max(n, 1), modbus.MaxWriteRegisters)
	}
}

func newConfig(options []Option) config {
	config := config{
		bases:       []uint16{40000, 50000, 0},
		definitions: map[uint16]*model.Definition{},
		maxRead:     modbus.MaxReadRegisters,
		maxWrite:    modbus.MaxWriteRegisters,
	}
	for _, option := range options {
		option(&config)
	}

	return config
}

// definition returns the definition of model id: WithDefinitions', or else SunSpec's, or nil.
func (c config) definition(id uint16) *model.Definition {
	if d, ok := c.definitions[id]; ok {
		return d
	}

	return model.Lookup(id)
}

// Device is a SunSpec device: a unit behind a Modbus client, and the models Discover found in its holding registers.
// It is safe for concurrent use across its models, each of which is not.
type Device struct {
	client *modbus.Client
	unit   uint8
	base   uint16
	config config
	models []*Model
}

// Discover finds the SunSpec device at unit through client, and its models, without reading them: see Model.Read.
//
// A SunSpec device holds, from a base address among its holding registers, the marker "SunS", then its models one
// after the other, each a header, its ID and its length L, then L registers, and ends with a header of ID 0xFFFF.
// Discover reads the marker at each base in turn (WithBases), then each header, one request each.
//
// A Modbus exception at a base, such as IllegalDataAddress, moves on to the next base, and at a header ends the
// models, since some devices answer one past their last model rather than an end header. Any other error, such as a
// timeout, returns at once: the device is unreachable, and the other bases would only time out too. It returns an
// error matching ErrNotSunSpec if no base holds the marker.
func Discover(ctx context.Context, client *modbus.Client, unit uint8, options ...Option) (*Device, error) {
	config := newConfig(options)
	for _, base := range config.bases {
		registers, err := client.ReadRegisters(ctx, unit, modbus.HoldingRegisters, base, 2)
		if _, ok := errors.AsType[*modbus.ExceptionError](err); ok {
			continue
		}
		if err != nil {
			return nil, err
		}
		if [2]uint16(registers) != marker {
			continue
		}

		d := &Device{client: client, unit: unit, base: base, config: config}
		if err := d.discover(ctx); err != nil {
			return nil, err
		}

		return d, nil
	}

	return nil, fmt.Errorf("%w at unit %d, base addresses %v", ErrNotSunSpec, unit, config.bases)
}

// Base returns the address of the SunSpec marker, from which the device's models follow.
func (d *Device) Base() uint16 {
	return d.base
}

// Models returns the device's models, in the order of their registers.
func (d *Device) Models() []*Model {
	return append([]*Model(nil), d.models...)
}

// Model returns the device's first model of that ID, or nil if it has none.
func (d *Device) Model(id uint16) *Model {
	for _, m := range d.models {
		if m.id == id {
			return m
		}
	}

	return nil
}

// discover walks the headers after the marker. Each model starts past the last one's end, so the walk goes forward
// and ends, at the end header or, at the latest, at the end of the address space.
func (d *Device) discover(ctx context.Context) error {
	// An int, so that the end of the address space, 65536, does not wrap to 0.
	address := int(d.base) + 2
	for {
		if address+2 > 1<<16 {
			return &MapError{Address: d.base, Reason: "no end header before address 65535"}
		}
		header, err := d.client.ReadRegisters(ctx, d.unit, modbus.HoldingRegisters, uint16(address), 2)
		if _, ok := errors.AsType[*modbus.ExceptionError](err); ok {
			return nil
		}
		if err != nil {
			return err
		}
		id, length := header[0], header[1]
		if id == endID {
			return nil
		}
		if address+2+int(length) > 1<<16 {
			return &MapError{Address: uint16(address), Reason: fmt.Sprintf("model %d of length %d goes past address 65535", id, length)}
		}
		d.models = append(d.models, &Model{
			id:         id,
			address:    uint16(address),
			length:     length,
			definition: d.config.definition(id),
			device:     d,
		})
		address += 2 + int(length)
	}
}
