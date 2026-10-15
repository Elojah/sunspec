// Package modbus is a Modbus client and server, over TCP and RTU, with the standard library only.
//
//	client := modbus.NewClient(modbus.NewTCPTransport("192.168.1.10:502"))
//	defer client.Close()
//	registers, err := client.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 40000, 2)
//
// # Data model
//
// A device has four tables: coils (read-write bits), discrete inputs (read-only bits), input registers (read-only
// 16-bit words) and holding registers (read-write words). Each has its own addresses, from 0 to 65535.
//
// Addresses here are what goes on the wire, from 0. Documentation often numbers registers from 1 instead, with the
// table as a leading digit: "holding register 40001" is address 0 of HoldingRegisters, and "30010" is address 9 of
// InputRegisters. A device whose documentation says "40001" for what is on the wire at 40000 is common too, so when
// reads come back shifted by one, that is why.
//
// # Transports
//
// A Client sends requests through a Transport, which frames them:
//
//   - TCPTransport: Modbus TCP, to a device or a gateway on port 502.
//   - RTUTransport: Modbus RTU, on a serial line through any io.ReadWriter, or over TCP to a gateway that forwards
//     frames as they are.
//   - Loopback: in process, to a Handler, for tests.
//
// A Transport sends one request at a time. Each request is bounded by its ctx, and by WithTimeout's timeout, 1 s by
// default. Nothing is retried, since a write may not be safe to repeat.
//
// # Server
//
// Serve and ServeRTU serve a Handler, which Memory implements with tables in memory. They check every request, and
// answer an exception for one they cannot serve.
//
// # Errors
//
// A server's exception comes back as an *ExceptionError, which matches the sentinel of its code with errors.Is, such
// as ErrIllegalDataAddress. A request out of the protocol's limits fails with a *RequestError before it is sent, and
// a response that does not answer the request wraps ErrInvalidResponse. A timeout is context.DeadlineExceeded.
package modbus
