package gofins

import "time"

// 命令构造器只产出「命令码 + 数据段」。
// FINS 头（含 SID）由 Client 在锁内生成（见 Client.do），这样并发共享一个 Client 时
// SID 分配与一问一答都在同一条临界区里，不会互相抢。

// readCommand builds a request to read words from PLC memory.
// Data format: area(1) + address(2) + bitOffset(1) + itemCount(2) = 6 bytes
func readCommand(ma MemoryAddress, count uint16) (uint16, []byte) {
	data := make([]byte, 0, 6)
	data = append(data, ma.Encode()...) // 4 bytes: area + address + bitOffset
	data = append(data, byte(count>>8), byte(count&0xFF))
	return CmdMemoryRead, data
}

// writeCommand builds a request to write words to PLC memory.
// Data format: area(1) + address(2) + bitOffset(1) + itemCount(2) + writeData
func writeCommand(ma MemoryAddress, dataBytes []byte) (uint16, []byte) {
	wordCount := uint16(len(dataBytes) / 2)
	buf := make([]byte, 0, 6+len(dataBytes))
	buf = append(buf, ma.Encode()...) // 4 bytes
	buf = append(buf, byte(wordCount>>8), byte(wordCount&0xFF))
	buf = append(buf, dataBytes...)
	return CmdMemoryWrite, buf
}

// readBitsCommand builds a request to read bits from a bit-accessible area.
// Data format: area(1) + address(2) + bit(1) + itemCount(2) = 6 bytes
// itemCount 是**位数**（2 字节）——位读写用的是同一个 0101/0102 命令格式，
// 只有"地址+位号"和"位数据"不同；写 1 字节 count 会让 PLC 判为命令过短。
func readBitsCommand(area MemoryArea, address uint16, startBit byte, count uint16) (uint16, []byte) {
	data := []byte{
		byte(area),
		byte(address >> 8),
		byte(address & 0xFF),
		startBit,
		byte(count >> 8),
		byte(count & 0xFF),
	}
	return CmdMemoryRead, data
}

// writeBitsCommand builds a request to write bits.
// Data format: area(1) + address(2) + bit(1) + bitCount(2) + 位数据
//
// bitCount 是**位数**，位数据按"1 位 1 字节"给（0x00/0x01）——与上游在 CJ2M 上
// 验证过的编码一致。传补齐后的字节数×8 会让 PLC 多写后面几位（SetBit 会连带清掉
// 同字里后续 7 位）。
func writeBitsCommand(area MemoryArea, address uint16, startBit byte, bitCount uint16, bitsData []byte) (uint16, []byte) {
	data := make([]byte, 0, 6+len(bitsData))
	data = append(data, byte(area), byte(address>>8), byte(address&0xFF), startBit,
		byte(bitCount>>8), byte(bitCount&0xFF))
	data = append(data, bitsData...)
	return CmdMemoryWrite, data
}

// runCommand requests PLC to enter RUN or MONITOR mode.
func runCommand(mode byte) (uint16, []byte) {
	return CmdRun, []byte{mode}
}

// stopCommand requests PLC to stop.
func stopCommand() (uint16, []byte) {
	return CmdStop, []byte{0x00}
}

// clockReadCommand reads the PLC clock.
func clockReadCommand() (uint16, []byte) {
	return CmdClockRead, nil
}

// clockWriteCommand writes the PLC clock.
// FINS clock format: year(1) month(1) day(1) hour(1) minute(1) second(1) dayOfWeek(1) — all single-byte BCD.
func clockWriteCommand(t time.Time) (uint16, []byte) {
	clockData := []byte{
		BCDEncodeByte(byte(t.Year() % 100)),
		BCDEncodeByte(byte(t.Month())),
		BCDEncodeByte(byte(t.Day())),
		BCDEncodeByte(byte(t.Hour())),
		BCDEncodeByte(byte(t.Minute())),
		BCDEncodeByte(byte(t.Second())),
		BCDEncodeByte(byte(t.Weekday())),
	}
	return CmdClockWrite, clockData
}

// statusReadCommand reads PLC status.
func statusReadCommand() (uint16, []byte) {
	return CmdStatusRead, nil
}
