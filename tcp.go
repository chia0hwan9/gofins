package gofins

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

const (
	finsMagic                  = "FINS"
	finsCmdHandshake    uint32 = 0 // Initial connection
	finsCmdHandshakeRsp uint32 = 1 // Handshake response
	finsCmdData         uint32 = 2 // Data frame

	minFrameLength = 16 // Minimum FINS TCP frame (header alone)
	maxFrameLength = 2048

	defaultConnectTimeout  = 5 * time.Second
	defaultResponseTimeout = 10 * time.Second
)

// TCPTransport implements FINS over TCP with FINS init frame wrapping,
// persistent connection, handshake, SID management, and optional reconnect.
type TCPTransport struct {
	addr              string
	node, unit, netw  byte // Source FINS address
	plcNode           byte // PLC node (learned from handshake)
	timeout           time.Duration
	reconnect         bool
	keepaliveInterval time.Duration

	conn     net.Conn
	reader   *bufio.Reader
	mu       sync.Mutex
	sid      byte // Next SID
	closed   bool
	stopKeep chan struct{}

	resp   map[byte]chan Response
	respMu sync.Mutex
}

// NewTCPTransport creates a new TCP transport. Call Connect() before using.
// addr: "host:port", srcNode/srcUnit: source FINS address.
func NewTCPTransport(addr string, srcNode, srcUnit, srcNetwork byte) *TCPTransport {
	return &TCPTransport{
		addr:      addr,
		node:      srcNode,
		unit:      srcUnit,
		netw:      srcNetwork,
		timeout:   defaultResponseTimeout,
		reconnect: true,
		resp:      make(map[byte]chan Response),
	}
}

// SetTimeout sets the response timeout.
func (t *TCPTransport) SetTimeout(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timeout = d
}

// SetReconnect enables or disables automatic reconnect.
func (t *TCPTransport) SetReconnect(enable bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reconnect = enable
}

// SetKeepalive sets the keepalive interval (0 to disable).
func (t *TCPTransport) SetKeepalive(interval time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keepaliveInterval = interval
	if t.conn != nil && interval > 0 {
		t.startKeepaliveLocked()
	}
}

// Connect establishes the TCP connection and performs the FINS handshake.
func (t *TCPTransport) Connect() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ConnectionClosedError{}
	}
	if t.conn != nil {
		return nil // Already connected
	}

	dialer := net.Dialer{Timeout: defaultConnectTimeout}
	conn, err := dialer.Dial("tcp", t.addr)
	if err != nil {
		return fmt.Errorf("TCP dial failed: %w", err)
	}

	t.conn = conn
	t.reader = bufio.NewReader(conn)

	if err := t.handshakeLocked(); err != nil {
		conn.Close()
		t.conn = nil
		t.reader = nil
		return fmt.Errorf("FINS handshake failed: %w", err)
	}

	go t.listenLoop()

	if t.keepaliveInterval > 0 {
		t.startKeepaliveLocked()
	}

	return nil
}

// handshakeLocked performs the FINS TCP connection handshake.
// Must be called with t.mu held.
func (t *TCPTransport) handshakeLocked() error {
	// Build handshake frame: FINS init header (16 bytes) + clientNode (4 bytes) = 20 bytes
	frame := make([]byte, 20)
	copy(frame[0:4], finsMagic)
	binary.BigEndian.PutUint32(frame[4:8], 12)                // Length = 12 (init frame is 8 + 4 for clientNode)
	binary.BigEndian.PutUint32(frame[8:12], finsCmdHandshake) // Command = 0
	binary.BigEndian.PutUint32(frame[12:16], 0)               // Error code = 0
	// clientNode bytes 16-19 are already zero (auto-assign)

	if _, err := t.conn.Write(frame); err != nil {
		return fmt.Errorf("handshake write: %w", err)
	}

	resp := make([]byte, 24)
	if _, err := t.reader.Read(resp); err != nil {
		return fmt.Errorf("handshake read: %w", err)
	}

	// Verify FINS marker
	if string(resp[0:4]) != finsMagic {
		return ProtocolError{Msg: "invalid handshake response: missing FINS marker"}
	}

	// Extract assigned node numbers
	clientNode := resp[19] // Our node assigned by PLC
	serverNode := resp[23] // PLC's node

	t.node = clientNode
	t.plcNode = serverNode

	return nil
}

// Send sends a raw FINS frame (no TCP wrapping) and returns the raw response.
// The TCP wrapping (FINS init frame) is added/removed internally.
func (t *TCPTransport) Send(frame []byte) ([]byte, error) {
	// Fast path: lock, check state
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ConnectionClosedError{}
	}
	if t.conn == nil {
		t.mu.Unlock()
		return nil, NotConnectedError{}
	}

	// Get next SID and inject into frame header byte 9 (SID field)
	sid := t.nextSIDLocked()
	// frame[9] is the SID byte in the 10-byte FINS header
	if len(frame) >= 10 {
		frame[9] = sid
	}

	// Build TCP-wrapped frame: FINS init header (16) + raw FINS frame
	finsLen := uint32(len(frame))
	wrappedFrame := make([]byte, 16+len(frame))
	copy(wrappedFrame[0:4], finsMagic)
	binary.BigEndian.PutUint32(wrappedFrame[4:8], finsLen)
	binary.BigEndian.PutUint32(wrappedFrame[8:12], finsCmdData)
	binary.BigEndian.PutUint32(wrappedFrame[12:16], 0) // Error code = 0
	copy(wrappedFrame[16:], frame)

	// Create response channel before sending
	respCh := make(chan Response, 1)
	t.respMu.Lock()
	t.resp[sid] = respCh
	t.respMu.Unlock()

	// Cleanup after
	defer func() {
		t.respMu.Lock()
		delete(t.resp, sid)
		t.respMu.Unlock()
	}()

	if _, err := t.conn.Write(wrappedFrame); err != nil {
		t.mu.Unlock()
		return nil, fmt.Errorf("TCP write: %w", err)
	}
	t.mu.Unlock()

	// Wait for response
	select {
	case resp, ok := <-respCh:
		if !ok {
			return nil, ConnectionClosedError{}
		}
		return EncodeResponse(resp), nil
	case <-time.After(t.timeout):
		return nil, ResponseTimeoutError{Duration: t.timeout}
	}
}

// nextSIDLocked increments and returns the next SID. Must hold t.mu.
func (t *TCPTransport) nextSIDLocked() byte {
	t.sid++
	if t.sid == 0 {
		t.sid = 1 // SID 0 is reserved for handshake
	}
	return t.sid
}

// listenLoop reads TCP frames, unwraps FINS init frame, and dispatches responses by SID.
func (t *TCPTransport) listenLoop() {
	defer func() {
		t.mu.Lock()
		t.conn = nil
		t.mu.Unlock()

		t.respMu.Lock()
		for sid, ch := range t.resp {
			close(ch)
			delete(t.resp, sid)
		}
		t.respMu.Unlock()
	}()

	scanner := bufio.NewScanner(t.reader)
	scanBuf := make([]byte, maxFrameLength)
	scanner.Buffer(scanBuf, maxFrameLength)
	scanner.Split(finsSplitFunc)

	for scanner.Scan() {
		t.mu.Lock()
		closed := t.closed
		t.mu.Unlock()
		if closed {
			return
		}

		// Unwrap: skip FINS init frame header (16 bytes) to get raw FINS response
		frameData := scanner.Bytes()
		if len(frameData) < 16 {
			continue
		}
		rawFINS := frameData[16:]

		resp, err := DecodeResponse(rawFINS)
		if err != nil {
			log.Printf("FINS decode error: %v, data: % X", err, rawFINS)
			continue
		}

		t.respMu.Lock()
		ch, ok := t.resp[resp.Header.SID]
		t.respMu.Unlock()
		if !ok {
			log.Printf("FINS: no waiter for SID %d", resp.Header.SID)
			continue
		}

		select {
		case ch <- resp:
		default:
			log.Printf("FINS: response channel full for SID %d", resp.Header.SID)
		}
		if err := scanner.Err(); err != nil {
			log.Printf("FINS scanner error: %v", err)
		}
	}
}

// finsSplitFunc is a bufio.SplitFunc that splits TCP stream into FINS-wrapped frames.
func finsSplitFunc(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if len(data) < 8 {
		return 0, nil, nil // Need more data
	}

	// Look for FINS marker
	if string(data[0:4]) != finsMagic {
		// Try to resync by searching for next FINS marker
		for i := 1; i < len(data)-3; i++ {
			if string(data[i:i+4]) == finsMagic {
				return i, nil, nil // Skip garbage
			}
		}
		if atEOF {
			return len(data), nil, nil
		}
		return len(data) - 3, nil, nil // Hold last 4 bytes for re-check
	}

	msgLen := binary.BigEndian.Uint32(data[4:8])
	if msgLen == 0 || int(msgLen) > maxFrameLength {
		return 8, nil, nil // Invalid length, skip init header
	}

	totalLen := 16 + int(msgLen) // FINS init header + FINS message
	if len(data) < totalLen {
		return 0, nil, nil // Need more data
	}

	return totalLen, data[:totalLen], nil
}

// startKeepaliveLocked starts the keepalive goroutine. Must hold t.mu.
func (t *TCPTransport) startKeepaliveLocked() {
	if t.stopKeep != nil {
		close(t.stopKeep)
	}
	t.stopKeep = make(chan struct{})
	go func() {
		ticker := time.NewTicker(t.keepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Send status read as keepalive
				header := NewCommandHeader(t.plcNode, 0, t.node, t.unit, 0)
				req := statusReadCommand(header)
				frame := EncodeRequest(req)
				t.Send(frame) // Ignore errors
			case <-t.stopKeep:
				return
			}
		}
	}()
}

// Close closes the connection and cleans up.
func (t *TCPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if t.stopKeep != nil {
		close(t.stopKeep)
		t.stopKeep = nil
	}
	if t.conn != nil {
		err := t.conn.Close()
		t.conn = nil
		return err
	}
	return nil
}
