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

func TestTunnelResponseRoundTripWithURL(t *testing.T) {
	resp := &TunnelResponse{Subdomain: "cool-bear-5678", URL: "http://cool-bear-5678.localhost:8080", Success: true}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.URL != resp.URL {
		t.Errorf("URL = %q, want %q", got.URL, resp.URL)
	}
}

func TestTunnelResponseURLOmitEmpty(t *testing.T) {
	resp := &TunnelResponse{Subdomain: "neat-hawk-0001", Success: true}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	if strings.Contains(buf.String(), `"url"`) {
		t.Errorf("JSON contains 'url' key despite omitempty: %s", buf.String())
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

func TestTunnelRequestWithAuthToken(t *testing.T) {
	req := &TunnelRequest{Protocol: ProtoHTTP, LocalPort: 3000, AuthToken: "control-secret"}

	var buf bytes.Buffer
	if err := WriteRequest(&buf, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	got, err := ReadRequest(&buf)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got.AuthToken != req.AuthToken {
		t.Fatalf("AuthToken = %q, want %q", got.AuthToken, req.AuthToken)
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

func TestReadResponseRejectsOversizedMessage(t *testing.T) {
	message := `{"success":false,"error":"` + strings.Repeat("x", 64<<10) + `"}`
	if _, err := ReadResponse(strings.NewReader(message)); err == nil {
		t.Fatal("ReadResponse accepted an oversized control message")
	}
}

func TestReadResponseRejectsUnknownFields(t *testing.T) {
	message := `{"success":true,"unexpected":true}`
	if _, err := ReadResponse(strings.NewReader(message)); err == nil {
		t.Fatal("ReadResponse accepted an unknown field")
	}
}

func TestReadResponseRejectsTrailingJSON(t *testing.T) {
	message := `{"success":true}{"success":false}`
	if _, err := ReadResponse(strings.NewReader(message)); err == nil {
		t.Fatal("ReadResponse accepted multiple JSON values")
	}
}

func TestReadRequestRejectsOversizedMessage(t *testing.T) {
	message := `{"protocol":"http","local_port":3000,"basic_auth":"` + strings.Repeat("x", 64<<10) + `"}`
	if _, err := ReadRequest(strings.NewReader(message)); err == nil {
		t.Fatal("ReadRequest accepted an oversized control message")
	}
}

func TestReadRequestRejectsUnknownFields(t *testing.T) {
	message := `{"protocol":"http","local_port":3000,"unexpected":true}`
	if _, err := ReadRequest(strings.NewReader(message)); err == nil {
		t.Fatal("ReadRequest accepted an unknown field")
	}
}

func TestReadRequestRejectsTrailingJSON(t *testing.T) {
	message := `{"protocol":"http","local_port":3000}{"protocol":"tcp","local_port":22}`
	if _, err := ReadRequest(strings.NewReader(message)); err == nil {
		t.Fatal("ReadRequest accepted multiple JSON values")
	}
}

func TestTunnelRequestTCPRoundTrip(t *testing.T) {
	req := &TunnelRequest{Protocol: ProtoTCP, LocalPort: 22}

	var buf bytes.Buffer
	if err := WriteRequest(&buf, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	got, err := ReadRequest(&buf)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got.Protocol != ProtoTCP {
		t.Errorf("Protocol = %q, want %q", got.Protocol, ProtoTCP)
	}
	if got.LocalPort != 22 {
		t.Errorf("LocalPort = %d, want 22", got.LocalPort)
	}
}

func TestTunnelRequestUDPRoundTrip(t *testing.T) {
	req := &TunnelRequest{Protocol: ProtoUDP, LocalPort: 25565}

	var buf bytes.Buffer
	if err := WriteRequest(&buf, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	got, err := ReadRequest(&buf)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got.Protocol != ProtoUDP {
		t.Errorf("Protocol = %q, want %q", got.Protocol, ProtoUDP)
	}
	if got.LocalPort != 25565 {
		t.Errorf("LocalPort = %d, want 25565", got.LocalPort)
	}
}

func TestTunnelResponseWithPort(t *testing.T) {
	resp := &TunnelResponse{Port: 12345, Success: true}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Port != 12345 {
		t.Errorf("Port = %d, want 12345", got.Port)
	}
	if !got.Success {
		t.Error("Success = false, want true")
	}
}

func TestTunnelResponsePortOmitEmpty(t *testing.T) {
	resp := &TunnelResponse{Subdomain: "calm-fox-0001", Success: true}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	if strings.Contains(buf.String(), `"port"`) {
		t.Errorf("JSON contains 'port' key despite omitempty: %s", buf.String())
	}
}

func TestTunnelResponseSubdomainOmitEmpty(t *testing.T) {
	resp := &TunnelResponse{Port: 12345, Success: true}

	var buf bytes.Buffer
	if err := WriteResponse(&buf, resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	if strings.Contains(buf.String(), `"subdomain"`) {
		t.Errorf("JSON contains 'subdomain' key despite omitempty: %s", buf.String())
	}
}
