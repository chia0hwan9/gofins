package gofins

import (
	"testing"
	"time"
)

// startSim 起一个模拟器并返回 addr + 清理函数。
func startSim(t *testing.T) (string, *Server) {
	t.Helper()
	server := NewServer()
	go func() { _ = server.ListenAndServe("127.0.0.1:0") }()
	for range 100 {
		time.Sleep(10 * time.Millisecond)
		if a := server.Addr(); a != nil {
			t.Cleanup(func() { _ = server.Stop() })
			return a.String(), server
		}
	}
	t.Fatal("模拟器未启动")
	return "", nil
}

func newSimClient(t *testing.T, addr string) *Client {
	t.Helper()
	tr, err := NewUDPTransport(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return NewClient(tr, 2, 0, 1, 0, 0)
}

// 位区端到端（模拟器现在建模位区）：
// SetBit 只能改动目标位——旧实现发 itemCount=8，会把同字里后续 7 位一起写 0。
func TestBitOpsAgainstSimulator(t *testing.T) {
	addr, _ := startSim(t)
	c := newSimClient(t, addr)

	const word = 10
	// 先把该字整字置 1（写 16 位），作为"邻居位"哨兵
	all := make([]bool, 16)
	for i := range all {
		all[i] = true
	}
	if err := c.WriteBits(MemAreaCIOBit, word, 0, all); err != nil {
		t.Fatalf("写整字 16 位: %v", err)
	}

	// 只清 bit 3
	if err := c.ResetBit(MemAreaCIOBit, word, 3); err != nil {
		t.Fatalf("ResetBit: %v", err)
	}
	bits, err := c.ReadBits(MemAreaCIOBit, word, 0, 16)
	if err != nil {
		t.Fatalf("ReadBits: %v", err)
	}
	for i, b := range bits {
		want := i != 3
		if b != want {
			t.Fatalf("ResetBit 后 bit %d = %v，期望 %v（邻居位被改动）", i, b, want)
		}
	}

	// ToggleBit 翻回来
	if err := c.ToggleBit(MemAreaCIOBit, word, 3); err != nil {
		t.Fatalf("ToggleBit: %v", err)
	}
	if bits, err = c.ReadBits(MemAreaCIOBit, word, 3, 1); err != nil || !bits[0] {
		t.Fatalf("ToggleBit 后 bit3 = %v (err=%v)，期望 true", bits, err)
	}

	// 单点读：读 bit 2 不能受影响
	if bits, err = c.ReadBits(MemAreaCIOBit, word, 2, 1); err != nil || !bits[0] {
		t.Fatalf("bit2 = %v (err=%v)，期望 true", bits, err)
	}
}

// 跨字位读写：从 bit 14 起读/写 4 位要跨到下一字的 bit 0-1。
func TestBitOpsCrossWord(t *testing.T) {
	addr, _ := startSim(t)
	c := newSimClient(t, addr)

	zero := make([]bool, 16)
	if err := c.WriteBits(MemAreaCIOBit, 20, 0, zero); err != nil {
		t.Fatalf("清字 20: %v", err)
	}
	if err := c.WriteBits(MemAreaCIOBit, 21, 0, zero); err != nil {
		t.Fatalf("清字 21: %v", err)
	}

	// 从 (20, bit14) 起写 4 位：true,false,true,false → 20.14, 20.15, 21.0, 21.1
	if err := c.WriteBits(MemAreaCIOBit, 20, 14, []bool{true, false, true, false}); err != nil {
		t.Fatalf("跨字写: %v", err)
	}
	got, err := c.ReadBits(MemAreaCIOBit, 20, 14, 4)
	if err != nil {
		t.Fatalf("跨字读: %v", err)
	}
	want := []bool{true, false, true, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("跨字第 %d 位 = %v，期望 %v", i, got[i], want[i])
		}
	}

	// 邻居检查：21.2 起仍是 0
	rest, err := c.ReadBits(MemAreaCIOBit, 21, 2, 6)
	if err != nil {
		t.Fatalf("邻居读: %v", err)
	}
	for i, b := range rest {
		if b {
			t.Fatalf("21.%d 被误写为 1", i+2)
		}
	}
}

// 位区与字区互不干扰：字区写 DM 不应影响位区读数。
func TestBitAreaIndependentOfWordArea(t *testing.T) {
	addr, _ := startSim(t)
	c := newSimClient(t, addr)

	if err := c.WriteWords(MemAreaDM, 40, []uint16{0xFFFF, 0xFFFF}); err != nil {
		t.Fatalf("写 DM: %v", err)
	}
	bits, err := c.ReadBits(MemAreaCIOBit, 40, 0, 8)
	if err != nil {
		t.Fatalf("读 CIO 位: %v", err)
	}
	for i, b := range bits {
		if b {
			t.Fatalf("CIO 位 %d 被 DM 写入影响", i)
		}
	}
}
