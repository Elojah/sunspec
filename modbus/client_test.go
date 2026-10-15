package modbus_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/elojah/sunspec/modbus"
)

func TestClientReadBits(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	for _, table := range []modbus.Table{modbus.Coils, modbus.DiscreteInputs} {
		got, err := c.ReadBits(t.Context(), 1, table, 2, 10)
		if err != nil {
			t.Fatal(err)
		}
		// From 2, every third: 3, 6, 9.
		want := []bool{false, true, false, false, true, false, false, true, false, false}
		if !slices.Equal(got, want) {
			t.Errorf("ReadBits(%s) = %v, want %v", table, got, want)
		}
	}
}

func TestClientReadRegisters(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	got, err := c.ReadRegisters(t.Context(), 1, modbus.InputRegisters, 98, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{1098, 1099}; !slices.Equal(got, want) {
		t.Errorf("ReadRegisters(input) = %v, want %v", got, want)
	}
	got, err = c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, modbus.MaxReadRegisters-25)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 || got[99] != 99 {
		t.Errorf("ReadRegisters(holding) = %v, want 0 to 99", got)
	}
}

func TestClientWriteCoil(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	if err := c.WriteCoil(t.Context(), 1, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteCoil(t.Context(), 1, 0, false); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadBits(t.Context(), 1, modbus.Coils, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []bool{false, true, false}; !slices.Equal(got, want) {
		t.Errorf("coils = %v, want %v", got, want)
	}
}

// WriteCoils packs 10 values into 2 bytes, LSB first, which the server unpacks back.
func TestClientWriteCoils(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	values := []bool{true, true, false, true, false, false, true, true, true, false}
	if err := c.WriteCoils(t.Context(), 1, 50, values); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadBits(t.Context(), 1, modbus.Coils, 50, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, values) {
		t.Errorf("coils = %v, want %v", got, values)
	}
}

func TestClientWriteRegister(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	if err := c.WriteRegister(t.Context(), 1, 7, 0xBEEF); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 6, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{6, 0xBEEF, 8}; !slices.Equal(got, want) {
		t.Errorf("registers = %v, want %v", got, want)
	}
}

func TestClientWriteRegisters(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	if err := c.WriteRegisters(t.Context(), 1, 10, []uint16{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 9, 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{9, 1, 2, 3, 13}; !slices.Equal(got, want) {
		t.Errorf("registers = %v, want %v", got, want)
	}
}

// ReadWriteRegisters writes before it reads, so the read sees the write.
func TestClientReadWriteRegisters(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	got, err := c.ReadWriteRegisters(t.Context(), 1, 20, 4, 21, []uint16{100, 200})
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{20, 100, 200, 23}; !slices.Equal(got, want) {
		t.Errorf("ReadWriteRegisters = %v, want %v", got, want)
	}
}

// Each request out of the protocol's limits fails before it is sent.
func TestClient_InvalidRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		send func(c *modbus.Client) error
	}{
		{"bits of a register table", func(c *modbus.Client) error {
			_, err := c.ReadBits(t.Context(), 1, modbus.HoldingRegisters, 0, 1)

			return err
		}},
		{"registers of a bit table", func(c *modbus.Client) error {
			_, err := c.ReadRegisters(t.Context(), 1, modbus.Coils, 0, 1)

			return err
		}},
		{"no bit", func(c *modbus.Client) error {
			_, err := c.ReadBits(t.Context(), 1, modbus.Coils, 0, 0)

			return err
		}},
		{"too many bits", func(c *modbus.Client) error {
			_, err := c.ReadBits(t.Context(), 1, modbus.Coils, 0, modbus.MaxReadBits+1)

			return err
		}},
		{"too many registers", func(c *modbus.Client) error {
			_, err := c.ReadRegisters(t.Context(), 1, modbus.InputRegisters, 0, modbus.MaxReadRegisters+1)

			return err
		}},
		{"past the last address", func(c *modbus.Client) error {
			_, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 65535, 2)

			return err
		}},
		{"no coil to write", func(c *modbus.Client) error {
			return c.WriteCoils(t.Context(), 1, 0, nil)
		}},
		{"too many registers to write", func(c *modbus.Client) error {
			return c.WriteRegisters(t.Context(), 1, 0, make([]uint16, modbus.MaxWriteRegisters+1))
		}},
		{"too many registers to read-write", func(c *modbus.Client) error {
			_, err := c.ReadWriteRegisters(t.Context(), 1, 0, 1, 0, make([]uint16, 122))

			return err
		}},
		{"exception function code", func(c *modbus.Client) error {
			_, err := c.Send(t.Context(), 1, modbus.PDU{Function: 0x83, Data: []byte{0, 0, 0, 1}})

			return err
		}},
		{"PDU too long", func(c *modbus.Client) error {
			_, err := c.Send(t.Context(), 1, modbus.PDU{Function: 0x41, Data: make([]byte, 253)})

			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &recorder{}
			err := tt.send(modbus.NewClient(r))
			if !errors.Is(err, modbus.ErrInvalidRequest) {
				t.Errorf("got %v, want an ErrInvalidRequest", err)
			}
			if len(r.requests) != 0 {
				t.Errorf("sent %v, want nothing", r.requests)
			}
		})
	}

	_, err := modbus.NewClient(&recorder{}).ReadRegisters(t.Context(), 1, modbus.InputRegisters, 0, 126)
	if want := "modbus: invalid request for function 0x04: quantity 126 out of [1, 125]"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

// An exception response comes back as an *ExceptionError matching its code's sentinel.
func TestClient_Exception(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	_, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 99, 2)
	if !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Fatalf("got %v, want ErrIllegalDataAddress", err)
	}
	e, ok := errors.AsType[*modbus.ExceptionError](err)
	if !ok || e.Function != modbus.ReadHoldingRegisters || e.Code != modbus.IllegalDataAddress {
		t.Errorf("got %#v, want an *ExceptionError for function 3, code 2", err)
	}
	if want := "modbus: illegal data address, for function 0x03"; err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

// A response that does not answer the request wraps ErrInvalidResponse.
func TestClient_InvalidResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response modbus.PDU
	}{
		{"other function", modbus.PDU{Function: modbus.ReadInputRegisters, Data: []byte{2, 0, 1}}},
		{"short", modbus.PDU{Function: modbus.ReadHoldingRegisters, Data: []byte{4, 0, 1}}},
		{"wrong byte count", modbus.PDU{Function: modbus.ReadHoldingRegisters, Data: []byte{3, 0, 1, 0}}},
		{"long exception", modbus.PDU{Function: modbus.ReadHoldingRegisters | 0x80, Data: []byte{2, 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := modbus.NewClient(&recorder{responses: []modbus.PDU{tt.response}})
			if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 2); !errors.Is(err, modbus.ErrInvalidResponse) {
				t.Errorf("got %v, want an ErrInvalidResponse", err)
			}
		})
	}

	c := modbus.NewClient(&recorder{responses: []modbus.PDU{{Function: modbus.WriteSingleRegister, Data: []byte{0, 1, 0, 3}}}})
	if err := c.WriteRegister(t.Context(), 1, 1, 2); !errors.Is(err, modbus.ErrInvalidResponse) {
		t.Errorf("WriteRegister with a wrong echo: got %v, want an ErrInvalidResponse", err)
	}
}

// Each method sends the PDU the specification gives.
func TestClient_Requests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		send     func(c *modbus.Client) error
		request  modbus.PDU
		response modbus.PDU
	}{
		{
			"read coils 20 to 38",
			func(c *modbus.Client) error {
				_, err := c.ReadBits(t.Context(), 1, modbus.Coils, 19, 19)

				return err
			},
			modbus.PDU{Function: 0x01, Data: []byte{0x00, 0x13, 0x00, 0x13}},
			modbus.PDU{Function: 0x01, Data: []byte{0x03, 0xCD, 0x6B, 0x05}},
		},
		{
			"write coil 173 on",
			func(c *modbus.Client) error { return c.WriteCoil(t.Context(), 1, 172, true) },
			modbus.PDU{Function: 0x05, Data: []byte{0x00, 0xAC, 0xFF, 0x00}},
			modbus.PDU{Function: 0x05, Data: []byte{0x00, 0xAC, 0xFF, 0x00}},
		},
		{
			"write coils 20 to 29",
			func(c *modbus.Client) error {
				return c.WriteCoils(t.Context(), 1, 19, []bool{true, false, true, true, false, false, true, true, true, false})
			},
			modbus.PDU{Function: 0x0F, Data: []byte{0x00, 0x13, 0x00, 0x0A, 0x02, 0xCD, 0x01}},
			modbus.PDU{Function: 0x0F, Data: []byte{0x00, 0x13, 0x00, 0x0A}},
		},
		{
			"write registers 2 and 3",
			func(c *modbus.Client) error { return c.WriteRegisters(t.Context(), 1, 1, []uint16{0x000A, 0x0102}) },
			modbus.PDU{Function: 0x10, Data: []byte{0x00, 0x01, 0x00, 0x02, 0x04, 0x00, 0x0A, 0x01, 0x02}},
			modbus.PDU{Function: 0x10, Data: []byte{0x00, 0x01, 0x00, 0x02}},
		},
		{
			"read 6 registers from 4, write 3 from 15",
			func(c *modbus.Client) error {
				_, err := c.ReadWriteRegisters(t.Context(), 1, 3, 6, 14, []uint16{0x00FF, 0x00FF, 0x00FF})

				return err
			},
			modbus.PDU{Function: 0x17, Data: []byte{0x00, 0x03, 0x00, 0x06, 0x00, 0x0E, 0x00, 0x03, 0x06, 0x00, 0xFF, 0x00, 0xFF, 0x00, 0xFF}},
			modbus.PDU{Function: 0x17, Data: []byte{0x0C, 0x00, 0xFE, 0x0A, 0xCD, 0x00, 0x01, 0x00, 0x03, 0x00, 0x0D, 0x00, 0xFF}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &recorder{responses: []modbus.PDU{tt.response}}
			if err := tt.send(modbus.NewClient(r)); err != nil {
				t.Fatal(err)
			}
			if len(r.requests) != 1 || r.requests[0].Function != tt.request.Function || !slices.Equal(r.requests[0].Data, tt.request.Data) {
				t.Errorf("sent %v, want %v", r.requests, tt.request)
			}
		})
	}
}

func TestClientSend(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewLoopback(newMemory()))
	data, err := c.Send(t.Context(), 1, modbus.PDU{Function: modbus.ReadHoldingRegisters, Data: []byte{0, 5, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{2, 0, 5}; !slices.Equal(data, want) {
		t.Errorf("Send = % X, want % X", data, want)
	}
	if _, err := c.Send(t.Context(), 1, modbus.PDU{Function: 0x2B, Data: []byte{0x0E, 0x01, 0x00}}); !errors.Is(err, modbus.ErrIllegalFunction) {
		t.Errorf("Send(0x2B) = %v, want ErrIllegalFunction", err)
	}
}
