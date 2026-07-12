package updater

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minio/selfupdate"
)

const repoOwner = "ragnarok22"
const repoName = "ratatosk"

const (
	foregroundUpdateTimeout = 2 * time.Minute
	maxUpdateSize           = int64(128 << 20)
	maxChecksumSize         = int64(1 << 20)
)

type githubRelease struct {
	TagName string `json:"tag_name"`
}

var (
	updaterFetchLatest  = fetchLatestVersion
	updaterExecutable   = os.Executable
	updaterEvalSymlinks = filepath.EvalSymlinks
	foregroundClient    = &http.Client{Timeout: foregroundUpdateTimeout, CheckRedirect: checkUpdateRedirect}
	updaterHTTPGet      = foregroundClient.Get
	updaterApplyUpdate  = func(r io.Reader) error {
		return selfupdate.Apply(r, selfupdate.Options{})
	}
)

func checkUpdateRedirect(req *http.Request, _ []*http.Request) error {
	if req.URL.Scheme != "https" || !trustedUpdateHost(req.URL) {
		return fmt.Errorf("refusing update redirect to %s", req.URL.Redacted())
	}
	return nil
}

func trustedUpdateHost(target *url.URL) bool {
	host := strings.ToLower(target.Hostname())
	return host == "github.com" || host == "objects.githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// isHomebrewPath reports whether the given executable path belongs to a
// Homebrew (or Linuxbrew) installation.
func isHomebrewPath(execPath string) bool {
	return strings.Contains(execPath, "/Cellar/") ||
		strings.Contains(execPath, "/homebrew/") ||
		strings.Contains(execPath, "/linuxbrew/") ||
		strings.Contains(execPath, "/.linuxbrew/")
}

// compareVersions compares two semver strings (with or without "v" prefix).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareVersions(a, b string) (int, error) {
	pa, err := parseVersion(a)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", a, err)
	}
	pb, err := parseVersion(b)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", b, err)
	}
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1, nil
		}
		if pa[i] > pb[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersion(v string) ([3]int, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("expected major.minor.patch, got %q", v)
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, err
		}
		out[i] = n
	}
	return out, nil
}

// buildAssetURL returns the GitHub release download URL for the given tag
// and the current OS/architecture.
func buildAssetURL(tag string) string {
	return fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/%s/%s",
		repoOwner, repoName, tag, buildAssetName(),
	)
}

func buildAssetName() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("ratatosk-cli-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

func buildChecksumURL(tag string) string {
	return fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/%s/checksums.txt",
		repoOwner, repoName, tag,
	)
}

func readAtMost(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("download exceeds maximum size of %d bytes", max)
	}
	return data, nil
}

func readResponseAtMost(resp *http.Response, max int64) ([]byte, error) {
	if resp.ContentLength > max {
		return nil, fmt.Errorf("download exceeds maximum size of %d bytes", max)
	}
	return readAtMost(resp.Body, max)
}

func checksumForAsset(data []byte, assetName string) ([sha256.Size]byte, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != assetName {
			continue
		}

		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return [sha256.Size]byte{}, fmt.Errorf("invalid SHA-256 checksum for %s", assetName)
		}
		var checksum [sha256.Size]byte
		copy(checksum[:], decoded)
		return checksum, nil
	}
	return [sha256.Size]byte{}, fmt.Errorf("SHA-256 checksum for %s not found", assetName)
}

// fetchLatestVersion queries the GitHub API for the latest release tag.
func fetchLatestVersion(client *http.Client) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	return fetchLatestVersionFromURL(client, url)
}

// fetchLatestVersionFromURL is the underlying implementation that accepts a
// custom URL, making it testable with httptest.
func fetchLatestVersionFromURL(client *http.Client, url string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to reach GitHub API: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// handled below
	case http.StatusForbidden:
		return "", fmt.Errorf("GitHub API rate limit exceeded; try again later or set GITHUB_TOKEN")
	case http.StatusNotFound:
		return "", fmt.Errorf("no releases found for %s/%s", repoOwner, repoName)
	default:
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to parse release response: %w", err)
	}
	if release.TagName == "" {
		return "", fmt.Errorf("release response missing tag_name")
	}
	return release.TagName, nil
}

// UpdateCLI checks for a newer release on GitHub and replaces the current
// binary in-place. It aborts if the binary was installed via Homebrew.
func UpdateCLI(currentVersion string) error {
	if currentVersion == "dev" {
		fmt.Println("You are running a development build. Cannot check for updates.")
		return nil
	}

	latest, err := updaterFetchLatest(foregroundClient)
	if err != nil {
		return err
	}

	cmp, err := compareVersions(currentVersion, latest)
	if err != nil {
		return fmt.Errorf("version comparison failed: %w", err)
	}
	if cmp >= 0 {
		fmt.Printf("You are up to date (version %s).\n", currentVersion)
		return nil
	}

	// An update is available — check installation method before proceeding.
	execPath, err := updaterExecutable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}
	resolved, err := updaterEvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}
	if isHomebrewPath(resolved) {
		fmt.Printf("Update available (%s), but Ratatosk was installed via Homebrew.\nPlease run: brew upgrade ratatosk\n", latest)
		return nil
	}

	// Download and apply the update.
	assetURL := buildAssetURL(latest)
	fmt.Printf("Downloading %s ...\n", latest)

	resp, err := updaterHTTPGet(assetURL)
	if err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("release asset not found for %s/%s (url: %s)", runtime.GOOS, runtime.GOARCH, assetURL)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download update: HTTP %d", resp.StatusCode)
	}

	payload, err := readResponseAtMost(resp, maxUpdateSize)
	if err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}

	checksumURL := buildChecksumURL(latest)
	checksumResp, err := updaterHTTPGet(checksumURL)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}
	defer checksumResp.Body.Close()

	if checksumResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download checksums: HTTP %d", checksumResp.StatusCode)
	}
	checksumData, err := readResponseAtMost(checksumResp, maxChecksumSize)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}
	expectedChecksum, err := checksumForAsset(checksumData, buildAssetName())
	if err != nil {
		return fmt.Errorf("failed to verify update: %w", err)
	}
	actualChecksum := sha256.Sum256(payload)
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("failed to verify update: SHA-256 checksum mismatch for %s", buildAssetName())
	}

	if err := updaterApplyUpdate(bytes.NewReader(payload)); err != nil {
		return fmt.Errorf("failed to apply update: %w", err)
	}

	fmt.Printf("Updated from %s to %s.\n", currentVersion, latest)
	return nil
}

const (
	// checkInterval is the minimum time between remote update checks.
	checkInterval = 8 * time.Hour
	// updateCheckTimeout is the HTTP timeout for the background version check.
	updateCheckTimeout = 3 * time.Second
	// cacheDirPerm is the permission mode for the cache directory.
	cacheDirPerm = 0o755
	// cacheFilePerm is the permission mode for the cache file.
	cacheFilePerm = 0o644
)

// updateCache is the on-disk format for the update check cache.
type updateCache struct {
	LastCheck     time.Time `json:"last_check"`
	LatestVersion string    `json:"latest_version"`
}

var (
	updaterUserCacheDir = os.UserCacheDir
	updaterTimeNow      = time.Now
	updateCheckMu       sync.Mutex
)

// cacheFilePath returns the path to the update check cache file.
func cacheFilePath() (string, error) {
	dir, err := updaterUserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ratatosk", "update-check.json"), nil
}

// readCache loads the cached update check result. Returns a zero-value
// cache if the file is missing or unreadable.
func readCache() updateCache {
	path, err := cacheFilePath()
	if err != nil {
		return updateCache{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return updateCache{}
	}
	var c updateCache
	if json.Unmarshal(data, &c) != nil {
		return updateCache{}
	}
	return c
}

// writeCache persists the update check result to disk.
func writeCache(c updateCache) {
	path, err := cacheFilePath()
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, cacheDirPerm); err != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	temp, err := os.CreateTemp(dir, ".update-check-*")
	if err != nil {
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(cacheFilePerm); err != nil {
		temp.Close()
		return
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return
	}
	if err := temp.Close(); err != nil {
		return
	}
	_ = os.Rename(tempPath, path)
}

// CheckForUpdate checks if a newer version is available on GitHub.
// Results are cached for checkInterval to avoid hitting the API on every
// invocation. Returns the latest version tag if an update is available,
// or an empty string if the current version is up to date or cannot be
// determined.
func CheckForUpdate(currentVersion string) string {
	if currentVersion == "dev" {
		return ""
	}
	updateCheckMu.Lock()
	defer updateCheckMu.Unlock()

	now := updaterTimeNow()
	cached := readCache()
	if !cached.LastCheck.IsZero() && now.Sub(cached.LastCheck) < checkInterval {
		if cached.LatestVersion == "" {
			return ""
		}
		cmp, err := compareVersions(currentVersion, cached.LatestVersion)
		if err != nil {
			return ""
		}
		if cmp < 0 {
			return cached.LatestVersion
		}
		return ""
	}

	client := &http.Client{Timeout: updateCheckTimeout}
	latest, err := updaterFetchLatest(client)
	if err != nil {
		return ""
	}

	writeCache(updateCache{LastCheck: now, LatestVersion: latest})

	cmp, err := compareVersions(currentVersion, latest)
	if err != nil {
		return ""
	}
	if cmp < 0 {
		return latest
	}
	return ""
}
