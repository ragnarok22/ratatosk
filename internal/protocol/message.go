package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
)

// Protocol identifiers sent in TunnelRequest.
const (
	ProtoHTTP = "http"
	ProtoTCP  = "tcp"
	ProtoUDP  = "udp"
)

// TunnelRequest is sent by the CLI client to request a new tunnel.
type TunnelRequest struct {
	Protocol  string `json:"protocol"`
	LocalPort int    `json:"local_port"`
	BasicAuth string `json:"basic_auth,omitempty"`
}

// TunnelResponse is sent by the server after processing a tunnel request.
type TunnelResponse struct {
	Subdomain string `json:"subdomain,omitempty"`
	URL       string `json:"url,omitempty"`
	Port      int    `json:"port,omitempty"`
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
	"mighty", "silent", "golden", "ancient", "frozen",
	"hidden", "iron", "runic", "fated", "stormy",
	"fierce", "hollow", "vast", "ashen", "starlit",
	"bright", "shadow", "sworn", "bitter", "winged",
	"eternal", "sunken", "woven", "fearless", "shining",
	"restless", "primal", "veiled", "wyrd", "sacred",
	"blazing", "grim", "steadfast", "lone", "twilight",
	"undying", "northern", "cursed", "elder", "wild",
}

var nouns = []string{
	"odin", "thor", "freya", "loki", "fenrir",
	"ymir", "sigyn", "bragi", "baldr", "tyr",
	"mjolnir", "asgard", "bifrost", "niflheim", "muspel",
	"valkyrie", "jotunn", "hugin", "munin", "sleipnir",
	"heimdall", "frigg", "skadi", "njord", "vidar",
	"hel", "surtr", "idun", "ull", "forseti",
	"gungnir", "draupnir", "midgard", "valhalla", "ragnarok",
	"norn", "rune", "einherjar", "gjallar", "yggdrasil",
}

// GenerateSubdomain returns a human-readable subdomain in adjective-noun-NNNN format.
func GenerateSubdomain() string {
	adj := adjectives[rand.IntN(len(adjectives))]
	noun := nouns[rand.IntN(len(nouns))]
	num := rand.IntN(1000000)
	return fmt.Sprintf("%s-%s-%06d", adj, noun, num)
}
