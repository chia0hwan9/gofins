package gofins

import (
	"testing"
	"time"
)

// mockTransport for testing client operations
type mockTransport struct {
	responseFrame []byte
	err           error
}

func (m *mockTransport) Send(frame []byte) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.responseFrame, nil
}
func (m *mockTransport) Close() error               { return nil }
func (m *mockTransport) SetTimeout(d time.Duration) {}

// buildMockResponse builds a raw FINS response frame: header(10) + command(2) + endCode(2) + data
func buildMockResponse(cmd uint16, endCode uint16, data []byte) []byte {
	h := NewResponseHeader(NewCommandHeader(1, 0, 2, 0, 3))
	resp := Response{
		Header:  h,
		Command: cmd,
		EndCode: endCode,
		Data:    data,
	}
	return EncodeResponse(resp)
}

func TestClientReadWords(t *testing.T) {
	// Mock response for reading 2 words from DM100: 0x1234, 0x5678
	data := []byte{0x12, 0x34, 0x56, 0x78}
	respFrame := buildMockResponse(CmdMemoryRead, EndCodeNormal, data)

	transport := &mockTransport{responseFrame: respFrame}
	client := NewClient(transport, 2, 0, 1, 0, 0)

	words, err := client.ReadWords(MemAreaDM, 100, 2)
	if err != nil {
		t.Fatalf("ReadWords failed: %v", err)
	}
	if len(words) != 2 {
		t.Fatalf("expected 2 words, got %d", len(words))
	}
	if words[0] != 0x1234 || words[1] != 0x5678 {
		t.Errorf("unexpected words: %#v", words)
	}
}

func TestClientWriteWords(t *testing.T) {
	// Mock response for write (no data, just end code)
	respFrame := buildMockResponse(CmdMemoryWrite, EndCodeNormal, nil)

	transport := &mockTransport{responseFrame: respFrame}
	client := NewClient(transport, 2, 0, 1, 0, 0)

	err := client.WriteWords(MemAreaDM, 100, []uint16{0x1234, 0x5678})
	if err != nil {
		t.Fatalf("WriteWords failed: %v", err)
	}
}

func TestClientEndCodeError(t *testing.T) {
	// Response with end code 0x1103 (address range error)
	respFrame := buildMockResponse(CmdMemoryRead, EndCodeAddressRangeError, nil)

	transport := &mockTransport{responseFrame: respFrame}
	client := NewClient(transport, 2, 0, 1, 0, 0)

	_, err := client.ReadWords(MemAreaDM, 100, 2)
	endCodeErr, ok := err.(EndCodeError)
	if !ok {
		t.Fatalf("expected EndCodeError, got %T: %v", err, err)
	}
	if endCodeErr.Code != EndCodeAddressRangeError {
		t.Errorf("expected end code 0x%04X, got 0x%04X", EndCodeAddressRangeError, endCodeErr.Code)
	}
}

func TestIncompatibleMemoryArea(t *testing.T) {
	transport := &mockTransport{}
	client := NewClient(transport, 2, 0, 1, 0, 0)

	// Try to read words from a bit-only area
	_, err := client.ReadWords(MemAreaDMBit, 100, 2)
	if _, ok := err.(IncompatibleMemoryAreaError); !ok {
		t.Fatalf("expected IncompatibleMemoryAreaError, got %T: %v", err, err)
	}
}

func TestBCDEncodeDecode(t *testing.T) {
	val := uint16(1234)
	enc := BCDEncode(val)
	if len(enc) != 2 || enc[0] != 0x12 || enc[1] != 0x34 {
		t.Errorf("BCDEncode(1234) = %#v, want [0x12, 0x34]", enc)
	}
	dec, err := BCDDecode(enc)
	if err != nil || dec != val {
		t.Errorf("BCDDecode failed: %v, %d", err, dec)
	}
}

func TestBCDEncodeDecodeByte(t *testing.T) {
	val := byte(59)
	enc := BCDEncodeByte(val)
	if enc != 0x59 {
		t.Errorf("BCDEncodeByte(59) = 0x%02X, want 0x59", enc)
	}
	dec, err := BCDDecodeByte(enc)
	if err != nil || dec != val {
		t.Errorf("BCDDecodeByte failed: %v, %d", err, dec)
	}
	// Test invalid BCD
	_, err = BCDDecodeByte(0xFA)
	if err == nil {
		t.Error("expected error for invalid BCD byte 0xFA")
	}
}

func TestHeaderEncodeDecode(t *testing.T) {
	h := NewCommandHeader(1, 0, 2, 0, 5)
	enc := h.Encode()
	if len(enc) != 10 {
		t.Fatalf("expected 10 bytes, got %d", len(enc))
	}
	if enc[0] != ICFCommand || enc[9] != 5 {
		t.Errorf("header encode: ICF=0x%02X (want 0x%02X), SID=%d (want 5)", enc[0], ICFCommand, enc[9])
	}

	dec, err := DecodeHeader(enc)
	if err != nil {
		t.Fatalf("DecodeHeader failed: %v", err)
	}
	if !dec.IsCommand() {
		t.Error("expected IsCommand() = true")
	}
	if dec.IsResponse() {
		t.Error("expected IsResponse() = false")
	}
}

func TestRequestEncodeDecode(t *testing.T) {
	ma := NewWordAddress(MemAreaDM, 100)
	req := readCommand(NewCommandHeader(1, 0, 2, 0, 5), ma, 2)
	frame := EncodeRequest(req)

	// Verify frame structure
	if len(frame) < 12 {
		t.Fatalf("frame too short: %d bytes", len(frame))
	}
	// header[9] = SID
	if frame[9] != 5 {
		t.Errorf("SID = %d, want 5", frame[9])
	}
	// command code
	cmd := uint16(frame[10])<<8 | uint16(frame[11])
	if cmd != CmdMemoryRead {
		t.Errorf("command = 0x%04X, want 0x%04X", cmd, CmdMemoryRead)
	}
}

// Integration test with UDP server
func TestUDPIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Start server on random port
	server := NewServer()
	go func() {
		server.ListenAndServe("127.0.0.1:0")
	}()

	// Wait for server to be ready
	var addr string
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		if a := server.Addr(); a != nil {
			addr = a.String()
			break
		}
	}
	if addr == "" {
		t.Fatal("server did not start")
	}

	transport, err := NewUDPTransport(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()

	client := NewClient(transport, 2, 0, 1, 0, 0)
	defer client.Close()

	// Write and read back
	err = client.WriteWords(MemAreaDM, 100, []uint16{0xABCD, 0x1234})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	words, err := client.ReadWords(MemAreaDM, 100, 2)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(words) != 2 || words[0] != 0xABCD || words[1] != 0x1234 {
		t.Errorf("read back incorrect: %v", words)
	}

	server.Stop()
}
