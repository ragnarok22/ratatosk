package main

import (
	"bytes"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"text/template"

	"github.com/charmbracelet/huh"
	"github.com/spf13/viper"
)

func TestWriteFileAtomicEnforcesPermissionsWhenOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ratatosk.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := writeFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q, want new", data)
	}
}

func TestValidateDomain(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"tunnel.example.com", false},
		{"a.b", false},
		{"", true},
		{"   ", true},
		{"localhost", true},
		{"nodot", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := validateDomain(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDomain(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateEmail(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"user@example.com", false},
		{"a@b", false},
		{"", true},
		{"   ", true},
		{"noatsign", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := validateEmail(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateEmail(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateAPIToken(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"some-token-value", false},
		{"", true},
		{"   ", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := validateAPIToken(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAPIToken(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestConfigDir(t *testing.T) {
	tests := []struct {
		name string
		euid int
		want string
	}{
		{"root", 0, "/etc/ratatosk"},
		{"non-root", 1000, "."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := initGetEUID
			initGetEUID = func() int { return tt.euid }
			t.Cleanup(func() { initGetEUID = old })

			if got := configDir(); got != tt.want {
				t.Errorf("configDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderConfigWithTLS(t *testing.T) {
	answers := initAnswers{
		BaseDomain:  "tunnel.example.com",
		TLSAuto:     true,
		TLSEmail:    "admin@example.com",
		TLSProvider: "cloudflare",
		TLSAPIToken: "cf-token-secret",
	}

	data, err := renderConfig(initConfigTmpl, answers)
	if err != nil {
		t.Fatalf("renderConfig() error = %v", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		t.Fatalf("failed to parse rendered YAML: %v", err)
	}

	if got := v.GetString("base_domain"); got != "tunnel.example.com" {
		t.Errorf("base_domain = %q, want %q", got, "tunnel.example.com")
	}
	if got := v.GetInt("public_port"); got != 443 {
		t.Errorf("public_port = %d, want 443", got)
	}
	if got := v.GetBool("tls_auto"); !got {
		t.Error("tls_auto = false, want true")
	}
	if got := v.GetString("tls_email"); got != "admin@example.com" {
		t.Errorf("tls_email = %q, want %q", got, "admin@example.com")
	}
	if got := v.GetString("tls_provider"); got != "cloudflare" {
		t.Errorf("tls_provider = %q, want %q", got, "cloudflare")
	}
	if got := v.GetString("tls_api_token"); got != "cf-token-secret" {
		t.Errorf("tls_api_token = %q, want %q", got, "cf-token-secret")
	}
}

func TestRenderConfigWithoutTLS(t *testing.T) {
	answers := initAnswers{
		BaseDomain: "tunnel.example.com",
		TLSAuto:    false,
	}

	data, err := renderConfig(initConfigTmpl, answers)
	if err != nil {
		t.Fatalf("renderConfig() error = %v", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		t.Fatalf("failed to parse rendered YAML: %v", err)
	}

	if got := v.GetString("base_domain"); got != "tunnel.example.com" {
		t.Errorf("base_domain = %q, want %q", got, "tunnel.example.com")
	}
	if got := v.GetInt("public_port"); got != 8080 {
		t.Errorf("public_port = %d, want 8080", got)
	}
	if got := v.GetBool("tls_auto"); got {
		t.Error("tls_auto = true, want false")
	}
	if got := v.GetString("control_host"); got != "127.0.0.1" {
		t.Errorf("control_host = %q, want 127.0.0.1", got)
	}
	if got := v.GetBool("control_tls_enabled"); got {
		t.Error("control_tls_enabled = true, want false")
	}
	if got := v.GetString("control_token_file"); got != "" {
		t.Errorf("control_token_file = %q, want empty", got)
	}
	// TLS fields should not be present
	if got := v.GetString("tls_email"); got != "" {
		t.Errorf("tls_email = %q, want empty", got)
	}
}

func TestRenderConfigWithRemoteControl(t *testing.T) {
	answers := initAnswers{
		BaseDomain:       "tunnel.example.com",
		TLSAuto:          true,
		TLSEmail:         "admin@example.com",
		TLSProvider:      "cloudflare",
		TLSAPIToken:      "cf-token-secret",
		RemoteControl:    true,
		ControlTokenFile: "/etc/ratatosk/control-token",
	}

	data, err := renderConfig(initConfigTmpl, answers)
	if err != nil {
		t.Fatalf("renderConfig() error = %v", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		t.Fatalf("failed to parse rendered YAML: %v", err)
	}
	if got := v.GetString("control_host"); got != "0.0.0.0" {
		t.Errorf("control_host = %q, want 0.0.0.0", got)
	}
	if got := v.GetBool("control_tls_enabled"); !got {
		t.Error("control_tls_enabled = false, want true")
	}
	if got := v.GetString("control_token_file"); got != answers.ControlTokenFile {
		t.Errorf("control_token_file = %q, want %q", got, answers.ControlTokenFile)
	}
	if got := v.GetString("control_tls_cert_file"); got != "" {
		t.Errorf("control_tls_cert_file = %q, want empty", got)
	}
	if got := v.GetString("control_tls_key_file"); got != "" {
		t.Errorf("control_tls_key_file = %q, want empty", got)
	}
	if got := v.GetString("control_token"); got != "" {
		t.Errorf("control_token = %q, want empty", got)
	}
}

func TestCanExposeControlRemotely(t *testing.T) {
	tests := []struct {
		name    string
		answers initAnswers
		want    bool
	}{
		{name: "automatic TLS and domain", answers: initAnswers{BaseDomain: "tunnel.example.com", TLSAuto: true}, want: true},
		{name: "no automatic TLS", answers: initAnswers{BaseDomain: "tunnel.example.com"}, want: false},
		{name: "invalid domain", answers: initAnswers{BaseDomain: "localhost", TLSAuto: true}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canExposeControlRemotely(tt.answers); got != tt.want {
				t.Errorf("canExposeControlRemotely() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderConfigPortRange(t *testing.T) {
	answers := initAnswers{BaseDomain: "test.dev", TLSAuto: false}

	data, err := renderConfig(initConfigTmpl, answers)
	if err != nil {
		t.Fatalf("renderConfig() error = %v", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		t.Fatalf("failed to parse rendered YAML: %v", err)
	}

	if got := v.GetInt("port_range_start"); got != 10000 {
		t.Errorf("port_range_start = %d, want 10000", got)
	}
	if got := v.GetInt("port_range_end"); got != 20000 {
		t.Errorf("port_range_end = %d, want 20000", got)
	}
}

func TestRenderConfigTemplateError(t *testing.T) {
	badTmpl := template.Must(template.New("bad").Parse("{{ .Missing.Nested }}"))
	_, err := renderConfig(badTmpl, initAnswers{})
	if err == nil {
		t.Fatal("renderConfig() with bad template should return error")
	}
}

func TestRunInitSuccess(t *testing.T) {
	var stdout bytes.Buffer
	var writtenPath string
	var writtenData []byte
	var writtenPerm fs.FileMode

	oldStdout := initStdout
	oldGetEUID := initGetEUID
	oldWriteFile := initWriteFile
	oldMkdirAll := initMkdirAll
	oldStat := initStat
	oldRunForm := initRunForm
	t.Cleanup(func() {
		initStdout = oldStdout
		initGetEUID = oldGetEUID
		initWriteFile = oldWriteFile
		initMkdirAll = oldMkdirAll
		initStat = oldStat
		initRunForm = oldRunForm
	})

	initStdout = &stdout
	initGetEUID = func() int { return 1000 }
	initMkdirAll = func(path string, perm os.FileMode) error { return nil }
	initStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	initWriteFile = func(name string, data []byte, perm os.FileMode) error {
		writtenPath = name
		writtenData = data
		writtenPerm = perm
		return nil
	}

	callCount := 0
	initRunForm = func(f *huh.Form) error {
		callCount++
		return nil
	}

	code := runInit()
	if code != 0 {
		t.Fatalf("runInit() = %d, want 0; output:\n%s", code, stdout.String())
	}

	if writtenPath != "ratatosk.yaml" {
		t.Errorf("written path = %q, want %q", writtenPath, "ratatosk.yaml")
	}
	if writtenPerm != 0600 {
		t.Errorf("written perm = %o, want 0600", writtenPerm)
	}
	if len(writtenData) == 0 {
		t.Error("written data is empty")
	}
	if !strings.Contains(stdout.String(), "Configuration saved successfully") {
		t.Errorf("output missing success message: %s", stdout.String())
	}
}

func TestRunInitWriteError(t *testing.T) {
	var stdout bytes.Buffer

	oldStdout := initStdout
	oldGetEUID := initGetEUID
	oldWriteFile := initWriteFile
	oldMkdirAll := initMkdirAll
	oldStat := initStat
	oldRunForm := initRunForm
	t.Cleanup(func() {
		initStdout = oldStdout
		initGetEUID = oldGetEUID
		initWriteFile = oldWriteFile
		initMkdirAll = oldMkdirAll
		initStat = oldStat
		initRunForm = oldRunForm
	})

	initStdout = &stdout
	initGetEUID = func() int { return 1000 }
	initMkdirAll = func(path string, perm os.FileMode) error { return nil }
	initStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	initWriteFile = func(name string, data []byte, perm os.FileMode) error {
		return os.ErrPermission
	}
	initRunForm = func(f *huh.Form) error { return nil }

	code := runInit()
	if code != 1 {
		t.Fatalf("runInit() = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "Error writing config") {
		t.Errorf("output missing error message: %s", stdout.String())
	}
}

func TestRunInitMkdirError(t *testing.T) {
	var stdout bytes.Buffer

	oldStdout := initStdout
	oldGetEUID := initGetEUID
	oldMkdirAll := initMkdirAll
	oldStat := initStat
	oldRunForm := initRunForm
	t.Cleanup(func() {
		initStdout = oldStdout
		initGetEUID = oldGetEUID
		initMkdirAll = oldMkdirAll
		initStat = oldStat
		initRunForm = oldRunForm
	})

	initStdout = &stdout
	initGetEUID = func() int { return 0 }
	initStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	initMkdirAll = func(path string, perm os.FileMode) error {
		return os.ErrPermission
	}
	initRunForm = func(f *huh.Form) error { return nil }

	code := runInit()
	if code != 1 {
		t.Fatalf("runInit() = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "Error creating directory") {
		t.Errorf("output missing mkdir error message: %s", stdout.String())
	}
}

func TestRunInitRootWritesToEtc(t *testing.T) {
	var writtenPath string

	oldStdout := initStdout
	oldGetEUID := initGetEUID
	oldWriteFile := initWriteFile
	oldMkdirAll := initMkdirAll
	oldStat := initStat
	oldRunForm := initRunForm
	t.Cleanup(func() {
		initStdout = oldStdout
		initGetEUID = oldGetEUID
		initWriteFile = oldWriteFile
		initMkdirAll = oldMkdirAll
		initStat = oldStat
		initRunForm = oldRunForm
	})

	initStdout = &bytes.Buffer{}
	initGetEUID = func() int { return 0 }
	initMkdirAll = func(path string, perm os.FileMode) error { return nil }
	initStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	initWriteFile = func(name string, data []byte, perm os.FileMode) error {
		writtenPath = name
		return nil
	}
	initRunForm = func(f *huh.Form) error { return nil }

	code := runInit()
	if code != 0 {
		t.Fatalf("runInit() = %d, want 0", code)
	}
	if writtenPath != "/etc/ratatosk/ratatosk.yaml" {
		t.Errorf("written path = %q, want %q", writtenPath, "/etc/ratatosk/ratatosk.yaml")
	}
}

func TestRunInitUserAborted(t *testing.T) {
	var stdout bytes.Buffer

	oldStdout := initStdout
	oldRunForm := initRunForm
	t.Cleanup(func() {
		initStdout = oldStdout
		initRunForm = oldRunForm
	})

	initStdout = &stdout
	initRunForm = func(f *huh.Form) error { return huh.ErrUserAborted }

	code := runInit()
	if code != 0 {
		t.Fatalf("runInit() = %d, want 0 for user abort", code)
	}
	if !strings.Contains(stdout.String(), "Setup cancelled") {
		t.Errorf("output missing abort message: %s", stdout.String())
	}
}

func TestRunInitFormError(t *testing.T) {
	var stdout bytes.Buffer

	oldStdout := initStdout
	oldRunForm := initRunForm
	t.Cleanup(func() {
		initStdout = oldStdout
		initRunForm = oldRunForm
	})

	initStdout = &stdout
	initRunForm = func(f *huh.Form) error { return os.ErrClosed }

	code := runInit()
	if code != 1 {
		t.Fatalf("runInit() = %d, want 1 for form error", code)
	}
}

// stubInitDeps saves and restores all init.go function vars.
func stubInitDeps(t *testing.T, stdout *bytes.Buffer) {
	t.Helper()
	oldStdout := initStdout
	oldGetEUID := initGetEUID
	oldWriteFile := initWriteFile
	oldMkdirAll := initMkdirAll
	oldStat := initStat
	oldRunForm := initRunForm
	oldCollectAnswers := initCollectAnswers
	oldConfirmOverwrite := initConfirmOverwrite
	oldRandomReader := initRandomReader
	oldRemoveFile := initRemoveFile
	t.Cleanup(func() {
		initStdout = oldStdout
		initGetEUID = oldGetEUID
		initWriteFile = oldWriteFile
		initMkdirAll = oldMkdirAll
		initStat = oldStat
		initRunForm = oldRunForm
		initCollectAnswers = oldCollectAnswers
		initConfirmOverwrite = oldConfirmOverwrite
		initRandomReader = oldRandomReader
		initRemoveFile = oldRemoveFile
	})
	initStdout = stdout
	initGetEUID = func() int { return 1000 }
	initMkdirAll = func(string, os.FileMode) error { return nil }
	initStat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	initWriteFile = func(string, []byte, os.FileMode) error { return nil }
	initRunForm = func(*huh.Form) error { return nil }
	initCollectAnswers = collectInitAnswers
	initConfirmOverwrite = func(string) (bool, error) { return false, nil }
	initRandomReader = bytes.NewReader(make([]byte, 32))
	initRemoveFile = func(string) error { return nil }
}

func TestRunInitSecureRemoteControl(t *testing.T) {
	var stdout bytes.Buffer
	stubInitDeps(t, &stdout)

	type write struct {
		path string
		data []byte
		perm os.FileMode
	}
	var writes []write
	wantRandom := bytes.Repeat([]byte{0xa5}, 32)
	wantToken := base64.StdEncoding.EncodeToString(wantRandom)

	initGetEUID = func() int { return 0 }
	initCollectAnswers = func(answers *initAnswers) error {
		*answers = initAnswers{
			BaseDomain:    "tunnel.example.com",
			TLSAuto:       true,
			TLSEmail:      "admin@example.com",
			TLSProvider:   "cloudflare",
			TLSAPIToken:   "cf-token-secret",
			RemoteControl: true,
		}
		return nil
	}
	initRandomReader = bytes.NewReader(wantRandom)
	initWriteFile = func(path string, data []byte, perm os.FileMode) error {
		writes = append(writes, write{path: path, data: append([]byte(nil), data...), perm: perm})
		return nil
	}

	code := runInit()
	if code != 0 {
		t.Fatalf("runInit() = %d, want 0; output:\n%s", code, stdout.String())
	}
	if len(writes) != 2 {
		t.Fatalf("writes = %d, want token and config writes", len(writes))
	}
	if writes[0].path != "/etc/ratatosk/control-token" {
		t.Errorf("token path = %q, want /etc/ratatosk/control-token", writes[0].path)
	}
	if writes[0].perm != 0o600 {
		t.Errorf("token mode = %o, want 600", writes[0].perm)
	}
	if got := strings.TrimSpace(string(writes[0].data)); got != wantToken {
		t.Fatalf("token file content = %q, want deterministic base64 token", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(writes[0].data)))
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if !bytes.Equal(decoded, wantRandom) {
		t.Fatalf("decoded token has %d bytes with unexpected content", len(decoded))
	}
	if writes[1].path != "/etc/ratatosk/ratatosk.yaml" {
		t.Errorf("config path = %q, want /etc/ratatosk/ratatosk.yaml", writes[1].path)
	}
	if writes[1].perm != 0o600 {
		t.Errorf("config mode = %o, want 600", writes[1].perm)
	}
	if strings.Contains(string(writes[1].data), wantToken) {
		t.Error("rendered config contains the control token")
	}
	if strings.Contains(stdout.String(), wantToken) {
		t.Error("stdout contains the control token")
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(writes[1].data)); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if got := v.GetString("control_host"); got != "0.0.0.0" {
		t.Errorf("control_host = %q, want 0.0.0.0", got)
	}
	if !v.GetBool("control_tls_enabled") {
		t.Error("control_tls_enabled = false, want true")
	}
	if got := v.GetString("control_token_file"); got != writes[0].path {
		t.Errorf("control_token_file = %q, want %q", got, writes[0].path)
	}
}

func TestRunInitControlTokenRandomError(t *testing.T) {
	var stdout bytes.Buffer
	stubInitDeps(t, &stdout)

	initCollectAnswers = func(answers *initAnswers) error {
		*answers = initAnswers{BaseDomain: "tunnel.example.com", TLSAuto: true, RemoteControl: true}
		return nil
	}
	initRandomReader = iotest.ErrReader(os.ErrInvalid)
	written := false
	initWriteFile = func(string, []byte, os.FileMode) error {
		written = true
		return nil
	}

	code := runInit()
	if code != 1 {
		t.Fatalf("runInit() = %d, want 1", code)
	}
	if written {
		t.Error("file written after random generation failed")
	}
	if !strings.Contains(stdout.String(), "Error generating control token") {
		t.Errorf("output missing random generation error: %s", stdout.String())
	}
}

func TestRunInitRemovesNewTokenWhenConfigWriteFails(t *testing.T) {
	var stdout bytes.Buffer
	stubInitDeps(t, &stdout)

	initCollectAnswers = func(answers *initAnswers) error {
		*answers = initAnswers{BaseDomain: "tunnel.example.com", TLSAuto: true, RemoteControl: true}
		return nil
	}
	writes := 0
	initWriteFile = func(string, []byte, os.FileMode) error {
		writes++
		if writes == 2 {
			return os.ErrPermission
		}
		return nil
	}
	var removedPath string
	initRemoveFile = func(path string) error {
		removedPath = path
		return nil
	}

	code := runInit()
	if code != 1 {
		t.Fatalf("runInit() = %d, want 1", code)
	}
	if removedPath != "control-token" {
		t.Errorf("removed path = %q, want control-token", removedPath)
	}
	if !strings.Contains(stdout.String(), "Error writing config") {
		t.Errorf("output missing config write error: %s", stdout.String())
	}
}

func TestRunInitPreservesExistingTokenWhenConfigWriteFails(t *testing.T) {
	var stdout bytes.Buffer
	stubInitDeps(t, &stdout)

	initCollectAnswers = func(answers *initAnswers) error {
		*answers = initAnswers{BaseDomain: "tunnel.example.com", TLSAuto: true, RemoteControl: true}
		return nil
	}
	initStat = func(path string) (os.FileInfo, error) {
		if path == "ratatosk.yaml" || path == "control-token" {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
	initConfirmOverwrite = func(string) (bool, error) { return true, nil }

	existingToken := []byte("existing-control-token-must-survive\n")
	currentToken := append([]byte(nil), existingToken...)
	tokenWrites := 0
	initWriteFile = func(path string, data []byte, _ os.FileMode) error {
		switch path {
		case "control-token":
			tokenWrites++
			currentToken = append(currentToken[:0], data...)
			return nil
		case "ratatosk.yaml":
			return os.ErrPermission
		default:
			return nil
		}
	}

	code := runInit()
	if code != 1 {
		t.Fatalf("runInit() = %d, want 1", code)
	}
	if tokenWrites != 0 {
		t.Errorf("existing control token was rewritten %d time(s)", tokenWrites)
	}
	if !bytes.Equal(currentToken, existingToken) {
		t.Fatalf("control token changed to %q after config write failure", currentToken)
	}
	if !strings.Contains(stdout.String(), "Error writing config") {
		t.Errorf("output missing config write error: %s", stdout.String())
	}
}

func TestRunInitRenderError(t *testing.T) {
	var stdout bytes.Buffer
	stubInitDeps(t, &stdout)

	oldTmpl := initConfigTmpl
	initConfigTmpl = template.Must(template.New("bad").Parse("{{ .Missing.Nested }}"))
	t.Cleanup(func() { initConfigTmpl = oldTmpl })

	code := runInit()
	if code != 1 {
		t.Fatalf("runInit() = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "Error generating config") {
		t.Errorf("output missing render error message: %s", stdout.String())
	}
}

func TestRunInitOverwriteConfirmed(t *testing.T) {
	var stdout bytes.Buffer
	var written bool
	stubInitDeps(t, &stdout)

	initStat = func(string) (os.FileInfo, error) { return nil, nil }
	initConfirmOverwrite = func(string) (bool, error) { return true, nil }
	initWriteFile = func(string, []byte, os.FileMode) error {
		written = true
		return nil
	}

	code := runInit()
	if code != 0 {
		t.Fatalf("runInit() = %d, want 0", code)
	}
	if !written {
		t.Error("expected file to be written when overwrite is confirmed")
	}
	if !strings.Contains(stdout.String(), "Configuration saved successfully") {
		t.Errorf("output missing success message: %s", stdout.String())
	}
}

func TestRunInitOverwriteDeclined(t *testing.T) {
	var stdout bytes.Buffer
	var written bool
	stubInitDeps(t, &stdout)

	initStat = func(string) (os.FileInfo, error) { return nil, nil }
	initConfirmOverwrite = func(string) (bool, error) { return false, nil }
	initWriteFile = func(string, []byte, os.FileMode) error {
		written = true
		return nil
	}

	code := runInit()
	if code != 0 {
		t.Fatalf("runInit() = %d, want 0", code)
	}
	if written {
		t.Error("expected file NOT to be written when overwrite is declined")
	}
	if !strings.Contains(stdout.String(), "Keeping existing config") {
		t.Errorf("output missing keep message: %s", stdout.String())
	}
}

func TestRunInitOverwriteFormError(t *testing.T) {
	var stdout bytes.Buffer
	stubInitDeps(t, &stdout)

	initStat = func(string) (os.FileInfo, error) { return nil, nil }
	initConfirmOverwrite = func(string) (bool, error) { return false, os.ErrClosed }

	code := runInit()
	if code != 1 {
		t.Fatalf("runInit() = %d, want 1", code)
	}
}

func TestConfirmOverwrite(t *testing.T) {
	oldRunForm := initRunForm
	t.Cleanup(func() { initRunForm = oldRunForm })

	initRunForm = func(f *huh.Form) error { return nil }

	got, err := confirmOverwrite("/etc/ratatosk/ratatosk.yaml")
	if err != nil {
		t.Fatalf("confirmOverwrite() error = %v", err)
	}
	if got {
		t.Error("confirmOverwrite() = true, want false (default)")
	}
}

func TestConfirmOverwriteError(t *testing.T) {
	oldRunForm := initRunForm
	t.Cleanup(func() { initRunForm = oldRunForm })

	initRunForm = func(f *huh.Form) error { return os.ErrClosed }

	_, err := confirmOverwrite("/etc/ratatosk/ratatosk.yaml")
	if err == nil {
		t.Fatal("confirmOverwrite() error = nil, want error")
	}
}
