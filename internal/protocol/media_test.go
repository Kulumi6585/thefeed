package protocol

import (
	"strings"
	"testing"
)

func TestEncodeMediaTextRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		meta    MediaMeta
		caption string
	}{
		{
			name: "image with caption",
			meta: MediaMeta{
				Tag:          MediaImage,
				Size:         123456,
				Downloadable: true,
				Channel:      12345,
				Blocks:       42,
				CRC32:        0xabcdef01,
			},
			caption: "hello world\nmulti-line",
		},
		{
			name: "file with filename",
			meta: MediaMeta{
				Tag:          MediaFile,
				Size:         800,
				Downloadable: true,
				Channel:      MediaChannelStart,
				Blocks:       2,
				CRC32:        0,
				Filename:     "report.zip",
			},
			caption: "",
		},
		{
			name: "filename strips path traversal",
			meta: MediaMeta{
				Tag:          MediaFile,
				Size:         100,
				Downloadable: true,
				Channel:      MediaChannelStart + 1,
				Blocks:       1,
				CRC32:        0xdeadbeef,
				// Server-side sanitisation strips dirs, control chars, and ":"
				// before the metadata reaches the wire — so a parsed filename
				// is never going to contain any of those.
				Filename: "/tmp/../etc/passwd:bad\nname",
			},
			caption: "",
		},
		{
			name: "non-downloadable image",
			meta: MediaMeta{
				Tag:          MediaImage,
				Size:         50_000_000,
				Downloadable: false,
				Channel:      0,
				Blocks:       0,
				CRC32:        0xdeadbeef,
			},
			caption: "too big",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := EncodeMediaText(tc.meta, tc.caption)
			meta, caption, ok := ParseMediaText(body)
			if !ok {
				t.Fatalf("ParseMediaText returned ok=false for body %q", body)
			}
			if caption != tc.caption {
				t.Fatalf("caption = %q, want %q", caption, tc.caption)
			}
			if meta.Tag != tc.meta.Tag {
				t.Fatalf("Tag = %q, want %q", meta.Tag, tc.meta.Tag)
			}
			if meta.Size != tc.meta.Size {
				t.Fatalf("Size = %d, want %d", meta.Size, tc.meta.Size)
			}
			if meta.Downloadable != tc.meta.Downloadable {
				t.Fatalf("Downloadable = %v, want %v", meta.Downloadable, tc.meta.Downloadable)
			}
			if meta.Channel != tc.meta.Channel {
				t.Fatalf("Channel = %d, want %d", meta.Channel, tc.meta.Channel)
			}
			if meta.Blocks != tc.meta.Blocks {
				t.Fatalf("Blocks = %d, want %d", meta.Blocks, tc.meta.Blocks)
			}
			if meta.CRC32 != tc.meta.CRC32 {
				t.Fatalf("CRC32 = %x, want %x", meta.CRC32, tc.meta.CRC32)
			}
			wantFilename := SanitiseMediaFilename(tc.meta.Filename)
			if meta.Filename != wantFilename {
				t.Fatalf("Filename = %q, want %q", meta.Filename, wantFilename)
			}
		})
	}
}

func TestSanitiseMediaFilename(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"report.zip":           "report.zip",
		"path/to/report.zip":   "report.zip",
		"..":                   "",
		"a:b\nc.txt":           "abc.txt",
		"hello":                "hello",
		"WeIrD-Name_v2.tar.gz": "WeIrD-Name_v2.tar.gz",
		"\xff\xfe.txt":         "media.txt",
		"\u062d\u0645\u0644\u0647.zip":             "media.zip",
	}
	for in, want := range cases {
		if got := SanitiseMediaFilename(in); got != want {
			t.Errorf("SanitiseMediaFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitiseMediaFilenameLongName(t *testing.T) {
	long := strings.Repeat("abc", 50) + ".zip"
	got := SanitiseMediaFilename(long)
	if !strings.HasPrefix(got, "media-") || !strings.HasSuffix(got, ".zip") {
		t.Fatalf("long filename = %q, want media-<hash>.zip", got)
	}
	if len(got) > 6+8+1+3 {
		t.Fatalf("long filename too long: %q", got)
	}
	if again := SanitiseMediaFilename(long); again != got {
		t.Fatalf("non-deterministic: %q vs %q", got, again)
	}
}

// Backward compat: legacy "[IMAGE]\ncaption" must still parse cleanly with
// caption preserved and Downloadable=false.
func TestParseMediaTextLegacy(t *testing.T) {
	body := "[IMAGE]\nlook at this"
	meta, caption, ok := ParseMediaText(body)
	if !ok {
		t.Fatalf("ParseMediaText ok=false on legacy body")
	}
	if meta.Tag != MediaImage {
		t.Fatalf("Tag = %q, want %q", meta.Tag, MediaImage)
	}
	if meta.Downloadable {
		t.Fatalf("Downloadable should be false on legacy body")
	}
	if caption != "look at this" {
		t.Fatalf("caption = %q, want %q", caption, "look at this")
	}
}

// Backward compat: legacy [IMAGE] with no caption.
func TestParseMediaTextLegacyNoCaption(t *testing.T) {
	for _, body := range []string{"[IMAGE]", "[IMAGE]\n"} {
		meta, caption, ok := ParseMediaText(body)
		if !ok {
			t.Fatalf("ok=false on %q", body)
		}
		if meta.Tag != MediaImage {
			t.Fatalf("Tag = %q, want [IMAGE]", meta.Tag)
		}
		if meta.Downloadable {
			t.Fatalf("legacy body should not be downloadable")
		}
		if caption != "" {
			t.Fatalf("caption = %q, want empty", caption)
		}
	}
}

// A normal caption that happens to lead with a media tag should not be
// misparsed as downloadable metadata.
func TestParseMediaTextHumanCaption(t *testing.T) {
	body := "[IMAGE]nice picture\nrest of post"
	meta, caption, ok := ParseMediaText(body)
	if !ok {
		t.Fatalf("ok=false on caption-leading body")
	}
	if meta.Downloadable {
		t.Fatalf("downloadable should be false for a human caption")
	}
	if meta.Channel != 0 {
		t.Fatalf("channel should be 0 for non-metadata body, got %d", meta.Channel)
	}
	want := "nice picture\nrest of post"
	if caption != want {
		t.Fatalf("caption = %q, want %q", caption, want)
	}
}

// Unknown tag → ok=false.
func TestParseMediaTextUnknownTag(t *testing.T) {
	_, _, ok := ParseMediaText("not a tag")
	if ok {
		t.Fatalf("ok=true for non-tag body")
	}
}

// A metadata line that names a channel outside the media range must NOT be
// surfaced as downloadable.
func TestParseMediaTextRejectsOutOfRangeChannel(t *testing.T) {
	body := "[IMAGE]100:1:5:200:00000000\ncaption"
	meta, _, ok := ParseMediaText(body)
	if !ok {
		t.Fatalf("ok=false on otherwise-valid metadata")
	}
	if meta.Downloadable {
		t.Fatalf("Downloadable should be false for channel %d outside media range", meta.Channel)
	}
}

func TestIsMediaChannel(t *testing.T) {
	checks := map[uint16]bool{
		0:                       false,
		1:                       false,
		MediaChannelStart - 1:   false,
		MediaChannelStart:       true,
		MediaChannelStart + 100: true,
		MediaChannelEnd:         true,
		MediaChannelEnd + 1:     false,
		65535:                   false,
	}
	for ch, want := range checks {
		if got := IsMediaChannel(ch); got != want {
			t.Errorf("IsMediaChannel(%d) = %v, want %v", ch, got, want)
		}
	}
}
