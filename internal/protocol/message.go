package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
)

// TunnelRequest is sent by the CLI client to request a new tunnel.
type TunnelRequest struct {
	Protocol  string `json:"protocol"`
	LocalPort int    `json:"local_port"`
}

// TunnelResponse is sent by the server after processing a tunnel request.
type TunnelResponse struct {
	Subdomain string `json:"subdomain"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

// ReadRequest decodes a JSON TunnelRequest from the given reader.
func ReadRequest(r io.Reader) (*TunnelRequest, error) {
	var req TunnelRequest
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return nil, err
	}
	return &req, nil
}

// WriteRequest encodes a TunnelRequest as JSON to the given writer.
func WriteRequest(w io.Writer, req *TunnelRequest) error {
	return json.NewEncoder(w).Encode(req)
}

// ReadResponse decodes a JSON TunnelResponse from the given reader.
func ReadResponse(r io.Reader) (*TunnelResponse, error) {
	var resp TunnelResponse
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// WriteResponse encodes a TunnelResponse as JSON to the given writer.
func WriteResponse(w io.Writer, resp *TunnelResponse) error {
	return json.NewEncoder(w).Encode(resp)
}

var adjectives = []string{
	"quick", "brave", "calm", "eager", "happy",
	"kind", "lively", "proud", "sharp", "warm",
	"bold", "cool", "deep", "fair", "grand",
	"neat", "rare", "safe", "swift", "wise",
}

var nouns = []string{
	"fox", "bear", "hawk", "wolf", "deer",
	"hare", "lynx", "seal", "wren", "dove",
	"otter", "raven", "tiger", "panda", "eagle",
	"crane", "bison", "coral", "cedar", "maple",
}

// GenerateSubdomain returns a human-readable subdomain in adjective-noun-NNNN format.
func GenerateSubdomain() string {
	adj := adjectives[rand.IntN(len(adjectives))]
	noun := nouns[rand.IntN(len(nouns))]
	num := rand.IntN(1000000)
	return fmt.Sprintf("%s-%s-%06d", adj, noun, num)
}
