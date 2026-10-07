package gofins

import "time"

// Transport defines the interface for FINS communication.
// Send takes a raw FINS frame (header + command + data, NO transport wrapping)
// and returns the raw FINS response frame.
type Transport interface {
	Send(rawFINSFrame []byte) ([]byte, error)
	Close() error
	SetTimeout(timeout time.Duration)
}

// Reconnecter 传输的可选能力：自动重连开关 + 显式重连。
// 目前只有 TCPTransport 实现（UDP 无连接语义）。
// 自己管重连的上层（网关的通道重连调度）应当 SetReconnect(false)，避免两套重连叠加。
type Reconnecter interface {
	SetReconnect(enabled bool)
	Reconnect() error
}
