# CLAUDE.md

Commands, fragile invariants and settled decisions. Behaviour is in godoc and the README, plans in TODO.md.

## Overview

`github.com/elojah/sunspec`, Modbus and SunSpec. Go 1.26.1, no dependency.

Packages: `sunspec` (`Discover`, `Device`, `Model`, `Group`, `Point`, `NewModel`, errors), `modbus` (`Client`,
`Transport`s, `Serve`/`ServeRTU`, `Handler`, `Memory`, errors), `model` (definitions: `Lookup`, `Parse`, binary form),
`simulator` (`Simulator`, its hooks, `Limit`), `fault` (a `Handler` and a `Transport` failing on demand), `record`
(`Entry` and its text, `Recorder`, `Replayer`), `examples/`, and `cmd/simulator`, a binary serving an inverter over
TCP, standard library only (`flag`, `log/slog`), with its `Dockerfile` built from the root (`.dockerignore`). Imports
go `simulator` → `sunspec` → `modbus`, `model`; `fault` and `record` → `modbus`. `modbus` and `model` import nothing
from the library.

`tools/modelgen` is a module of its own (`replace ../..`), so that the vendored JSON stays out of the library's module.
It generates `model/models.go`: never edit it, run `make generate`.
The JSON's LICENSE is out of the module's zip too: `make models` copies it to `model/LICENSE`, for `models.go`.

## Commands

```sh
go test -race ./...                 # always -race
go test -race -run TestName ./...
make check                          # golangci-lint + tests (-shuffle=on -cpu 1,4) + each benchmark once, both modules
make generate                       # model/models.go from tools/modelgen/json
make models                         # vendor github.com/sunspec/models at MODELS_REF, then generate
make fuzz                           # each fuzz target for FUZZ_TIME (30s)
make bench                          # BENCH=regexp
make benchcmp                       # main (BENCH_BASE) vs the working tree, with benchstat
make tinygo                         # tinygo-test, tinygo-run (examples, output checked), tinygo-build (Pico)
go run ./cmd/simulator -address :1502 -day 10m  # an inverter on Modbus TCP, a day every 10 minutes
```

Lint (`.golangci.yml`) is `default: all` minus a disable list, tests included. Mind `nlreturn`, `paralleltest`
(`t.Parallel()` first), `err113`, `godot`, `funcorder` (exported methods first), `exhaustive` (a switch on an enum
lists every constant, even with a default) against `staticcheck`'s QF1002 (use a tagged switch), and `gosec`'s G115
on integer conversions: bound them, or use the helpers that hold the one `nolint` (`byteCount`, `signExtend`,
`truncate`). CI runs `make check` with a pinned golangci-lint in `./bin`.

## Conventions

- Comments are short: invariants, non-obvious whys, the public contract. Protocol details that are easy to get wrong
  (framing, byte order, unimplemented values, scale factor arithmetic) get a fuller explanation where they are handled.
- Addresses are wire addresses, from 0, everywhere. "40001" only ever appears to explain that.
- Errors are typed structs matching one sentinel with `Is` (`modbus.ExceptionError` matches its code's), or wrapping
  one (`sunspec.PointError`), so `errors.Is` works. `modbus` errors start with `modbus: `, `sunspec`'s with `sunspec: `.
- Options have no prefix: `modbus.WithTimeout`, `sunspec.WithBases`.
- The unit goes on each `Client` call, never on the client.
- Getters return `(value, ok)`, never an error, and are nil-safe on `*Point` and `*Group`.

## Invariants

- **One request in flight per transport.** `sem`, a 1-token channel taken by `acquire`, so that waiting for it selects
  on ctx.
- **A TCP connection that failed or timed out is closed, never reused** (`TestTCPTransport_Timeout`): a late or
  half response must not answer the next request. The transaction ID check is for misbehaving devices only.
- **An RTU request that failed drains the line before the next** (`dirty`, `TestRTUTransport_LateResponse`), until it
  is quiet for t3.5 (`rtuOverTCPQuiet` without line timing).
- **The RTU port contract**: `Read` takes deadlines (`readDeadliner`), or returns at once with 0 bytes, which `line`
  polls. A `Read` that blocks without deadlines holds a request past its ctx. Documented on `RTUTransport`.
- **`watch`'s stop waits for its AfterFunc**, so a late expired deadline never lands on the next request.
- **RTU frames are delimited by length** (`responseLength`, `requestLength`), silence only before sending and to drain.
  Adding a function means adding its lengths, or RTU refuses it (`checkRTURequest`).
- **`line.idleAt` only moves forward** (`busyUntil`): a buffered response read at once must not make the line idle
  before the request has left (`TestRTUTransport_Silence`).
- **A server checks in the specification's order**: function, then length and quantity (IllegalDataValue), then range
  (IllegalDataAddress), then the handler (`TestServe_Checks`).
- **Points exist only after the first Read**, and a model whose groups are counted by a point is laid out again by each
  Read. One without keeps its layout: its points stay valid (`counted`), but the doc promises only until the next Read.
- **Layout always ends**: `model.Parse` refuses a group without points, so each repetition takes a register, and a
  group repeating to the end only as the model's last, without groups (`size` divides by it).
- **Setters encode into a scratch buffer and copy only on success**, so a refused value changes nothing.
- **A model from a Device refuses non-writable points, one from NewModel does not**: the simulator sets anything, and
  checks writability itself on Modbus writes, before applying any.
- **Simulator models change only inside `Update`**, under the lock reads and writes are served under, which the hooks
  run under too. A hook of `OnWrite` failing restores every model from the copy taken before the write
  (`registers`, `restore`), so a refused write changes nothing, what the hooks changed included.
- **`ErrNoResponse` is never answered**: `serve` reports it, and `serveConn`, `ServeRTU` write nothing, `Loopback`
  waits until its timeout (`wait`), measured from the request's start (`newBoundFrom`), and drops a response later
  than it. It reads the clock twice per request, only with a timeout, and takes a timer only when silent: no
  allocation, ~100ns in `BenchmarkModelRead`.
- **A `Replayer`'s log time never goes back** (`advance`), and its state before an entry is the first one's (`apply`ed
  backwards in `NewReplayer`). Only answered entries set values, only reads' exceptions set codes.
- **`model.Lookup` decodes from `encoded`, a constant**: a table of literals sat in RAM and overflowed a Pico; one
  function per model took TinyGo over 10 minutes to compile. `reader` returns slices of the constant, so strings stay
  in flash.

## Decisions not to revert

- **No dependency**, Modbus included: simonvetter/modbus has a stateful unit, no ctx, and logs to stdout.
- **No retry, no pipelining** in transports: a write may not be safe to repeat, and many devices mix up concurrent
  requests. Retries are the caller's.
- **A request's timeout is a deadline, not a derived ctx** (`bound`): the port's deadline enforces it, and `acquire`'s
  and `sleep`'s timers start only once they block. `context.WithTimeout` per request cost a timer and ~200 B, and
  `watch` skips its AfterFunc for a ctx that is never canceled. `bound.deadline` always includes ctx's own.
- **FC6 and FC16 are separate calls**: devices disagree on which a single register needs. `Model.Write` uses FC16.
- **Exceptions at a base or a header are not errors in `Discover`**: they move to the next base, or end the models.
  A transport error returns at once, rather than time out at each base.
- **Unimplemented values are pysunspec2's**, SunSpec's reference implementation (`sunspec2/mb.py`): `count` at
  0xFFFF like uint16, accumulators at 0, a string by its first byte, a pad filled with 0.
- **A negative scale factor divides** (`scale`), so 2301 × 10^-1 is exactly 230.1.
- **`Point.String` formats an unscaled integer exactly**, not through a float64.
- **Generated definitions keep labels, leave descriptions out**: the JSON has them.

## Tests

- External test packages only (`_test`); `modbus/export_test.go` exports the CRC.
- RTU and timing tests run in `synctest` bubbles, over a `net.Pipe` (deadlines) and a `uart` (polling): `ports()`
  runs a test over both. TCP tests use real sockets on 127.0.0.1:0 and are `//go:build !tinygo`: TinyGo's net has no
  `ListenConfig.Listen`, and its listeners report port 0.
- `sunspec`, `simulator`, `fault` and `record` tests talk through `modbus.Loopback`, so they run under TinyGo, and time
  in `synctest` bubbles: a `Loopback`'s timeout, a fault's delay, a `Limit`'s ramp, a replay. `counter` records
  requests, and fails them on demand.
- `model` tests check every generated definition against SunSpec's lengths (`TestDefinitions_Length`) and Parse's
  rules again, and the binary form round trip.
- Each example has an `Example()` with `// Output:`, and `tinygo-run` diffs `tinygo run`'s output with it. Examples
  reach their simulated device through a Loopback, but 01-registers, which serves TCP and so skips `tinygo-run`.
  09-replay runs on the real clock: its replay polls 50ms away from what it recorded.
