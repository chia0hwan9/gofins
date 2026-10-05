package gofins

import (
	"fmt"
	"time"
)

// EndCodeError is returned when the PLC responds with a non-zero end code.
type EndCodeError struct {
	Code    uint16
	Command uint16
}

func (e EndCodeError) Error() string {
	return fmt.Sprintf("FINS error: %s (code 0x%04X) for command 0x%04X",
		EndCodeToString(e.Code), e.Code, e.Command)
}

// ResponseTimeoutError occurs when a request exceeds the response timeout.
type ResponseTimeoutError struct {
	Duration time.Duration
}

func (e ResponseTimeoutError) Error() string {
	return fmt.Sprintf("response timeout after %v", e.Duration)
}

// TimeoutError occurs when a request exceeds the timeout.
type TimeoutError struct {
	Duration time.Duration
}

func (e TimeoutError) Error() string {
	return fmt.Sprintf("FINS operation timed out after %v", e.Duration)
}

// Timeout implements the net.Error interface.
func (e TimeoutError) Timeout() bool { return true }

// Temporary implements the net.Error interface.
func (e TimeoutError) Temporary() bool { return true }

// ProtocolError indicates malformed or unexpected protocol data.
type ProtocolError struct {
	Msg string
}

func (e ProtocolError) Error() string {
	return "FINS protocol error: " + e.Msg
}

// InvalidAddressError is for invalid memory address/area.
type InvalidAddressError struct {
	Area    MemoryArea
	Address uint16
}

func (e InvalidAddressError) Error() string {
	return fmt.Sprintf("invalid FINS address: area %v, offset %d",
		MemoryAreaToString(e.Area), e.Address)
}

// IncompatibleMemoryAreaError is returned when the memory area doesn't support the operation.
type IncompatibleMemoryAreaError struct {
	Area MemoryArea
}

func (e IncompatibleMemoryAreaError) Error() string {
	return fmt.Sprintf("memory area incompatible with operation: 0x%02X", byte(e.Area))
}

// NotConnectedError indicates the transport is not connected.
type NotConnectedError struct{}

func (e NotConnectedError) Error() string {
	return "FINS transport not connected"
}

// ConnectionClosedError is returned when the connection is closed.
type ConnectionClosedError struct{}

func (e ConnectionClosedError) Error() string {
	return "FINS connection closed"
}

// BCDError indicates a BCD encoding/decoding error.
type BCDError struct {
	Msg string
}

func (e BCDError) Error() string {
	return "BCD error: " + e.Msg
}

// FatalErrorCode represents fatal PLC error flags.
type FatalErrorCode uint16

const (
	FatalWatchDogTimer FatalErrorCode = 1 << 0  // Watch dog timer error
	FatalFALS          FatalErrorCode = 1 << 6  // FALS error
	FatalSFC           FatalErrorCode = 1 << 7  // Fatal SFC error
	FatalCycleTimeOver FatalErrorCode = 1 << 8  // Cycle time over
	FatalProgram       FatalErrorCode = 1 << 9  // Program error
	FatalIOSetting     FatalErrorCode = 1 << 10 // I/O setting error
	FatalIOOverflow    FatalErrorCode = 1 << 11 // I/O point overflow
	FatalCPUBus        FatalErrorCode = 1 << 12 // CPU bus error
	FatalDuplication   FatalErrorCode = 1 << 13 // Duplication error
	FatalIOBus         FatalErrorCode = 1 << 14 // I/O bus error
	FatalMemory        FatalErrorCode = 1 << 15 // Memory error
)
