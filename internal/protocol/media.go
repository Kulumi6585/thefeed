package protocol

import (
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

// MediaMeta describes a downloadable media blob attached to a feed message.
//
// Wire format embedded in a message's text body (immediately after the media
// tag, before any caption):
//
//	[IMAGE]<size>:<dl>:<ch>:<blk>:<crc32hex>[:<filename>]
//	caption goes here on the next line(s)
//
// The filename field is optional; when present it carries an OS-friendly
// suggested filename (server-sanitised: no newlines, no path separators, no
// control characters, length-capped). Old clients that split on ':' and
// only read parts[0..4] keep working — they just ignore the trailing field.
type MediaMeta struct {
	Tag          string // e.g. MediaImage, MediaVideo, MediaFile
	Size         int64
	Downloadable bool
	Channel      uint16
	Blocks       uint16
	CRC32        uint32
	Filename     string
}

// String renders the metadata in the wire format documented above, including
// the leading tag and trailing newline that separates the metadata row from
// any caption.
func (m MediaMeta) String() string {
	dl := 0
	if m.Downloadable {
		dl = 1
	}
	if fn := SanitiseMediaFilename(m.Filename); fn != "" {
		return fmt.Sprintf("%s%d:%d:%d:%d:%08x:%s\n",
			m.Tag, m.Size, dl, m.Channel, m.Blocks, m.CRC32, fn)
	}
	return fmt.Sprintf("%s%d:%d:%d:%d:%08x\n",
		m.Tag, m.Size, dl, m.Channel, m.Blocks, m.CRC32)
}

// SanitiseMediaFilename returns a filename safe to embed in the wire
// metadata line. The output uses a restricted alphabet ([A-Za-z0-9._-]) so
// no path separator, colon, newline, or control char can ever survive.
// When the input is too long the base name is replaced with a short
// hash-derived id but the extension is preserved so other OSes still
// recognise the file type.
func SanitiseMediaFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	cleaned := filterFilenameRunes(s)
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return ""
	}

	const maxBase = 24
	const maxExt = 8

	base, ext := splitFilenameExt(cleaned)
	if len(ext) > maxExt {
		ext = ext[:maxExt]
	}
	if len(base) > maxBase {
		h := fnv.New64a()
		_, _ = h.Write([]byte(cleaned))
		base = "media-" + hex.EncodeToString(h.Sum(nil))[:8]
	}
	if base == "" || base == "." {
		base = "media"
	}
	if ext != "" {
		return base + "." + ext
	}
	return base
}

func filterFilenameRunes(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9',
			r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func splitFilenameExt(s string) (base, ext string) {
	if i := strings.LastIndexByte(s, '.'); i >= 0 && i < len(s)-1 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// EncodeMediaText prepends the metadata line to an optional caption and
// returns the combined message text. A nil/empty caption yields just the tag
// + metadata + trailing newline-less string (the caption split is by the
// metadata line's trailing \n, so an empty caption simply has no extra body).
func EncodeMediaText(meta MediaMeta, caption string) string {
	header := meta.String()
	if caption == "" {
		// Drop the trailing newline so the message text doesn't end with a
		// blank line for caption-less media.
		return strings.TrimSuffix(header, "\n")
	}
	return header + caption
}

// ParseMediaText parses a message body that begins with a known media tag.
// On success it returns the metadata and the remaining caption (which may be
// empty). When the body uses the legacy "[TAG]\ncaption" form (no metadata
// suffix), ParseMediaText returns ok=true with Downloadable=false and
// Channel=0 — the caller can treat it as a non-downloadable placeholder
// exactly like before.
//
// Unknown tags return ok=false. Malformed metadata for a known tag also
// returns ok=false so the caller falls back to legacy display.
func ParseMediaText(body string) (meta MediaMeta, caption string, ok bool) {
	tag, rest, found := splitKnownMediaTag(body)
	if !found {
		return MediaMeta{}, body, false
	}
	meta.Tag = tag

	// The bit between the tag and the first newline is the metadata payload.
	nl := strings.IndexByte(rest, '\n')
	var metaLine string
	if nl < 0 {
		metaLine = rest
		caption = ""
	} else {
		metaLine = rest[:nl]
		caption = rest[nl+1:]
	}
	metaLine = strings.TrimSpace(metaLine)

	if metaLine == "" {
		// Legacy [TAG]\ncaption — no per-file metadata. Treat as not-downloadable.
		return MediaMeta{Tag: tag}, caption, true
	}

	parts := strings.Split(metaLine, ":")
	if len(parts) < 5 {
		// Looks like a caption line that happens to start with this tag (e.g.
		// "[IMAGE]nice photo"). Don't claim a structured parse — return the
		// whole `rest` as caption so the message still renders.
		return MediaMeta{Tag: tag}, rest, true
	}

	size, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || size < 0 {
		return MediaMeta{Tag: tag}, rest, true
	}
	dl, err := strconv.Atoi(parts[1])
	if err != nil || (dl != 0 && dl != 1) {
		return MediaMeta{Tag: tag}, rest, true
	}
	ch, err := strconv.ParseUint(parts[2], 10, 16)
	if err != nil {
		return MediaMeta{Tag: tag}, rest, true
	}
	blk, err := strconv.ParseUint(parts[3], 10, 16)
	if err != nil {
		return MediaMeta{Tag: tag}, rest, true
	}
	crc, err := strconv.ParseUint(parts[4], 16, 32)
	if err != nil {
		return MediaMeta{Tag: tag}, rest, true
	}
	// Reject any channel claimed inside a parseable metadata line that falls
	// outside the reserved media range — that can only be a malformed message
	// or a tampering attempt; refuse to surface it as downloadable.
	channel := uint16(ch)
	downloadable := dl == 1
	if downloadable && (!IsMediaChannel(channel) || blk == 0) {
		downloadable = false
	}

	meta.Size = size
	meta.Downloadable = downloadable
	meta.Channel = channel
	meta.Blocks = uint16(blk)
	meta.CRC32 = uint32(crc)
	if len(parts) >= 6 {
		// SanitiseMediaFilename strips the field separator, so we can't
		// reach this point with a colon inside the filename. Take parts[5]
		// directly and re-sanitise defensively.
		meta.Filename = SanitiseMediaFilename(parts[5])
	}
	return meta, caption, true
}

// knownMediaTags are the message text prefixes that mark a downloadable media
// attachment. Order matters only for prefix matching; longer/more-specific
// tags are not currently aliased so the order is alphabetical for clarity.
var knownMediaTags = []string{
	MediaAudio,
	MediaFile,
	MediaGIF,
	MediaImage,
	MediaSticker,
	MediaVideo,
}

// splitKnownMediaTag returns the matched tag and the remainder of the body
// when body starts with one of knownMediaTags.
func splitKnownMediaTag(body string) (tag, rest string, ok bool) {
	for _, t := range knownMediaTags {
		if strings.HasPrefix(body, t) {
			return t, body[len(t):], true
		}
	}
	return "", body, false
}
