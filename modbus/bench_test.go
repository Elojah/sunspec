package modbus_test

import (
	"testing"

	"github.com/elojah/sunspec/modbus"
)

// A full RTU frame.
func BenchmarkCRC16(b *testing.B) {
	frame := make([]byte, 254)
	b.SetBytes(int64(len(frame)))
	for b.Loop() {
		modbus.CRC16(frame)
	}
}

// The largest read, through the codec and a Memory, without framing.
func BenchmarkClientReadRegisters(b *testing.B) {
	c := modbus.NewClient(modbus.NewLoopback(modbus.NewMemory(modbus.Tables{HoldingRegisters: make([]uint16, modbus.MaxReadRegisters)})))
	for b.Loop() {
		if _, err := c.ReadRegisters(b.Context(), 1, modbus.HoldingRegisters, 0, modbus.MaxReadRegisters); err != nil {
			b.Fatal(err)
		}
	}
}
