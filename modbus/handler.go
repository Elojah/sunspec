package modbus

import (
	"context"
	"encoding/binary"
	"errors"
)

// Handler serves the tables of the devices behind a server: Serve, ServeRTU and Loopback decode each request into one
// of its calls, or two for ReadWriteMultipleRegisters, write first. Serve calls it from a goroutine per connection, so
// it must be safe for concurrent use.
//
// The server checks quantities and ranges before calling it, so a call never asks for more than the protocol allows,
// nor past address 65535. To answer an exception, return an *ExceptionError or one of the sentinels, such as
// ErrIllegalDataAddress for an address the device does not have. Any other error answers ServerDeviceFailure, as does
// a read returning other than quantity values.
type Handler interface {
	// ReadBits returns quantity bits of table, Coils or DiscreteInputs, from address.
	ReadBits(ctx context.Context, unit uint8, table Table, address, quantity uint16) ([]bool, error)

	// WriteBits writes values to the coils from address.
	WriteBits(ctx context.Context, unit uint8, address uint16, values []bool) error

	// ReadRegisters returns quantity registers of table, InputRegisters or HoldingRegisters, from address.
	ReadRegisters(ctx context.Context, unit uint8, table Table, address, quantity uint16) ([]uint16, error)

	// WriteRegisters writes values to the holding registers from address.
	WriteRegisters(ctx context.Context, unit uint8, address uint16, values []uint16) error
}

// errReadLength is a Handler's read returning other than the quantity asked, which the server answers as
// ServerDeviceFailure.
var errReadLength = errors.New("modbus: handler returned a wrong quantity")

// serve answers request with h: its response, or an exception response. A server answers every request it can frame,
// so serve has no error of its own.
func serve(ctx context.Context, h Handler, unit uint8, request PDU) PDU {
	data, err := dispatch(ctx, h, unit, request)
	if err != nil {
		return PDU{Function: request.Function | exceptionBit, Data: []byte{byte(exceptionCode(err))}}
	}

	return PDU{Function: request.Function, Data: data}
}

// dispatch decodes request, calls h and encodes the data of the response. Its checks follow the order of the
// specification's state diagrams: the function (IllegalFunction), the request's length and quantity
// (IllegalDataValue), the range (IllegalDataAddress), then h.
func dispatch(ctx context.Context, h Handler, unit uint8, request PDU) ([]byte, error) {
	data := request.Data
	switch request.Function {
	case ReadCoils:
		return serveReadBits(ctx, h, unit, Coils, data)
	case ReadDiscreteInputs:
		return serveReadBits(ctx, h, unit, DiscreteInputs, data)
	case ReadHoldingRegisters:
		return serveReadRegisters(ctx, h, unit, HoldingRegisters, data)
	case ReadInputRegisters:
		return serveReadRegisters(ctx, h, unit, InputRegisters, data)
	case WriteSingleCoil:
		return serveWriteCoil(ctx, h, unit, data)
	case WriteSingleRegister:
		return serveWriteRegister(ctx, h, unit, data)
	case WriteMultipleCoils:
		return serveWriteCoils(ctx, h, unit, data)
	case WriteMultipleRegisters:
		return serveWriteRegisters(ctx, h, unit, data)
	case ReadWriteMultipleRegisters:
		return serveReadWriteRegisters(ctx, h, unit, data)
	default:
		return nil, ErrIllegalFunction
	}
}

// parseRange decodes the address and the quantity a request starts with. len(data) must be at least 4.
func parseRange(data []byte) (uint16, uint16) {
	return binary.BigEndian.Uint16(data), binary.BigEndian.Uint16(data[2:])
}

// checkQuantity returns ErrIllegalDataValue unless 1 <= quantity <= limit.
func checkQuantity(quantity uint16, limit int) error {
	if quantity < 1 || int(quantity) > limit {
		return ErrIllegalDataValue
	}

	return nil
}

// checkAddress returns ErrIllegalDataAddress for a range going past address 65535.
func checkAddress(address, quantity uint16) error {
	if int(address)+int(quantity) > addressSpace {
		return ErrIllegalDataAddress
	}

	return nil
}

// parseRead decodes and checks the address and quantity of a read request.
func parseRead(data []byte, limit int) (uint16, uint16, error) {
	if len(data) != 4 {
		return 0, 0, ErrIllegalDataValue
	}
	address, quantity := parseRange(data)
	if err := checkQuantity(quantity, limit); err != nil {
		return 0, 0, err
	}

	return address, quantity, checkAddress(address, quantity)
}

func serveReadBits(ctx context.Context, h Handler, unit uint8, table Table, data []byte) ([]byte, error) {
	address, quantity, err := parseRead(data, MaxReadBits)
	if err != nil {
		return nil, err
	}
	values, err := h.ReadBits(ctx, unit, table, address, quantity)
	if err != nil {
		return nil, err
	}
	if len(values) != int(quantity) {
		return nil, errReadLength
	}
	bits := packBits(values)

	return append([]byte{byteCount(len(bits))}, bits...), nil
}

func serveReadRegisters(ctx context.Context, h Handler, unit uint8, table Table, data []byte) ([]byte, error) {
	address, quantity, err := parseRead(data, MaxReadRegisters)
	if err != nil {
		return nil, err
	}

	return readRegisters(ctx, h, unit, table, address, quantity)
}

// readRegisters calls h and encodes its registers with their byte count, as the read responses carry them.
func readRegisters(ctx context.Context, h Handler, unit uint8, table Table, address, quantity uint16) ([]byte, error) {
	values, err := h.ReadRegisters(ctx, unit, table, address, quantity)
	if err != nil {
		return nil, err
	}
	if len(values) != int(quantity) {
		return nil, errReadLength
	}

	return appendUint16s([]byte{byteCount(2 * len(values))}, values...), nil
}

func serveWriteCoil(ctx context.Context, h Handler, unit uint8, data []byte) ([]byte, error) {
	if len(data) != 4 {
		return nil, ErrIllegalDataValue
	}
	address, value := parseRange(data)
	if value != coilOn && value != coilOff {
		return nil, ErrIllegalDataValue
	}
	if err := h.WriteBits(ctx, unit, address, []bool{value == coilOn}); err != nil {
		return nil, err
	}

	return data, nil
}

func serveWriteRegister(ctx context.Context, h Handler, unit uint8, data []byte) ([]byte, error) {
	if len(data) != 4 {
		return nil, ErrIllegalDataValue
	}
	address, value := parseRange(data)
	if err := h.WriteRegisters(ctx, unit, address, []uint16{value}); err != nil {
		return nil, err
	}

	return data, nil
}

func serveWriteCoils(ctx context.Context, h Handler, unit uint8, data []byte) ([]byte, error) {
	// Address, quantity, then a byte count that must match both the quantity and what follows.
	if len(data) < 5 {
		return nil, ErrIllegalDataValue
	}
	address, quantity := parseRange(data)
	if err := checkQuantity(quantity, MaxWriteBits); err != nil {
		return nil, err
	}
	if n := int(data[4]); n != (int(quantity)+7)/8 || len(data) != 5+n {
		return nil, ErrIllegalDataValue
	}
	if err := checkAddress(address, quantity); err != nil {
		return nil, err
	}
	if err := h.WriteBits(ctx, unit, address, unpackBits(data[5:], int(quantity))); err != nil {
		return nil, err
	}

	return data[:4], nil
}

func serveWriteRegisters(ctx context.Context, h Handler, unit uint8, data []byte) ([]byte, error) {
	address, values, err := parseWriteRegisters(data, MaxWriteRegisters)
	if err != nil {
		return nil, err
	}
	if err := h.WriteRegisters(ctx, unit, address, values); err != nil {
		return nil, err
	}

	return data[:4], nil
}

func serveReadWriteRegisters(ctx context.Context, h Handler, unit uint8, data []byte) ([]byte, error) {
	// The read's address and quantity, then the same as a WriteMultipleRegisters request.
	if len(data) < 4 {
		return nil, ErrIllegalDataValue
	}
	readAddress, readQuantity := parseRange(data)
	if err := checkQuantity(readQuantity, MaxReadRegisters); err != nil {
		return nil, err
	}
	writeAddress, values, err := parseWriteRegisters(data[4:], maxReadWriteRegisters)
	if err != nil {
		return nil, err
	}
	if err := checkAddress(readAddress, readQuantity); err != nil {
		return nil, err
	}
	if err := h.WriteRegisters(ctx, unit, writeAddress, values); err != nil {
		return nil, err
	}

	return readRegisters(ctx, h, unit, HoldingRegisters, readAddress, readQuantity)
}

// parseWriteRegisters decodes the address, quantity, byte count and values of a write of registers, and checks them as
// a server does: a quantity, a byte count or a length that disagree are IllegalDataValue, before any range error.
func parseWriteRegisters(data []byte, limit int) (uint16, []uint16, error) {
	if len(data) < 5 {
		return 0, nil, ErrIllegalDataValue
	}
	address, quantity := parseRange(data)
	if err := checkQuantity(quantity, limit); err != nil {
		return 0, nil, err
	}
	if n := int(data[4]); n != 2*int(quantity) || len(data) != 5+n {
		return 0, nil, ErrIllegalDataValue
	}

	return address, uint16s(data[5:]), checkAddress(address, quantity)
}
