// Package update talks to the public thefeed-files repo to find out
// whether a newer client is available, and hands the frontend a
// platform-correct download URL.
//
// This is independent of the in-protocol /api/version-check flow that
// reads the version from the server over DNS — that path requires a
// configured profile, while this one works as soon as the binary boots
// and only needs plain HTTPS reachability.
package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sartoopjj/thefeed/internal/version"
)

// BaseURL is the directory the release assets live under. The "raw"
// path resolves to the actual file bytes; pointing the user's browser
// here triggers a download.
const BaseURL = "https://github.com/sartoopjj/thefeed-files/raw/main/clients"

// VersionURL returns the plain-text VERSION file. Plain raw.githubusercontent
// host avoids the HTML wrapper github.com puts around blob views.
const VersionURL = "https://raw.githubusercontent.com/sartoopjj/thefeed-files/main/clients/VERSION"

// Status is the JSON returned to the frontend.
type Status struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	HasUpdate   bool   `json:"hasUpdate"`
	DownloadURL string `json:"downloadURL"`
}

// httpClient is shared so we get connection reuse between repeated
// background checks. 30s is plenty for a 16-byte text file.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// Check fetches the VERSION file and assembles a Status for the running
// platform. Errors are returned to the caller — the frontend decides
// whether to surface them or stay quiet.
func Check(ctx context.Context) (Status, error) {
	s := Status{Current: version.Version}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, VersionURL, nil)
	if err != nil {
		return s, err
	}
	// GitHub's raw host doesn't require a UA but rejects empty Accept
	// occasionally; set both defensively.
	req.Header.Set("User-Agent", "thefeed-client/"+version.Version)
	req.Header.Set("Accept", "text/plain")

	resp, err := httpClient.Do(req)
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return s, fmt.Errorf("VERSION fetch: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return s, err
	}
	s.Latest = strings.TrimSpace(string(body))
	if s.Latest == "" {
		return s, fmt.Errorf("VERSION file empty")
	}
	s.HasUpdate = IsNewer(s.Latest, s.Current)
	s.DownloadURL = AssetURL(s.Latest)
	return s, nil
}

// AssetURL builds the download URL for the running platform at the
// requested version. Falls back to a runtime-derived template if
// AssetTemplate wasn't injected at build time (e.g., `go run`).
func AssetURL(latest string) string {
	tmpl := version.AssetTemplate
	if isAndroidAPK() {
		// APK wrapper takes priority over the bare client binary —
		// users who installed the APK should update the APK.
		tmpl = androidAPKTemplate()
	}
	if tmpl == "" {
		tmpl = defaultTemplate()
	}
	if tmpl == "" {
		return ""
	}
	name := strings.ReplaceAll(tmpl, "{V}", strings.TrimSpace(latest))
	return BaseURL + "/" + name
}

// IsNewer compares semver-ish version strings, tolerating the "v" prefix
// and numeric pre-release suffixes. Returns false if either side is "dev".
func IsNewer(latest, current string) bool {
	a := strings.TrimPrefix(strings.TrimSpace(latest), "v")
	b := strings.TrimPrefix(strings.TrimSpace(current), "v")
	if a == "" || b == "" {
		return false
	}
	if b == "dev" {
		// `go run` / unreleased build — never nag.
		return false
	}
	if a == b {
		return false
	}
	as := strings.Split(stripPre(a), ".")
	bs := strings.Split(stripPre(b), ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai, bi := 0, 0
		if i < len(as) {
			ai, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(bs[i])
		}
		if ai > bi {
			return true
		}
		if ai < bi {
			return false
		}
	}
	return false
}

func stripPre(v string) string {
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		return v[:i]
	}
	return v
}

// isAndroidAPK returns true when this binary is running inside the
// Android APK wrapper rather than as a standalone Termux/CLI client.
// Two signals are checked:
//   - THEFEED_ANDROID_APK=1 set by ThefeedService.kt before exec.
//   - The executable path lives under com.thefeed.android's
//     nativeLibraryDir, which always contains "com.thefeed.android".
func isAndroidAPK() bool {
	if runtime.GOOS != "android" {
		return false
	}
	if os.Getenv("THEFEED_ANDROID_APK") == "1" {
		return true
	}
	if exe, err := os.Executable(); err == nil {
		if strings.Contains(exe, "com.thefeed.android") {
			return true
		}
	}
	return false
}

// androidAPKTemplate returns the asset name for the user-facing APK
// (not the raw client binary) at version "{V}".
func androidAPKTemplate() string {
	abi := "arm64-v8a"
	if runtime.GOARCH == "arm" {
		abi = "armeabi-v7a"
	}
	return "thefeed-android-{V}-" + abi + ".apk"
}

// defaultTemplate is the fallback used when AssetTemplate wasn't
// injected by ldflags. Mirrors the matrix in .github/workflows/build.yml.
func defaultTemplate() string {
	switch runtime.GOOS {
	case "android":
		return "thefeed-client-android-" + runtime.GOARCH
	case "windows":
		return "thefeed-client-{V}-windows-" + runtime.GOARCH + ".exe"
	default:
		return "thefeed-client-{V}-" + runtime.GOOS + "-" + runtime.GOARCH
	}
}
