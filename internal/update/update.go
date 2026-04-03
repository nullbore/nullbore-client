// Package update handles version checking and self-updating.
package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	Repo          = "nullbore/nullbore-client"
	latestURL     = "https://api.github.com/repos/" + Repo + "/releases/latest"
	allReleasesURL = "https://api.github.com/repos/" + Repo + "/releases?per_page=10"
)

// Release represents a GitHub release.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
	HTMLURL string  `json:"html_url"`
}

// Asset represents a release binary.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// CheckLatest fetches the latest release from GitHub.
// Tries /releases/latest first (stable releases), then falls back to
// /releases?per_page=1 which includes pre-releases.
func CheckLatest() (*Release, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	// Try stable release first
	rel, err := fetchRelease(client, latestURL)
	if err == nil {
		return rel, nil
	}

	// Fall back to newest release (includes pre-releases)
	rels, err := fetchReleaseList(client, allReleasesURL)
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return nil, fmt.Errorf("no releases found")
	}
	// Find the highest version (GitHub order isn't always by version)
	best := 0
	for i := 1; i < len(rels); i++ {
		if compareVersions(normalizeVersion(rels[i].TagName), normalizeVersion(rels[best].TagName)) > 0 {
			best = i
		}
	}
	return &rels[best], nil
}

func fetchRelease(client *http.Client, url string) (*Release, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("parsing release: %w", err)
	}
	return &rel, nil
}

func fetchReleaseList(client *http.Client, url string) ([]Release, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rels []Release
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, fmt.Errorf("parsing releases: %w", err)
	}
	return rels, nil
}

// IsNewer returns true if the release version is newer than current.
// Handles semver with pre-release tags like beta.N correctly.
func IsNewer(current, latest string) bool {
	current = normalizeVersion(current)
	latest = normalizeVersion(latest)

	if current == "" || latest == "" {
		return false
	}

	// Don't consider dev builds as "up to date"
	if strings.Contains(current, "dev") {
		return true
	}

	return latest != current && compareVersions(latest, current) > 0
}

// compareVersions compares two version strings.
// Returns >0 if a > b, <0 if a < b, 0 if equal.
// Handles versions like "0.1.0-beta.10" correctly.
func compareVersions(a, b string) int {
	partsA := splitVersion(a)
	partsB := splitVersion(b)

	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := 0; i < maxLen; i++ {
		var pA, pB string
		if i < len(partsA) {
			pA = partsA[i]
		}
		if i < len(partsB) {
			pB = partsB[i]
		}

		// Try numeric comparison first
		nA, errA := strconv.Atoi(pA)
		nB, errB := strconv.Atoi(pB)
		if errA == nil && errB == nil {
			if nA != nB {
				return nA - nB
			}
			continue
		}

		// String comparison for non-numeric parts
		if pA != pB {
			if pA < pB {
				return -1
			}
			return 1
		}
	}
	return 0
}

// splitVersion splits a version string into comparable parts.
// "0.1.0-beta.10" → ["0", "1", "0", "beta", "10"]
func splitVersion(v string) []string {
	// Replace - and . with a common delimiter
	v = strings.ReplaceAll(v, "-", ".")
	return strings.Split(v, ".")
}

// AssetName returns the expected binary name for this platform.
func AssetName() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("nullbore-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

// FindAsset finds the download URL for the current platform.
func FindAsset(rel *Release) (string, error) {
	want := AssetName()
	for _, a := range rel.Assets {
		if a.Name == want {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no binary found for %s/%s (looked for %s)", runtime.GOOS, runtime.GOARCH, want)
}

// Download fetches a URL to a temp file and returns the path.
func Download(url string) (string, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "nullbore-update-*")
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()

	if err := os.Chmod(tmp.Name(), 0755); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	return tmp.Name(), nil
}

// ReplaceBinary replaces the current binary with the downloaded one.
func ReplaceBinary(newPath string) error {
	currentBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}

	// Resolve symlinks
	currentBin, err = resolveLinks(currentBin)
	if err != nil {
		return err
	}

	// Rename current binary as backup
	backupPath := currentBin + ".bak"
	os.Remove(backupPath) // ignore error

	if err := os.Rename(currentBin, backupPath); err != nil {
		return fmt.Errorf("backing up current binary: %w", err)
	}

	if err := moveFile(newPath, currentBin); err != nil {
		// Try to restore backup
		os.Rename(backupPath, currentBin)
		return fmt.Errorf("installing new binary: %w", err)
	}

	// Clean up backup
	os.Remove(backupPath)

	return nil
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "nullbore ")
	return v
}

// moveFile moves src to dst, falling back to copy+remove if rename fails
// (e.g. cross-device link when /tmp is a different filesystem).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Fall back to copy
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	out.Close()
	os.Remove(src)
	return nil
}

func resolveLinks(path string) (string, error) {
	resolved, err := os.Readlink(path)
	if err != nil {
		return path, nil // not a symlink
	}
	return resolved, nil
}
