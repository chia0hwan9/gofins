package gofins

import "fmt"

// Header represents the FINS 10-byte frame header.
type Header struct {
	ICF byte // Information Control Field
	RSV byte // Reserved (0x00)
	GCT byte // Gateway Count (0x02 = direct connection)
	DNA byte // Destination Network Address
	DA1 byte // Destination Node Number
	DA2 byte // Destination Unit Address
	SNA byte // Source Network Address
	SA1 byte // Source Node Number
	SA2 byte // Source Unit Address
	SID byte // Service ID (sequence number, used for request/response matching)
}

// NewCommandHeader creates a header for a command frame.
func NewCommandHeader(destNode, destUnit, srcNode, srcUnit, sid byte) Header {
	return Header{
		ICF: ICFCommand,
		RSV: DefaultRSV,
		GCT: DefaultGCT,
		DNA: 0x00,
		DA1: destNode,
		DA2: destUnit,
		SNA: 0x00,
		SA1: srcNode,
		SA2: srcUnit,
		SID: sid,
	}
}

// NewResponseHeader creates a response header by swapping source/destination.
func NewResponseHeader(srcHeader Header) Header {
	return Header{
		ICF: ICFResponse,
		RSV: DefaultRSV,
		GCT: srcHeader.GCT,
		DNA: srcHeader.SNA,
		DA1: srcHeader.SA1,
		DA2: srcHeader.SA2,
		SNA: srcHeader.DNA,
		SA1: srcHeader.DA1,
		SA2: srcHeader.DA2,
		SID: srcHeader.SID,
	}
}

// Encode returns the 10-byte wire representation.
func (h Header) Encode() []byte {
	return []byte{
		h.ICF, h.RSV, h.GCT,
		h.DNA, h.DA1, h.DA2,
		h.SNA, h.SA1, h.SA2,
		h.SID,
	}
}

// DecodeHeader parses a 10-byte slice into a Header.
func DecodeHeader(data []byte) (Header, error) {
	if len(data) < 10 {
		return Header{}, ProtocolError{Msg: "header too short"}
	}
	return Header{
		ICF: data[0],
		RSV: data[1],
		GCT: data[2],
		DNA: data[3],
		DA1: data[4],
		DA2: data[5],
		SNA: data[6],
		SA1: data[7],
		SA2: data[8],
		SID: data[9],
	}, nil
}

// IsResponse returns true if the header is a response frame (ICF bit 6 == 1).
func (h Header) IsResponse() bool {
	return h.ICF&icfMessageTypeBit != 0
}

// IsCommand returns true if the header is a command frame (ICF bit 6 == 0).
func (h Header) IsCommand() bool {
	return h.ICF&icfMessageTypeBit == 0
}

// String returns a human-readable representation.
func (h Header) String() string {
	return fmt.Sprintf("ICF=0x%02X GCT=%d DNA=%d DA1=%d DA2=%d SNA=%d SA1=%d SA2=%d SID=%d",
		h.ICF, h.GCT, h.DNA, h.DA1, h.DA2, h.SNA, h.SA1, h.SA2, h.SID)
}
