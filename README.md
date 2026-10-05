# gofins

[![Go Reference](https://pkg.go.dev/badge/github.com/chia0hwan9/gofins.svg)](https://pkg.go.dev/github.com/chia0hwan9/gofins)

Go library for communicating with Omron PLCs via the **FINS** (Factory Interface Network Service) protocol over TCP or UDP.

Merged and refined from [folke99/gofins](https://github.com/folke99/gofins) (TCP) and [l1va/gofins](https://github.com/l1va/gofins) (UDP), providing a unified API with shared protocol logic across both transports.

## Features

- **Dual transport**: TCP (persistent connection + FINS handshake) and UDP (lightweight datagrams)
- **Unified API**: same `Client` interface for both transports
- **Full FINS command set**: 25+ command codes covering memory read/write, clock, run/stop, status
- **Word access**: `DM`, `CIO`, `WR`, `HR`, `AR`, timer/counter PV
- **Bit access**: `CIO`, `WR`, `HR`, `AR`, `DM`, timer/counter flags
- **BCD encoding**: built-in BCD encode/decode for clock and numeric values
- **PLC control**: Run, Stop, Clock read/write, Status
- **Keepalive**: optional periodic status polling on TCP connections
- **Error handling**: typed errors for timeouts, end codes, protocol violations
- **Pluggable transport**: implement `Transport` for custom transports

## Installation

```bash
go get github.com/chia0hwan9/gofins
```

Requires Go 1.25+.

## Quick Start

### TCP

```go
package main

import (
    "fmt"
    "github.com/chia0hwan9/gofins"
)

func main() {
    // 1. Create transport
    transport := gofins.NewTCPTransport("192.168.1.100:9600", 1, 0, 0) // srcNode=1, srcUnit=0, srcNetwork=0

    // 2. Connect (performs FINS handshake)
    if err := transport.Connect(); err != nil {
        panic(err)
    }
    defer transport.Close()

    // 3. Create client
    client := gofins.NewClient(transport, 1, 0, 2, 0, 0) // dstNode=2

    // 4. Read 10 words from DM100
    words, err := client.ReadWords(gofins.MemAreaDM, 100, 10)
    if err != nil {
        panic(err)
    }
    fmt.Printf("DM100..DM109: % X\n", words)

    // 5. Write to DM200
    err = client.WriteWords(gofins.MemAreaDM, 200, []uint16{0x1234, 0x5678})
    if err != nil {
        panic(err)
    }

    // 6. Read PLC status
    status, err := client.Status()
    if err != nil {
        panic(err)
    }
    fmt.Printf("PLC is running: %v\n", status.IsRunning())
}
```

### UDP

```go
transport, err := gofins.NewUDPTransport("192.168.1.100:9600")
if err != nil {
    panic(err)
}
defer transport.Close()

client := gofins.NewClient(transport, 1, 0, 2, 0, 0)
words, err := client.ReadWords(gofins.MemAreaDM, 100, 10)
```

## Transport Configuration

### TCP Options

```go
t := gofins.NewTCPTransport("192.168.1.100:9600", 1, 0, 0)
t.SetTimeout(5 * time.Second)         // Response timeout (default: 10s)
t.SetKeepalive(30 * time.Second)      // Periodic status polling
t.SetReconnect(true)                  // Auto-reconnect on failure (default: true)
t.Connect()                           // Establishes TCP + FINS handshake
```

### UDP Options

```go
t, _ := gofins.NewUDPTransport("192.168.1.100:9600")
t.SetTimeout(3 * time.Second)         // Response timeout (default: 5s)
t.Connect()                           // Opens UDP socket
```

## Client API

### Memory Areas

| Constant | Area | Access | Description |
|---|---|---|---|
| `MemAreaDM` | DM | Word | Data Memory |
| `MemAreaCIOWord` | CIO | Word | Core I/O |
| `MemAreaWRWord` | WR | Word | Work Relay |
| `MemAreaHRWord` | HR | Word | Holding Relay |
| `MemAreaARWord` | AR | Word | Auxiliary Relay |
| `MemAreaDMBit` | DM | Bit | Data Memory bits |
| `MemAreaCIOBit` | CIO | Bit | Core I/O bits |
| `MemAreaWRBit` | WR | Bit | Work Relay bits |
| `MemAreaHRBit` | HR | Bit | Holding Relay bits |
| `MemAreaARBit` | AR | Bit | Auxiliary Relay bits |
| `MemAreaTimerCounterPV` | TIM/CNT | Word | Timer/counter present value |
| `MemAreaTimerCounterCompletionFlag` | TIM/CNT | Bit | Timer/counter completion flag |

### Word Operations

```go
// Read words (16-bit values)
words, err := client.ReadWords(MemAreaDM, address, count)

// Write words
err := client.WriteWords(MemAreaDM, address, []uint16{0x1234, 0x5678})

// Read raw bytes (must be even count)
bytes, err := client.ReadBytes(MemAreaDM, address, byteCount)

// Write raw bytes (must be even length)
err := client.WriteBytes(MemAreaDM, address, []byte{0x12, 0x34})
```

### String Operations

```go
// Read string (specify byte count, trailing nulls trimmed)
s, err := client.ReadString(MemAreaDM, address, byteCount)

// Write string (null-padded for word alignment)
err := client.WriteString(MemAreaDM, address, "hello")
```

### Bit Operations

```go
// Read bits (startBit 0-15, count max 256)
bits, err := client.ReadBits(MemAreaHRBit, address, startBit, count)

// Write bits
err := client.WriteBits(MemAreaHRBit, address, startBit, []bool{true, false, true})

// Single bit helpers
client.SetBit(MemAreaHRBit, address, bit)
client.ResetBit(MemAreaHRBit, address, bit)
client.ToggleBit(MemAreaHRBit, address, bit)
```

### PLC Clock

```go
t, err := client.ReadClock()          // Returns *time.Time
err := client.WriteClock(time.Now())  // Sets PLC clock
```

### PLC Control

```go
client.Run()                          // Enter RUN mode
client.Stop()                         // Enter PROGRAM (stop) mode
status, err := client.Status()        // Read operating status

// PLCStatus fields
status.IsRunning()                    // true if RUN mode
status.IsStopped()                    // true if STOP mode
status.IsStandby()                    // true if STANDBY
status.HasFatalError()                // true if any fatal error
```

### Byte Order

```go
client.SetByteOrder(binary.LittleEndian) // Default: BigEndian
```

### Ping

```go
err := client.Ping()                  // Sends status read to verify connectivity
```

## PLC Simulator (UDP only)

Built-in simulator for testing without hardware:

```go
server := gofins.NewServer()
go server.ListenAndServe("127.0.0.1:0")
defer server.Stop()

addr := server.Addr().String()
transport, _ := gofins.NewUDPTransport(addr)
client := gofins.NewClient(transport, 1, 0, 2, 0, 0)

client.WriteWords(gofins.MemAreaDM, 100, []uint16{0xABCD})
words, _ := client.ReadWords(gofins.MemAreaDM, 100, 1)
fmt.Printf("Read back: 0x%04X\n", words[0]) // 0xABCD
```

### Custom Command Handlers

```go
server.RegisterHandler(gofins.CmdMemoryRead, func(req gofins.Request) ([]byte, error) {
    // Custom read logic
    return []byte{0x12, 0x34}, nil
})
```

## Error Handling

All errors are typed for programmatic handling:

```go
words, err := client.ReadWords(gofins.MemAreaDM, 100, 10)
if err != nil {
    switch e := err.(type) {
    case gofins.EndCodeError:
        fmt.Printf("PLC error: %s (0x%04X)\n", e.Error(), e.Code)
    case gofins.ResponseTimeoutError:
        fmt.Printf("Timeout after %v\n", e.Duration)
    case gofins.IncompatibleMemoryAreaError:
        fmt.Printf("Wrong area type: %v\n", gofins.MemoryAreaToString(e.Area))
    case gofins.NotConnectedError:
        fmt.Println("Not connected")
    default:
        fmt.Printf("Unexpected: %v\n", err)
    }
}
```

## Architecture

```
gofins/
├── constants.go    — Command codes, end codes, memory areas, ICF bits
├── address.go      — FINS node address + memory address types
├── header.go       — 10-byte FINS header encode/decode
├── driver.go       — Request/Response types, BCD encode/decode
├── command.go      — Command builders (read, write, bit, clock, run/stop)
├── error.go        — All error types
├── transport.go    — Transport interface
├── tcp.go          — TCP transport (FINS init frame wrapping, handshake)
├── udp.go          — UDP transport (raw datagrams)
├── client.go       — Client with all read/write/bit/clock/status operations
└── server.go       — UDP PLC simulator
```

### Protocol Layers

```
┌──────────────┐
│   Client     │  ReadWords, WriteWords, ReadBits, Status, ...
├──────────────┤
│   FINS Core  │  Header, Request/Response, Command builders, BCD
├──────────────┤
│  Transport   │  TCP (init frames + handshake) / UDP (raw datagrams)
└──────────────┘
```

## License

MIT — see [LICENSE](LICENSE). Based on [folke99/gofins](https://github.com/folke99/gofins) and [l1va/gofins](https://github.com/l1va/gofins).
