package modbus

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinels matched by *ExceptionError with errors.Is, one per exception code. A Handler may return them as they are.
var (
	ErrIllegalFunction        = errors.New("modbus: illegal function")
	ErrIllegalDataAddress     = errors.New("modbus: illegal data address")
	ErrIllegalDataValue       = errors.New("modbus: illegal data value")
	ErrServerDeviceFailure    = errors.New("modbus: server device failure")
	ErrAcknowledge            = errors.New("modbus: acknowledge")
	ErrServerDeviceBusy       = errors.New("modbus: server device busy")
	ErrMemoryParityError      = errors.New("modbus: memory parity error")
	ErrGatewayPathUnavailable = errors.New("modbus: gateway path unavailable")
	ErrGatewayTargetFailed    = errors.New("modbus: gateway target device failed to respond")
)

var (
	// ErrInvalidRequest is matched by *RequestError.
	ErrInvalidRequest = errors.New("modbus: invalid request")

	// ErrInvalidResponse is wrapped by the errors about a response that does not answer the request: a wrong length,
	// function code, unit or transaction ID.
	ErrInvalidResponse = errors.New("modbus: invalid response")

	// ErrCRC is returned for an RTU frame whose CRC does not match its content.
	ErrCRC = errors.New("modbus: CRC mismatch")

	// ErrClosed is returned by a Transport's Send after Close.
	ErrClosed = errors.New("modbus: transport closed")
)

// ExceptionCode is what a server answers instead of data when it cannot serve a request.
type ExceptionCode uint8

// The exception codes of the Modbus application protocol, v1.1b3, section 7.
const (
	IllegalFunction        ExceptionCode = 0x01
	IllegalDataAddress     ExceptionCode = 0x02
	IllegalDataValue       ExceptionCode = 0x03
	ServerDeviceFailure    ExceptionCode = 0x04
	Acknowledge            ExceptionCode = 0x05
	ServerDeviceBusy       ExceptionCode = 0x06
	MemoryParityError      ExceptionCode = 0x08
	GatewayPathUnavailable ExceptionCode = 0x0A
	GatewayTargetFailed    ExceptionCode = 0x0B
)

// exceptions maps each code to its sentinel, in both directions: ExceptionError.Is reads it one way, exceptionCode the
// other.
var exceptions = [...]struct {
	code ExceptionCode
	err  error
}{
	{IllegalFunction, ErrIllegalFunction},
	{IllegalDataAddress, ErrIllegalDataAddress},
	{IllegalDataValue, ErrIllegalDataValue},
	{ServerDeviceFailure, ErrServerDeviceFailure},
	{Acknowledge, ErrAcknowledge},
	{ServerDeviceBusy, ErrServerDeviceBusy},
	{MemoryParityError, ErrMemoryParityError},
	{GatewayPathUnavailable, ErrGatewayPathUnavailable},
	{GatewayTargetFailed, ErrGatewayTargetFailed},
}

// String returns the code's name in the specification, or its number for a code it does not define.
func (c ExceptionCode) String() string {
	for _, e := range exceptions {
		if e.code == c {
			return strings.TrimPrefix(e.err.Error(), "modbus: ")
		}
	}

	return fmt.Sprintf("exception 0x%02X", uint8(c))
}

// ExceptionError is a server's exception response. It matches the sentinel of its code, such as ErrIllegalDataAddress
// for IllegalDataAddress, so that errors.Is works without errors.As.
type ExceptionError struct {
	Function FunctionCode
	Code     ExceptionCode
}

func (e *ExceptionError) Error() string {
	return fmt.Sprintf("modbus: %s, for function 0x%02X", e.Code, uint8(e.Function))
}

func (e *ExceptionError) Is(target error) bool {
	for _, x := range exceptions {
		if x.code == e.Code {
			return target == x.err
		}
	}

	return false
}

// exceptionCode returns the code a server answers for a Handler's error: the code of an *ExceptionError, or of the
// sentinel err matches, or else ServerDeviceFailure, since the request was valid but the device failed to serve it.
func exceptionCode(err error) ExceptionCode {
	if e, ok := errors.AsType[*ExceptionError](err); ok {
		return e.Code
	}
	for _, x := range exceptions {
		if errors.Is(err, x.err) {
			return x.code
		}
	}

	return ServerDeviceFailure
}

// RequestError is returned for a request the client refuses to send, such as a quantity out of the protocol's limits.
// It matches ErrInvalidRequest.
type RequestError struct {
	Function FunctionCode
	Reason   string
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("modbus: invalid request for function 0x%02X: %s", uint8(e.Function), e.Reason)
}

func (e *RequestError) Is(target error) bool {
	return target == ErrInvalidRequest
}
