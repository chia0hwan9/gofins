package gofins

import (
	"bytes"
	"encoding/binary"
	"time"
)

// Client is the high-level FINS client, transport-agnostic.
type Client struct {
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
func (c *Client) SetByteOrder(order binary.ByteOrder) {
	c.byteOrder = order
}

// Close closes the underlying transport.
func (c *Client) Close() error {
	return c.transport.Close()
}

// nextHeader creates a command header with an incremented SID.
func (c *Client) nextHeader() Header {
	c.sid++
	if c.sid == 0 {
		c.sid = 1
	}
	return NewCommandHeader(c.dstNode, c.dstUnit, c.srcNode, c.srcUnit, c.sid)
}

// sendRequest encodes, sends, and decodes a FINS request/response cycle.
func (c *Client) sendRequest(req Request) (Response, error) {
	frame := EncodeRequest(req)
	respBytes, err := c.transport.Send(frame)
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

// ---------- Word Read/Write ----------

// ReadWords reads count words from the specified memory area starting at address.
func (c *Client) ReadWords(area MemoryArea, address uint16, count uint16) ([]uint16, error) {
	if !IsWordArea(area) {
		return nil, IncompatibleMemoryAreaError{Area: area}
	}
	ma := NewWordAddress(area, address)
	req := readCommand(c.nextHeader(), ma, count)
	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	words := make([]uint16, count)
	for i := uint16(0); i < count && i*2+1 < uint16(len(resp.Data)); i++ {
		words[i] = c.byteOrder.Uint16(resp.Data[i*2 : i*2+2])
	}
	return words, nil
}

// WriteWords writes words to the specified memory area.
func (c *Client) WriteWords(area MemoryArea, address uint16, values []uint16) error {
	if !IsWordArea(area) {
		return IncompatibleMemoryAreaError{Area: area}
	}
	data := WordsToBytes(values)
	ma := NewWordAddress(area, address)
	req := writeCommand(c.nextHeader(), ma, data)
	_, err := c.sendRequest(req)
	return err
}

// ---------- Byte Read/Write ----------

// ReadBytes reads raw bytes from a word memory area (must be even count).
func (c *Client) ReadBytes(area MemoryArea, address uint16, countBytes uint16) ([]byte, error) {
	if countBytes%2 != 0 {
		return nil, ProtocolError{Msg: "byte count must be even for word-based memory"}
	}
	if !IsWordArea(area) {
		return nil, IncompatibleMemoryAreaError{Area: area}
	}
	ma := NewWordAddress(area, address)
	req := readCommand(c.nextHeader(), ma, countBytes/2)
	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// WriteBytes writes raw bytes to a word memory area (must be even length).
func (c *Client) WriteBytes(area MemoryArea, address uint16, data []byte) error {
	if len(data)%2 != 0 {
		return ProtocolError{Msg: "data length must be even for word-based memory"}
	}
	return c.WriteWords(area, address, bytesToWordsRaw(data, c.byteOrder))
}

// ---------- String Read/Write ----------

// ReadString reads a string from PLC memory.
// byteCount is the number of BYTES to read (must be even for word areas).
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
func (c *Client) ReadBits(area MemoryArea, address uint16, startBit byte, count uint16) ([]bool, error) {
	if !IsBitArea(area) {
		return nil, IncompatibleMemoryAreaError{Area: area}
	}
	if count > 256 {
		return nil, ProtocolError{Msg: "bit count cannot exceed 256"}
	}
	req := readBitsCommand(c.nextHeader(), area, address, startBit, count)
	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	bits := make([]bool, count)
	for i := uint16(0); i < count && i < uint16(len(resp.Data)); i++ {
		bits[i] = resp.Data[i]&0x01 != 0
	}
	return bits, nil
}

// WriteBits writes bits starting at the given address and bit offset.
func (c *Client) WriteBits(area MemoryArea, address uint16, startBit byte, values []bool) error {
	if !IsBitArea(area) {
		return IncompatibleMemoryAreaError{Area: area}
	}
	if len(values) > 256 {
		return ProtocolError{Msg: "bit count cannot exceed 256"}
	}
	// Pack bits into bytes
	byteLen := (len(values) + 7) / 8
	bitsData := make([]byte, byteLen)
	for i, v := range values {
		if v {
			bitsData[i/8] |= 1 << (i % 8)
		}
	}
	req := writeBitsCommand(c.nextHeader(), area, address, startBit, bitsData)
	_, err := c.sendRequest(req)
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
	if len(bits) == 0 {
		return ProtocolError{Msg: "failed to read bit"}
	}
	return c.WriteBits(area, address, bit, []bool{!bits[0]})
}

// ---------- Clock ----------

// ReadClock reads the PLC's internal clock.
func (c *Client) ReadClock() (*time.Time, error) {
	req := clockReadCommand(c.nextHeader())
	resp, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) < 7 {
		return nil, ProtocolError{Msg: "clock response too short"}
	}
	year, _ := BCDDecodeByte(resp.Data[0])
	month, _ := BCDDecodeByte(resp.Data[1])
	day, _ := BCDDecodeByte(resp.Data[2])
	hour, _ := BCDDecodeByte(resp.Data[3])
	min, _ := BCDDecodeByte(resp.Data[4])
	sec, _ := BCDDecodeByte(resp.Data[5])
	// Year is two-digit: < 50 → 2000+, >= 50 → 1900+
	fullYear := int(year) + 2000
	if year >= 50 {
		fullYear = int(year) + 1900
	}
	t := time.Date(fullYear, time.Month(month), int(day), int(hour), int(min), int(sec), 0, time.Local)
	return &t, nil
}

// WriteClock sets the PLC's clock.
func (c *Client) WriteClock(t time.Time) error {
	req := clockWriteCommand(c.nextHeader(), t)
	_, err := c.sendRequest(req)
	return err
}

// ---------- PLC Control ----------

// Run requests the PLC to enter RUN mode.
func (c *Client) Run() error {
	req := runCommand(c.nextHeader(), 0x00) // 0x00 = RUN
	_, err := c.sendRequest(req)
	return err
}

// Stop requests the PLC to enter PROGRAM (stop) mode.
func (c *Client) Stop() error {
	req := stopCommand(c.nextHeader())
	_, err := c.sendRequest(req)
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
	req := statusReadCommand(c.nextHeader())
	resp, err := c.sendRequest(req)
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

// ---------- Helpers ----------

// bytesToWordsRaw converts bytes to uint16 slice using the given byte order.
func bytesToWordsRaw(data []byte, order binary.ByteOrder) []uint16 {
	words := make([]uint16, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		words[i/2] = order.Uint16(data[i : i+2])
	}
	return words
}
