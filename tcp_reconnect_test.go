package gofins

import (
	"errors"
	"net"
	"testing"
	"time"
)

// freeTCPAddr 占一个端口再释放，供"模拟器停掉再在同端口重启"的用例复用。
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// startTCPSim 起一个 TCP 模拟器（就绪后返回）。
func startTCPSim(t *testing.T, addr string) *Server {
	t.Helper()
	srv := NewServer()
	go func() { _ = srv.ListenAndServeTCP(addr) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Addr() != nil {
			return srv
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("TCP 模拟器未启动")
	return nil
}

func waitDisconnected(t *testing.T, tr *TCPTransport) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !tr.IsConnected() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("传输未在 3s 内变为断开（读循环应已收尾）")
}

// 完整跑一遍 FINS/TCP 会话（用自带 TCP 模拟器）：字/位读写、时钟、状态、越界 end code。
func TestTCPIntegrationWithSimulator(t *testing.T) {
	srv := startTCPSim(t, freeTCPAddr(t))
	defer srv.Stop()

	tr := NewTCPTransport(srv.Addr().String(), 0, 0, 0)
	defer tr.Close()
	if err := tr.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	c := NewClient(tr, 11, 0, 1, 0, 0)

	if err := c.WriteWords(MemAreaDM, 100, []uint16{0x1234, 0x5678}); err != nil {
		t.Fatalf("WriteWords: %v", err)
	}
	words, err := c.ReadWords(MemAreaDM, 100, 2)
	if err != nil {
		t.Fatalf("ReadWords: %v", err)
	}
	if words[0] != 0x1234 || words[1] != 0x5678 {
		t.Errorf("TCP 读回 = %#v", words)
	}

	if err := c.SetBit(MemAreaCIOBit, 10, 3); err != nil {
		t.Fatalf("SetBit: %v", err)
	}
	bits, err := c.ReadBits(MemAreaCIOBit, 10, 3, 1)
	if err != nil || !bits[0] {
		t.Fatalf("TCP 位读 = %v (err=%v)", bits, err)
	}

	if _, err := c.ReadClock(); err != nil {
		t.Errorf("ReadClock: %v", err)
	}
	st, err := c.Status()
	if err != nil {
		t.Errorf("Status: %v", err)
	} else if !st.IsRunning() {
		t.Errorf("Status = %+v", st)
	}

	_, err = c.ReadWords(MemAreaDM, 32767, 2)
	var ecErr EndCodeError
	if !errors.As(err, &ecErr) || ecErr.Code != EndCodeAddressExceeded {
		t.Errorf("越界读应回 0x%04X，实际 %T: %v", EndCodeAddressExceeded, err, err)
	}
}

// 自动重连（默认开）：模拟器重启后，下一次读自动恢复。
func TestTCPAutoReconnect(t *testing.T) {
	addr := freeTCPAddr(t)
	srv := startTCPSim(t, addr)

	tr := NewTCPTransport(addr, 0, 0, 0)
	tr.SetReconnectPolicy(2, 20*time.Millisecond) // 测试里缩短退避
	defer tr.Close()
	if err := tr.Connect(); err != nil {
		t.Fatal(err)
	}
	c := NewClient(tr, 11, 0, 1, 0, 0)
	if err := c.WriteWords(MemAreaDM, 1, []uint16{42}); err != nil {
		t.Fatal(err)
	}

	_ = srv.Stop()
	waitDisconnected(t, tr)

	// 断线后的这一次读会尝试重连，但端口上没人监听 → 报错
	if _, err := c.ReadWords(MemAreaDM, 1, 1); err == nil {
		t.Fatal("模拟器停掉后读应失败")
	}

	// 同端口重启模拟器 → 下一次读自动重连成功
	srv2 := startTCPSim(t, addr)
	defer srv2.Stop()
	if _, err := c.ReadWords(MemAreaDM, 1, 1); err != nil {
		t.Fatalf("模拟器恢复后应自动重连成功，实际: %v", err)
	}
}

// 关掉自动重连（网关的用法）：断线不自动恢复、不等待退避；显式 Reconnect() 仍可用。
func TestTCPReconnectDisabled(t *testing.T) {
	addr := freeTCPAddr(t)
	srv := startTCPSim(t, addr)

	tr := NewTCPTransport(addr, 0, 0, 0)
	tr.SetReconnect(false)
	tr.SetReconnectPolicy(2, time.Second) // 关掉后不应有任何退避等待
	defer tr.Close()
	if err := tr.Connect(); err != nil {
		t.Fatal(err)
	}
	c := NewClient(tr, 11, 0, 1, 0, 0)
	if _, err := c.ReadWords(MemAreaDM, 1, 1); err != nil {
		t.Fatal(err)
	}

	_ = srv.Stop()
	waitDisconnected(t, tr)

	start := time.Now()
	_, err := c.ReadWords(MemAreaDM, 1, 1)
	var nce NotConnectedError
	if !errors.As(err, &nce) {
		t.Fatalf("关掉自动重连后应立刻返回 NotConnectedError，实际 %T: %v", err, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("关掉后不应有重连退避，耗时 %v", elapsed)
	}

	srv2 := startTCPSim(t, addr)
	defer srv2.Stop()
	if _, err := c.ReadWords(MemAreaDM, 1, 1); err == nil {
		t.Error("关掉自动重连后不应自动恢复")
	}

	if err := c.Reconnect(); err != nil {
		t.Fatalf("显式 Reconnect: %v", err)
	}
	if _, err := c.ReadWords(MemAreaDM, 1, 1); err != nil {
		t.Errorf("显式重连后应可读: %v", err)
	}
}

// 开关只在 TCP 传输上生效；UDP 无连接语义。
func TestReconnectSwitchAvailability(t *testing.T) {
	utr, err := NewUDPTransport("127.0.0.1:9600")
	if err != nil {
		t.Fatal(err)
	}
	defer utr.Close()
	if NewClient(utr, 1, 0, 2, 0, 0).SetReconnect(false) {
		t.Error("UDP 传输不应支持自动重连开关")
	}
	if err := NewClient(utr, 1, 0, 2, 0, 0).Reconnect(); err == nil {
		t.Error("UDP 显式 Reconnect 应返回不支持")
	}

	tr := NewTCPTransport("127.0.0.1:9600", 0, 0, 0)
	defer tr.Close()
	if !NewClient(tr, 1, 0, 2, 0, 0).SetReconnect(false) {
		t.Error("TCP 传输应支持自动重连开关")
	}
}

// 连接状态查询（连接器/网关卡的状态判断要用）。
func TestTransportIsConnected(t *testing.T) {
	addr := freeTCPAddr(t)
	srv := startTCPSim(t, addr)
	defer srv.Stop()

	tr := NewTCPTransport(addr, 0, 0, 0)
	defer tr.Close()
	if tr.IsConnected() {
		t.Error("Connect 之前不应为已连接")
	}
	if err := tr.Connect(); err != nil {
		t.Fatal(err)
	}
	if !tr.IsConnected() {
		t.Error("Connect 之后应为已连接")
	}

	utr, err := NewUDPTransport(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer utr.Close()
	if utr.IsConnected() {
		t.Error("UDP Connect 之前不应为已连接")
	}
	if err := utr.Connect(); err != nil {
		t.Fatal(err)
	}
	if !utr.IsConnected() {
		t.Error("UDP Connect 之后应为已连接")
	}
	if err := utr.Close(); err != nil {
		t.Fatal(err)
	}
	if utr.IsConnected() {
		t.Error("UDP Close 之后不应为已连接")
	}
}
