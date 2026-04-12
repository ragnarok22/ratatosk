package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.0.1", "v1.0.0", 1},
		{"v0.9.9", "v1.0.0", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.2.3", "v1.2.10", -1},
		{"v1.10.0", "v1.9.0", 1},
		{"1.0.0", "1.0.0", 0},   // without v prefix
		{"v1.0.0", "1.0.1", -1}, // mixed prefix
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_vs_%s", tt.a, tt.b), func(t *testing.T) {
			got, err := compareVersions(tt.a, tt.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCompareVersionsInvalid(t *testing.T) {
	tests := []struct {
		a, b string
	}{
		{"not-a-version", "v1.0.0"},
		{"v1.0.0", "v1.0"},
		{"v1.0.0", ""},
		{"v1.0.abc", "v1.0.0"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_vs_%s", tt.a, tt.b), func(t *testing.T) {
			_, err := compareVersions(tt.a, tt.b)
			if err == nil {
				t.Errorf("expected error for compareVersions(%q, %q)", tt.a, tt.b)
			}
		})
	}
}

func TestIsHomebrewPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/opt/homebrew/Cellar/ratatosk/1.0.0/bin/ratatosk", true},
		{"/usr/local/Cellar/ratatosk/1.0.0/bin/ratatosk", true},
		{"/home/user/.linuxbrew/bin/ratatosk", true},
		{"/opt/homebrew/bin/ratatosk", true},
		{"/usr/local/bin/ratatosk", false},
		{"/home/user/bin/ratatosk", false},
		{"/tmp/ratatosk", false},
		{"C:\\Users\\user\\ratatosk.exe", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isHomebrewPath(tt.path)
			if got != tt.want {
				t.Errorf("isHomebrewPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestBuildAssetURL(t *testing.T) {
	url := buildAssetURL("v1.2.3")
	expected := fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/v1.2.3/ratatosk-cli-%s-%s",
		repoOwner, repoName, runtime.GOOS, runtime.GOARCH,
	)
	if runtime.GOOS == "windows" {
		expected += ".exe"
	}
	if url != expected {
		t.Errorf("buildAssetURL(\"v1.2.3\") =\n  %s\nwant\n  %s", url, expected)
	}
}

func TestFetchLatestVersion(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(githubRelease{TagName: "v1.5.0"})
		}))
		defer srv.Close()

		// Override the fetch by calling the server directly.
		got, err := fetchLatestVersionFromURL(srv.Client(), srv.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "v1.5.0" {
			t.Errorf("got %q, want %q", got, "v1.5.0")
		}
	})

	t.Run("rate_limit", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		_, err := fetchLatestVersionFromURL(srv.Client(), srv.URL)
		if err == nil {
			t.Fatal("expected error for rate limit")
		}
	})

	t.Run("not_found", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		_, err := fetchLatestVersionFromURL(srv.Client(), srv.URL)
		if err == nil {
			t.Fatal("expected error for not found")
		}
	})

	t.Run("empty_tag", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(githubRelease{TagName: ""})
		}))
		defer srv.Close()

		_, err := fetchLatestVersionFromURL(srv.Client(), srv.URL)
		if err == nil {
			t.Fatal("expected error for empty tag")
		}
	})
}

// transportFunc implements http.RoundTripper for test transport stubbing.
type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// stubTransport replaces http.DefaultTransport so all HTTP calls are routed
// to srv. Returns a restore function that must be deferred.
func stubTransport(t *testing.T, srv *httptest.Server) func() {
	t.Helper()
	orig := http.DefaultTransport
	http.DefaultTransport = transportFunc(func(req *http.Request) (*http.Response, error) {
		req2 := req.Clone(req.Context())
		req2.URL.Scheme = "http"
		req2.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return orig.RoundTrip(req2)
	})
	return func() { http.DefaultTransport = orig }
}

// stubCache points the cache directory at a temp dir and returns a
// restore function. Tests that call CheckForUpdate must use this to
// avoid polluting the real user cache.
func stubCache(t *testing.T) {
	t.Helper()
	oldCacheDir := updaterUserCacheDir
	oldTimeNow := updaterTimeNow
	t.Cleanup(func() {
		updaterUserCacheDir = oldCacheDir
		updaterTimeNow = oldTimeNow
	})
	dir := t.TempDir()
	updaterUserCacheDir = func() (string, error) { return dir, nil }
	updaterTimeNow = time.Now
}

func TestCheckForUpdateDevBuild(t *testing.T) {
	if got := CheckForUpdate("dev"); got != "" {
		t.Fatalf("CheckForUpdate(\"dev\") = %q, want empty", got)
	}
}

func TestCheckForUpdateUpToDate(t *testing.T) {
	stubCache(t)
	oldFetchLatest := updaterFetchLatest
	t.Cleanup(func() { updaterFetchLatest = oldFetchLatest })

	updaterFetchLatest = func(*http.Client) (string, error) { return "v1.0.0", nil }

	if got := CheckForUpdate("v1.0.0"); got != "" {
		t.Fatalf("CheckForUpdate = %q, want empty", got)
	}
}

func TestCheckForUpdateAvailable(t *testing.T) {
	stubCache(t)
	oldFetchLatest := updaterFetchLatest
	t.Cleanup(func() { updaterFetchLatest = oldFetchLatest })

	updaterFetchLatest = func(*http.Client) (string, error) { return "v2.0.0", nil }

	if got := CheckForUpdate("v1.0.0"); got != "v2.0.0" {
		t.Fatalf("CheckForUpdate = %q, want %q", got, "v2.0.0")
	}
}

func TestCheckForUpdateFetchError(t *testing.T) {
	stubCache(t)
	oldFetchLatest := updaterFetchLatest
	t.Cleanup(func() { updaterFetchLatest = oldFetchLatest })

	updaterFetchLatest = func(*http.Client) (string, error) { return "", errors.New("network error") }

	if got := CheckForUpdate("v1.0.0"); got != "" {
		t.Fatalf("CheckForUpdate = %q, want empty on error", got)
	}
}

func TestCheckForUpdateBadRemoteVersion(t *testing.T) {
	stubCache(t)
	oldFetchLatest := updaterFetchLatest
	t.Cleanup(func() { updaterFetchLatest = oldFetchLatest })

	updaterFetchLatest = func(*http.Client) (string, error) { return "not-a-version", nil }

	if got := CheckForUpdate("v1.0.0"); got != "" {
		t.Fatalf("CheckForUpdate = %q, want empty for bad version", got)
	}
}

func TestCheckForUpdateUsesCache(t *testing.T) {
	stubCache(t)
	oldFetchLatest := updaterFetchLatest
	t.Cleanup(func() { updaterFetchLatest = oldFetchLatest })

	fetchCount := 0
	updaterFetchLatest = func(*http.Client) (string, error) {
		fetchCount++
		return "v2.0.0", nil
	}

	// First call should hit the network and cache.
	if got := CheckForUpdate("v1.0.0"); got != "v2.0.0" {
		t.Fatalf("first call = %q, want %q", got, "v2.0.0")
	}
	if fetchCount != 1 {
		t.Fatalf("fetchCount = %d, want 1", fetchCount)
	}

	// Second call within the interval should use cache.
	if got := CheckForUpdate("v1.0.0"); got != "v2.0.0" {
		t.Fatalf("second call = %q, want %q", got, "v2.0.0")
	}
	if fetchCount != 1 {
		t.Fatalf("fetchCount = %d, want 1 (should use cache)", fetchCount)
	}
}

func TestCheckForUpdateCacheExpired(t *testing.T) {
	stubCache(t)
	oldFetchLatest := updaterFetchLatest
	t.Cleanup(func() { updaterFetchLatest = oldFetchLatest })

	fetchCount := 0
	updaterFetchLatest = func(*http.Client) (string, error) {
		fetchCount++
		return "v2.0.0", nil
	}

	// First call — populates cache.
	CheckForUpdate("v1.0.0")
	if fetchCount != 1 {
		t.Fatalf("fetchCount = %d, want 1", fetchCount)
	}

	// Advance time past the check interval.
	updaterTimeNow = func() time.Time { return time.Now().Add(checkInterval + time.Minute) }

	// Should fetch again because the cache is stale.
	if got := CheckForUpdate("v1.0.0"); got != "v2.0.0" {
		t.Fatalf("after expiry = %q, want %q", got, "v2.0.0")
	}
	if fetchCount != 2 {
		t.Fatalf("fetchCount = %d, want 2", fetchCount)
	}
}

func TestCheckForUpdateCacheUpToDate(t *testing.T) {
	stubCache(t)
	oldFetchLatest := updaterFetchLatest
	t.Cleanup(func() { updaterFetchLatest = oldFetchLatest })

	updaterFetchLatest = func(*http.Client) (string, error) { return "v1.0.0", nil }

	// First call populates cache with same version.
	if got := CheckForUpdate("v1.0.0"); got != "" {
		t.Fatalf("first call = %q, want empty", got)
	}

	// Second call from cache should still return empty.
	updaterFetchLatest = func(*http.Client) (string, error) {
		t.Fatal("should not fetch when cache is fresh")
		return "", nil
	}

	if got := CheckForUpdate("v1.0.0"); got != "" {
		t.Fatalf("cached call = %q, want empty", got)
	}
}

func TestUpdateCLIDevBuild(t *testing.T) {
	if err := UpdateCLI("dev"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateCLIUpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(githubRelease{TagName: "v1.0.0"})
	}))
	defer srv.Close()
	defer stubTransport(t, srv)()

	if err := UpdateCLI("v1.0.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateCLIFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	defer stubTransport(t, srv)()

	if err := UpdateCLI("v1.0.0"); err == nil {
		t.Fatal("expected error when API returns 500")
	}
}

func TestUpdateCLIBadRemoteVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(githubRelease{TagName: "not-a-version"})
	}))
	defer srv.Close()
	defer stubTransport(t, srv)()

	err := UpdateCLI("v1.0.0")
	if err == nil {
		t.Fatal("expected error for invalid remote version")
	}
	if !strings.Contains(err.Error(), "version comparison failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateCLINewerVersionAssetNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "releases/latest") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(githubRelease{TagName: "v99.0.0"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	defer stubTransport(t, srv)()

	err := UpdateCLI("v1.0.0")
	if err == nil {
		t.Fatal("expected error for missing asset")
	}
	if !strings.Contains(err.Error(), "release asset not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateCLINewerVersionDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "releases/latest") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(githubRelease{TagName: "v99.0.0"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	defer stubTransport(t, srv)()

	err := UpdateCLI("v1.0.0")
	if err == nil {
		t.Fatal("expected error for download failure")
	}
	if !strings.Contains(err.Error(), "failed to download update") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateCLIDownloadNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(githubRelease{TagName: "v99.0.0"})
	}))
	defer srv.Close()

	orig := http.DefaultTransport
	http.DefaultTransport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "releases/download") {
			return nil, fmt.Errorf("simulated download error")
		}
		req2 := req.Clone(req.Context())
		req2.URL.Scheme = "http"
		req2.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return orig.RoundTrip(req2)
	})
	defer func() { http.DefaultTransport = orig }()

	err := UpdateCLI("v1.0.0")
	if err == nil {
		t.Fatal("expected error for download network failure")
	}
	if !strings.Contains(err.Error(), "failed to download update") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFetchLatestVersionFromURLTransportError(t *testing.T) {
	client := &http.Client{
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("simulated transport error")
		}),
	}
	_, err := fetchLatestVersionFromURL(client, "http://example.com/api")
	if err == nil {
		t.Fatal("expected error for transport failure")
	}
	if !strings.Contains(err.Error(), "failed to reach GitHub API") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFetchLatestVersionFromURLBadURL(t *testing.T) {
	_, err := fetchLatestVersionFromURL(http.DefaultClient, "://bad-url")
	if err == nil {
		t.Fatal("expected error for bad URL")
	}
}

func TestFetchLatestVersionFromURLServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := fetchLatestVersionFromURL(srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFetchLatestVersionFromURLInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not valid json"))
	}))
	defer srv.Close()

	_, err := fetchLatestVersionFromURL(srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "failed to parse") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateCLIExecutableError(t *testing.T) {
	oldFetchLatest := updaterFetchLatest
	oldExecutable := updaterExecutable
	t.Cleanup(func() {
		updaterFetchLatest = oldFetchLatest
		updaterExecutable = oldExecutable
	})

	updaterFetchLatest = func(*http.Client) (string, error) { return "v2.0.0", nil }
	updaterExecutable = func() (string, error) { return "", errors.New("boom") }

	err := UpdateCLI("v1.0.0")
	if err == nil {
		t.Fatal("expected executable lookup error")
	}
	if !strings.Contains(err.Error(), "failed to determine executable path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateCLIEvalSymlinksError(t *testing.T) {
	oldFetchLatest := updaterFetchLatest
	oldExecutable := updaterExecutable
	oldEvalSymlinks := updaterEvalSymlinks
	t.Cleanup(func() {
		updaterFetchLatest = oldFetchLatest
		updaterExecutable = oldExecutable
		updaterEvalSymlinks = oldEvalSymlinks
	})

	updaterFetchLatest = func(*http.Client) (string, error) { return "v2.0.0", nil }
	updaterExecutable = func() (string, error) { return "/tmp/ratatosk", nil }
	updaterEvalSymlinks = func(string) (string, error) { return "", errors.New("symlink failed") }

	err := UpdateCLI("v1.0.0")
	if err == nil {
		t.Fatal("expected symlink resolution error")
	}
	if !strings.Contains(err.Error(), "failed to resolve executable path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateCLIHomebrewInstall(t *testing.T) {
	oldFetchLatest := updaterFetchLatest
	oldExecutable := updaterExecutable
	oldEvalSymlinks := updaterEvalSymlinks
	oldHTTPGet := updaterHTTPGet
	t.Cleanup(func() {
		updaterFetchLatest = oldFetchLatest
		updaterExecutable = oldExecutable
		updaterEvalSymlinks = oldEvalSymlinks
		updaterHTTPGet = oldHTTPGet
	})

	downloadCalled := false
	updaterFetchLatest = func(*http.Client) (string, error) { return "v2.0.0", nil }
	updaterExecutable = func() (string, error) { return "/tmp/ratatosk", nil }
	updaterEvalSymlinks = func(string) (string, error) {
		return "/opt/homebrew/Cellar/ratatosk/2.0.0/bin/ratatosk", nil
	}
	updaterHTTPGet = func(string) (*http.Response, error) {
		downloadCalled = true
		return nil, errors.New("should not download homebrew install")
	}

	if err := UpdateCLI("v1.0.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if downloadCalled {
		t.Fatal("download should not be attempted for Homebrew install")
	}
}

func TestUpdateCLIApplyError(t *testing.T) {
	oldFetchLatest := updaterFetchLatest
	oldExecutable := updaterExecutable
	oldEvalSymlinks := updaterEvalSymlinks
	oldHTTPGet := updaterHTTPGet
	oldApplyUpdate := updaterApplyUpdate
	t.Cleanup(func() {
		updaterFetchLatest = oldFetchLatest
		updaterExecutable = oldExecutable
		updaterEvalSymlinks = oldEvalSymlinks
		updaterHTTPGet = oldHTTPGet
		updaterApplyUpdate = oldApplyUpdate
	})

	updaterFetchLatest = func(*http.Client) (string, error) { return "v2.0.0", nil }
	updaterExecutable = func() (string, error) { return "/tmp/ratatosk", nil }
	updaterEvalSymlinks = func(string) (string, error) { return "/tmp/ratatosk", nil }
	updaterHTTPGet = func(string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("payload")),
		}, nil
	}
	updaterApplyUpdate = func(r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(data) != "payload" {
			t.Fatalf("payload = %q, want %q", string(data), "payload")
		}
		return errors.New("apply failed")
	}

	err := UpdateCLI("v1.0.0")
	if err == nil {
		t.Fatal("expected apply error")
	}
	if !strings.Contains(err.Error(), "failed to apply update") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateCLISuccess(t *testing.T) {
	oldFetchLatest := updaterFetchLatest
	oldExecutable := updaterExecutable
	oldEvalSymlinks := updaterEvalSymlinks
	oldHTTPGet := updaterHTTPGet
	oldApplyUpdate := updaterApplyUpdate
	t.Cleanup(func() {
		updaterFetchLatest = oldFetchLatest
		updaterExecutable = oldExecutable
		updaterEvalSymlinks = oldEvalSymlinks
		updaterHTTPGet = oldHTTPGet
		updaterApplyUpdate = oldApplyUpdate
	})

	applied := false
	updaterFetchLatest = func(*http.Client) (string, error) { return "v2.0.0", nil }
	updaterExecutable = func() (string, error) { return "/tmp/ratatosk", nil }
	updaterEvalSymlinks = func(string) (string, error) { return "/tmp/ratatosk", nil }
	updaterHTTPGet = func(string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("payload")),
		}, nil
	}
	updaterApplyUpdate = func(r io.Reader) error {
		applied = true
		_, err := io.ReadAll(r)
		return err
	}

	if err := UpdateCLI("v1.0.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !applied {
		t.Fatal("update payload was not applied")
	}
}
