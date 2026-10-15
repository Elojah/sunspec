package sunspec_test

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"

	"github.com/elojah/sunspec"
	"github.com/elojah/sunspec/modbus"
	"github.com/elojah/sunspec/model"
	"github.com/elojah/sunspec/simulator"
)

// newModel returns a model of SunSpec's model id, every point not implemented.
func newModel(t *testing.T, id uint16, repeats map[string]int) *sunspec.Model {
	t.Helper()

	m, err := sunspec.NewModel(model.Lookup(id), repeats)
	if err != nil {
		t.Fatal(err)
	}

	return m
}

// newSimulator serves models from 40000 with a simulator, and returns it and a Loopback to it, which counts requests.
func newSimulator(t *testing.T, models ...*sunspec.Model) (*simulator.Simulator, *counter) {
	t.Helper()

	sim, err := simulator.New(40000, models...)
	if err != nil {
		t.Fatal(err)
	}

	return sim, &counter{Transport: modbus.NewLoopback(sim)}
}

// newDevice serves models from 40000 with a simulator, and returns the device Discover finds through a Loopback, and
// the requests it gets after.
func newDevice(t *testing.T, models ...*sunspec.Model) (*sunspec.Device, *simulator.Simulator, *counter) {
	t.Helper()

	sim, transport := newSimulator(t, models...)
	d, err := sunspec.Discover(t.Context(), modbus.NewClient(transport), 1)
	if err != nil {
		t.Fatal(err)
	}
	transport.reset()

	return d, sim, transport
}

// counter is a Transport recording each request's function, address and quantity.
type counter struct {
	modbus.Transport

	mu       sync.Mutex
	requests []request
	// err, when set, fails every request after recording it.
	err error
}

type request struct {
	function modbus.FunctionCode
	address  uint16
	quantity uint16
}

func (c *counter) Send(ctx context.Context, unit uint8, pdu modbus.PDU) (modbus.PDU, error) {
	c.mu.Lock()
	c.requests = append(c.requests, request{
		function: pdu.Function,
		address:  binary.BigEndian.Uint16(pdu.Data),
		quantity: binary.BigEndian.Uint16(pdu.Data[2:]),
	})
	err := c.err
	c.mu.Unlock()
	if err != nil {
		return modbus.PDU{}, err
	}

	return c.Transport.Send(ctx, unit, pdu)
}

// fail makes every request fail with err, or none if err is nil.
func (c *counter) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.err = err
}

var errInjected = errors.New("injected")

func (c *counter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.requests = nil
}

func (c *counter) sent() []request {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]request(nil), c.requests...)
}

// read reads m, failing the test on error.
func read(t *testing.T, m *sunspec.Model) {
	t.Helper()

	if err := m.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// set calls each setter, failing the test on error.
func set(t *testing.T, setters ...error) {
	t.Helper()

	for _, err := range setters {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// memory returns a Memory whose holding registers are registers from address, and 0 elsewhere up to 65535.
func memory(address uint16, registers ...uint16) *modbus.Memory {
	holding := make([]uint16, 1<<16)
	copy(holding[address:], registers)

	return modbus.NewMemory(modbus.Tables{HoldingRegisters: holding})
}
