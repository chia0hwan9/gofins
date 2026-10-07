package gofins

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
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

	// 分帧缓冲上限：合法 FINS/TCP 帧最大 = 16(头) + 10(FINS 头) + 2(命令) + 2(end code)
	// + 999×2(项上限) = 2044。留一倍余量：bufio.Scanner 一旦返回 ErrTooLong 就永久停止，
	// 整条传输作废——不能因为一次坏长度就无声死掉。
	maxFrameLength = 4096

	defaultConnectTimeout  = 5 * time.Second
	defaultResponseTimeout = 10 * time.Second

	// 自动重连默认策略：最多 3 次尝试、首次退避 1s（1s → 2s，上限 10s）。
	// Send 里的重连是"发之前先把连接建好"，最坏阻塞 ≈ 3s；Close() 能打断退避。
	defaultReconnectAttempts = 3
	defaultReconnectBackoff  = time.Second
	maxReconnectBackoff      = 10 * time.Second

	// OS 级 TCP keepalive 默认周期。
	defaultTCPKeepAlivePeriod = 30 * time.Second
)

// wrapFINSTCPFrame 给 payload 加 FINS/TCP 头。payload 是第 8 字节之后的部分：
// 命令(4) + 错误码(4) + [FINS 帧]。
//
// Length 字段 = len(payload)（规范定义：从第 8 字节到帧尾的字节数）。
// 握手帧 payload 12 字节 → Length=12（帧总长 20）；数据帧 payload 8+len(FINS) → Length=8+len(FINS)。
// 这个字段写错（例如只写 FINS 帧长度）时 PLC 会按错误长度切帧，TCP 链路直接不通。
func wrapFINSTCPFrame(payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	copy(frame[0:4], finsMagic)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}

// wrapDataFrame 把裸 FINS 帧打成 FINS/TCP 数据帧（命令=2、错误码=0）。
// 客户端发送与模拟器应答共用，保证两侧分帧一致。
func wrapDataFrame(finsFrame []byte) []byte {
	payload := make([]byte, 8+len(finsFrame))
	binary.BigEndian.PutUint32(payload[0:4], finsCmdData)
	copy(payload[8:], finsFrame)
	return wrapFINSTCPFrame(payload)
}

// TCPTransport implements FINS over TCP with FINS init frame wrapping,
// persistent connection, handshake and SID management.
//
// 自动重连默认**开启**（SetReconnect(false) 关掉）：连接断了以后，下一次 Send 会先按
// 退避策略把连接重新建起来再发这一条。自己管重连的上层（例如网关的通道重连调度）
// 应当关掉它，避免两套重连叠加。
//
// 注意：断在"发送途中"的请求不会被自动重发——写命令可能已经到达 PLC，重发会造成
// 重复写入；这类失败原样返回错误，由调用方决定是否重试（读命令可安全重试）。
type TCPTransport struct {
	addr             string
	node, unit, netw byte // Source FINS address
	plcNode          byte // PLC node (learned from handshake)
	timeout          time.Duration

	keepaliveInterval  time.Duration // 应用层：周期性 FINS status read
	tcpKeepAlive       bool          // OS 级 SO_KEEPALIVE
	tcpKeepAlivePeriod time.Duration

	reconnect         bool
	reconnectAttempts int
	reconnectBackoff  time.Duration

	conn     net.Conn
	reader   *bufio.Reader
	mu       sync.Mutex
	gen      int // 连接世代：旧读循环退出时不得清掉新世代的 conn
	sid      byte
	closed   bool
	closedCh chan struct{}
	stopKeep chan struct{}

	resp   map[byte]chan tcpResult
	respMu sync.Mutex
}

// tcpResult 是响应或连接层错误（FINS/TCP Error Code 非 0 时没有可用的 FINS 响应）。
type tcpResult struct {
	resp Response
	err  error
}

// NewTCPTransport creates a new TCP transport. Call Connect() before using.
// addr: "host:port", srcNode/srcUnit: source FINS address.
func NewTCPTransport(addr string, srcNode, srcUnit, srcNetwork byte) *TCPTransport {
	return &TCPTransport{
		addr:               addr,
		node:               srcNode,
		unit:               srcUnit,
		netw:               srcNetwork,
		timeout:            defaultResponseTimeout,
		tcpKeepAlive:       true,
		tcpKeepAlivePeriod: defaultTCPKeepAlivePeriod,
		reconnect:          true,
		reconnectAttempts:  defaultReconnectAttempts,
		reconnectBackoff:   defaultReconnectBackoff,
		closedCh:           make(chan struct{}),
		resp:               make(map[byte]chan tcpResult),
	}
}

// SetTimeout sets the response timeout.
func (t *TCPTransport) SetTimeout(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timeout = d
}

// SetKeepalive 设置应用层 keepalive 周期：每隔该时长发一次 FINS status read（0 = 关）。
// 它能发现"TCP 还活着但 PLC 不响应"的情况；OS 级探测见 SetTCPKeepAlive。
func (t *TCPTransport) SetKeepalive(interval time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keepaliveInterval = interval
	if t.conn != nil && interval > 0 {
		t.startKeepaliveLocked()
	}
}

// SetTCPKeepAlive 设置 OS 级 TCP keepalive（默认开，周期 30s）。
// 与 SetKeepalive 互补：内核负责探测半开连接，应用层负责探测"连接在但 PLC 不回"。
func (t *TCPTransport) SetTCPKeepAlive(enabled bool, period time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tcpKeepAlive = enabled
	if period > 0 {
		t.tcpKeepAlivePeriod = period
	}
	t.applyTCPKeepAliveLocked()
}

func (t *TCPTransport) applyTCPKeepAliveLocked() {
	tc, ok := t.conn.(*net.TCPConn)
	if !ok {
		return
	}
	if t.tcpKeepAlive {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(t.tcpKeepAlivePeriod)
		return
	}
	_ = tc.SetKeepAlive(false)
}

// SetReconnect 打开/关闭自动重连（默认开）。
func (t *TCPTransport) SetReconnect(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reconnect = enabled
}

// IsConnected 报告当前是否已连接（连接存在且已完成握手）。
// 连接器/网关卡的状态判断用得上：断线后这里是 false，Send 会按需重连。
func (t *TCPTransport) IsConnected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn != nil
}

// SetReconnectPolicy 设置重连策略：最多尝试 attempts 次、首次退避 initialBackoff
// （之后按 2 倍递增，上限 10s）。attempts < 1 视为 1，backoff <= 0 用默认 1s。
func (t *TCPTransport) SetReconnectPolicy(attempts int, initialBackoff time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if attempts < 1 {
		attempts = 1
	}
	if initialBackoff <= 0 {
		initialBackoff = defaultReconnectBackoff
	}
	t.reconnectAttempts = attempts
	t.reconnectBackoff = initialBackoff
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
	return t.dialLocked()
}

// Reconnect 关闭当前连接（若有）并按退避策略重新拨号 + 握手。
// 旧世代上在途的请求会立刻拿到 ConnectionClosedError，而不是各自等到响应超时；
// Close() 会打断退避等待。自动重连关闭时也可以手动调用它。
func (t *TCPTransport) Reconnect() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ConnectionClosedError{}
	}
	attempts, backoff := t.reconnectAttempts, t.reconnectBackoff
	t.mu.Unlock()

	if err := t.dropConn(); err != nil {
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			select {
			case <-time.After(nextBackoff(backoff, attempt)):
			case <-t.closedCh:
				return ConnectionClosedError{}
			}
		}
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return ConnectionClosedError{}
		}
		err := t.dialLocked()
		t.mu.Unlock()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("FINS/TCP 重连失败（已尝试 %d 次）: %w", attempts, lastErr)
}

// nextBackoff 第 attempt 次尝试前的退避：backoff, 2×backoff, …（上限 10s）。
func nextBackoff(backoff time.Duration, attempt int) time.Duration {
	d := backoff
	for i := 2; i < attempt; i++ {
		d *= 2
		if d >= maxReconnectBackoff {
			return maxReconnectBackoff
		}
	}
	if d > maxReconnectBackoff {
		return maxReconnectBackoff
	}
	return d
}

// dropConn 断掉当前连接，并让该世代在途的等待者立刻失败。
// gen 递增后旧读循环的收尾不会清掉新世代的 conn。
func (t *TCPTransport) dropConn() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ConnectionClosedError{}
	}
	conn := t.conn
	t.conn = nil
	t.reader = nil
	t.gen++
	if t.stopKeep != nil {
		close(t.stopKeep)
		t.stopKeep = nil
	}
	t.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	t.failAllWaiters(ConnectionClosedError{})
	return nil
}

// dialLocked 拨号 + 握手 + 启动本世代读循环。必须持有 t.mu。
func (t *TCPTransport) dialLocked() error {
	dialer := net.Dialer{Timeout: defaultConnectTimeout}
	conn, err := dialer.Dial("tcp", t.addr)
	if err != nil {
		return fmt.Errorf("TCP dial failed: %w", err)
	}

	t.conn = conn
	t.reader = bufio.NewReader(conn)
	t.applyTCPKeepAliveLocked()

	if err := t.handshakeLocked(); err != nil {
		_ = conn.Close()
		t.conn = nil
		t.reader = nil
		return fmt.Errorf("FINS handshake failed: %w", err)
	}

	t.gen++
	gen := t.gen
	reader := t.reader // 随连接一起传给读循环：不能在 goroutine 里再读 t.reader（重连会把它置 nil）
	go t.listenLoop(gen, reader)

	if t.keepaliveInterval > 0 {
		t.startKeepaliveLocked()
	}
	return nil
}

// handshakeLocked performs the FINS TCP connection handshake.
// Must be called with t.mu held.
func (t *TCPTransport) handshakeLocked() error {
	// Handshake frame: FINS/TCP header + 4-byte client node address (0 = auto-assign)
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], finsCmdHandshake) // Command = 0
	binary.BigEndian.PutUint32(payload[4:8], 0)                // Error code = 0
	// payload[8:12] = client node address, 0 = auto-assign
	frame := wrapFINSTCPFrame(payload) // 20 bytes

	if _, err := t.conn.Write(frame); err != nil {
		return fmt.Errorf("handshake write: %w", err)
	}

	// Response: 24 bytes = FINS/TCP header + client node + server node。
	// 必须 io.ReadFull：单次 Read 可能只拿到 TCP 分段的一部分，会把节点号读成 0
	// 并把剩余字节留在缓冲里让后续分帧错位。
	resp := make([]byte, 24)
	if _, err := io.ReadFull(t.reader, resp); err != nil {
		return fmt.Errorf("handshake read: %w", err)
	}

	// Verify FINS marker
	if string(resp[0:4]) != finsMagic {
		return ProtocolError{Msg: "invalid handshake response: missing FINS marker"}
	}
	if cmd := binary.BigEndian.Uint32(resp[8:12]); cmd != finsCmdHandshakeRsp {
		return ProtocolError{Msg: fmt.Sprintf("invalid handshake response: command %d, want %d", cmd, finsCmdHandshakeRsp)}
	}
	if code := binary.BigEndian.Uint32(resp[12:16]); code != 0 {
		return TCPError{Code: code, Stage: "handshake"}
	}

	// Extract assigned node numbers
	clientNode := resp[19] // Our node assigned by PLC
	serverNode := resp[23] // PLC's node

	t.node = clientNode
	t.plcNode = serverNode

	return nil
}

// ensureConnected 发送前确认连接可用：连接已断且自动重连开着时先重连。
func (t *TCPTransport) ensureConnected() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ConnectionClosedError{}
	}
	connected, auto := t.conn != nil, t.reconnect
	t.mu.Unlock()

	if connected {
		return nil
	}
	if !auto {
		return NotConnectedError{}
	}
	return t.Reconnect()
}

// Send sends a raw FINS frame (no TCP wrapping) and returns the raw response.
// The TCP wrapping (FINS init frame) is added/removed internally.
//
// 自动重连开着时，连接已断会先重连再发这一条；断在发送途中的请求不自动重发（见类型注释）。
func (t *TCPTransport) Send(frame []byte) ([]byte, error) {
	if err := t.ensureConnected(); err != nil {
		return nil, err
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ConnectionClosedError{}
	}
	conn := t.conn
	if conn == nil {
		t.mu.Unlock()
		return nil, NotConnectedError{}
	}
	timeout := t.timeout

	// Get next SID and inject into frame header byte 9 (SID field)
	sid := t.nextSIDLocked()
	// frame[9] is the SID byte in the 10-byte FINS header
	if len(frame) >= 10 {
		frame[9] = sid
	}

	// Build TCP-wrapped frame: FINS/TCP header (16) + raw FINS frame。
	// payload = 命令(4) + 错误码(4) + FINS 帧 → Length = 8 + len(frame)。
	wrappedFrame := wrapDataFrame(frame)

	// Create response channel before sending
	respCh := make(chan tcpResult, 1)
	t.respMu.Lock()
	t.resp[sid] = respCh
	t.respMu.Unlock()

	// Cleanup after
	defer func() {
		t.respMu.Lock()
		delete(t.resp, sid)
		t.respMu.Unlock()
	}()

	if _, err := conn.Write(wrappedFrame); err != nil {
		t.mu.Unlock()
		return nil, fmt.Errorf("TCP write: %w", err)
	}
	t.mu.Unlock()

	// Wait for response
	select {
	case r, ok := <-respCh:
		if !ok {
			return nil, ConnectionClosedError{}
		}
		if r.err != nil {
			return nil, r.err
		}
		return EncodeResponse(r.resp), nil
	case <-time.After(timeout):
		return nil, ResponseTimeoutError{Duration: timeout}
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
// gen 是本次连接的世代号：收尾时只有仍属当前世代才清理 conn 与在途请求，
// 否则会把重连后的新连接一起清掉。reader 是本世代自己的读缓冲（重连不会影响它）。
func (t *TCPTransport) listenLoop(gen int, reader *bufio.Reader) {
	defer func() {
		t.mu.Lock()
		current := t.gen == gen
		if current {
			t.conn = nil
			t.reader = nil
		}
		t.mu.Unlock()

		if current {
			// 本世代断了：让在途请求立刻失败，而不是各自等到响应超时
			t.failAllWaiters(ConnectionClosedError{})
		}
	}()

	scanner := bufio.NewScanner(reader)
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

		// FINS/TCP 头第 12-15 字节是连接层 Error Code（与 FINS end code 不是一套）：
		// 非 0 时说明连接/节点层就失败了，直接作为错误投递给等待方——否则调用方会一直
		// 等到响应超时，看不出"连接数占满/节点地址冲突"这类真原因。
		if code := binary.BigEndian.Uint32(frameData[12:16]); code != 0 {
			t.deliverError(frameData[16:], TCPError{Code: code, Stage: "data"})
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
		case ch <- tcpResult{resp: resp}:
		default:
			log.Printf("FINS: response channel full for SID %d", resp.Header.SID)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("FINS scanner error: %v", err)
	}
}

// failAllWaiters 让所有在途等待者立刻拿到错误（断线/重连/关闭时用）。
func (t *TCPTransport) failAllWaiters(err error) {
	t.respMu.Lock()
	defer t.respMu.Unlock()
	for sid, ch := range t.resp {
		select {
		case ch <- tcpResult{err: err}:
		default:
		}
		delete(t.resp, sid)
	}
}

// deliverError 把连接层错误投给等待方：先按 FINS 头里的 SID 精确匹配，
// 帧太短/ SID 不认识时投给所有等待方（宁可让每个调用方立刻拿到错误，也不要各自超时）。
func (t *TCPTransport) deliverError(finsFrame []byte, err error) {
	t.respMu.Lock()
	defer t.respMu.Unlock()

	if len(finsFrame) >= 10 {
		if ch, ok := t.resp[finsFrame[9]]; ok {
			select {
			case ch <- tcpResult{err: err}:
			default:
			}
			return
		}
	}
	for _, ch := range t.resp {
		select {
		case ch <- tcpResult{err: err}:
		default:
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

	// Length = 从第 8 字节到帧尾，所以整帧 = 8 + Length（不是 16 + Length）
	totalLen := 8 + int(msgLen)
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
				// Send status read as keepalive（SID 由 Send 用传输自己的计数器覆盖）。
				// 节点号在重连握手时会被改写，取用要持锁。
				t.mu.Lock()
				header := NewCommandHeader(t.plcNode, 0, t.node, t.unit, 0)
				t.mu.Unlock()
				command, data := statusReadCommand()
				t.Send(EncodeRequest(Request{Header: header, Command: command, Data: data})) // Ignore errors
			case <-t.stopKeep:
				return
			}
		}
	}()
}

// Close closes the connection and cleans up. 它是终态：之后再 Send/Connect 都返回
// ConnectionClosedError（不会悄悄重连）；自动重连的退避等待会被立刻打断。
func (t *TCPTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	close(t.closedCh)
	if t.stopKeep != nil {
		close(t.stopKeep)
		t.stopKeep = nil
	}
	conn := t.conn
	t.conn = nil
	t.reader = nil
	t.gen++ // 让读循环的收尾不再清理
	t.mu.Unlock()

	t.failAllWaiters(ConnectionClosedError{})
	if conn != nil {
		return conn.Close()
	}
	return nil
}
