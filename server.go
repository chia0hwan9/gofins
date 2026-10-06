package gofins

import (
	"encoding/binary"
	"log"
	"net"
	"sync"
	"time"
)

// Server is a simple FINS UDP server that simulates a PLC for testing.
type Server struct {
	conn     *net.UDPConn
	mu       sync.RWMutex
	memory   map[MemoryArea]map[uint16][]byte // Word-aligned storage per area
	handlers map[uint16]CommandHandlerFunc
	closed   bool
}

// CommandHandlerFunc handles a FINS command and returns response data (excluding header, command, end code).
type CommandHandlerFunc func(req Request) ([]byte, error)

// NewServer creates a new UDP server with default handlers.
func NewServer() *Server {
	s := &Server{
		memory:   make(map[MemoryArea]map[uint16][]byte),
		handlers: make(map[uint16]CommandHandlerFunc),
	}
	s.RegisterHandler(CmdMemoryRead, s.handleMemoryRead)
	s.RegisterHandler(CmdMemoryWrite, s.handleMemoryWrite)
	s.RegisterHandler(CmdRun, s.handleRun)
	s.RegisterHandler(CmdStop, s.handleStop)
	s.RegisterHandler(CmdClockRead, s.handleClockRead)
	s.RegisterHandler(CmdStatusRead, s.handleStatusRead)
	return s
}

// RegisterHandler registers a custom handler for a command code.
func (s *Server) RegisterHandler(cmd uint16, fn CommandHandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[cmd] = fn
}

// ListenAndServe starts the UDP server on the given address. Blocks until Stop() is called.
func (s *Server) ListenAndServe(addr string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	buf := make([]byte, 65535)
	for {
		s.mu.RLock()
		c := s.conn
		closed := s.closed
		s.mu.RUnlock()

		if closed || c == nil {
			return nil
		}

		n, remoteAddr, err := c.ReadFromUDP(buf)
		if err != nil {
			if !closed {
				log.Printf("FINS server read error: %v", err)
			}
			return err
		}
		// 拷贝出本次请求再交给 goroutine：读循环马上会复用 buf，
		// 直接传 buf[:n] 会与 handleRequest 里解析的请求互相覆盖（并发下必须 copy）。
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		go s.handleRequest(pkt, remoteAddr)
	}
}

// handleRequest processes a single UDP datagram.
func (s *Server) handleRequest(data []byte, remote *net.UDPAddr) {
	respFrame, err := s.processFrame(data)
	if err != nil {
		return
	}
	s.mu.RLock()
	conn := s.conn
	s.mu.RUnlock()
	if conn != nil {
		conn.WriteToUDP(respFrame, remote)
	}
}

// processFrame decodes a request, dispatches to handler, and builds the response frame.
func (s *Server) processFrame(frame []byte) ([]byte, error) {
	// Parse raw FINS request: header(10) + command(2) + data
	if len(frame) < 12 {
		return nil, ProtocolError{Msg: "request frame too short"}
	}
	header, err := DecodeHeader(frame[0:10])
	if err != nil {
		return nil, err
	}
	cmd := binary.BigEndian.Uint16(frame[10:12])
	reqData := frame[12:]

	req := Request{
		Header:  header,
		Command: cmd,
		Data:    reqData,
	}

	s.mu.RLock()
	handler, ok := s.handlers[cmd]
	s.mu.RUnlock()
	if !ok {
		handler = s.defaultHandler
	}

	respData, err := handler(req)
	endCode := EndCodeNormal
	if err != nil {
		endCode = mapErrorToEndCode(err)
		respData = nil
	}

	respHeader := NewResponseHeader(header)
	resp := Response{
		Header:  respHeader,
		Command: cmd,
		EndCode: endCode,
		Data:    respData,
	}
	return EncodeResponse(resp), nil
}

func mapErrorToEndCode(err error) uint16 {
	switch err.(type) {
	case InvalidAddressError:
		return EndCodeAddressRangeError
	case addressExceededError:
		return EndCodeAddressExceeded
	default:
		return EndCodeUndefinedCommand
	}
}

func (s *Server) defaultHandler(req Request) ([]byte, error) {
	return nil, ProtocolError{Msg: "unsupported command"}
}

// ---------- Memory access ----------

func (s *Server) getWord(area MemoryArea, addr uint16) (uint16, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	areaMap, ok := s.memory[area]
	if !ok {
		return 0, false
	}
	data, ok := areaMap[addr]
	if !ok || len(data) < 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(data), true
}

func (s *Server) setWord(area MemoryArea, addr uint16, value uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.memory[area] == nil {
		s.memory[area] = make(map[uint16][]byte)
	}
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, value)
	s.memory[area][addr] = buf
}

// getBit/setBit 位区也存成字（与 word 区共用 memory），位号 0 = 字的 LSB。
// 读写时按 (word, bit) 折算，跨字自动进位（bit 14 起写 4 位会落到下一字的 bit 0-1）。
func (s *Server) getBit(area MemoryArea, word uint16, bit byte) bool {
	v, _ := s.getWord(area, word)
	return v&(1<<bit) != 0
}

func (s *Server) setBit(area MemoryArea, word uint16, bit byte, on bool) {
	v, _ := s.getWord(area, word)
	if on {
		v |= 1 << bit
	} else {
		v &^= 1 << bit
	}
	s.setWord(area, word, v)
}

// ---------- Command handlers ----------

// simMaxWordAddress 模拟器的字地址上限（CJ/CS 系列 DM/EM 最大 D32767）。
// 上游 l1va 的模拟器会做越界检查并回 0x1104，我们保留这条——越界路径也能端到端测。
const simMaxWordAddress = 32767

// addressExceededError 让模拟器 handler 指定 end code 0x1104（地址范围溢出）。
type addressExceededError struct{}

func (addressExceededError) Error() string { return "address range exceeded" }

func (s *Server) handleMemoryRead(req Request) ([]byte, error) {
	// Data format: area(1) + address(2) + bitOffset(1) + itemCount(2) = 6 bytes
	if len(req.Data) < 6 {
		return nil, ProtocolError{Msg: "read command too short"}
	}
	area := MemoryArea(req.Data[0])
	address := binary.BigEndian.Uint16(req.Data[1:3])
	bitOffset := req.Data[3]
	count := binary.BigEndian.Uint16(req.Data[4:6])
	if count == 0 {
		return nil, ProtocolError{Msg: "read item count is zero"}
	}

	// 位区：响应是"1 位 1 字节"
	if IsBitArea(area) {
		if uint32(address)+(uint32(bitOffset)+uint32(count)-1)/16 > simMaxWordAddress {
			return nil, addressExceededError{}
		}
		data := make([]byte, count)
		for i := uint16(0); i < count; i++ {
			word, bit, ok := bitAt(address, bitOffset, i)
			if !ok {
				break
			}
			if s.getBit(area, word, bit) {
				data[i] = 0x01
			}
		}
		return data, nil
	}

	if uint32(address)+uint32(count)-1 > simMaxWordAddress {
		return nil, addressExceededError{}
	}

	data := make([]byte, count*2)
	for i := uint16(0); i < count; i++ {
		val, ok := s.getWord(area, address+i)
		if !ok {
			val = 0
		}
		binary.BigEndian.PutUint16(data[i*2:], val)
	}
	return data, nil
}

// bitAt 把「起始字 + 起始位 + 第 i 位」折算成 (字地址, 位号)，越界返回 ok=false。
func bitAt(address uint16, startBit byte, i uint16) (uint16, byte, bool) {
	total := uint32(startBit) + uint32(i)
	word := uint32(address) + total/16
	if word > 0xFFFF {
		return 0, 0, false
	}
	return uint16(word), byte(total % 16), true
}

func (s *Server) handleMemoryWrite(req Request) ([]byte, error) {
	// Data format: area(1) + address(2) + bitOffset(1) + itemCount(2) + writeData
	if len(req.Data) < 6 {
		return nil, ProtocolError{Msg: "write command too short"}
	}
	area := MemoryArea(req.Data[0])
	address := binary.BigEndian.Uint16(req.Data[1:3])
	bitOffset := req.Data[3]
	count := binary.BigEndian.Uint16(req.Data[4:6])
	payload := req.Data[6:]

	// 位区：数据是"1 位 1 字节"，itemCount 是位数
	if IsBitArea(area) {
		if count == 0 || len(payload) < int(count) {
			return nil, ProtocolError{Msg: "bit write data shorter than item count"}
		}
		if uint32(address)+(uint32(bitOffset)+uint32(count)-1)/16 > simMaxWordAddress {
			return nil, addressExceededError{}
		}
		for i := uint16(0); i < count; i++ {
			word, bit, ok := bitAt(address, bitOffset, i)
			if !ok {
				break
			}
			s.setBit(area, word, bit, payload[i]&0x01 != 0)
		}
		return nil, nil
	}

	if len(payload)%2 != 0 {
		return nil, ProtocolError{Msg: "write data not word-aligned"}
	}
	if words := uint32(len(payload) / 2); words > 0 && uint32(address)+words-1 > simMaxWordAddress {
		return nil, addressExceededError{}
	}
	for i := 0; i < len(payload); i += 2 {
		val := binary.BigEndian.Uint16(payload[i:])
		s.setWord(area, address+uint16(i/2), val)
	}
	return nil, nil
}

func (s *Server) handleRun(req Request) ([]byte, error) {
	return nil, nil
}

func (s *Server) handleStop(req Request) ([]byte, error) {
	return nil, nil
}

func (s *Server) handleClockRead(req Request) ([]byte, error) {
	now := time.Now()
	return []byte{
		BCDEncodeByte(byte(now.Year() % 100)),
		BCDEncodeByte(byte(now.Month())),
		BCDEncodeByte(byte(now.Day())),
		BCDEncodeByte(byte(now.Hour())),
		BCDEncodeByte(byte(now.Minute())),
		BCDEncodeByte(byte(now.Second())),
		BCDEncodeByte(byte(now.Weekday())),
	}, nil
}

func (s *Server) handleStatusRead(req Request) ([]byte, error) {
	// Omron spec: byte[0]=status(0x01=RUN), byte[1]=mode, bytes[2:17]=fatal errors
	statusData := make([]byte, 18)
	statusData[0] = 0x01 // RUN
	statusData[1] = 0x04 // RUN mode
	return statusData, nil
}

// Stop closes the UDP socket.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.conn != nil {
		err := s.conn.Close()
		s.conn = nil
		return err
	}
	return nil
}

// Addr returns the server's local address (valid after ListenAndServe is called).
func (s *Server) Addr() net.Addr {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.conn != nil {
		return s.conn.LocalAddr()
	}
	return nil
}
