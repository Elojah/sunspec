// Package record records a real device, and replays what it recorded, so that code reading devices is tested against
// what one did in the field, its quirks, outages and exceptions included, rather than a simulation of it.
//
// A Recorder is a modbus.Handler forwarding each request to the device, which modbus.Serve puts in front of it: the
// code under test talks to the Recorder instead, unchanged, and each request becomes an Entry, which the caller keeps,
// one line of text each:
//
//	recorder := record.NewRecorder(device, func(e record.Entry) {
//		line, _ := e.MarshalText()
//		_, _ = log.Write(append(line, '\n'))
//	})
//	err := modbus.Serve(ctx, listener, recorder)
//
// A Replayer is a modbus.Handler answering as the device did, from the entries: its registers as they were at each
// time, silent while it did not answer, and its exceptions. It replays a timeline, not requests one by one, so that
// the code it serves may poll differently from the code recorded:
//
//	replayer := record.NewReplayer(entries)
//	client := modbus.NewClient(modbus.NewLoopback(replayer))
package record
