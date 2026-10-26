package main

func Example() {
	main()

	// Output:
	// up: W: 4200 W, WH: 12345678 Wh
	// off: context deadline exceeded
	// starting: modbus: server device busy, for function 0x03
	// up again: W: 4200 W, WH: not implemented
	// noise: modbus: CRC mismatch
	// limit: context deadline exceeded
	// inverter: WMaxLimPct: 50 % WMax | WMaxLim_Ena: DISABLED
	// limit again: <nil>
	// inverter: WMaxLimPct: 50 % WMax | WMaxLim_Ena: ENABLED
}
