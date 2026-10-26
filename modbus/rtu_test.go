package modbus_test

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elojah/sunspec/modbus"
)

const (
	// baudRate is the line's in the RTU tests: 9600 baud, where a character takes 1.15 ms and t3.5 4 ms.
	baudRate = 9600
	charTime = 11 * time.Second / baudRate
	silence  = 7 * charTime / 2
)

// ports returns both ends of a line, for each kind of port: a net.Pipe, whose reads take deadlines, and a uart, whose
// reads return at once.
func ports() map[string]func() (io.ReadWriter, io.ReadWriter) {
	return map[string]func() (io.ReadWriter, io.ReadWriter){
		"deadlines": func() (io.ReadWriter, io.ReadWriter) {
			a, b := net.Pipe()

			return a, b
		},
		"polling": func() (io.ReadWriter, io.ReadWriter) {
			a, b, _ := newUARTs()

			return a, b
		},
	}
}

// serveRTU serves h as unit 1 on port until the bubble's test ends.
func serveRTU(t *testing.T, port io.ReadWriter, h modbus.Handler) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- modbus.ServeRTU(ctx, port, baudRate, 1, h) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("ServeRTU returned %v, want nil", err)
		}
	})
}

// scripted is a port whose Read returns at once: response, once a frame has been written, which it records.
type scripted struct {
	mu       sync.Mutex
	written  []byte
	response []byte
	pending  []byte
}

func (s *scripted) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := copy(p, s.pending)
	s.pending = s.pending[n:]

	return n, nil
}

func (s *scripted) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.written = append(s.written, p...)
	s.pending = append(s.pending, s.response...)

	return len(p), nil
}

// The frames of the specification's example: reading 3 holding registers from 108 of device 17.
func TestRTUTransport_Frame(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		port := &scripted{response: []byte{0x11, 0x03, 0x06, 0xAE, 0x41, 0x56, 0x52, 0x43, 0x40, 0x49, 0xAD}}
		c := modbus.NewClient(modbus.NewRTUTransport(port, baudRate))
		got, err := c.ReadRegisters(t.Context(), 0x11, modbus.HoldingRegisters, 0x6B, 3)
		if err != nil {
			t.Fatal(err)
		}
		if want := []byte{0x11, 0x03, 0x00, 0x6B, 0x00, 0x03, 0x76, 0x87}; !slices.Equal(port.written, want) {
			t.Errorf("wrote % X, want % X", port.written, want)
		}
		if want := []uint16{0xAE41, 0x5652, 0x4340}; !slices.Equal(got, want) {
			t.Errorf("registers = %04X, want %04X", got, want)
		}
	})
}

func TestCRC16(t *testing.T) {
	t.Parallel()

	tests := []struct {
		data []byte
		want uint16
	}{
		{[]byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A}, 0xCDC5},
		{[]byte{0x11, 0x03, 0x00, 0x6B, 0x00, 0x03}, 0x8776},
		{[]byte{0x11, 0x03, 0x06, 0xAE, 0x41, 0x56, 0x52, 0x43, 0x40}, 0xAD49},
		{nil, 0xFFFF},
	}
	for _, tt := range tests {
		if got := modbus.CRC16(tt.data); got != tt.want {
			t.Errorf("CRC16(% X) = %04X, want %04X", tt.data, got, tt.want)
		}
	}
}

// A client and ServeRTU talk over each kind of port.
func TestRTU(t *testing.T) {
	t.Parallel()

	for name, newPorts := range ports() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				client, server := newPorts()
				serveRTU(t, server, newMemory())
				c := modbus.NewClient(modbus.NewRTUTransport(client, baudRate))

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
				if err := c.WriteCoil(t.Context(), 1, 1, false); err != nil {
					t.Fatal(err)
				}
				if err := c.WriteCoils(t.Context(), 1, 3, []bool{false, true}); err != nil {
					t.Fatal(err)
				}
				bits, err := c.ReadBits(t.Context(), 1, modbus.Coils, 0, 5)
				if err != nil {
					t.Fatal(err)
				}
				if want := []bool{true, false, false, false, true}; !slices.Equal(bits, want) {
					t.Errorf("coils = %v, want %v", bits, want)
				}
				got, err = c.ReadWriteRegisters(t.Context(), 1, 0, 2, 0, []uint16{7})
				if err != nil {
					t.Fatal(err)
				}
				if want := []uint16{7, 1}; !slices.Equal(got, want) {
					t.Errorf("ReadWriteRegisters = %v, want %v", got, want)
				}
				if _, err := c.ReadRegisters(t.Context(), 1, modbus.InputRegisters, 99, 2); !errors.Is(err, modbus.ErrIllegalDataAddress) {
					t.Errorf("read past the registers: got %v, want ErrIllegalDataAddress", err)
				}
			})
		})
	}
}

// A device ignores the requests for other units, which time out, and the next request to it goes through.
func TestRTU_OtherUnit(t *testing.T) {
	t.Parallel()

	for name, newPorts := range ports() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				client, server := newPorts()
				serveRTU(t, server, newMemory())
				c := modbus.NewClient(modbus.NewRTUTransport(client, baudRate, modbus.WithTimeout(100*time.Millisecond)))

				start := time.Now()
				if _, err := c.ReadRegisters(t.Context(), 2, modbus.HoldingRegisters, 0, 1); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("unit 2: got %v, want context.DeadlineExceeded", err)
				}
				if waited := time.Since(start); waited != 100*time.Millisecond {
					t.Errorf("waited %v, want WithTimeout's 100ms", waited)
				}
				if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); err != nil {
					t.Errorf("unit 1: %v", err)
				}
			})
		})
	}
}

// A request the Handler answers ErrNoResponse gets nothing back, and times out, and the next one is served.
func TestServeRTU_NoResponse(t *testing.T) {
	t.Parallel()

	for name, newPorts := range ports() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				client, server := newPorts()
				serveRTU(t, server, silent{Memory: newMemory()})
				c := modbus.NewClient(modbus.NewRTUTransport(client, baudRate, modbus.WithTimeout(100*time.Millisecond)))

				start := time.Now()
				if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 1, 1); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("got %v, want context.DeadlineExceeded", err)
				}
				if waited := time.Since(start); waited != 100*time.Millisecond {
					t.Errorf("waited %v, want WithTimeout's 100ms", waited)
				}
				if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); err != nil {
					t.Errorf("next request: %v", err)
				}
			})
		})
	}
}

// A request ends at the earlier of its ctx's deadline and WithTimeout's, and one whose ctx is done already is never
// sent.
func TestRTUTransport_Deadline(t *testing.T) {
	t.Parallel()

	for name, newPorts := range ports() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				client, server := newPorts()
				serveRTU(t, server, newMemory())
				c := modbus.NewClient(modbus.NewRTUTransport(client, baudRate, modbus.WithTimeout(100*time.Millisecond)))

				for _, tt := range []struct{ ctx, want time.Duration }{
					{ctx: 30 * time.Millisecond, want: 30 * time.Millisecond},
					{ctx: 200 * time.Millisecond, want: 100 * time.Millisecond},
				} {
					ctx, cancel := context.WithTimeout(t.Context(), tt.ctx)
					start := time.Now()
					// Unit 2 never answers.
					if _, err := c.ReadRegisters(ctx, 2, modbus.HoldingRegisters, 0, 1); !errors.Is(err, context.DeadlineExceeded) {
						t.Errorf("ctx of %v: got %v, want context.DeadlineExceeded", tt.ctx, err)
					}
					if waited := time.Since(start); waited != tt.want {
						t.Errorf("ctx of %v: waited %v, want %v", tt.ctx, waited, tt.want)
					}
					cancel()
				}

				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := c.WriteRegister(ctx, 1, 0, 42); !errors.Is(err, context.Canceled) {
					t.Errorf("done ctx: got %v, want context.Canceled", err)
				}
				got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1)
				if err != nil {
					t.Fatal(err)
				}
				if got[0] != 0 {
					t.Errorf("register 0 = %d, want 0: the write with a done ctx was sent", got[0])
				}
			})
		})
	}
}

// A request waiting for another in flight ends with its own ctx: at its deadline, or once canceled.
func TestRTUTransport_Queue(t *testing.T) {
	t.Parallel()

	for name, newPorts := range ports() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				client, server := newPorts()
				serveRTU(t, server, newMemory())
				c := modbus.NewClient(modbus.NewRTUTransport(client, baudRate, modbus.WithTimeout(100*time.Millisecond)))

				// Unit 2 never answers, so this request holds the line for WithTimeout's 100ms.
				first := make(chan error, 1)
				go func() {
					_, err := c.ReadRegisters(t.Context(), 2, modbus.HoldingRegisters, 0, 1)
					first <- err
				}()
				synctest.Wait()

				start := time.Now()
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
				defer cancel()
				if _, err := c.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 0, 1); !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("deadline: got %v, want context.DeadlineExceeded", err)
				}
				ctx, cancel = context.WithCancel(t.Context())
				time.AfterFunc(20*time.Millisecond, cancel)
				if _, err := c.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 0, 1); !errors.Is(err, context.Canceled) {
					t.Errorf("cancel: got %v, want context.Canceled", err)
				}
				if waited := time.Since(start); waited != 50*time.Millisecond {
					t.Errorf("waited %v, want 30ms, then 20ms", waited)
				}

				if err := <-first; !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("request in flight: got %v, want context.DeadlineExceeded", err)
				}
				if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); err != nil {
					t.Errorf("after: %v", err)
				}
			})
		})
	}
}

// stuck is a port without deadlines whose Write blocks until release is closed, as a serial port's may while a slow
// line carries a long frame: a request in flight on it outlives its ctx.
type stuck struct{ release chan struct{} }

func (s stuck) Read([]byte) (int, error) { return 0, nil }

func (s stuck) Write(p []byte) (int, error) {
	<-s.release

	return len(p), nil
}

// A request waiting behind one that outlives its ctx ends at its own WithTimeout, even with a ctx without deadline.
func TestRTUTransport_QueueTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		port := stuck{release: make(chan struct{})}
		c := modbus.NewClient(modbus.NewRTUTransport(port, baudRate, modbus.WithTimeout(100*time.Millisecond)))

		first := make(chan error, 1)
		go func() {
			_, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1)
			first <- err
		}()
		synctest.Wait()

		time.Sleep(50 * time.Millisecond)
		start := time.Now()
		if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("waiting: got %v, want context.DeadlineExceeded", err)
		}
		if waited := time.Since(start); waited != 100*time.Millisecond {
			t.Errorf("waited %v, want WithTimeout's 100ms", waited)
		}

		close(port.release)
		if err := <-first; !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("request in flight: got %v, want context.DeadlineExceeded", err)
		}
	})
}

// A broadcast write reaches the device, which does not answer, and a broadcast read is refused.
func TestRTU_Broadcast(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client, server, _ := newUARTs()
		m := newMemory()
		serveRTU(t, server, m)
		c := modbus.NewClient(modbus.NewRTUTransport(client, baudRate))

		if err := c.WriteRegister(t.Context(), 0, 3, 33); err != nil {
			t.Fatal(err)
		}
		if err := c.WriteCoils(t.Context(), 0, 0, []bool{false}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		m.Update(func(tables *modbus.Tables) {
			if tables.HoldingRegisters[3] != 33 || tables.Coils[0] {
				t.Errorf("register 3 = %d and coil 0 = %t, want 33 and false", tables.HoldingRegisters[3], tables.Coils[0])
			}
		})
		if _, err := c.ReadRegisters(t.Context(), 0, modbus.HoldingRegisters, 0, 1); !errors.Is(err, modbus.ErrInvalidRequest) {
			t.Errorf("broadcast read: got %v, want ErrInvalidRequest", err)
		}
		// The device answers nothing, so the next request's response is its own.
		got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 3, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] != 33 {
			t.Errorf("register 3 = %d, want 33", got[0])
		}
	})
}

// A device drops a request whose CRC does not match, which times out, and the next request goes through.
func TestServeRTU_CRC(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client, server, l := newUARTs()
		serveRTU(t, server, newMemory())
		c := modbus.NewClient(modbus.NewRTUTransport(client, baudRate, modbus.WithTimeout(100*time.Millisecond)))

		l.mu.Lock()
		l.corrupt = true
		l.mu.Unlock()
		if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("corrupt request: got %v, want context.DeadlineExceeded", err)
		}
		if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); err != nil {
			t.Errorf("next request: %v", err)
		}
	})
}

// A response that comes after its request timed out is drained before the next request, so that it never passes for
// the next one's.
func TestRTUTransport_LateResponse(t *testing.T) {
	t.Parallel()

	for name, newPorts := range ports() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				client, server := newPorts()
				h := blocking{Memory: newMemory(), release: make(chan struct{})}
				serveRTU(t, server, h)
				c := modbus.NewClient(modbus.NewRTUTransport(client, baudRate, modbus.WithTimeout(100*time.Millisecond)))

				if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 1, 1); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("got %v, want context.DeadlineExceeded", err)
				}
				close(h.release)
				if name == "deadlines" {
					// A net.Pipe has no buffer: something must read the late response for the server to write it.
					go func() { _ = c.WriteRegister(t.Context(), 1, 0, 0) }()
				}
				time.Sleep(time.Second)
				synctest.Wait()

				got, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 5, 1)
				if err != nil {
					t.Fatal(err)
				}
				if got[0] != 5 {
					t.Errorf("register 5 = %d, want 5, not the late response", got[0])
				}
			})
		})
	}
}

// Requests are t3.5 apart at least, after the last one's response.
func TestRTUTransport_Silence(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		port := &timed{scripted: scripted{response: []byte{0x01, 0x06, 0x00, 0x00, 0x00, 0x01, 0x48, 0x0A}}}
		c := modbus.NewClient(modbus.NewRTUTransport(port, baudRate))
		for range 2 {
			if err := c.WriteRegister(t.Context(), 1, 0, 1); err != nil {
				t.Fatal(err)
			}
		}
		// The first request leaves 8 characters after Write returns. Its response is read at once, and the line is
		// idle from then: the second waits t3.5 after it.
		if gap := port.writes[1].Sub(port.writes[0]); gap < 8*charTime+silence {
			t.Errorf("requests %v apart, want at least %v", gap, 8*charTime+silence)
		}
	})
}

// timed is a scripted port that records when each Write came.
type timed struct {
	scripted

	writes []time.Time
}

func (p *timed) Write(b []byte) (int, error) {
	p.writes = append(p.writes, time.Now())

	return p.scripted.Write(b)
}

// A frame for a function RTU does not define is answered IllegalFunction once the line goes quiet.
func TestServeRTU_UnknownFunction(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client, server, _ := newUARTs()
		serveRTU(t, server, newMemory())

		// Read device identification, which ServeRTU cannot frame.
		if _, err := client.Write(modbus.AppendCRC([]byte{0x01, 0x2B, 0x0E, 0x01, 0x00})); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()

		response := make([]byte, 16)
		n, _ := client.Read(response)
		if want := modbus.AppendCRC([]byte{0x01, 0xAB, 0x01}); !slices.Equal(response[:n], want) {
			t.Errorf("response % X, want % X", response[:n], want)
		}
	})
}

// RTU frames only the functions this package defines.
func TestRTUTransport_UnknownFunction(t *testing.T) {
	t.Parallel()

	c := modbus.NewClient(modbus.NewRTUTransport(&scripted{}, baudRate))
	if _, err := c.Send(t.Context(), 1, modbus.PDU{Function: 0x2B, Data: []byte{0x0E, 0x01, 0x00}}); !errors.Is(err, modbus.ErrInvalidRequest) {
		t.Errorf("got %v, want ErrInvalidRequest", err)
	}
}

func TestRTUTransport_Close(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		tr := modbus.NewRTUTransport(client, baudRate)
		if err := tr.Close(); err != nil {
			t.Fatal(err)
		}
		c := modbus.NewClient(tr)
		if _, err := c.ReadRegisters(t.Context(), 1, modbus.HoldingRegisters, 0, 1); !errors.Is(err, modbus.ErrClosed) {
			t.Errorf("after Close: got %v, want ErrClosed", err)
		}
		if err := tr.Close(); !errors.Is(err, modbus.ErrClosed) {
			t.Errorf("second Close: got %v, want ErrClosed", err)
		}
	})
}

// ServeRTU returns the port's error.
func TestServeRTU_PortError(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client, server := net.Pipe()
		_ = client.Close()
		if err := modbus.ServeRTU(t.Context(), server, baudRate, 1, newMemory()); !errors.Is(err, io.EOF) {
			t.Errorf("got %v, want io.EOF", err)
		}
	})
}
