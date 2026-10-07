package gofins

import (
	"log"
	"net"
	"sync"
	"time"
)

// UDPTransport implements FINS over UDP datagrams.
// No handshake, no TCP wrapping — raw FINS frames on the wire.
type UDPTransport struct {
	conn    *net.UDPConn
	addr    *net.UDPAddr
	timeout time.Duration
	closed  bool

	mu   sync.Mutex
	resp map[byte]chan Response // Response dispatch by SID
}

// NewUDPTransport creates a UDP transport. Call Connect() before sending.
func NewUDPTransport(addr string) (*UDPTransport, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	return &UDPTransport{
		addr:    udpAddr,
		timeout: 5 * time.Second,
		resp:    make(map[byte]chan Response),
	}, nil
}

// Connect establishes the UDP socket.
func (t *UDPTransport) Connect() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ConnectionClosedError{}
	}
	if t.conn != nil {
		return nil
	}
	conn, err := net.DialUDP("udp", nil, t.addr)
	if err != nil {
		return err
	}
	t.conn = conn
	go t.listenLoop()
	return nil
}

// SetTimeout sets the read/write deadline.
func (t *UDPTransport) SetTimeout(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timeout = d
}

// IsConnected 报告 socket 是否已建立（UDP 无连接语义，仅表示 socket 可用）。
func (t *UDPTransport) IsConnected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn != nil && !t.closed
}

// Send sends a raw FINS frame and waits for the response.
func (t *UDPTransport) Send(frame []byte) ([]byte, error) {
	t.mu.Lock()
	// Close() 是终态：不能在 Close 之后又悄悄把 socket 建回来（否则 ListenLoop
	// 会多起一份，且调用方以为已经断开了）。
	if t.closed {
		t.mu.Unlock()
		return nil, ConnectionClosedError{}
	}
	if t.conn == nil {
		t.mu.Unlock()
		if err := t.Connect(); err != nil {
			return nil, err
		}
		t.mu.Lock()
	}
	conn := t.conn
	timeout := t.timeout
	t.mu.Unlock()

	if conn == nil {
		return nil, NotConnectedError{}
	}

	// Extract SID from frame header (byte 9) for response matching
	var sid byte
	if len(frame) >= 10 {
		sid = frame[9]
	}

	respCh := make(chan Response, 1)
	t.mu.Lock()
	t.resp[sid] = respCh
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.resp, sid)
		t.mu.Unlock()
	}()

	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	if _, err := conn.Write(frame); err != nil {
		return nil, err
	}

	select {
	case resp, ok := <-respCh:
		if !ok {
			return nil, ConnectionClosedError{}
		}
		return EncodeResponse(resp), nil
	case <-time.After(timeout):
		return nil, ResponseTimeoutError{Duration: timeout}
	}
}

// listenLoop reads UDP datagrams and dispatches responses by SID.
func (t *UDPTransport) listenLoop() {
	buf := make([]byte, 65535)
	for {
		t.mu.Lock()
		conn := t.conn
		closed := t.closed
		t.mu.Unlock()

		if closed || conn == nil {
			return
		}

		n, err := conn.Read(buf)
		if err != nil {
			t.mu.Lock()
			if !t.closed {
				log.Printf("UDP read error: %v", err)
			}
			t.mu.Unlock()
			return
		}

		if n < 14 {
			continue // Too short for a FINS response
		}

		// 拷贝出本次数据报：resp.Data 会指向它，而下一个 datagram 会覆盖共享的 buf
		// （等待方还在 EncodeResponse 时会读到被覆盖的内容）。
		pkt := make([]byte, n)
		copy(pkt, buf[:n])

		resp, err := DecodeResponse(pkt)
		if err != nil {
			log.Printf("UDP decode error: %v", err)
			continue
		}

		t.mu.Lock()
		ch, ok := t.resp[resp.Header.SID]
		t.mu.Unlock()
		if !ok {
			log.Printf("UDP: no waiter for SID %d", resp.Header.SID)
			continue
		}

		select {
		case ch <- resp:
		default:
			log.Printf("UDP: response channel full for SID %d", resp.Header.SID)
		}
	}
}

// Close closes the UDP socket.
func (t *UDPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if t.conn != nil {
		err := t.conn.Close()
		t.conn = nil
		return err
	}
	return nil
}
