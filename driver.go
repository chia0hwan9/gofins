package gofins

import (
	"encoding/binary"
)

// Request represents a FINS command to be sent.
type Request struct {
	Header  Header
	Command uint16
	Data    []byte
}

// Response represents a FINS response received from the PLC.
type Response struct {
	Header  Header
	Command uint16
	EndCode uint16
	Data    []byte
}

// EncodeRequest serializes a Request to raw FINS frame bytes (header + command + data).
// This is the raw FINS frame WITHOUT any transport wrapping.
func EncodeRequest(req Request) []byte {
	h := req.Header.Encode() // 10 bytes
	frame := make([]byte, 10+2+len(req.Data))
	copy(frame[0:10], h)
	binary.BigEndian.PutUint16(frame[10:12], req.Command)
	copy(frame[12:], req.Data)
	return frame
}

// DecodeResponse parses a raw FINS response frame (without transport wrapping).
// Frame format: header(10) + commandCode(2) + endCode(2) + data
func DecodeResponse(data []byte) (Response, error) {
	if len(data) < 14 {
		return Response{}, ProtocolError{Msg: "response too short"}
	}
	header, err := DecodeHeader(data[0:10])
	if err != nil {
		return Response{}, err
	}
	return Response{
		Header:  header,
		Command: binary.BigEndian.Uint16(data[10:12]),
		EndCode: binary.BigEndian.Uint16(data[12:14]),
		Data:    data[14:],
	}, nil
}

// EncodeResponse serializes a Response to raw FINS response frame bytes.
func EncodeResponse(resp Response) []byte {
	h := resp.Header.Encode()
	frame := make([]byte, 10+2+2+len(resp.Data))
	copy(frame[0:10], h)
	binary.BigEndian.PutUint16(frame[10:12], resp.Command)
	binary.BigEndian.PutUint16(frame[12:14], resp.EndCode)
	copy(frame[14:], resp.Data)
	return frame
}

// BCDEncode converts a uint16 to 2-byte BCD (0-9999).
func BCDEncode(value uint16) []byte {
	if value > 9999 {
		value = 9999
	}
	thousands := (value / 1000) % 10
	hundreds := (value / 100) % 10
	tens := (value / 10) % 10
	units := value % 10
	return []byte{
		byte(thousands)<<4 | byte(hundreds),
		byte(tens)<<4 | byte(units),
	}
}

// BCDDecode decodes a 2-byte BCD value to uint16.
func BCDDecode(data []byte) (uint16, error) {
	if len(data) < 2 {
		return 0, BCDError{Msg: "data too short"}
	}
	hi := uint16(data[0]>>4)*1000 + uint16(data[0]&0x0F)*100
	lo := uint16(data[1]>>4)*10 + uint16(data[1]&0x0F)
	if data[0]>>4 > 9 || data[0]&0x0F > 9 || data[1]>>4 > 9 || data[1]&0x0F > 9 {
		return 0, BCDError{Msg: "invalid BCD digit"}
	}
	return hi + lo, nil
}

// BCDEncodeByte converts a byte (0-99) to single-byte BCD.
func BCDEncodeByte(v byte) byte {
	if v > 99 {
		v = 99
	}
	return ((v / 10) << 4) | (v % 10)
}

// BCDDecodeByte decodes a single-byte BCD value.
func BCDDecodeByte(b byte) (byte, error) {
	hi, lo := b>>4, b&0x0F
	if hi > 9 || lo > 9 {
		return 0, BCDError{Msg: "invalid BCD digit"}
	}
	return hi*10 + lo, nil
}

// WordsToBytes converts a slice of uint16 to bytes (big-endian).
func WordsToBytes(words []uint16) []byte {
	b := make([]byte, len(words)*2)
	for i, w := range words {
		binary.BigEndian.PutUint16(b[i*2:], w)
	}
	return b
}

// BytesToWords converts bytes to uint16 slice (big-endian).
func BytesToWords(data []byte) ([]uint16, error) {
	if len(data)%2 != 0 {
		return nil, ProtocolError{Msg: "odd number of bytes for word conversion"}
	}
	words := make([]uint16, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		words[i/2] = binary.BigEndian.Uint16(data[i:])
	}
	return words, nil
}
