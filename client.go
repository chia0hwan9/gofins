package gofins

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// FINS 单命令上限：超过必须由调用方拆包（否则响应帧会超出传输的分帧缓冲，
// TCP 侧 scanner 一旦超限整条连接作废）。
const (
	MaxItemsPerCommand = 999 // 0101/0102 字（项）上限
	MaxBitsPerCommand  = 256 // 位读写上限
)

// Client is the high-level FINS client, transport-agnostic.
//
// 并发安全：同一个 Client 可被多个 goroutine 共用——请求/响应周期（一问一答）、
// SID 分配与字节序读写都在内部串行化（与 gomc 的处理一致）。代价是同一时刻只有
// 一个命令在途：若某次调用等到响应超时，其它调用会排队。
type Client struct {
	mu        sync.Mutex
	transport Transport
	srcNode   byte
	srcUnit   byte
	dstNode   byte
	dstUnit   byte
	dstNetw   byte
	sid       byte
	byteOrder binary.ByteOrder
}

// NewClient creates a new client with the given transport.
// srcNode/srcUnit: source FINS address (our node).
// dstNode/dstUnit: destination PLC FINS address.
// dstNetwork: destination network (0 for local).
func NewClient(transport Transport, srcNode, srcUnit, dstNode, dstUnit, dstNetwork byte) *Client {
	return &Client{
		transport: transport,
		srcNode:   srcNode,
		srcUnit:   srcUnit,
		dstNode:   dstNode,
		dstUnit:   dstUnit,
		dstNetw:   dstNetwork,
		byteOrder: binary.BigEndian,
	}
}

// SetTimeout sets the transport timeout.
func (c *Client) SetTimeout(d time.Duration) {
	c.transport.SetTimeout(d)
}

// SetByteOrder sets the byte order for word read/write operations. Default: BigEndian.
// 读写双向都生效（读解码与写编码用同一个设置）。
func (c *Client) SetByteOrder(order binary.ByteOrder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byteOrder = order
}

// Close closes the underlying transport.
// 不等待在途命令；在途命令会以传输层错误返回。
func (c *Client) Close() error {
	return c.transport.Close()
}

// order 取当前字节序快照（一次操作内保持一致，不必持锁做 I/O）。
func (c *Client) order() binary.ByteOrder {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byteOrder
}

// do 编码、发送并解码一条命令（锁内完成 SID 分配与一问一答）。
// end code 非 0 时返回 EndCodeError（响应同时返回，便于调用方取现场）。
func (c *Client) do(command uint16, data []byte) (Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sid++
	if c.sid == 0 {
		c.sid = 1 // SID 0 保留给握手
	}
	header := NewCommandHeader(c.dstNode, c.dstUnit, c.srcNode, c.srcUnit, c.sid)
	respBytes, err := c.transport.Send(EncodeRequest(Request{Header: header, Command: command, Data: data}))
	if err != nil {
		return Response{}, err
	}
	resp, err := DecodeResponse(respBytes)
	if err != nil {
		return Response{}, err
	}
	if resp.EndCode != EndCodeNormal {
		return resp, EndCodeError{Code: resp.EndCode, Command: resp.Command}
	}
	return resp, nil
}

// encodeWords 按给定字节序编码字（写路径也要遵守 SetByteOrder）。
func encodeWords(values []uint16, order binary.ByteOrder) []byte {
	b := make([]byte, len(values)*2)
	for i, v := range values {
		order.PutUint16(b[i*2:], v)
	}
	return b
}

// ---------- Word Read/Write ----------

// ReadWords reads count words from the specified memory area starting at address.
func (c *Client) ReadWords(area MemoryArea, address uint16, count uint16) ([]uint16, error) {
	if !IsWordArea(area) {
		return nil, IncompatibleMemoryAreaError{Area: area}
	}
	if count == 0 || count > MaxItemsPerCommand {
		return nil, ProtocolError{Msg: fmt.Sprintf("word count %d out of range 1..%d", count, MaxItemsPerCommand)}
	}
	order := c.order()
	command, data := readCommand(NewWordAddress(area, address), count)
	resp, err := c.do(command, data)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) < int(count)*2 {
		return nil, ProtocolError{Msg: fmt.Sprintf("short read response: got %d bytes, want %d words", len(resp.Data), count)}
	}
	words := make([]uint16, count)
	for i := range words {
		words[i] = order.Uint16(resp.Data[i*2:])
	}
	return words, nil
}

// WriteWords writes words to the specified memory area.
func (c *Client) WriteWords(area MemoryArea, address uint16, values []uint16) error {
	if !IsWordArea(area) {
		return IncompatibleMemoryAreaError{Area: area}
	}
	if len(values) == 0 || len(values) > MaxItemsPerCommand {
		return ProtocolError{Msg: fmt.Sprintf("word count %d out of range 1..%d", len(values), MaxItemsPerCommand)}
	}
	command, data := writeCommand(NewWordAddress(area, address), encodeWords(values, c.order()))
	_, err := c.do(command, data)
	return err
}

// ---------- Byte Read/Write ----------

// ReadBytes reads raw bytes from a word memory area (byte count must be even).
func (c *Client) ReadBytes(area MemoryArea, address uint16, countBytes uint16) ([]byte, error) {
	if countBytes == 0 || countBytes%2 != 0 {
		return nil, ProtocolError{Msg: "byte count must be non-zero and even for word-based memory"}
	}
	if !IsWordArea(area) {
		return nil, IncompatibleMemoryAreaError{Area: area}
	}
	if countBytes/2 > MaxItemsPerCommand {
		return nil, ProtocolError{Msg: fmt.Sprintf("byte count %d out of range 2..%d", countBytes, MaxItemsPerCommand*2)}
	}
	command, data := readCommand(NewWordAddress(area, address), countBytes/2)
	resp, err := c.do(command, data)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) < int(countBytes) {
		return nil, ProtocolError{Msg: fmt.Sprintf("short read response: got %d bytes, want %d", len(resp.Data), countBytes)}
	}
	out := make([]byte, countBytes)
	copy(out, resp.Data) // 给调用方独立切片，不与响应缓冲共享
	return out, nil
}

// WriteBytes writes raw bytes to a word memory area (must be even length).
func (c *Client) WriteBytes(area MemoryArea, address uint16, data []byte) error {
	if len(data) == 0 || len(data)%2 != 0 {
		return ProtocolError{Msg: "data length must be non-zero and even for word-based memory"}
	}
	order := c.order()
	values := make([]uint16, len(data)/2)
	for i := range values {
		values[i] = order.Uint16(data[i*2:])
	}
	return c.WriteWords(area, address, values)
}

// ---------- String Read/Write ----------

// ReadString reads a string from PLC memory.
// byteCount is the number of BYTES to read (odd values are rounded up for word alignment).
// Returns the string with trailing null bytes trimmed.
func (c *Client) ReadString(area MemoryArea, address uint16, byteCount uint16) (string, error) {
	if byteCount%2 != 0 {
		byteCount++ // Word-align
	}
	data, err := c.ReadBytes(area, address, byteCount)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimRight(data, "\x00")), nil
}

// WriteString writes a string to PLC memory.
// The string is padded with a null byte if odd length for word alignment.
func (c *Client) WriteString(area MemoryArea, address uint16, s string) error {
	b := []byte(s)
	if len(b)%2 != 0 {
		b = append(b, 0x00)
	}
	return c.WriteBytes(area, address, b)
}

// ---------- Bit Read/Write ----------

// ReadBits reads count bits starting at the given address and bit offset.
// 响应里的位数据是"1 位 1 字节"。
func (c *Client) ReadBits(area MemoryArea, address uint16, startBit byte, count uint16) ([]bool, error) {
	if !IsBitArea(area) {
		return nil, IncompatibleMemoryAreaError{Area: area}
	}
	if startBit > 15 {
		return nil, InvalidAddressError{Area: area, Address: address}
	}
	if count == 0 || count > MaxBitsPerCommand {
		return nil, ProtocolError{Msg: fmt.Sprintf("bit count %d out of range 1..%d", count, MaxBitsPerCommand)}
	}
	command, data := readBitsCommand(area, address, startBit, count)
	resp, err := c.do(command, data)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) < int(count) {
		return nil, ProtocolError{Msg: fmt.Sprintf("short bit read response: got %d bytes, want %d", len(resp.Data), count)}
	}
	bits := make([]bool, count)
	for i := range bits {
		bits[i] = resp.Data[i]&0x01 != 0
	}
	return bits, nil
}

// WriteBits writes bits starting at the given address and bit offset.
// 位数据按"1 位 1 字节"（0x00/0x01）发出，命令里的 itemCount = 位数。
func (c *Client) WriteBits(area MemoryArea, address uint16, startBit byte, values []bool) error {
	if !IsBitArea(area) {
		return IncompatibleMemoryAreaError{Area: area}
	}
	if startBit > 15 {
		return InvalidAddressError{Area: area, Address: address}
	}
	if len(values) == 0 || len(values) > MaxBitsPerCommand {
		return ProtocolError{Msg: fmt.Sprintf("bit count %d out of range 1..%d", len(values), MaxBitsPerCommand)}
	}
	bitsData := make([]byte, len(values))
	for i, v := range values {
		if v {
			bitsData[i] = 0x01
		}
	}
	command, data := writeBitsCommand(area, address, startBit, uint16(len(values)), bitsData)
	_, err := c.do(command, data)
	return err
}

// SetBit sets a single bit to 1.
func (c *Client) SetBit(area MemoryArea, address uint16, bit byte) error {
	return c.WriteBits(area, address, bit, []bool{true})
}

// ResetBit sets a single bit to 0.
func (c *Client) ResetBit(area MemoryArea, address uint16, bit byte) error {
	return c.WriteBits(area, address, bit, []bool{false})
}

// ToggleBit reads the current bit and writes the opposite.
func (c *Client) ToggleBit(area MemoryArea, address uint16, bit byte) error {
	bits, err := c.ReadBits(area, address, bit, 1)
	if err != nil {
		return err
	}
	return c.WriteBits(area, address, bit, []bool{!bits[0]})
}

// ---------- Clock ----------

// ReadClock reads the PLC's internal clock.
func (c *Client) ReadClock() (*time.Time, error) {
	command, data := clockReadCommand()
	resp, err := c.do(command, data)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) < 7 {
		return nil, ProtocolError{Msg: "clock response too short"}
	}
	fields := make([]int, 6)
	for i := range fields {
		v, err := BCDDecodeByte(resp.Data[i])
		if err != nil {
			return nil, fmt.Errorf("clock field %d: %w", i, err)
		}
		fields[i] = int(v)
	}
	year, month, day, hour, min, sec := fields[0], fields[1], fields[2], fields[3], fields[4], fields[5]
	// Year is two-digit: < 50 → 2000+, >= 50 → 1900+
	fullYear := year + 2000
	if year >= 50 {
		fullYear = year + 1900
	}
	t := time.Date(fullYear, time.Month(month), day, hour, min, sec, 0, time.Local)
	return &t, nil
}

// WriteClock sets the PLC's clock.
func (c *Client) WriteClock(t time.Time) error {
	command, data := clockWriteCommand(t)
	_, err := c.do(command, data)
	return err
}

// ---------- PLC Control ----------

// Run requests the PLC to enter RUN mode.
func (c *Client) Run() error {
	command, data := runCommand(0x00) // 0x00 = RUN
	_, err := c.do(command, data)
	return err
}

// Stop requests the PLC to enter PROGRAM (stop) mode.
func (c *Client) Stop() error {
	command, data := stopCommand()
	_, err := c.do(command, data)
	return err
}

// ---------- Status ----------

// PLCStatus holds PLC status information.
// Based on Omron spec: byte[0]=status, byte[1]=mode, bytes[2:17]=fatal error flags (16x1-bit).
type PLCStatus struct {
	Status     byte // 0x00=STOP, 0x01=RUN, 0x80=STANDBY
	Mode       byte // 0x00=PROGRAM, 0x02=MONITOR, 0x04=RUN
	FatalError FatalErrorCode
}

// Status reads the PLC operating status.
func (c *Client) Status() (*PLCStatus, error) {
	command, data := statusReadCommand()
	resp, err := c.do(command, data)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) < 18 {
		return nil, ProtocolError{Msg: "status response too short"}
	}
	s := &PLCStatus{
		Status: resp.Data[0],
		Mode:   resp.Data[1],
	}
	// Bytes 2-17: 16 fatal error flags (1=error, 0=normal)
	var fatal FatalErrorCode
	for i := 0; i < 16; i++ {
		if resp.Data[2+i] == 1 {
			fatal |= FatalErrorCode(1 << i)
		}
	}
	s.FatalError = fatal
	return s, nil
}

// IsRunning returns true if the PLC is in RUN mode.
func (s *PLCStatus) IsRunning() bool { return s.Status == 0x01 }

// IsStopped returns true if the PLC is in STOP mode.
func (s *PLCStatus) IsStopped() bool { return s.Status == 0x00 }

// IsStandby returns true if the PLC is in STANDBY mode.
func (s *PLCStatus) IsStandby() bool { return s.Status == 0x80 }

// HasFatalError returns true if any fatal error flag is set.
func (s *PLCStatus) HasFatalError() bool { return s.FatalError != 0 }

// ---------- Ping ----------

// Ping sends a status read to check PLC connectivity.
func (c *Client) Ping() error {
	_, err := c.Status()
	return err
}
