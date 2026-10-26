// Package sunspec reads and writes SunSpec devices, such as inverters, meters and batteries, over Modbus: it finds the
// models a device holds, and decodes their points, by name, with their scale factors and types.
//
//	client := modbus.NewClient(modbus.NewTCPTransport("192.168.1.10:502"))
//	device, err := sunspec.Discover(ctx, client, 1)
//	...
//	inverter := device.Model(103)
//	err = inverter.Read(ctx)
//	...
//	w, ok := inverter.Point("W").Float() // scaled by W_SF, ok false if not implemented
//
// Package model holds the definitions, package modbus the transport, and package simulator a device in memory, for
// tests: package fault makes it, or the link to it, fail as in the field, and package record records a real device to
// replay it.
//
// # The register map
//
// A SunSpec device holds its models in its holding registers, from a base address, 40000 most often:
//
//	40000  0x5375 0x6E53  the marker, "SunS"
//	40002  1 66           model 1, the common one: its ID, its length L, then L registers
//	40070  103 50         model 103, a three-phase inverter
//	40122  0xFFFF 0       the end
//
// 40000 is the address on the wire, which documentation numbering registers from 1 calls 40001.
//
// # Points
//
// A model's points follow each other in its registers, as its definition lays them out, groups included: model 160
// repeats a group per MPPT module. A point's type sets how its registers decode, high word first, and reserves a value
// meaning not implemented: an int16 holds 0x8000 for a measurement the device does not make. Most measurements are
// integers scaled by a power of ten, a scale factor that another point holds, so that W is 4200 W when it holds 42 and
// W_SF holds 2.
//
// Getters return a value and whether there is one: none when the point is not implemented, or its scale factor is
// not, or the device's model lacks the point altogether, for which Point returns nil, whose getters say so too.
//
// # Reading and writing
//
// Model.Read reads a model in as few requests as the protocol allows, and lays its points out again, since how many
// times a group repeats may be held by a point. Setters change the registers in memory, checking the type and the
// range, and Model.Write sends what they changed.
package sunspec
