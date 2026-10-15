package modbus

import (
	"context"
	"slices"
	"sync"
)

// Tables holds the four tables of a Memory. Each address past a table's length is IllegalDataAddress.
type Tables struct {
	Coils            []bool
	DiscreteInputs   []bool
	InputRegisters   []uint16
	HoldingRegisters []uint16
}

// Memory is a Handler holding its Tables in memory, the same for every unit: a simulated device. It is safe for
// concurrent use.
type Memory struct {
	mu     sync.Mutex
	tables Tables
}

// NewMemory returns a Memory serving tables, which it owns from then on: change them with Update.
func NewMemory(tables Tables) *Memory {
	return &Memory{tables: tables}
}

// Update calls f with the tables, under the lock requests are served under, so a client never reads half an update.
// f may change the values, and replace or resize the slices.
func (m *Memory) Update(f func(tables *Tables)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f(&m.tables)
}

// ReadBits implements Handler.
func (m *Memory) ReadBits(_ context.Context, _ uint8, table Table, address, quantity uint16) ([]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	bits := m.tables.Coils
	if table == DiscreteInputs {
		bits = m.tables.DiscreteInputs
	}
	if int(address)+int(quantity) > len(bits) {
		return nil, ErrIllegalDataAddress
	}

	return slices.Clone(bits[address : int(address)+int(quantity)]), nil
}

// WriteBits implements Handler.
func (m *Memory) WriteBits(_ context.Context, _ uint8, address uint16, values []bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if int(address)+len(values) > len(m.tables.Coils) {
		return ErrIllegalDataAddress
	}
	copy(m.tables.Coils[address:], values)

	return nil
}

// ReadRegisters implements Handler.
func (m *Memory) ReadRegisters(_ context.Context, _ uint8, table Table, address, quantity uint16) ([]uint16, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	registers := m.tables.HoldingRegisters
	if table == InputRegisters {
		registers = m.tables.InputRegisters
	}
	if int(address)+int(quantity) > len(registers) {
		return nil, ErrIllegalDataAddress
	}

	return slices.Clone(registers[address : int(address)+int(quantity)]), nil
}

// WriteRegisters implements Handler.
func (m *Memory) WriteRegisters(_ context.Context, _ uint8, address uint16, values []uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if int(address)+len(values) > len(m.tables.HoldingRegisters) {
		return ErrIllegalDataAddress
	}
	copy(m.tables.HoldingRegisters[address:], values)

	return nil
}
