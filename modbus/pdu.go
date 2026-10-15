package modbus

import (
	"encoding/binary"
	"fmt"
)

// FunctionCode is the first byte of a PDU: what the request asks for. The response echoes it, with exceptionBit set
// when it is an exception.
type FunctionCode uint8

// The function codes this package serves and sends with Client's methods. Client.Send sends any other.
const (
	ReadCoils                  FunctionCode = 0x01
	ReadDiscreteInputs         FunctionCode = 0x02
	ReadHoldingRegisters       FunctionCode = 0x03
	ReadInputRegisters         FunctionCode = 0x04
	WriteSingleCoil            FunctionCode = 0x05
	WriteSingleRegister        FunctionCode = 0x06
	WriteMultipleCoils         FunctionCode = 0x0F
	WriteMultipleRegisters     FunctionCode = 0x10
	ReadWriteMultipleRegisters FunctionCode = 0x17
)

// Limits of one request, from the Modbus application protocol, v1.1b3, section 6: what fits in a PDU.
const (
	MaxReadBits       = 2000
	MaxReadRegisters  = 125
	MaxWriteBits      = 1968
	MaxWriteRegisters = 123

	// maxReadWriteRegisters is the most ReadWriteMultipleRegisters writes, since its request also carries the read.
	maxReadWriteRegisters = 121
)

const (
	// exceptionBit is set in a response's function code when it is an exception. Untyped, so that a switch on a
	// FunctionCode need not list it.
	exceptionBit = 0x80

	// maxPDU is the largest PDU, function code included: an RTU frame is at most 256 bytes, of which the address
	// takes 1 and the CRC 2.
	maxPDU = 253

	// addressSpace is how many addresses each table has. A range must end within it: address + quantity <= 65536.
	addressSpace = 1 << 16

	// coilOn and coilOff are WriteSingleCoil's only valid values.
	coilOn  = 0xFF00
	coilOff = 0x0000
)

// PDU is a protocol data unit: a function code and its data. It is what a request or a response holds whatever the
// transport, which only adds framing around it: an MBAP header over TCP, a unit address and a CRC over RTU.
type PDU struct {
	Function FunctionCode
	Data     []byte
}

// Table is one of the four tables of the Modbus data model. Each has its own 65536 addresses, from 0.
type Table uint8

// The four tables. Bits are coils (read-write) and discrete inputs (read-only), registers are 16-bit words: holding
// registers (read-write) and input registers (read-only).
const (
	Coils Table = iota + 1
	DiscreteInputs
	InputRegisters
	HoldingRegisters
)

// String returns the table's name.
func (t Table) String() string {
	switch t {
	case Coils:
		return "coils"
	case DiscreteInputs:
		return "discrete inputs"
	case InputRegisters:
		return "input registers"
	case HoldingRegisters:
		return "holding registers"
	default:
		return fmt.Sprintf("table %d", uint8(t))
	}
}

// readFunction returns the function that reads t, or 0 for an unknown table.
func (t Table) readFunction() FunctionCode {
	switch t {
	case Coils:
		return ReadCoils
	case DiscreteInputs:
		return ReadDiscreteInputs
	case InputRegisters:
		return ReadInputRegisters
	case HoldingRegisters:
		return ReadHoldingRegisters
	default:
		return 0
	}
}

// checkRange returns a *RequestError unless 1 <= quantity <= limit and the range ends within the address space.
func checkRange(function FunctionCode, address uint16, quantity, limit int) error {
	if quantity < 1 || quantity > limit {
		return &RequestError{Function: function, Reason: fmt.Sprintf("quantity %d out of [1, %d]", quantity, limit)}
	}
	if int(address)+quantity > addressSpace {
		return &RequestError{Function: function, Reason: fmt.Sprintf("range %d+%d past address 65535", address, quantity)}
	}

	return nil
}

// byteCount returns n as the byte count field of a PDU, which every caller bounds to what fits in one: at most 250.
func byteCount(n int) byte {
	return byte(n) //nolint:gosec // bounded by the callers
}

// appendUint16s appends each value big-endian, the byte order of every Modbus field but the RTU CRC.
func appendUint16s(b []byte, values ...uint16) []byte {
	for _, v := range values {
		b = binary.BigEndian.AppendUint16(b, v)
	}

	return b
}

// uint16s decodes big-endian words. len(data) must be even.
func uint16s(data []byte) []uint16 {
	values := make([]uint16, len(data)/2)
	for i := range values {
		values[i] = binary.BigEndian.Uint16(data[2*i:])
	}

	return values
}

// packBits packs bits LSB first: values[0] is bit 0 of the first byte, values[8] bit 0 of the second, and the bits
// after the last value are 0. So coils 20 to 27 holding 1,0,1,1,0,0,1,1 go over the wire as 0xCD, which reads
// backwards when written MSB first: 1100 1101.
func packBits(values []bool) []byte {
	data := make([]byte, (len(values)+7)/8)
	for i, v := range values {
		if v {
			data[i/8] |= 1 << (i % 8)
		}
	}

	return data
}

// unpackBits is packBits' inverse, for the first quantity bits of data.
func unpackBits(data []byte, quantity int) []bool {
	values := make([]bool, quantity)
	for i := range values {
		values[i] = data[i/8]&(1<<(i%8)) != 0
	}

	return values
}

// invalidResponse returns an error wrapping ErrInvalidResponse.
func invalidResponse(function FunctionCode, format string, args ...any) error {
	return fmt.Errorf("%w for function 0x%02X: %s", ErrInvalidResponse, uint8(function), fmt.Sprintf(format, args...))
}

// responseData returns the data of response, which answers a request for function, or the *ExceptionError it holds.
func responseData(function FunctionCode, response PDU) ([]byte, error) {
	if response.Function == function {
		return response.Data, nil
	}
	if response.Function != function|exceptionBit {
		return nil, invalidResponse(function, "got function 0x%02X", uint8(response.Function))
	}
	if len(response.Data) != 1 {
		return nil, invalidResponse(function, "exception of %d bytes, want 1", len(response.Data))
	}

	return nil, &ExceptionError{Function: function, Code: ExceptionCode(response.Data[0])}
}

// parseBits decodes the data of a response to a read of quantity bits: a byte count, then the packed bits.
func parseBits(function FunctionCode, data []byte, quantity int) ([]bool, error) {
	n := (quantity + 7) / 8
	if len(data) != 1+n || int(data[0]) != n {
		return nil, invalidResponse(function, "%d bytes for %d bits", len(data), quantity)
	}

	return unpackBits(data[1:], quantity), nil
}

// parseRegisters decodes the data of a response to a read of quantity registers: a byte count, then the registers.
func parseRegisters(function FunctionCode, data []byte, quantity int) ([]uint16, error) {
	n := 2 * quantity
	if len(data) != 1+n || int(data[0]) != n {
		return nil, invalidResponse(function, "%d bytes for %d registers", len(data), quantity)
	}

	return uint16s(data[1:]), nil
}

// parseEcho checks the data of a response to a write, which echoes the first 4 bytes of the request's: the address,
// then the value or the quantity.
func parseEcho(function FunctionCode, data, request []byte) error {
	if len(data) != 4 || [4]byte(data) != [4]byte(request[:4]) {
		return invalidResponse(function, "echo % X, want % X", data, request[:4])
	}

	return nil
}
