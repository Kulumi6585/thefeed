package update

import (
	"runtime"
	"strings"
	"testing"

	"github.com/sartoopjj/thefeed/internal/version"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.13.5", "v0.13.4", true},
		{"v0.13.5", "0.13.4", true},
		{"0.13.5", "v0.13.4", true},
		{"v0.13.5", "v0.13.5", false},
		{"v0.13.4", "v0.13.5", false},
		{"v1.0.0", "v0.99.99", true},
		{"v0.13.5", "dev", false},
		{"", "v0.13.5", false},
		{"v0.13.5", "", false},
		{"v0.13.5-rc1", "v0.13.4", true},
		{"v0.13.5", "v0.13.5-rc1", false}, // numeric parts equal → not newer
	}
	for _, c := range cases {
		if got := IsNewer(c.latest, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestAssetURLFromTemplate(t *testing.T) {
	old := version.AssetTemplate
	defer func() { version.AssetTemplate = old }()

	version.AssetTemplate = "thefeed-client-{V}-linux-amd64"
	url := AssetURL("v0.13.5")
	want := BaseURL + "/thefeed-client-v0.13.5-linux-amd64"
	if url != want {
		t.Errorf("got %q, want %q", url, want)
	}

	version.AssetTemplate = "thefeed-client-{V}-windows-amd64.exe"
	url = AssetURL("v0.14.0")
	want = BaseURL + "/thefeed-client-v0.14.0-windows-amd64.exe"
	if url != want {
		t.Errorf("got %q, want %q", url, want)
	}

	// Unversioned template (Android client binary) — {V} not present,
	// substitution should be a no-op.
	version.AssetTemplate = "thefeed-client-android-arm64"
	url = AssetURL("v0.13.5")
	want = BaseURL + "/thefeed-client-android-arm64"
	if url != want {
		t.Errorf("got %q, want %q", url, want)
	}
}

func TestAssetURLFallback(t *testing.T) {
	old := version.AssetTemplate
	defer func() { version.AssetTemplate = old }()
	version.AssetTemplate = ""

	url := AssetURL("v0.13.5")
	if url == "" {
		t.Fatal("expected non-empty URL even without AssetTemplate")
	}
	if !strings.HasPrefix(url, BaseURL+"/") {
		t.Errorf("URL %q missing base prefix", url)
	}
	// Should at minimum mention the running OS.
	if !strings.Contains(url, runtime.GOOS) && runtime.GOOS != "android" {
		t.Errorf("URL %q should mention %q", url, runtime.GOOS)
	}
}
