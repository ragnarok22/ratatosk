package tunnel

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestFrameRoundTrip(t *testing.T) {
	payload := []byte("hello UDP")
	var buf bytes.Buffer

	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload = %q, want %q", got, payload)
	}
}

func TestFrameEmptyPayload(t *testing.T) {
	var buf bytes.Buffer

	if err := WriteFrame(&buf, []byte{}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(got))
	}
}

func TestFrameMaxSize(t *testing.T) {
	// Craft a header declaring a frame larger than MaxUDPFrameSize.
	var buf bytes.Buffer
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxUDPFrameSize+1)
	buf.Write(header[:])

	_, err := ReadFrame(&buf)
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
}

func TestFramePartialHeader(t *testing.T) {
	// Only 2 bytes instead of 4.
	buf := bytes.NewReader([]byte{0x00, 0x01})
	_, err := ReadFrame(buf)
	if err == nil {
		t.Fatal("expected error for partial header")
	}
}

func TestFramePartialPayload(t *testing.T) {
	// Header says 10 bytes, but only 3 available.
	var buf bytes.Buffer
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 10)
	buf.Write(header[:])
	buf.Write([]byte{1, 2, 3})

	_, err := ReadFrame(&buf)
	if err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

func TestFrameMultipleRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	payloads := [][]byte{
		[]byte("first"),
		[]byte("second"),
		[]byte("third"),
	}

	for _, p := range payloads {
		if err := WriteFrame(&buf, p); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	for _, want := range payloads {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("payload = %q, want %q", got, want)
		}
	}

	// No more frames.
	_, err := ReadFrame(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestWriteFrameHeaderWriteError(t *testing.T) {
	err := WriteFrame(failingWriter{}, []byte("payload"))
	if err == nil {
		t.Fatal("expected write error")
	}
}
