package gofins

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ---------- 并发安全 ----------

// 一个 Client 被多个 goroutine 共用（内部串行化一问一答 + SID 分配）。
// 旧实现没有锁：-race 会报 c.sid 竞争，SID 撞车时还会把响应投给错误的请求。
func TestClientConcurrentUse(t *testing.T) {
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

	tr, err := NewUDPTransport(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	client := NewClient(tr, 2, 0, 1, 0, 0) // 共享同一个 Client

	const workers = 8
	const rounds = 30

	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			base := uint16(1000 + id*10)
			for r := range rounds {
				values := []uint16{uint16(id), uint16(r), uint16(id*100 + r)}
				if err := client.WriteWords(MemAreaDM, base, values); err != nil {
					errCh <- fmt.Errorf("worker %d 写: %w", id, err)
					return
				}
				got, err := client.ReadWords(MemAreaDM, base, 3)
				if err != nil {
					errCh <- fmt.Errorf("worker %d 读: %w", id, err)
					return
				}
				for i, want := range values {
					if got[i] != want {
						errCh <- fmt.Errorf("worker %d 第 %d 轮: got[%d]=%#x want %#x", id, r, i, got[i], want)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// ---------- 短响应 / 边界 ----------

func TestReadWordsShortResponse(t *testing.T) {
	cmd, _ := readCommand(NewWordAddress(MemAreaDM, 100), 2)
	tr := &captureTransport{reply: buildMockResponse(cmd, EndCodeNormal, []byte{0x12, 0x34})} // 只回 1 个字
	c := NewClient(tr, 2, 0, 1, 0, 0)

	words, err := c.ReadWords(MemAreaDM, 100, 2)
	if err == nil {
		t.Fatalf("短响应应报错，实际返回 %v（旧实现静默补 0）", words)
	}
	var perr ProtocolError
	if !errors.As(err, &perr) {
		t.Errorf("期望 ProtocolError，实际 %T: %v", err, err)
	}
}

func TestReadBitsShortResponse(t *testing.T) {
	cmd, _ := readBitsCommand(MemAreaCIOBit, 0, 0, 8)
	tr := &captureTransport{reply: buildMockResponse(cmd, EndCodeNormal, []byte{0x01, 0x00})} // 只回 2 位
	c := NewClient(tr, 2, 0, 1, 0, 0)

	if _, err := c.ReadBits(MemAreaCIOBit, 0, 0, 8); err == nil {
		t.Error("位读短响应应报错")
	}
}

func TestReadBytesShortResponse(t *testing.T) {
	cmd, _ := readCommand(NewWordAddress(MemAreaDM, 100), 2)
	tr := &captureTransport{reply: buildMockResponse(cmd, EndCodeNormal, []byte{0x12})} // 只回 1 字节
	c := NewClient(tr, 2, 0, 1, 0, 0)

	if _, err := c.ReadBytes(MemAreaDM, 100, 4); err == nil {
		t.Error("字节读短响应应报错")
	}
}

func TestCountLimits(t *testing.T) {
	c, _ := newCaptureClient()

	if _, err := c.ReadWords(MemAreaDM, 0, MaxItemsPerCommand+1); err == nil {
		t.Error("超过单命令字上限应报错")
	}
	if _, err := c.ReadWords(MemAreaDM, 0, 0); err == nil {
		t.Error("count=0 应报错")
	}
	if err := c.WriteWords(MemAreaDM, 0, nil); err == nil {
		t.Error("空写应报错")
	}
	if err := c.WriteBits(MemAreaCIOBit, 0, 0, make([]bool, MaxBitsPerCommand+1)); err == nil {
		t.Error("超过位上限应报错")
	}
	if err := c.WriteBits(MemAreaCIOBit, 0, 16, []bool{true}); err == nil {
		t.Error("startBit>15 应报错")
	}
}

// ---------- 字节序（写路径以前恒为大端） ----------

func TestByteOrderAppliesToWrites(t *testing.T) {
	tr := &captureTransport{}
	c := NewClient(tr, 2, 0, 1, 0, 0)
	c.SetByteOrder(binary.LittleEndian)

	cmd, _ := writeCommand(NewWordAddress(MemAreaDM, 100), nil)
	tr.reply = buildMockResponse(cmd, EndCodeNormal, nil)

	if err := c.WriteWords(MemAreaDM, 100, []uint16{0x1234}); err != nil {
		t.Fatalf("WriteWords: %v", err)
	}
	if got := tr.last[12+6:]; string(got) != "\x34\x12" {
		t.Errorf("小端写的数据 = % X，期望 34 12", got)
	}

	// 读回也要按同一字节序解码
	tr.reply = buildMockResponse(cmd, EndCodeNormal, []byte{0x34, 0x12})
	words, err := c.ReadWords(MemAreaDM, 100, 1)
	if err != nil {
		t.Fatalf("ReadWords: %v", err)
	}
	if words[0] != 0x1234 {
		t.Errorf("小端读 = %#x，期望 0x1234", words[0])
	}
}

// ---------- 时钟 BCD 校验 ----------

func TestReadClockBadBCD(t *testing.T) {
	cmd, _ := clockReadCommand()
	// 月份 0x1A：BCD 非法（旧实现用 `_` 吞掉错误，返回 0 月）
	tr := &captureTransport{reply: buildMockResponse(cmd, EndCodeNormal, []byte{0x26, 0x1A, 0x05, 0x0A, 0x0B, 0x0C, 0x01})}
	c := NewClient(tr, 2, 0, 1, 0, 0)

	if _, err := c.ReadClock(); err == nil {
		t.Error("非法 BCD 应报错")
	}
}

// ---------- UDP：Close 是终态 ----------

func TestUDPCloseStaysClosed(t *testing.T) {
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

	tr, err := NewUDPTransport(addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}

	cmd, data := statusReadCommand()
	frame := EncodeRequest(goldenRequest(cmd, data))
	if _, err := tr.Send(frame); !errors.As(err, &ConnectionClosedError{}) {
		t.Errorf("Close 后 Send 应返回 ConnectionClosedError，实际 %T: %v", err, err)
	}
	if err := tr.Connect(); !errors.As(err, &ConnectionClosedError{}) {
		t.Errorf("Close 后 Connect 应返回 ConnectionClosedError，实际 %T: %v", err, err)
	}
}
