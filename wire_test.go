package gofins

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"
)

// captureTransport 记录最后一次发出的原始 FINS 帧（不带传输包装），用于断言线格式。
type captureTransport struct {
	last  []byte
	reply []byte
	err   error
}

func (m *captureTransport) Send(frame []byte) ([]byte, error) {
	m.last = append([]byte(nil), frame...)
	if m.err != nil {
		return nil, m.err
	}
	return m.reply, nil
}
func (m *captureTransport) Close() error               { return nil }
func (m *captureTransport) SetTimeout(d time.Duration) {}

func newCaptureClient() (*Client, *captureTransport) {
	tr := &captureTransport{reply: buildMockResponse(CmdMemoryRead, EndCodeNormal, nil)}
	return NewClient(tr, 2, 0, 1, 0, 0), tr
}

// ---------- 线格式（golden bytes） ----------
//
// 这些字节是 FINS 命令的真实线格式，改动命令构造/地址编码时必须一起改，
// 并对照上游实现（folke99/gofins、l1va/gofins、node-omron-fins）确认：
//   数据段 = area(1) + address(2) + bit(1) + itemCount(2)[ + 位数据 ]
// 参考：https://github.com/folke99/gofins（README 声明在 CJ2M-CPU32/CJ2H-CPU64 上验证）

func goldenHeader() Header { return NewCommandHeader(1, 0, 2, 0, 3) }

// goldenRequest 用固定 FINS 头 + 命令/数据段拼请求，便于断言完整帧。
func goldenRequest(command uint16, data []byte) Request {
	return Request{Header: goldenHeader(), Command: command, Data: data}
}

func TestWireFormatWordRead(t *testing.T) {
	command, data := readCommand(NewWordAddress(MemAreaDM, 100), 10)
	frame := EncodeRequest(goldenRequest(command, data))

	wantFrame := []byte{
		0x80, 0x00, 0x02, 0x00, 0x01, 0x00, 0x00, 0x02, 0x00, 0x03, // FINS header
		0x01, 0x01, // MEMORY AREA READ
		0x82,       // DM (word)
		0x00, 0x64, // address 100
		0x00,       // bit offset
		0x00, 0x0A, // count 10
	}
	if !bytes.Equal(frame, wantFrame) {
		t.Errorf("DM100 x10 读:\n got % X\nwant % X", frame, wantFrame)
	}
}

func TestWireFormatWordWrite(t *testing.T) {
	command, data := writeCommand(NewWordAddress(MemAreaDM, 100), WordsToBytes([]uint16{0x1234}))
	frame := EncodeRequest(goldenRequest(command, data))

	wantData := []byte{0x82, 0x00, 0x64, 0x00, 0x00, 0x01, 0x12, 0x34}
	if got := frame[12:]; !bytes.Equal(got, wantData) {
		t.Errorf("DM100 写 1 字数据段:\n got % X\nwant % X", got, wantData)
	}
}

// 位读写与字读写共用 0101/0102，itemCount 固定 2 字节：
// 之前写成 1 字节会让真实 PLC 判为命令过短，自带模拟器也会回 0x0401。
func TestWireFormatBitRead(t *testing.T) {
	command, data := readBitsCommand(MemAreaCIOBit, 0, 0, 8)
	frame := EncodeRequest(goldenRequest(command, data))

	wantData := []byte{
		0x30,       // CIO (bit)
		0x00, 0x00, // word address 0
		0x00,       // bit 0
		0x00, 0x08, // count 8 bits（2 字节）
	}
	if got := frame[12:]; !bytes.Equal(got, wantData) {
		t.Errorf("CIO0.00 x8 位读数据段:\n got % X\nwant % X", got, wantData)
	}
}

// 位写：itemCount 是位数，位数据「1 位 1 字节」。
// 传 len(bytes)*8 会让 PLC 多写后面几位（SetBit 连带清掉同字里后续 7 位）。
func TestWireFormatBitWrite(t *testing.T) {
	command, data := writeBitsCommand(MemAreaHRBit, 100, 3, 1, []byte{0x01})
	frame := EncodeRequest(goldenRequest(command, data))

	wantData := []byte{
		0x32,       // HR (bit)
		0x00, 0x64, // word address 100
		0x03,       // bit 3
		0x00, 0x01, // count 1 bit
		0x01, // 位 3 = ON
	}
	if got := frame[12:]; !bytes.Equal(got, wantData) {
		t.Errorf("HR100.3 写 1 位数据段:\n got % X\nwant % X", got, wantData)
	}
}

func TestClientWriteBitsWire(t *testing.T) {
	c, tr := newCaptureClient()
	if err := c.WriteBits(MemAreaHRBit, 100, 3, []bool{true, false, true}); err != nil {
		t.Fatalf("WriteBits: %v", err)
	}

	wantData := []byte{
		0x32, 0x00, 0x64, 0x03,
		0x00, 0x03, // count = 3 位（不是补齐后的 1 字节×8）
		0x01, 0x00, 0x01, // 1 位 1 字节
	}
	if got := tr.last[12:]; !bytes.Equal(got, wantData) {
		t.Errorf("WriteBits 数据段:\n got % X\nwant % X", got, wantData)
	}

	// SetBit 是 1 位写：count 必须是 1（旧实现写 8，会连带清掉后续 7 位）
	c2, tr2 := newCaptureClient()
	if err := c2.SetBit(MemAreaCIOBit, 0, 3); err != nil {
		t.Fatalf("SetBit: %v", err)
	}
	if got := tr2.last[12:]; !bytes.Equal(got, []byte{0x30, 0x00, 0x00, 0x03, 0x00, 0x01, 0x01}) {
		t.Errorf("SetBit 数据段:\n got % X\nwant 30 00 00 03 00 01 01", got)
	}
}

// 256 位边界：count 用 2 字节，不能像旧的 1 字节写法被截断成 0。
func TestWireFormatBitWrite256(t *testing.T) {
	values := make([]bool, 256)
	for i := range values {
		values[i] = true
	}
	c, tr := newCaptureClient()
	if err := c.WriteBits(MemAreaHRBit, 0, 0, values); err != nil {
		t.Fatalf("WriteBits 256: %v", err)
	}
	data := tr.last[12:]
	if len(data) != 6+256 {
		t.Fatalf("数据段长度 = %d，期望 262（1 位 1 字节）", len(data))
	}
	if !bytes.Equal(data[4:6], []byte{0x01, 0x00}) {
		t.Errorf("256 位的 count 字段 = % X，期望 01 00", data[4:6])
	}
}

// ---------- FINS/TCP 帧 ----------

// Length 字段 = 从第 8 字节到帧尾（规范定义），数据帧 = 8 + len(FINS 帧)。
// 旧实现只写 len(FINS 帧)，比规范少 8，真实 PLC 会按错误长度切帧。
func TestFINSTCPFrameLength(t *testing.T) {
	command, cmdData := statusReadCommand()
	finsFrame := EncodeRequest(goldenRequest(command, cmdData)) // 10 + 2 = 12 字节
	payload := make([]byte, 8+len(finsFrame))
	binary.BigEndian.PutUint32(payload[0:4], finsCmdData)
	copy(payload[8:], finsFrame)
	frame := wrapFINSTCPFrame(payload)

	if got, want := len(frame), 16+len(finsFrame); got != want {
		t.Fatalf("帧总长 = %d，期望 %d", got, want)
	}
	if got := binary.BigEndian.Uint32(frame[4:8]); got != uint32(8+len(finsFrame)) {
		t.Errorf("Length = %d，期望 %d（8 + FINS 帧长）", got, 8+len(finsFrame))
	}
	if got := binary.BigEndian.Uint32(frame[8:12]); got != finsCmdData {
		t.Errorf("Command = %d，期望 %d", got, finsCmdData)
	}
	if !bytes.Equal(frame[16:], finsFrame) {
		t.Errorf("FINS 帧位置错误：% X", frame[16:])
	}

	// 握手帧：payload 12 字节 → 帧 20 字节、Length 12
	hs := wrapFINSTCPFrame(make([]byte, 12))
	if len(hs) != 20 || binary.BigEndian.Uint32(hs[4:8]) != 12 {
		t.Errorf("握手帧 = % X（长度 %d，Length %d），期望 20 字节 / Length 12",
			hs, len(hs), binary.BigEndian.Uint32(hs[4:8]))
	}
}

// 发送方的 Length 与 finsSplitFunc 的切帧必须一致，且能一次切出整帧。
func TestFINSTCPFrameRoundTrip(t *testing.T) {
	command, cmdData := statusReadCommand()
	inner := EncodeRequest(goldenRequest(command, cmdData))
	payload := make([]byte, 8+len(inner))
	binary.BigEndian.PutUint32(payload[0:4], finsCmdData)
	copy(payload[8:], inner)
	f1 := wrapFINSTCPFrame(payload)
	f2 := wrapFINSTCPFrame(payload)

	sc := bufio.NewScanner(bytes.NewReader(append(append([]byte(nil), f1...), f2...)))
	sc.Buffer(make([]byte, maxFrameLength), maxFrameLength)
	sc.Split(finsSplitFunc)

	var got [][]byte
	for sc.Scan() {
		got = append(got, append([]byte(nil), sc.Bytes()...))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("切出 %d 帧，期望 2", len(got))
	}
	for i, f := range got {
		if !bytes.Equal(f, f1) {
			t.Errorf("第 %d 帧:\n got % X\nwant % X", i+1, f, f1)
		}
	}
}

// ---------- 并发：模拟器/传输不得串包 ----------

// 多个客户端并发打同一个模拟器：每个客户端写自己的值再读回。
// 读循环与 handleRequest 共用读缓冲而不 copy 时，这里会串包（-race 下必报）。
func TestServerConcurrentClients(t *testing.T) {
	server := NewServer()
	go func() { _ = server.ListenAndServe("127.0.0.1:0") }()
	addr := ""
	for range 100 {
		time.Sleep(10 * time.Millisecond)
		if a := server.Addr(); a != nil {
			addr = a.String()
			break
		}
	}
	if addr == "" {
		t.Fatal("模拟器未启动")
	}
	defer server.Stop()

	const clients = 8
	const rounds = 20

	var wg sync.WaitGroup
	errCh := make(chan error, clients)
	for i := range clients {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tr, err := NewUDPTransport(addr)
			if err != nil {
				errCh <- fmt.Errorf("client %d: %w", id, err)
				return
			}
			defer tr.Close()
			c := NewClient(tr, byte(id+1), 0, 1, 0, 0)

			addrBase := uint16(100 + id*100)
			for r := range rounds {
				values := []uint16{uint16(id), uint16(r), 0xABCD}
				if err := c.WriteWords(MemAreaDM, addrBase, values); err != nil {
					errCh <- fmt.Errorf("client %d 写: %w", id, err)
					return
				}
				got, err := c.ReadWords(MemAreaDM, addrBase, 3)
				if err != nil {
					errCh <- fmt.Errorf("client %d 读: %w", id, err)
					return
				}
				for k, want := range values {
					if got[k] != want {
						errCh <- fmt.Errorf("client %d 第 %d 轮: got[%d]=%#x want %#x（串包）", id, r, k, got[k], want)
						return
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
