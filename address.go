package gofins

import (
	"encoding/binary"
)

// MemoryAddress represents a specific location in PLC memory.
type MemoryAddress struct {
	Area      MemoryArea
	Address   uint16 // Word address (for both word and bit access)
	BitOffset byte   // 0x00 for word access, 0-15 for bit access
}

// NewWordAddress creates a word address (bit offset = 0).
func NewWordAddress(area MemoryArea, addr uint16) MemoryAddress {
	return MemoryAddress{Area: area, Address: addr, BitOffset: 0}
}

// NewBitAddress creates a bit address within a word.
func NewBitAddress(area MemoryArea, addr uint16, bit byte) (MemoryAddress, error) {
	if bit > 15 {
		return MemoryAddress{}, InvalidAddressError{Area: area, Address: addr}
	}
	return MemoryAddress{Area: area, Address: addr, BitOffset: bit}, nil
}

// Encode returns the 4-byte FINS memory address: area(1) + address(2) + bitOffset(1).
func (m MemoryAddress) Encode() []byte {
	b := make([]byte, 4)
	b[0] = byte(m.Area)
	binary.BigEndian.PutUint16(b[1:3], m.Address)
	b[3] = m.BitOffset
	return b
}

// DecodeMemoryAddress parses a 4-byte FINS memory address from response data.
func DecodeMemoryAddress(data []byte) (MemoryAddress, error) {
	if len(data) < 4 {
		return MemoryAddress{}, ProtocolError{Msg: "insufficient data for memory address"}
	}
	return MemoryAddress{
		Area:      MemoryArea(data[0]),
		Address:   binary.BigEndian.Uint16(data[1:3]),
		BitOffset: data[3],
	}, nil
}
