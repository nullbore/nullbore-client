// Package update handles version checking and self-updating.
package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	Repo       = "nullbore/nullbore-client"
	releaseURL = "https://api.github.com/repos/" + Repo + "/releases/latest"
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
func CheckLatest() (*Release, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(releaseURL)
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

// IsNewer returns true if the release version is newer than current.
// Simple string comparison after normalizing — works for semver tags.
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

	return latest != current && latest > current
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

	if err := os.Rename(newPath, currentBin); err != nil {
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

func resolveLinks(path string) (string, error) {
	resolved, err := os.Readlink(path)
	if err != nil {
		return path, nil // not a symlink
	}
	return resolved, nil
}
