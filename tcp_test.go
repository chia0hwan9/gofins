package gofins

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// fakePLC 是一个按 FINS/TCP 规范手写的极简对端，用来在没有真机的情况下验证：
//   - 握手帧（20 字节，Length=12）与响应解析（client node @19 / server node @23）
//   - 数据帧 Length = 帧总长 - 8（P0 修的就是这里：旧实现写成了 FINS 帧长度）
//   - SID 匹配与解析
//   - FINS/TCP 连接层 Error Code 的投递（旧实现直接忽略，调用方只能等到超时）
//
// 这个对端是独立按规范写的：如果客户端帧格式错了，它拿到的 Length 就对不上。
type fakePLC struct {
	ln           net.Listener
	done         chan struct{}
	handshakeLen uint32
	dataLen      uint32
	finsFrameLen int
	errCode      uint32
	statusData   []byte
}

func startFakePLC(t *testing.T, errCode uint32, statusData []byte) (*fakePLC, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePLC{ln: ln, done: make(chan struct{}), errCode: errCode, statusData: statusData}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		defer close(f.done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// --- 握手：20 字节请求 ---
		hs := make([]byte, 20)
		if _, err := io.ReadFull(conn, hs); err != nil {
			return
		}
		if string(hs[0:4]) != "FINS" {
			return
		}
		f.handshakeLen = binary.BigEndian.Uint32(hs[4:8])

		resp := make([]byte, 24)
		copy(resp[0:4], "FINS")
		binary.BigEndian.PutUint32(resp[4:8], 16) // Length = 24-8
		binary.BigEndian.PutUint32(resp[8:12], 1) // handshake response
		binary.BigEndian.PutUint32(resp[12:16], 0)
		resp[19] = 11 // PLC 分配给客户端的节点号
		resp[23] = 1  // PLC 的节点号
		if _, err := conn.Write(resp); err != nil {
			return
		}

		// --- 数据帧：8 字节前缀 + Length 字节 ---
		head := make([]byte, 8)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		f.dataLen = binary.BigEndian.Uint32(head[4:8])
		if f.dataLen == 0 || f.dataLen > 4096 {
			return
		}
		payload := make([]byte, f.dataLen)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		f.finsFrameLen = len(payload) - 8 // 去掉命令(4) + 错误码(4)
		sid := payload[8+9]
		cmd := binary.BigEndian.Uint16(payload[8+10 : 8+12])

		// --- 响应：FINS/TCP 头 + FINS 响应帧 ---
		finsResp := make([]byte, 10+2+2+len(f.statusData))
		finsResp[0] = 0xC0 // ICF: response
		finsResp[2] = 0x02 // GCT
		finsResp[9] = sid
		binary.BigEndian.PutUint16(finsResp[10:12], cmd)
		binary.BigEndian.PutUint16(finsResp[12:14], 0) // end code = normal
		copy(finsResp[14:], f.statusData)

		out := make([]byte, 16+len(finsResp))
		copy(out[0:4], "FINS")
		binary.BigEndian.PutUint32(out[4:8], uint32(8+len(finsResp))) // Length = 总长-8
		binary.BigEndian.PutUint32(out[8:12], 2)                      // data frame
		binary.BigEndian.PutUint32(out[12:16], f.errCode)
		copy(out[16:], finsResp)
		_, _ = conn.Write(out)
		time.Sleep(100 * time.Millisecond) // 等客户端读完再关
	}()

	return f, ln.Addr().String()
}

func waitDone(t *testing.T, f *fakePLC) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(3 * time.Second):
		t.Fatal("fake PLC 未在 3s 内完成应答")
	}
}

// 走通一次真实 TCP 会话（握手 + 数据帧），并按规范核对两个 Length 字段。
func TestTCPFramingAgainstFakePLC(t *testing.T) {
	status := make([]byte, 18)
	status[0] = 0x01 // RUN
	status[1] = 0x04 // RUN mode
	plc, addr := startFakePLC(t, 0, status)

	tr := NewTCPTransport(addr, 0, 0, 0)
	defer tr.Close()
	if err := tr.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	client := NewClient(tr, 11, 0, 1, 0, 0)

	st, err := client.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.IsRunning() {
		t.Errorf("Status = %+v，期望 RUN", st)
	}

	waitDone(t, plc)
	if plc.handshakeLen != 12 {
		t.Errorf("握手帧 Length = %d，期望 12（20 字节帧，定义是总长-8）", plc.handshakeLen)
	}
	if want := uint32(8 + plc.finsFrameLen); plc.dataLen != want {
		t.Errorf("数据帧 Length = %d，期望 %d（8 + FINS 帧长 %d）", plc.dataLen, want, plc.finsFrameLen)
	}
}

// FINS/TCP 连接层错误（如"连接数占满"）要立刻作为错误返回，而不是让调用方等超时。
func TestTCPConnectionLevelError(t *testing.T) {
	plc, addr := startFakePLC(t, TCPErrConnectionInUse, nil)

	tr := NewTCPTransport(addr, 0, 0, 0)
	defer tr.Close()
	if err := tr.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	client := NewClient(tr, 11, 0, 1, 0, 0)

	start := time.Now()
	_, err := client.Status()
	elapsed := time.Since(start)

	var tcpErr TCPError
	if !errors.As(err, &tcpErr) {
		t.Fatalf("期望 TCPError，实际 %T: %v", err, err)
	}
	if tcpErr.Code != TCPErrConnectionInUse {
		t.Errorf("Code = 0x%02X，期望 0x%02X", tcpErr.Code, TCPErrConnectionInUse)
	}
	if elapsed > time.Second {
		t.Errorf("连接层错误应立即返回，实际耗时 %v（旧实现会等响应超时）", elapsed)
	}
	waitDone(t, plc)
}

// 连接层错误码表：已知码有文本，未知码带 16 进制。
func TestTCPErrorCodeToString(t *testing.T) {
	if got := TCPErrorCodeToString(TCPErrConnectionInUse); got != "all connections are in use" {
		t.Errorf("0x20 → %q", got)
	}
	if got := TCPErrorCodeToString(TCPErrSameNodeAddress); got != "the same FINS node address is used by client and server" {
		t.Errorf("0x24 → %q", got)
	}
	if got := TCPErrorCodeToString(0x7F); got != "unknown FINS/TCP error (0x7F)" {
		t.Errorf("未知码 → %q", got)
	}
	err := TCPError{Code: TCPErrNodeOutOfRange, Stage: "handshake"}
	if want := "FINS/TCP error 0x23 at handshake: client FINS node address is out of range"; err.Error() != want {
		t.Errorf("Error() = %q，期望 %q", err.Error(), want)
	}
}
