package version

// Set via ldflags at build time.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"

	// AssetTemplate is the GitHub release asset filename pattern for the
	// running platform. The literal "{V}" is replaced with the new version
	// string (with the "v" prefix, e.g., "v0.13.5") at update-check time.
	//
	// Examples baked in by the build workflow:
	//   thefeed-client-{V}-linux-amd64
	//   thefeed-client-{V}-windows-amd64.exe
	//   thefeed-client-android-arm64        (no version — Android client)
	//
	// The Android-APK case is detected at runtime in internal/update and
	// overrides this with thefeed-android-{V}-{abi}.apk because the same
	// client binary ships both inside the APK and as a Termux download.
	AssetTemplate = ""
)
