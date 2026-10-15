package modbus_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/elojah/sunspec/modbus"
)

// failing is a Handler whose every call returns err, and whose reads return n values.
type failing struct {
	err error
	n   int
}

func (f failing) ReadBits(context.Context, uint8, modbus.Table, uint16, uint16) ([]bool, error) {
	return make([]bool, f.n), f.err
}

func (f failing) WriteBits(context.Context, uint8, uint16, []bool) error {
	return f.err
}

func (f failing) ReadRegisters(context.Context, uint8, modbus.Table, uint16, uint16) ([]uint16, error) {
	return make([]uint16, f.n), f.err
}

func (f failing) WriteRegisters(context.Context, uint8, uint16, []uint16) error {
	return f.err
}

var errDevice = errors.New("device on fire")

// A server checks requests in the specification's order: the function, the length and quantity, then the range.
func TestServe_Checks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request modbus.PDU
		want    modbus.ExceptionCode
	}{
		{"unknown function", modbus.PDU{Function: 0x2B, Data: []byte{0x0E}}, modbus.IllegalFunction},
		{"read too short", modbus.PDU{Function: modbus.ReadCoils, Data: []byte{0, 0, 0}}, modbus.IllegalDataValue},
		{"read too long", modbus.PDU{Function: modbus.ReadCoils, Data: []byte{0, 0, 0, 1, 0}}, modbus.IllegalDataValue},
		{"no bit", modbus.PDU{Function: modbus.ReadDiscreteInputs, Data: []byte{0, 0, 0, 0}}, modbus.IllegalDataValue},
		{"too many bits", modbus.PDU{Function: modbus.ReadCoils, Data: []byte{0, 0, 0x07, 0xD1}}, modbus.IllegalDataValue},
		{"too many registers", modbus.PDU{Function: modbus.ReadInputRegisters, Data: []byte{0, 0, 0, 126}}, modbus.IllegalDataValue},
		{"past the last address", modbus.PDU{Function: modbus.ReadHoldingRegisters, Data: []byte{0xFF, 0xFF, 0, 2}}, modbus.IllegalDataAddress},
		{"bad coil value", modbus.PDU{Function: modbus.WriteSingleCoil, Data: []byte{0, 0, 0x12, 0x34}}, modbus.IllegalDataValue},
		{"coils byte count", modbus.PDU{Function: modbus.WriteMultipleCoils, Data: []byte{0, 0, 0, 9, 1, 0xFF}}, modbus.IllegalDataValue},
		{"coils length", modbus.PDU{Function: modbus.WriteMultipleCoils, Data: []byte{0, 0, 0, 8, 1, 0xFF, 0}}, modbus.IllegalDataValue},
		{"coils past the last address", modbus.PDU{Function: modbus.WriteMultipleCoils, Data: []byte{0xFF, 0xFF, 0, 2, 1, 0x03}}, modbus.IllegalDataAddress},
		{"registers byte count", modbus.PDU{Function: modbus.WriteMultipleRegisters, Data: []byte{0, 0, 0, 1, 1, 0}}, modbus.IllegalDataValue},
		{"registers too short", modbus.PDU{Function: modbus.WriteMultipleRegisters, Data: []byte{0, 0, 0, 1}}, modbus.IllegalDataValue},
		{"read-write read quantity", modbus.PDU{Function: modbus.ReadWriteMultipleRegisters, Data: []byte{0, 0, 0, 0, 0, 0, 0, 1, 2, 0, 0}}, modbus.IllegalDataValue},
		{"read-write write quantity", modbus.PDU{Function: modbus.ReadWriteMultipleRegisters, Data: []byte{0, 0, 0, 1, 0, 0, 0, 122, 244}}, modbus.IllegalDataValue},
		{"read-write read range", modbus.PDU{Function: modbus.ReadWriteMultipleRegisters, Data: []byte{0xFF, 0xFF, 0, 2, 0, 0, 0, 1, 2, 0, 0}}, modbus.IllegalDataAddress},
		{"memory range", modbus.PDU{Function: modbus.ReadHoldingRegisters, Data: []byte{0, 99, 0, 2}}, modbus.IllegalDataAddress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := modbus.NewClient(modbus.NewLoopback(newMemory()))
			_, err := c.Send(t.Context(), 1, tt.request)
			e, ok := errors.AsType[*modbus.ExceptionError](err)
			if !ok || e.Code != tt.want {
				t.Errorf("got %v, want exception %s", err, tt.want)
			}
		})
	}
}

// A request whose function code has the exception bit set is an unknown function, answered with the same code.
func TestServe_ExceptionFunction(t *testing.T) {
	t.Parallel()

	response, err := modbus.NewLoopback(newMemory()).Send(t.Context(), 1, modbus.PDU{Function: 0x83, Data: []byte{0, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Function != 0x83 || !slices.Equal(response.Data, []byte{byte(modbus.IllegalFunction)}) {
		t.Errorf("got %v, want an IllegalFunction exception", response)
	}
}

// A Handler's error answers the code of the sentinel it matches, or ServerDeviceFailure.
func TestServe_HandlerErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		h    failing
		want modbus.ExceptionCode
	}{
		{"sentinel", failing{err: modbus.ErrServerDeviceBusy}, modbus.ServerDeviceBusy},
		{"wrapped sentinel", failing{err: errors.Join(errDevice, modbus.ErrIllegalDataValue)}, modbus.IllegalDataValue},
		{"exception error", failing{err: &modbus.ExceptionError{Code: modbus.GatewayTargetFailed}}, modbus.GatewayTargetFailed},
		{"other error", failing{err: errDevice}, modbus.ServerDeviceFailure},
		{"wrong quantity", failing{n: 3}, modbus.ServerDeviceFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := modbus.NewClient(modbus.NewLoopback(tt.h))
			_, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 2)
			if e, ok := errors.AsType[*modbus.ExceptionError](err); !ok || e.Code != tt.want {
				t.Errorf("ReadRegisters: got %v, want exception %s", err, tt.want)
			}
			if tt.h.err == nil {
				return
			}
			err = c.WriteCoil(t.Context(), 1, 0, true)
			if e, ok := errors.AsType[*modbus.ExceptionError](err); !ok || e.Code != tt.want {
				t.Errorf("WriteCoil: got %v, want exception %s", err, tt.want)
			}
		})
	}
}

func TestExceptionCode_String(t *testing.T) {
	t.Parallel()

	tests := map[modbus.ExceptionCode]string{
		modbus.IllegalFunction:     "illegal function",
		modbus.GatewayTargetFailed: "gateway target device failed to respond",
		0x07:                       "exception 0x07",
	}
	for code, want := range tests {
		if got := code.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", code, got, want)
		}
	}
}

// An *ExceptionError matches its code's sentinel and no other, and an unknown code none.
func TestExceptionError_Is(t *testing.T) {
	t.Parallel()

	err := error(&modbus.ExceptionError{Function: modbus.ReadCoils, Code: modbus.ServerDeviceBusy})
	if !errors.Is(err, modbus.ErrServerDeviceBusy) || errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("%v matches the wrong sentinels", err)
	}
	unknown := error(&modbus.ExceptionError{Function: modbus.ReadCoils, Code: 0x07})
	if errors.Is(unknown, modbus.ErrIllegalFunction) {
		t.Errorf("%v matches ErrIllegalFunction", unknown)
	}
}

// Memory's Update changes what the server serves, its tables resized included.
func TestMemoryUpdate(t *testing.T) {
	t.Parallel()

	m := newMemory()
	c := modbus.NewClient(modbus.NewLoopback(m))
	m.Update(func(tables *modbus.Tables) {
		tables.InputRegisters = append(tables.InputRegisters, 7)
		tables.DiscreteInputs[0] = false
	})
	got, err := c.ReadRegisters(t.Context(), 1, modbus.InputRegisters, 99, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{1099, 7}; !slices.Equal(got, want) {
		t.Errorf("input registers = %v, want %v", got, want)
	}
	bits, err := c.ReadBits(t.Context(), 1, modbus.DiscreteInputs, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bits[0] {
		t.Error("discrete input 0 is on, want off")
	}
	if err := c.WriteCoils(t.Context(), 1, 99, []bool{true, true}); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("WriteCoils past the coils: got %v, want ErrIllegalDataAddress", err)
	}
}

// A Loopback serves nothing once ctx is done.
func TestLoopback_ContextDone(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	if _, err := c.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 0, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}
