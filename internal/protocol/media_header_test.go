package protocol

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeMediaBlockHeader(t *testing.T) {
	cases := []MediaBlockHeader{
		{CRC32: 0x01020304, Version: MediaHeaderVersion, Compression: MediaCompressionNone},
		{CRC32: 0xdeadbeef, Version: MediaHeaderVersion, Compression: MediaCompressionGzip},
		{CRC32: 0, Version: MediaHeaderVersion, Compression: MediaCompressionDeflate},
	}
	for _, h := range cases {
		buf := EncodeMediaBlockHeader(h)
		if len(buf) != MediaBlockHeaderLen {
			t.Fatalf("encoded length = %d, want %d", len(buf), MediaBlockHeaderLen)
		}
		// Reserved bytes must be zero for forward compatibility.
		if !bytes.Equal(buf[6:], make([]byte, MediaBlockHeaderLen-6)) {
			t.Fatalf("reserved bytes not zero: %x", buf[6:])
		}
		got, err := DecodeMediaBlockHeader(buf)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got != h {
			t.Fatalf("round-trip: got %+v, want %+v", got, h)
		}
	}
}

func TestDecodeMediaBlockHeaderRejectsBadVersion(t *testing.T) {
	buf := EncodeMediaBlockHeader(MediaBlockHeader{CRC32: 1, Version: MediaHeaderVersion, Compression: MediaCompressionNone})
	buf[4] = 9 // bogus version
	_, err := DecodeMediaBlockHeader(buf)
	if err == nil {
		t.Fatal("expected error for unknown version")
	}
}

func TestDecodeMediaBlockHeaderRejectsBadCompression(t *testing.T) {
	buf := EncodeMediaBlockHeader(MediaBlockHeader{Version: MediaHeaderVersion})
	buf[5] = 99
	_, err := DecodeMediaBlockHeader(buf)
	if err == nil {
		t.Fatal("expected error for unknown compression")
	}
}

func TestDecodeMediaBlockHeaderRejectsTruncated(t *testing.T) {
	_, err := DecodeMediaBlockHeader(make([]byte, MediaBlockHeaderLen-1))
	if err == nil {
		t.Fatal("expected error for truncated header")
	}
}

func TestParseMediaCompressionName(t *testing.T) {
	cases := map[string]MediaCompression{
		"":        MediaCompressionNone,
		"none":    MediaCompressionNone,
		"gzip":    MediaCompressionGzip,
		"deflate": MediaCompressionDeflate,
	}
	for in, want := range cases {
		got, err := ParseMediaCompressionName(in)
		if err != nil {
			t.Errorf("ParseMediaCompressionName(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseMediaCompressionName(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseMediaCompressionName("brotli"); err == nil {
		t.Fatal("expected error for unknown compression name")
	}
}
