package modbus

import "encoding/binary"

// crcTable holds the CRC of each byte value, so that crc16 takes one lookup per byte rather than eight shifts.
var crcTable = func() [256]uint16 {
	var table [256]uint16
	for i := range table {
		crc := uint16(i)
		for range 8 {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
		table[i] = crc
	}

	return table
}()

// crc16 is the CRC of RTU frames, CRC-16/MODBUS: polynomial 0x8005 reflected (hence 0xA001, shifting right), starting
// from 0xFFFF. A frame carries it low byte first, unlike every other Modbus field: 01 03 00 00 00 0A ends with C5 CD
// for a CRC of 0xCDC5.
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = crc>>8 ^ crcTable[(crc^uint16(b))&0xFF]
	}

	return crc
}

// appendCRC appends the CRC of b to b, low byte first.
func appendCRC(b []byte) []byte {
	return binary.LittleEndian.AppendUint16(b, crc16(b))
}

// checkCRC reports whether frame, CRC included, ends with the CRC of what precedes it.
func checkCRC(frame []byte) bool {
	n := len(frame) - 2

	return n >= 0 && crc16(frame[:n]) == binary.LittleEndian.Uint16(frame[n:])
}
