package main

func Example() {
	main()

	// Output:
	// record:
	//     0ms: W: 4200 W
	//   100ms: context deadline exceeded
	//   200ms: W: 3900 W
	// log:
	//   1 read holding 40000 2 ok
	//   1 read holding 40002 2 ok
	//   1 read holding 40070 2 ok
	//   1 read holding 40122 2 ok
	//   1 read holding 40070 52 ok
	//   1 read holding 40070 52 silent
	//   1 read holding 40070 52 ok
	// replay:
	//    50ms: W: 4200 W
	//   150ms: context deadline exceeded
	//   250ms: W: 3900 W
}
