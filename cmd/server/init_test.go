package main

import (
	"bytes"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/spf13/viper"
)

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

	data, err := renderConfig(answers)
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

	data, err := renderConfig(answers)
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
	// TLS fields should not be present
	if got := v.GetString("tls_email"); got != "" {
		t.Errorf("tls_email = %q, want empty", got)
	}
}

func TestRenderConfigPortRange(t *testing.T) {
	answers := initAnswers{BaseDomain: "test.dev", TLSAuto: false}

	data, err := renderConfig(answers)
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
	oldConfirmOverwrite := initConfirmOverwrite
	t.Cleanup(func() {
		initStdout = oldStdout
		initGetEUID = oldGetEUID
		initWriteFile = oldWriteFile
		initMkdirAll = oldMkdirAll
		initStat = oldStat
		initRunForm = oldRunForm
		initConfirmOverwrite = oldConfirmOverwrite
	})
	initStdout = stdout
	initGetEUID = func() int { return 1000 }
	initMkdirAll = func(string, os.FileMode) error { return nil }
	initStat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	initWriteFile = func(string, []byte, os.FileMode) error { return nil }
	initRunForm = func(*huh.Form) error { return nil }
	initConfirmOverwrite = func(string) (bool, error) { return false, nil }
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
