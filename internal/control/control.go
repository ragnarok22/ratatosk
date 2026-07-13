// Package control implements authentication for control connections before
// they are handed to yamux.
package control

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

const (
	protocolVersion = 1
	maxRecordSize   = 4 << 10
	minTokenSize    = 32
)

// ErrAuthenticationFailed is returned when the peer rejects authentication.
// It deliberately contains no detail that could disclose credentials.
var ErrAuthenticationFailed = errors.New("authentication failed")

type authRequest struct {
	Version int    `json:"version"`
	Token   string `json:"token"`
}

type authResponse struct {
	Success *bool  `json:"success"`
	Error   string `json:"error,omitempty"`
}

// AuthenticateAsClient authenticates conn using token before conn is passed
// to yamux. It applies timeout to the entire exchange and clears the deadline
// after successful authentication.
func AuthenticateAsClient(conn net.Conn, token string, timeout time.Duration) error {
	if conn == nil {
		return errors.New("control connection is nil")
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set authentication deadline: %w", err)
	}

	if err := writeRecord(conn, authRequest{Version: protocolVersion, Token: token}); err != nil {
		return fmt.Errorf("send authentication request: %w", err)
	}

	var response authResponse
	if err := readRecord(conn, &response); err != nil {
		return fmt.Errorf("read authentication response: %w", err)
	}
	if response.Success == nil {
		return errors.New("invalid authentication response")
	}
	if !*response.Success {
		if response.Error != ErrAuthenticationFailed.Error() {
			return errors.New("invalid authentication response")
		}
		return ErrAuthenticationFailed
	}
	if response.Error != "" {
		return errors.New("invalid authentication response")
	}

	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clear authentication deadline: %w", err)
	}
	return nil
}

// AuthenticateAsServer authenticates a client on conn using expectedToken
// before conn is passed to yamux. It applies timeout to the entire exchange
// and clears the deadline after successful authentication.
func AuthenticateAsServer(conn net.Conn, expectedToken string, timeout time.Duration) error {
	if conn == nil {
		return errors.New("control connection is nil")
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set authentication deadline: %w", err)
	}

	var request authRequest
	if err := readRecord(conn, &request); err != nil {
		return rejectAuthentication(conn)
	}

	expectedHash := sha256.Sum256([]byte(expectedToken))
	suppliedHash := sha256.Sum256([]byte(request.Token))
	validToken := subtle.ConstantTimeCompare(expectedHash[:], suppliedHash[:]) == 1
	if request.Version != protocolVersion || request.Token == "" || expectedToken == "" || !validToken {
		return rejectAuthentication(conn)
	}

	success := true
	if err := writeRecord(conn, authResponse{Success: &success}); err != nil {
		return fmt.Errorf("send authentication response: %w", err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clear authentication deadline: %w", err)
	}
	return nil
}

func rejectAuthentication(conn net.Conn) error {
	success := false
	err := writeRecord(conn, authResponse{Success: &success, Error: ErrAuthenticationFailed.Error()})
	if err == nil {
		return ErrAuthenticationFailed
	}
	return fmt.Errorf("send authentication response: %w", err)
}

func readRecord(r io.Reader, dst any) error {
	record := make([]byte, 0, 256)
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		if n > 0 {
			if b[0] == '\n' {
				break
			}
			if len(record) == maxRecordSize {
				return fmt.Errorf("authentication record exceeds %d bytes", maxRecordSize)
			}
			record = append(record, b[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("authentication record is not newline terminated")
			}
			return fmt.Errorf("read authentication record: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("read authentication record: %w", io.ErrNoProgress)
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(record))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return errors.New("decode authentication record")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("authentication record contains trailing JSON")
		}
		return errors.New("authentication record contains trailing data")
	}
	return nil
}

func writeRecord(w io.Writer, value any) error {
	record, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode authentication record")
	}
	if len(record) > maxRecordSize {
		return fmt.Errorf("authentication record exceeds %d bytes", maxRecordSize)
	}
	record = append(record, '\n')
	n, err := w.Write(record)
	if err != nil {
		return fmt.Errorf("write authentication record: %w", err)
	}
	if n != len(record) {
		return io.ErrShortWrite
	}
	return nil
}

// LoadToken loads a token from inline or file. Exactly one source must be set.
// Surrounding whitespace is removed, token files are limited to 4 KiB, and
// the resulting token must contain at least 32 bytes.
func LoadToken(inline, file string) (string, error) {
	if inline != "" && file != "" {
		return "", errors.New("control token sources are mutually exclusive")
	}
	if inline == "" && file == "" {
		return "", errors.New("control token is required")
	}

	token := inline
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			return "", fmt.Errorf("open control token file: %w", err)
		}
		defer f.Close()

		contents, err := io.ReadAll(io.LimitReader(f, maxRecordSize+1))
		if err != nil {
			return "", fmt.Errorf("read control token file: %w", err)
		}
		if len(contents) > maxRecordSize {
			return "", fmt.Errorf("control token file exceeds %d bytes", maxRecordSize)
		}
		token = string(contents)
	}

	token = strings.TrimSpace(token)
	if len(token) < minTokenSize {
		return "", fmt.Errorf("control token must be at least %d bytes", minTokenSize)
	}
	return token, nil
}
