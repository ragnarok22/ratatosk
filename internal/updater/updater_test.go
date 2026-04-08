package updater

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
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
