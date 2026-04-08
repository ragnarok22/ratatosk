package protocol

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestTunnelRequestRoundTrip(t *testing.T) {
	req := &TunnelRequest{Protocol: "http", LocalPort: 3000}

	var buf bytes.Buffer
	if err := WriteRequest(&buf, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	got, err := ReadRequest(&buf)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got.Protocol != req.Protocol {
		t.Errorf("Protocol = %q, want %q", got.Protocol, req.Protocol)
	}
	if got.LocalPort != req.LocalPort {
		t.Errorf("LocalPort = %d, want %d", got.LocalPort, req.LocalPort)
	}
}

func TestTunnelResponseRoundTripSuccess(t *testing.T) {
	resp := &TunnelResponse{Subdomain: "brave-fox-1234", Success: true}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Subdomain != resp.Subdomain {
		t.Errorf("Subdomain = %q, want %q", got.Subdomain, resp.Subdomain)
	}
	if !got.Success {
		t.Error("Success = false, want true")
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want empty", got.Error)
	}
}

func TestTunnelResponseRoundTripError(t *testing.T) {
	resp := &TunnelResponse{Success: false, Error: "subdomain unavailable"}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Success {
		t.Error("Success = true, want false")
	}
	if got.Error != resp.Error {
		t.Errorf("Error = %q, want %q", got.Error, resp.Error)
	}
}

func TestTunnelResponseOmitEmptyError(t *testing.T) {
	resp := &TunnelResponse{Subdomain: "calm-deer-0001", Success: true}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	if strings.Contains(buf.String(), `"error"`) {
		t.Errorf("JSON contains 'error' key despite omitempty: %s", buf.String())
	}
}

func TestGenerateSubdomain(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+-[a-z]+-\d{6}$`)
	for range 100 {
		sub := GenerateSubdomain()
		if !pattern.MatchString(sub) {
			t.Errorf("subdomain %q does not match expected pattern", sub)
		}
	}
}

func TestGenerateSubdomainUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 200 {
		sub := GenerateSubdomain()
		if seen[sub] {
			t.Fatalf("duplicate subdomain: %s", sub)
		}
		seen[sub] = true
	}
}

func TestTunnelRequestWithBasicAuth(t *testing.T) {
	req := &TunnelRequest{Protocol: "http", LocalPort: 3000, BasicAuth: "admin:secret"}

	var buf bytes.Buffer
	if err := WriteRequest(&buf, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	got, err := ReadRequest(&buf)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got.BasicAuth != "admin:secret" {
		t.Errorf("BasicAuth = %q, want %q", got.BasicAuth, "admin:secret")
	}
}

func TestTunnelRequestBasicAuthOmitEmpty(t *testing.T) {
	req := &TunnelRequest{Protocol: "http", LocalPort: 3000}

	var buf bytes.Buffer
	if err := WriteRequest(&buf, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	if strings.Contains(buf.String(), `"basic_auth"`) {
		t.Errorf("JSON contains 'basic_auth' key despite omitempty: %s", buf.String())
	}
}

func TestReadRequestInvalidJSON(t *testing.T) {
	r := strings.NewReader("not json")
	_, err := ReadRequest(r)
	if err == nil {
		t.Fatal("ReadRequest with invalid JSON should return error")
	}
}

func TestReadResponseInvalidJSON(t *testing.T) {
	r := strings.NewReader("{broken")
	_, err := ReadResponse(r)
	if err == nil {
		t.Fatal("ReadResponse with invalid JSON should return error")
	}
}
