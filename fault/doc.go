// Package fault makes a simulated device, or the link to it, misbehave as real ones do, so that the code reading them
// is tested against what happens in the field: a device that is off or reboots, answers an exception, answers late,
// stops updating its measurements or ignores a write; a line with noise, or a response lost after a write applied.
//
// Handler wraps the device's modbus.Handler, such as a simulator.Simulator, and serves over any server: Loopback,
// Serve or ServeRTU. Transport wraps the client's modbus.Transport. Each asks a decide function, for every request,
// which fault applies: it reads the time, counts requests, or draws at random, so that a test or a harness schedules
// faults as it sees fit.
//
//	device := fault.NewHandler(sim, func(r fault.Request) fault.Device {
//		if rebooting.Load() {
//			return fault.Device{Silent: true}
//		}
//		return fault.Device{}
//	})
//	client := modbus.NewClient(modbus.NewLoopback(device))
package fault
