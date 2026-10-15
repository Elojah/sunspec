# TODO

The library reads and writes SunSpec devices from gateways and microcontrollers: a Linux box polling inverters over
TCP, or a Pico on an RS-485 line. The next steps widen what devices it talks to, without a dependency, and keep it
building with TinyGo. Every change keeps the invariants in CLAUDE.md, a protocol change comes with the frames of the
specification in its test, and a performance change with `benchstat` against `main` (`make benchcmp`).

## Short-term: small, no new invariant

- [ ] `Device.Read(ctx)`: reads every model, in as few requests as WithMaxRegisters allows, across model boundaries,
  for a gateway logging a whole device at each poll.
- [ ] `sunspec.WithSingleWrites()`: `Model.Write` sends a run of one register with WriteSingleRegister, for devices
  that refuse WriteMultipleRegisters for it.
- [ ] A `Point.SetSymbol(name)` for enums, so that `SetUint(1) // ENABLED` reads `SetSymbol("ENABLED")`.
- [ ] Document a Linux serial port in the README: `os.OpenFile` on `/dev/ttyUSB0`, whose reads take deadlines, and
  termios for the baud rate and parity, or a library such as go.bug.st/serial with a short read timeout.

## Mid-term: more devices

- [ ] Word-swapped devices: some put the low word of 32-bit values first, against the specification. A
  `sunspec.WithWordOrder` would decode them, for every 32- and 64-bit type and floats.
- [ ] Function 0x2B/0x0E, Read Device Identification: `Client.ReadDeviceIdentification`, and its frame lengths in
  `responseLength`, so that RTU carries it.
- [ ] Modbus ASCII: a third framing, `NewASCIITransport`, with its LRC. Rare on SunSpec devices, common on old meters.
- [ ] Simulator write hooks: `simulator.WithWriteHook(func(m *sunspec.Model) error)`, called under the lock after a
  client's write, so that a simulated inverter reacts to its controls, such as applying `WMaxLimPct` to `W`.
- [ ] Typed accessors for the most common models, generated: `model103.W(m)`, without losing the dynamic access by
  name. First measure what it costs in flash on a Pico.

## Long-term: operations

- [ ] TCP pipelining, behind an option: several requests in flight on one connection, paired by transaction ID, for
  gateways that poll many units behind one Modbus TCP gateway. Many devices mix up concurrent requests, hence opt-in.
- [ ] Device-level consistency for models longer than one request: some devices offer a "read in one go" area, or
  freeze their registers on a trigger. Survey what vendors do before designing for it.
