package modbus

import (
	"context"
	"fmt"
	"time"
)

// Transport carries a request to a server and returns its response, adding the framing of its kind: TCPTransport,
// RTUTransport, or Loopback in process. Implementations are safe for concurrent use.
type Transport interface {
	// Send sends request to unit and returns the response, which may be an exception: Client checks it.
	Send(ctx context.Context, unit uint8, request PDU) (PDU, error)

	// Close releases the transport, after which Send returns ErrClosed.
	Close() error
}

// Option configures a TCPTransport or an RTUTransport.
type Option func(config *config)

// config is what Options set.
type config struct {
	timeout time.Duration
}

// defaultTimeout is WithTimeout's default.
const defaultTimeout = time.Second

// WithTimeout bounds each request, on top of its ctx, so that a device that never answers cannot hang a poll loop. It
// is 1s by default, and 0 leaves only ctx. A request that runs out of time fails with context.DeadlineExceeded.
func WithTimeout(timeout time.Duration) Option {
	return func(config *config) {
		config.timeout = timeout
	}
}

func newConfig(options []Option) config {
	config := config{timeout: defaultTimeout}
	for _, option := range options {
		option(&config)
	}

	return config
}

// requestContext returns ctx bounded by the configured timeout.
func (c config) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.timeout <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, c.timeout)
}

// Client sends requests through a Transport, with one method per function. It checks a request before sending it,
// returning a *RequestError, and the response against the request. A server's exception comes back as an
// *ExceptionError. It is safe for concurrent use, and the Transport sends one request at a time.
//
// unit addresses the device: the slave address on a serial line, or the unit identifier over TCP, which a gateway uses
// to pick a device on the serial line behind it and which a device on its own usually ignores, or wants at 1 or 255.
// Over RTU, unit 0 broadcasts a write to every device, and no device answers.
type Client struct {
	transport Transport
}

// NewClient returns a Client sending requests through transport.
func NewClient(transport Transport) *Client {
	return &Client{transport: transport}
}

// ReadBits reads quantity bits of table, Coils or DiscreteInputs, from address.
func (c *Client) ReadBits(ctx context.Context, unit uint8, table Table, address, quantity uint16) ([]bool, error) {
	function := table.readFunction()
	if table != Coils && table != DiscreteInputs {
		return nil, &RequestError{Function: function, Reason: "cannot read bits of " + table.String()}
	}
	if err := checkRange(function, address, int(quantity), MaxReadBits); err != nil {
		return nil, err
	}
	data, err := c.do(ctx, unit, PDU{Function: function, Data: appendUint16s(nil, address, quantity)})
	if err != nil {
		return nil, err
	}

	return parseBits(function, data, int(quantity))
}

// ReadRegisters reads quantity registers of table, InputRegisters or HoldingRegisters, from address.
func (c *Client) ReadRegisters(ctx context.Context, unit uint8, table Table, address, quantity uint16) ([]uint16, error) {
	function := table.readFunction()
	if table != InputRegisters && table != HoldingRegisters {
		return nil, &RequestError{Function: function, Reason: "cannot read registers of " + table.String()}
	}
	if err := checkRange(function, address, int(quantity), MaxReadRegisters); err != nil {
		return nil, err
	}
	data, err := c.do(ctx, unit, PDU{Function: function, Data: appendUint16s(nil, address, quantity)})
	if err != nil {
		return nil, err
	}

	return parseRegisters(function, data, int(quantity))
}

// WriteCoil writes one coil with WriteSingleCoil.
func (c *Client) WriteCoil(ctx context.Context, unit uint8, address uint16, value bool) error {
	v := uint16(coilOff)
	if value {
		v = coilOn
	}

	return c.write(ctx, unit, PDU{Function: WriteSingleCoil, Data: appendUint16s(nil, address, v)})
}

// WriteCoils writes values to the coils from address with WriteMultipleCoils, even a single one.
func (c *Client) WriteCoils(ctx context.Context, unit uint8, address uint16, values []bool) error {
	if err := checkRange(WriteMultipleCoils, address, len(values), MaxWriteBits); err != nil {
		return err
	}
	bits := packBits(values)
	data := appendUint16s(make([]byte, 0, 5+len(bits)), address, uint16(len(values))) //nolint:gosec // checkRange bounds it
	data = append(data, byteCount(len(bits)))
	data = append(data, bits...)

	return c.write(ctx, unit, PDU{Function: WriteMultipleCoils, Data: data})
}

// WriteRegister writes one holding register with WriteSingleRegister. Some devices accept only WriteRegisters, others
// only this: which one is the device's documentation's call.
func (c *Client) WriteRegister(ctx context.Context, unit uint8, address, value uint16) error {
	return c.write(ctx, unit, PDU{Function: WriteSingleRegister, Data: appendUint16s(nil, address, value)})
}

// WriteRegisters writes values to the holding registers from address with WriteMultipleRegisters, even a single one.
func (c *Client) WriteRegisters(ctx context.Context, unit uint8, address uint16, values []uint16) error {
	if err := checkRange(WriteMultipleRegisters, address, len(values), MaxWriteRegisters); err != nil {
		return err
	}

	return c.write(ctx, unit, PDU{Function: WriteMultipleRegisters, Data: registersRequest(address, values)})
}

// ReadWriteRegisters writes values to the holding registers from writeAddress, then reads readQuantity of them from
// readAddress, in one ReadWriteMultipleRegisters request. The server writes before it reads.
func (c *Client) ReadWriteRegisters(ctx context.Context, unit uint8, readAddress, readQuantity, writeAddress uint16, values []uint16) ([]uint16, error) {
	if err := checkRange(ReadWriteMultipleRegisters, readAddress, int(readQuantity), MaxReadRegisters); err != nil {
		return nil, err
	}
	if err := checkRange(ReadWriteMultipleRegisters, writeAddress, len(values), maxReadWriteRegisters); err != nil {
		return nil, err
	}
	data := appendUint16s(make([]byte, 0, 9+2*len(values)), readAddress, readQuantity)
	data = append(data, registersRequest(writeAddress, values)...)
	response, err := c.do(ctx, unit, PDU{Function: ReadWriteMultipleRegisters, Data: data})
	if err != nil {
		return nil, err
	}

	return parseRegisters(ReadWriteMultipleRegisters, response, int(readQuantity))
}

// Send sends request as it is, for a function Client has no method for, and returns the response's data, or the
// *ExceptionError it holds. RTUTransport frames only the functions this package defines.
func (c *Client) Send(ctx context.Context, unit uint8, request PDU) ([]byte, error) {
	if request.Function == 0 || request.Function&exceptionBit != 0 {
		return nil, &RequestError{Function: request.Function, Reason: "function codes go from 1 to 127"}
	}
	if len(request.Data)+1 > maxPDU {
		return nil, &RequestError{Function: request.Function, Reason: fmt.Sprintf("PDU of %d bytes, past %d", len(request.Data)+1, maxPDU)}
	}

	return c.do(ctx, unit, request)
}

// Close closes the Transport.
func (c *Client) Close() error {
	return c.transport.Close()
}

// do sends request and returns the response's data, or the exception it holds.
func (c *Client) do(ctx context.Context, unit uint8, request PDU) ([]byte, error) {
	response, err := c.transport.Send(ctx, unit, request)
	if err != nil {
		return nil, err
	}

	return responseData(request.Function, response)
}

// write sends a write request, whose response echoes the address and the value or quantity.
func (c *Client) write(ctx context.Context, unit uint8, request PDU) error {
	data, err := c.do(ctx, unit, request)
	if err != nil {
		return err
	}

	return parseEcho(request.Function, data, request.Data)
}

// registersRequest returns the part of a WriteMultipleRegisters request after its function code: the address, the
// quantity, the byte count, then the values. ReadWriteMultipleRegisters ends with the same.
func registersRequest(address uint16, values []uint16) []byte {
	data := appendUint16s(make([]byte, 0, 5+2*len(values)), address, uint16(len(values))) //nolint:gosec // the callers bound it
	data = append(data, byteCount(2*len(values)))

	return appendUint16s(data, values...)
}
