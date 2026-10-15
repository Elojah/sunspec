package modbus_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elojah/sunspec/modbus"
)

// Any request gets a response, with the request's function code and data that fit a PDU, or an exception of 1 byte.
func FuzzServe(f *testing.F) {
	f.Add(byte(modbus.ReadCoils), []byte{0, 0, 0, 10})
	f.Add(byte(modbus.ReadHoldingRegisters), []byte{0, 0, 0, 125})
	f.Add(byte(modbus.WriteMultipleCoils), []byte{0, 0, 0, 9, 2, 0xFF, 0x01})
	f.Add(byte(modbus.WriteMultipleRegisters), []byte{0, 0, 0, 1, 2, 0, 1})
	f.Add(byte(modbus.ReadWriteMultipleRegisters), []byte{0, 0, 0, 1, 0, 0, 0, 1, 2, 0, 1})
	f.Fuzz(func(t *testing.T, function byte, data []byte) {
		request := modbus.PDU{Function: modbus.FunctionCode(function), Data: data}
		response, err := modbus.NewLoopback(newMemory()).Send(t.Context(), 1, request)
		if err != nil {
			t.Fatal(err)
		}
		exception := response.Function == request.Function|0x80
		if response.Function != request.Function && !exception {
			t.Fatalf("response function 0x%02X to 0x%02X", response.Function, function)
		}
		if exception && len(response.Data) != 1 {
			t.Errorf("exception of %d bytes", len(response.Data))
		}
		if len(response.Data) > 252 {
			t.Errorf("response of %d bytes, past a PDU", len(response.Data))
		}
	})
}

// Any response to a read is the quantity asked for, or an error.
func FuzzClient(f *testing.F) {
	f.Add(byte(modbus.ReadHoldingRegisters), []byte{4, 0, 1, 0, 2}, uint16(2))
	f.Add(byte(modbus.ReadCoils), []byte{1, 0x05}, uint16(3))
	f.Add(byte(modbus.ReadHoldingRegisters|0x80), []byte{2}, uint16(1))
	f.Fuzz(func(t *testing.T, function byte, data []byte, quantity uint16) {
		response := modbus.PDU{Function: modbus.FunctionCode(function), Data: data}
		c := modbus.NewClient(&recorder{responses: []modbus.PDU{response, response}})
		if registers, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, quantity); err == nil && len(registers) != int(quantity) {
			t.Errorf("%d registers, want %d", len(registers), quantity)
		}
		if bits, err := c.ReadBits(t.Context(), 1, modbus.Coils, 0, quantity); err == nil && len(bits) != int(quantity) {
			t.Errorf("%d bits, want %d", len(bits), quantity)
		}
	})
}

// No bytes on the line make ServeRTU fail or panic.
func FuzzServeRTU(f *testing.F) {
	f.Add([]byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x01, 0x84, 0x0A})
	f.Add([]byte{0x01, 0x10, 0x00, 0x00, 0x00, 0x01, 0x02, 0x00, 0x01, 0x67, 0x90})
	f.Add([]byte{0x01, 0x17, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0xFF})
	f.Add([]byte{0x01, 0x2B, 0x0E, 0x01, 0x00, 0x70, 0x77})
	f.Fuzz(func(t *testing.T, line []byte) {
		synctest.Test(t, func(t *testing.T) {
			client, server, _ := newUARTs()
			serveRTU(t, server, newMemory())
			if _, err := client.Write(line); err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * time.Second)
			synctest.Wait()
		})
	})
}

// No bytes on a connection make Serve panic.
func FuzzServeTCP(f *testing.F) {
	f.Add([]byte{0, 1, 0, 0, 0, 6, 1, 3, 0, 0, 0, 1})
	f.Add([]byte{0, 1, 0, 0, 0, 1, 1})
	f.Add([]byte{0, 1, 0, 0, 0xFF, 0xFF, 1, 3})
	f.Fuzz(func(t *testing.T, stream []byte) {
		synctest.Test(t, func(t *testing.T) {
			client, server := net.Pipe()
			l := &pipeListener{conns: make(chan net.Conn, 1), done: make(chan struct{})}
			l.conns <- server
			ctx, cancel := context.WithCancel(t.Context())
			served := make(chan error, 1)
			go func() { served <- modbus.Serve(ctx, l, newMemory()) }()

			// Reads whatever the server answers, so that its writes never block.
			go func() {
				buf := make([]byte, 512)
				for {
					if _, err := client.Read(buf); err != nil {
						return
					}
				}
			}()
			_ = client.SetWriteDeadline(time.Now().Add(time.Second))
			_, _ = client.Write(stream)
			time.Sleep(time.Second)
			synctest.Wait()
			cancel()
			if err := <-served; err != nil {
				t.Errorf("Serve returned %v", err)
			}
			_ = client.Close()
		})
	})
}

// pipeListener is a net.Listener handing out the conns it is given.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })

	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return pipeAddr{}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }
