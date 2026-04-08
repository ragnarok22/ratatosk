package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
)

// MaxUDPFrameSize is the maximum payload size for a single UDP frame.
const MaxUDPFrameSize = 65535

// WriteFrame writes a length-prefixed frame: [4-byte big-endian length][payload].
func WriteFrame(w io.Writer, data []byte) error {
	if len(data) > MaxUDPFrameSize {
		return fmt.Errorf("frame too large: %d bytes (max %d)", len(data), MaxUDPFrameSize)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// ReadFrame reads a length-prefixed frame and returns the payload.
// Returns an error if the declared length exceeds MaxUDPFrameSize.
func ReadFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length > MaxUDPFrameSize {
		return nil, fmt.Errorf("frame too large: %d bytes (max %d)", length, MaxUDPFrameSize)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}
