package modbus

// Exported for tests, which check the CRC against the specification's frames, and build frames of their own.
var (
	CRC16     = crc16
	AppendCRC = appendCRC
)
