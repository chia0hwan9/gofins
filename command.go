package gofins

import "time"

// readCommand builds a request to read words from PLC memory.
// Data format: area(1) + address(2) + bitOffset(1) + itemCount(2) = 6 bytes
func readCommand(header Header, ma MemoryAddress, count uint16) Request {
	data := make([]byte, 0, 6)
	data = append(data, ma.Encode()...) // 4 bytes: area + address + bitOffset
	data = append(data, byte(count>>8), byte(count&0xFF))
	return Request{
		Header:  header,
		Command: CmdMemoryRead,
		Data:    data,
	}
}

// writeCommand builds a request to write words to PLC memory.
// Data format: area(1) + address(2) + bitOffset(1) + itemCount(2) + writeData
func writeCommand(header Header, ma MemoryAddress, dataBytes []byte) Request {
	wordCount := uint16(len(dataBytes) / 2)
	buf := make([]byte, 0, 6+len(dataBytes))
	buf = append(buf, ma.Encode()...) // 4 bytes
	buf = append(buf, byte(wordCount>>8), byte(wordCount&0xFF))
	buf = append(buf, dataBytes...)
	return Request{
		Header:  header,
		Command: CmdMemoryWrite,
		Data:    buf,
	}
}

// fillCommand builds a request to fill a range of words with a constant value.
func fillCommand(header Header, ma MemoryAddress, count uint16, fillWord uint16) Request {
	data := make([]byte, 0, 8)
	data = append(data, ma.Encode()...) // 4 bytes
	data = append(data, byte(count>>8), byte(count&0xFF))
	data = append(data, byte(fillWord>>8), byte(fillWord&0xFF))
	return Request{
		Header:  header,
		Command: CmdMemoryFill,
		Data:    data,
	}
}

// readBitsCommand builds a request to read bits from a bit-accessible area.
func readBitsCommand(header Header, area MemoryArea, address uint16, startBit byte, count uint16) Request {
	// Standard FINS bit read: area(1) + address(2) + bit(1) + count(1)
	// Note: bit count uses only 1 byte (max 255 bits per command)
	data := []byte{
		byte(area),
		byte(address >> 8),
		byte(address & 0xFF),
		startBit,
		byte(count),
	}
	return Request{
		Header:  header,
		Command: CmdMemoryRead,
		Data:    data,
	}
}

// writeBitsCommand builds a request to write bits.
func writeBitsCommand(header Header, area MemoryArea, address uint16, startBit byte, bitsData []byte) Request {
	// Data format: area(1) + address(2) + bit(1) + bitCount(1) + data
	bitCount := uint16(len(bitsData) * 8)
	data := make([]byte, 0, 5+len(bitsData))
	data = append(data, byte(area), byte(address>>8), byte(address&0xFF), startBit, byte(bitCount))
	data = append(data, bitsData...)
	return Request{
		Header:  header,
		Command: CmdMemoryWrite,
		Data:    data,
	}
}

// runCommand requests PLC to enter RUN or MONITOR mode.
func runCommand(header Header, mode byte) Request {
	return Request{
		Header:  header,
		Command: CmdRun,
		Data:    []byte{mode},
	}
}

// stopCommand requests PLC to stop.
func stopCommand(header Header) Request {
	return Request{
		Header:  header,
		Command: CmdStop,
		Data:    []byte{0x00},
	}
}

// clockReadCommand reads the PLC clock.
func clockReadCommand(header Header) Request {
	return Request{
		Header:  header,
		Command: CmdClockRead,
		Data:    nil,
	}
}

// clockWriteCommand writes the PLC clock.
// FINS clock format: year(1) month(1) day(1) hour(1) minute(1) second(1) dayOfWeek(1) — all single-byte BCD.
func clockWriteCommand(header Header, t time.Time) Request {
	yearLow := byte(t.Year() % 100)
	month := byte(t.Month())
	day := byte(t.Day())
	hour := byte(t.Hour())
	minute := byte(t.Minute())
	second := byte(t.Second())
	dow := byte(t.Weekday())
	clockData := []byte{
		BCDEncodeByte(yearLow),
		BCDEncodeByte(month),
		BCDEncodeByte(day),
		BCDEncodeByte(hour),
		BCDEncodeByte(minute),
		BCDEncodeByte(second),
		BCDEncodeByte(dow),
	}
	return Request{
		Header:  header,
		Command: CmdClockWrite,
		Data:    clockData,
	}
}

// statusReadCommand reads PLC status.
func statusReadCommand(header Header) Request {
	return Request{
		Header:  header,
		Command: CmdStatusRead,
		Data:    nil,
	}
}
