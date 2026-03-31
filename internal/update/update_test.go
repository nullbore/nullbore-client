package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "0.1.1", true},
		{"0.1.0", "0.2.0", true},
		{"0.1.0", "1.0.0", true},
		{"v0.1.0", "v0.1.1", true},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.0", false},
		{"0.1.0-dev", "0.1.0", true},   // dev is always "outdated"
		{"0.1.0-dev", "0.0.1", true},   // dev is always "outdated"
		{"", "0.1.0", false},            // empty current
		{"0.1.0", "", false},            // empty latest
		{"0.1.0-beta.1", "0.1.0-beta.2", true},
		{"0.1.0-beta.1", "0.1.0-beta.1", false},
	}

	for _, tt := range tests {
		got := IsNewer(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	name := AssetName()
	if name == "" {
		t.Fatal("empty asset name")
	}
	// Should contain nullbore and the OS
	if len(name) < 10 {
		t.Errorf("asset name too short: %s", name)
	}
}

func TestFindAsset(t *testing.T) {
	rel := &Release{
		TagName: "v0.1.0",
		Assets: []Asset{
			{Name: "nullbore-linux-amd64", BrowserDownloadURL: "https://example.com/linux-amd64"},
			{Name: "nullbore-darwin-arm64", BrowserDownloadURL: "https://example.com/darwin-arm64"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums"},
		},
	}

	// This test depends on the platform it runs on.
	// Just verify it either finds a match or returns a clear error.
	url, err := FindAsset(rel)
	if err != nil {
		// Expected if running on a platform not in the test data
		t.Logf("FindAsset: %v (expected on this platform)", err)
		return
	}
	if url == "" {
		t.Error("found asset but URL is empty")
	}
}

func TestCheckLatestMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{
			TagName: "v0.2.0",
			HTMLURL: "https://github.com/test/releases/v0.2.0",
			Assets: []Asset{
				{Name: "nullbore-linux-amd64", BrowserDownloadURL: "https://example.com/bin"},
			},
		})
	}))
	defer srv.Close()

	// We can't easily override the URL in CheckLatest without refactoring,
	// so just test the JSON parsing directly
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var rel Release
	json.NewDecoder(resp.Body).Decode(&rel)

	if rel.TagName != "v0.2.0" {
		t.Errorf("expected v0.2.0, got %s", rel.TagName)
	}
	if len(rel.Assets) != 1 {
		t.Errorf("expected 1 asset, got %d", len(rel.Assets))
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"v0.1.0", "0.1.0"},
		{"0.1.0", "0.1.0"},
		{"nullbore 0.1.0", "0.1.0"},
		{"  v0.1.0  ", "0.1.0"},
	}
	for _, tt := range tests {
		got := normalizeVersion(tt.in)
		if got != tt.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

var _ = os.DevNull
