# sunspec

[![CI](https://github.com/Elojah/sunspec/actions/workflows/ci.yml/badge.svg)](https://github.com/Elojah/sunspec/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/elojah/sunspec.svg)](https://pkg.go.dev/github.com/elojah/sunspec)

Read and write SunSpec devices from Go, such as inverters, meters and batteries, over a Modbus stack of its own.

- **Modbus**: client and server, over TCP, RTU on a serial line, and RTU over TCP, with `context` everywhere.
- **SunSpec**: discovers a device's models, and decodes its points by name, with their scale factors, types and
  repeating groups, from every model SunSpec publishes, or a vendor's.
- **Simulator**: a SunSpec device in memory, to test against without hardware, and `cmd/simulator`, an inverter served
  over Modbus TCP.
- **TinyGo**: runs on a microcontroller, RTU and SunSpec included. Every model fits a Raspberry Pi Pico.
- No dependency: the standard library only.

```sh
go get github.com/elojah/sunspec
```

## Quick start

```go
client := modbus.NewClient(modbus.NewTCPTransport("192.168.1.10:502"))
defer client.Close()

device, err := sunspec.Discover(ctx, client, 1) // unit 1
if err != nil {
	return err
}
inverter := device.Model(103) // three-phase inverter
if err := inverter.Read(ctx); err != nil {
	return err
}
if w, ok := inverter.Point("W").Float(); ok { // scaled by W_SF, ok false if not implemented
	fmt.Printf("%.0f W\n", w)
}
fmt.Println(inverter.Point("St")) // St: MPPT
```

Writing goes through setters, which check the type and the range, then one `Write`:

```go
controls := device.Model(123)
if err := controls.Read(ctx); err != nil { // the scale factors, among others
	return err
}
if err := controls.Point("WMaxLimPct").SetFloat(50); err != nil { // 50 % of the maximum power
	return err
}
if err := controls.Point("WMaxLim_Ena").SetUint(1); err != nil { // ENABLED
	return err
}
err = controls.Write(ctx) // sends what changed: one request for neighbouring registers
```

| Package | Holds |
| --- | --- |
| [`sunspec`](https://pkg.go.dev/github.com/elojah/sunspec) | `Discover`, `Device`, `Model`, `Group`, `Point`, `NewModel`. |
| [`modbus`](https://pkg.go.dev/github.com/elojah/sunspec/modbus) | `Client`, `TCPTransport`, `RTUTransport`, `Loopback`, `Serve`, `ServeRTU`, `Memory`. |
| [`model`](https://pkg.go.dev/github.com/elojah/sunspec/model) | SunSpec's model definitions: `Lookup`, and `Parse` for a vendor's. |
| [`simulator`](https://pkg.go.dev/github.com/elojah/sunspec/simulator) | `Simulator`, a SunSpec device in memory, which any Modbus server serves, and `Limit`, model 123's power limit. |
| [`fault`](https://pkg.go.dev/github.com/elojah/sunspec/fault) | `Handler` and `Transport`, a device and a link failing as in the field. |
| [`record`](https://pkg.go.dev/github.com/elojah/sunspec/record) | `Recorder` and `Replayer`, a real device recorded, then replayed. |

## Modbus

```go
client := modbus.NewClient(modbus.NewTCPTransport("192.168.1.10:502", modbus.WithTimeout(2*time.Second)))
registers, err := client.ReadRegisters(ctx, 1, modbus.HoldingRegisters, 40000, 2)
```

| Function | Client | Server |
| --- | --- | --- |
| 0x01 Read Coils, 0x02 Read Discrete Inputs | `ReadBits` | `Handler.ReadBits` |
| 0x03 Read Holding Registers, 0x04 Read Input Registers | `ReadRegisters` | `Handler.ReadRegisters` |
| 0x05 Write Single Coil, 0x0F Write Multiple Coils | `WriteCoil`, `WriteCoils` | `Handler.WriteBits` |
| 0x06 Write Single Register, 0x10 Write Multiple Registers | `WriteRegister`, `WriteRegisters` | `Handler.WriteRegisters` |
| 0x17 Read/Write Multiple Registers | `ReadWriteRegisters` | Both, write first |
| Any other | `Send`, over TCP | `IllegalFunction` |

| Transport | For |
| --- | --- |
| `NewTCPTransport(address)` | Modbus TCP. Dials on the first request, and again after any failure. |
| `NewRTUTransport(port, baudRate)` | Modbus RTU on a serial line, through any `io.ReadWriter`: an `os.File` on a tty, a serial library's port, TinyGo's `machine.UART`. |
| `NewRTUTransport(conn, 0)` | RTU over TCP, to a serial-to-Ethernet gateway that forwards frames as they are. |
| `NewLoopback(handler)` | In process, to a `Handler`, such as a `Simulator` in a test. It times out as the others do. |

Addresses are what goes on the wire, from 0. Documentation numbering registers from 1 calls address 40000 of the holding
registers "40001": when reads come back shifted by one, that is why.

Each request is bounded by its `ctx`, and by `WithTimeout`, 1 s by default, after which it fails with
`context.DeadlineExceeded`. A transport sends one request at a time and retries none, since a write may not be safe to
repeat. An RTU port's `Read` must take deadlines, or return at once when no byte came, so that a request can time out.

`Serve(ctx, listener, handler)` and `ServeRTU(ctx, port, baudRate, unit, handler)` serve a `Handler`: `Memory` holds
tables in memory, `Simulator` SunSpec models. A `Handler` returning `ErrNoResponse` answers nothing, as a device that is
off: the client times out, over a `Loopback` too. `WithMaxConnections(n, overflow)` serves at most n TCP connections, as
many inverters accept only one: past it, `RefuseNew` closes the new connection, `DropOldest` the oldest.

## SunSpec

`Discover` looks for the marker "SunS" at 40000, 50000 then 0 (`WithBases`), then walks the models' headers. `Read` reads
a model in as few requests as the protocol allows, 125 registers each (`WithMaxRegisters`), and lays its points out:

```go
mppt := device.Model(160)
if err := mppt.Read(ctx); err != nil {
	return err
}
for _, module := range mppt.Groups("module") { // a repeating group, one per MPPT input
	v, _ := module.Point("DCV").Float()
	fmt.Println(module.Index(), v)
}
```

A point the device's model lacks is `nil`, whose getters report no value, so `m.Point("W").Float()` needs no check.
Points and groups are valid until the next `Read`.

| Type | Registers | Not implemented | Getters |
| --- | --- | --- | --- |
| `int16`, `int32`, `int64` | 1, 2, 4 | most negative: `0x8000`… | `Int`, `Float` |
| `uint16`, `uint32`, `uint64`, `count` | 1, 2, 4, 1 | all ones: `0xFFFF`… | `Int`, `Uint`, `Float` |
| `acc16`, `acc32`, `acc64` | 1, 2, 4 | `0` | `Int`, `Uint`, `Float` |
| `enum16`, `enum32` | 1, 2 | all ones | `Symbol`, `Int`, `Uint` |
| `bitfield16`, `bitfield32` | 1, 2 | all ones | `Flags`, `Int`, `Uint` |
| `float32`, `float64` | 2, 4 | NaN | `Float` |
| `string` | as defined | a NUL first | `Text`, up to its first NUL |
| `sunssf` | 1 | `0x8000` | `Int` |
| `ipaddr`, `ipv6addr`, `eui48` | 2, 8, 4 | `0`, `0`, `FF:FF:FF:FF:FF:FF` | `Text` |
| `pad` | 1 | always: it holds no value | none |

`Float` scales by the point's scale factor: 42 with a scale factor of 2 is 4200. Setters refuse the value meaning not
implemented, which `SetUnimplemented` sets, but for accumulators, whose 0 is both.

A model SunSpec does not define, such as a vendor's, has registers but no points, unless `WithDefinitions` gives its
definition, which `model.Parse` reads from SunSpec's JSON format.

## Simulator

```go
inverter, err := sunspec.NewModel(model.Lookup(103), nil)
...
sim, err := simulator.New(40000, common, inverter)
...
err = sim.Update(func() error { return inverter.Point("W").SetFloat(4200) })
go modbus.Serve(ctx, listener, sim) // or modbus.NewLoopback(sim) in a test
```

A device reacts to what it is sent, and measures as time goes. `OnWrite` adds a hook called after each write, with the
points written, which a write undone refuses with its error, such as `modbus.ErrIllegalDataValue`. `OnRead` adds one
called before each read, which sets what it returns. Both run under the lock `Update` takes.

`Limit` simulates model 123's active power limit with both: W is what the panels give at the time of the read, capped
by `WMaxLimPct` while `WMaxLim_Ena` is enabled, ramping over `WMaxLimPct_RmpTms` and reverting after
`WMaxLimPct_RvrtTms` unless written again. A limit past 100 % is refused.

```go
limit, err := simulator.NewLimit(controls, inverter, 5000, func(now time.Time) float64 {
	return irradiance(now) * panels // what the panels give, from real data
})
...
sim.OnWrite(limit.Write)
sim.OnRead(limit.Read)
```

`cmd/simulator` serves a three-phase inverter over Modbus TCP, for a client in another process, or another language:
the common model (1) and model 103 from 40000, to any unit. Its output follows a sun rising at 06:00 UTC and setting at
18:00 UTC, of a day lasting `-day`: W peaks at `-w-max` at noon, A follows it, WH sums it, St is `MPPT` by day and
`SLEEPING` by night.

```sh
go run github.com/elojah/sunspec/cmd/simulator@latest -address :1502 -day 10m  # a day every 10 minutes
docker build -t sunspec-simulator -f cmd/simulator/Dockerfile https://github.com/Elojah/sunspec.git#v1.2.0
docker run -p 502:502 sunspec-simulator -day 10m
```

| Flag | Default | Is |
| --- | --- | --- |
| `-address` | `:502` | The host and port to listen on. |
| `-w-max` | `5000` | The output at noon, in watts, up to 32767. |
| `-day` | `24h` | How long a day lasts. |

## Faults

`fault.Handler` wraps a device's `Handler`, and `fault.Transport` a client's `Transport`, so that they fail as in the
field. Each asks a function, for every request, what fault applies: it reads the time, a schedule, a counter, or draws
at random.

```go
device := fault.NewHandler(sim, func(r fault.Request) fault.Device {
	if rebooting.Load() {
		return fault.Device{Silent: true}
	}
	return fault.Device{}
})
client := modbus.NewClient(fault.NewTransport(modbus.NewLoopback(device), func(unit uint8, r modbus.PDU) fault.Link {
	return fault.Link{} // or {Err: modbus.ErrCRC}, or {Err: context.DeadlineExceeded, Sent: true}
}))
```

| Fault | Is |
| --- | --- |
| `Device.Delay` | A response held back, as a busy device or a slow line does. |
| `Device.Silent` | No response: a device off or rebooting. The client times out. |
| `Device.Exception` | An exception rather than the response, such as `ServerDeviceBusy` while a device boots. |
| `Device.Frozen` | Each register as first read since the device froze: it answers, but its measurements stopped. |
| `Device.Ignored` | A write acknowledged, and not applied. |
| `Link.Delay` | A response, or an error, held back. |
| `Link.Err` | An error rather than the response, such as `modbus.ErrCRC`, the request never sent. |
| `Link.Err` with `Link.Sent` | The request served, a write applied, and its response lost: the client cannot tell it was. |

## Record and replay

A `record.Recorder` is a `Handler` forwarding each request to a real device, which `modbus.Serve` puts in front of it:
the code under test talks to it, unchanged, and each request becomes a `record.Entry`, a line of text once
`MarshalText` writes it. A `record.Replayer` answers as the device did, from the entries: its registers at each time,
silent while it did not answer, its exceptions. It replays a timeline rather than requests one by one, so that the code
it serves may poll differently, and `WithFrom` starts it at a time of the log, such as before an incident.

```go
recorder := record.NewRecorder(modbus.NewClient(modbus.NewTCPTransport("192.168.1.10:502")), func(e record.Entry) {
	line, _ := e.MarshalText() // 2026-10-26T12:00:00.5Z 85ms 1 read holding 40000 2 ok 5375 6E53
	_, _ = file.Write(append(line, '\n'))
})
go modbus.Serve(ctx, listener, recorder)
...
client := modbus.NewClient(modbus.NewLoopback(record.NewReplayer(entries)))
```

## Real data

What drives a simulated device, from devices and grids in the field:

| Data | Where |
| --- | --- |
| PV systems' production, minutes apart over years, outages included | NREL's [PVDAQ](https://data.openei.org/submissions/4568) |
| Irradiance anywhere, for what panels would give | [NSRDB](https://nsrdb.nlr.gov/), and the EU's [PVGIS](https://joint-research-centre.ec.europa.eu/photovoltaic-geographical-information-system-pvgis_en) |
| Homes' consumption and solar production, half-hourly | Ausgrid's [solar home electricity data](https://data.gov.au/data/dataset/nsw-solar-home-electricty-data) |
| Homes' consumption, appliance by appliance, every 6 s | [UK-DALE](https://jack-kelly.com/data/) |
| Homes' consumption, solar and electric vehicles, every second to every minute | Pecan Street's [Dataport](https://www.pecanstreet.org/dataport/) |
| Electric vehicle charging sessions | Caltech's [ACN-Data](https://ev.caltech.edu/dataset) |
| Utility tariffs, time of use included | OpenEI's [Utility Rate Database](https://openei.org/wiki/Utility_Rate_Database) |
| Electricity prices, day-ahead and more, across Europe | [ENTSO-E Transparency Platform](https://transparency.entsoe.eu/) |
| Grid frequency, every second | NESO's [system frequency data](https://www.neso.energy/data-portal/system-frequency-data), and the [power-grid frequency database](https://power-grid-frequency.org/) |

## Errors

| Error | When |
| --- | --- |
| `*modbus.ExceptionError` | The device answered an exception. Matches `modbus.ErrIllegalDataAddress` and the like with `errors.Is`. |
| `*modbus.RequestError` | A request out of the protocol's limits, refused before sending. Matches `modbus.ErrInvalidRequest`. |
| `modbus.ErrInvalidResponse`, `modbus.ErrCRC` | A response that does not answer the request. |
| `context.DeadlineExceeded` | No response before the timeout. |
| `modbus.ErrNoResponse` | Returned by a `Handler`, to answer nothing. |
| `sunspec.ErrNotSunSpec` | No base address holds the SunSpec marker. |
| `*sunspec.MapError` | Models that cannot be read as their headers say. Matches `sunspec.ErrInvalidMap`. |
| `*sunspec.PointError` | A setter's refusal: `ErrReadOnly`, `ErrType`, `ErrOutOfRange` or `ErrNotImplemented`. |

## TinyGo

The library builds and runs with TinyGo 0.43.0 or later: `make tinygo` runs the tests and the examples with it, and
builds the examples for a Raspberry Pi Pico. Every SunSpec model is linked, in about 150 KB of flash, decoded into RAM
only once used: `examples/04-inverter` takes 268 KB of flash and 8 KB of static RAM. An RTU port on a microcontroller
is its UART, whose `Read` returns at once.

## Examples

Each runs with `go run ./examples/NN-name`, against a device simulated in process.

| Example | Shows |
| --- | --- |
| [`01-registers`](examples/01-registers) | A Modbus TCP client and server, and an exception. |
| [`02-rtu`](examples/02-rtu) | RTU on a serial line, a broadcast, and a timeout. |
| [`03-discover`](examples/03-discover) | `Discover`, and the common model's nameplate. |
| [`04-inverter`](examples/04-inverter) | Scaled measurements, a state, events, and MPPT modules. |
| [`05-control`](examples/05-control) | Limiting an inverter's power through its controls. |
| [`06-vendor-model`](examples/06-vendor-model) | A vendor's model, read with `model.Parse`. |
| [`07-faults`](examples/07-faults) | An inverter rebooting, a line with noise, and a write whose response is lost, with `fault`. |
| [`08-limit`](examples/08-limit) | An inverter's output following its power limit, with `simulator.Limit`. |
| [`09-replay`](examples/09-replay) | An inverter recorded through an outage, then replayed, with `record`. |

## Models

SunSpec's model definitions come from [sunspec/models](https://github.com/sunspec/models), under the Apache License 2.0,
vendored in `tools/modelgen/json` at the commit `REF` names. `make models` vendors another (`MODELS_REF`) and regenerates
`model/models.go`.

## License

MIT, but for the SunSpec model definitions, in `model/models.go`: Apache 2.0, in `model/LICENSE`.
