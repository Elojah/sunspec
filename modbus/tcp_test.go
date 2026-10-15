//go:build !tinygo

// TinyGo's net has no ListenConfig.Listen, and its listeners report port 0 for 127.0.0.1:0, so these tests run under Go
// only.

package modbus_test

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/elojah/sunspec/modbus"
)

// serveTCP serves h on a free local port until the test ends, and returns its address. Cleanup checks that Serve
// returns nil once its ctx is done.
func serveTCP(tb testing.TB, h modbus.Handler) string {
	tb.Helper()

	l, err := new(net.ListenConfig).Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- modbus.Serve(ctx, l, h) }()
	tb.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			tb.Errorf("Serve returned %v, want nil", err)
		}
	})

	return l.Addr().String()
}

// fakeServer accepts connections on a free local port until the test ends, and runs serve for each, in its own
// goroutine. It returns the address.
func fakeServer(t *testing.T, serve func(conn net.Conn)) string {
	t.Helper()

	l, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer func() { _ = conn.Close() }()
				serve(conn)
			})
		}
	})
	t.Cleanup(func() {
		_ = l.Close()
		wg.Wait()
	})

	return l.Addr().String()
}

// readMBAP reads a request frame on conn and returns its transaction ID, unit and PDU.
func readMBAP(conn net.Conn) (uint16, byte, []byte, error) {
	var header [7]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, 0, nil, err
	}
	pdu := make([]byte, binary.BigEndian.Uint16(header[4:])-1)
	if _, err := io.ReadFull(conn, pdu); err != nil {
		return 0, 0, nil, err
	}

	return binary.BigEndian.Uint16(header[:]), header[6], pdu, nil
}

// writeMBAP writes a response frame on conn.
func writeMBAP(conn net.Conn, id uint16, unit byte, pdu []byte) error {
	frame := binary.BigEndian.AppendUint16(nil, id)
	frame = binary.BigEndian.AppendUint16(frame, 0)
	frame = binary.BigEndian.AppendUint16(frame, uint16(1+len(pdu))) //nolint:gosec // the tests' PDUs are short
	frame = append(frame, unit)
	_, err := conn.Write(append(frame, pdu...))

	return err
}

func TestTCPTransport(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewTCPTransport(serveTCP(t, newMemory())))
	t.Cleanup(func() { _ = c.Close() })

	if err := c.WriteRegisters(t.Context(), 1, 10, []uint16{1, 2}); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 9, 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{9, 1, 2, 12}; !slices.Equal(got, want) {
		t.Errorf("registers = %v, want %v", got, want)
	}
	if err := c.WriteCoils(t.Context(), 1, 0, []bool{false, true}); err != nil {
		t.Fatal(err)
	}
	bits, err := c.ReadBits(t.Context(), 1, modbus.Coils, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []bool{false, true, false}; !slices.Equal(bits, want) {
		t.Errorf("coils = %v, want %v", bits, want)
	}
	if _, err := c.ReadRegisters(t.Context(), 1, modbus.InputRegisters, 100, 1); !errors.Is(err, modbus.ErrIllegalDataAddress) {
		t.Errorf("read past the registers: got %v, want ErrIllegalDataAddress", err)
	}
}

// Requests from many goroutines go one at a time, and each gets its own response.
func TestTCPTransport_Concurrent(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewTCPTransport(serveTCP(t, newMemory())))
	t.Cleanup(func() { _ = c.Close() })

	var wg sync.WaitGroup
	for g := range uint16(20) {
		wg.Go(func() {
			address := g
			for i := range uint16(20) {
				value := 1000*g + i
				if err := c.WriteRegister(t.Context(), 1, address, value); err != nil {
					t.Error(err)

					return
				}
				got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, address, 1)
				if err != nil {
					t.Error(err)

					return
				}
				if got[0] != value {
					t.Errorf("register %d = %d, want %d", address, got[0], value)
				}
			}
		})
	}
	wg.Wait()
}

// A request failing on a connection the server closed is not retried, and the next request dials again.
func TestTCPTransport_Redial(t *testing.T) {
	t.Parallel()

	// Each connection answers one request, then closes.
	addr := fakeServer(t, func(conn net.Conn) {
		id, unit, pdu, err := readMBAP(conn)
		if err != nil {
			return
		}
		_ = writeMBAP(conn, id, unit, pdu[:5]) // echoes a WriteSingleRegister
	})
	c := modbus.NewClient(modbus.NewTCPTransport(addr))
	t.Cleanup(func() { _ = c.Close() })

	if err := c.WriteRegister(t.Context(), 1, 0, 1); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := c.WriteRegister(t.Context(), 1, 0, 2); err == nil {
		t.Error("request on the closed connection succeeded")
	}
	if err := c.WriteRegister(t.Context(), 1, 0, 3); err != nil {
		t.Errorf("request after a failure: %v, want a new connection", err)
	}
}

// A request that times out fails with context.DeadlineExceeded, and its late response never reaches the next request,
// which goes over a new connection.
func TestTCPTransport_Timeout(t *testing.T) {
	t.Parallel()

	h := blocking{Memory: newMemory(), release: make(chan struct{})}
	c := modbus.NewClient(modbus.NewTCPTransport(serveTCP(t, h), modbus.WithTimeout(50*time.Millisecond)))
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 1, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
	close(h.release)
	got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 5 {
		t.Errorf("register 5 = %d, want 5", got[0])
	}
}

// Canceling ctx fails a request at once with context.Canceled, without WithTimeout.
func TestTCPTransport_Cancel(t *testing.T) {
	t.Parallel()

	h := blocking{Memory: newMemory(), release: make(chan struct{})}
	t.Cleanup(func() { close(h.release) })
	c := modbus.NewClient(modbus.NewTCPTransport(serveTCP(t, h), modbus.WithTimeout(0)))
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, err := c.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 1, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestTCPTransport_Close(t *testing.T) {
	t.Parallel()

	tr := modbus.NewTCPTransport(serveTCP(t, newMemory()))
	c := modbus.NewClient(tr)
	if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); !errors.Is(err, modbus.ErrClosed) {
		t.Errorf("after Close: got %v, want ErrClosed", err)
	}
	if err := tr.Close(); !errors.Is(err, modbus.ErrClosed) {
		t.Errorf("second Close: got %v, want ErrClosed", err)
	}
}

// A response with another transaction ID or unit does not answer the request.
func TestTCPTransport_InvalidResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mangle func(id uint16, unit byte) (uint16, byte)
	}{
		{"transaction ID", func(id uint16, unit byte) (uint16, byte) { return id + 1, unit }},
		{"unit", func(id uint16, unit byte) (uint16, byte) { return id, unit + 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			addr := fakeServer(t, func(conn net.Conn) {
				id, unit, pdu, err := readMBAP(conn)
				if err != nil {
					return
				}
				id, unit = tt.mangle(id, unit)
				_ = writeMBAP(conn, id, unit, pdu[:5])
			})
			c := modbus.NewClient(modbus.NewTCPTransport(addr))
			t.Cleanup(func() { _ = c.Close() })
			if err := c.WriteRegister(t.Context(), 1, 0, 1); !errors.Is(err, modbus.ErrInvalidResponse) {
				t.Errorf("got %v, want an ErrInvalidResponse", err)
			}
		})
	}
}

// Serve closes a connection whose frame is not Modbus, rather than guess where the next one starts.
func TestServe_InvalidFrame(t *testing.T) {
	t.Parallel()

	conn, err := new(net.Dialer).DialContext(t.Context(), "tcp", serveTCP(t, newMemory()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// A header with protocol ID 1, alone, since the server closing with unread bytes would reset the connection.
	if _, err := conn.Write([]byte{0, 1, 0, 1, 0, 6, 1}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("read %d bytes, %v, want io.EOF", n, err)
	}
}

// Serve returns nil once its ctx is done, having closed every connection.
func TestServe_Shutdown(t *testing.T) {
	t.Parallel()

	l, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- modbus.Serve(ctx, l, newMemory()) }()

	conn, err := new(net.Dialer).DialContext(t.Context(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// A request answered, so that the server serves conn before the cancel.
	if err := writeMBAP(conn, 1, 1, []byte{3, 0, 0, 0, 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readMBAP(conn); err != nil {
		t.Fatal(err)
	}

	cancel()
	if err := <-served; err != nil {
		t.Errorf("Serve returned %v, want nil", err)
	}
	if n, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("read %d bytes after Serve returned, %v, want io.EOF", n, err)
	}
}

// Serve returns Accept's error, once l fails for another reason than its ctx.
func TestServe_ListenerError(t *testing.T) {
	t.Parallel()

	l, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := modbus.Serve(t.Context(), l, newMemory()); !errors.Is(err, net.ErrClosed) {
		t.Errorf("got %v, want net.ErrClosed", err)
	}
}

// The same over TCP, on the loopback interface.
func BenchmarkTCPTransport(b *testing.B) {
	c := modbus.NewClient(modbus.NewTCPTransport(serveTCP(b, modbus.NewMemory(modbus.Tables{HoldingRegisters: make([]uint16, modbus.MaxReadRegisters)}))))
	b.Cleanup(func() { _ = c.Close() })
	for b.Loop() {
		if _, err := c.ReadRegisters(b.Context(), 1, modbus.HoldingRegisters, 0, modbus.MaxReadRegisters); err != nil {
			b.Fatal(err)
		}
	}
}
