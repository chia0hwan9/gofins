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
