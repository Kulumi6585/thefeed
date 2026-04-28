package client

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/rand"
	"hash/crc32"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

// withMediaHeader prepends the protocol media block header to body. The
// CRC32 is computed over the DECOMPRESSED bytes the caller passes in, but
// `body` itself is what the server would have produced after compressing —
// which for compression=none is just the bytes themselves.
func withMediaHeader(crc uint32, body []byte, compression protocol.MediaCompression) []byte {
	hdr := protocol.EncodeMediaBlockHeader(protocol.MediaBlockHeader{
		CRC32:       crc,
		Version:     protocol.MediaHeaderVersion,
		Compression: compression,
	})
	out := make([]byte, 0, len(hdr)+len(body))
	out = append(out, hdr...)
	out = append(out, body...)
	return out
}

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(b); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// blockMockExchange wires the fetcher's exchangeFn so each (channel, block)
// pair returns the matching slice from blocks.
func blockMockExchange(f *Fetcher, want uint16, blocks [][]byte) func(context.Context, *dns.Msg, string) (*dns.Msg, time.Duration, error) {
	return func(ctx context.Context, m *dns.Msg, _ string) (*dns.Msg, time.Duration, error) {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		ch, blk, err := protocol.DecodeQuery(f.queryKey, m.Question[0].Name, f.domain)
		if err != nil {
			return nil, 0, err
		}
		if ch != want {
			return nil, 0, errFakeNotFound{}
		}
		if int(blk) >= len(blocks) {
			return nil, 0, errFakeNotFound{}
		}
		encoded, encErr := protocol.EncodeResponse(f.responseKey, blocks[int(blk)], 0)
		if encErr != nil {
			return nil, 0, encErr
		}
		resp := new(dns.Msg)
		resp.SetReply(m)
		resp.Rcode = dns.RcodeSuccess
		resp.Answer = []dns.RR{&dns.TXT{
			Hdr: dns.RR_Header{Name: m.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0},
			Txt: []string{encoded},
		}}
		return resp, time.Millisecond, nil
	}
}

type errFakeNotFound struct{}

func (errFakeNotFound) Error() string { return "fake nxdomain" }

func TestFetchMediaUncompressed(t *testing.T) {
	f := newTestFetcher(t, []string{"1.1.1.1:53"})
	original := make([]byte, 1500)
	if _, err := rand.Read(original); err != nil {
		t.Fatalf("rand: %v", err)
	}
	crc := crc32.ChecksumIEEE(original)
	blocks := protocol.SplitIntoBlocks(withMediaHeader(crc, original, protocol.MediaCompressionNone))

	channel := protocol.MediaChannelStart + 7
	f.exchangeFn = blockMockExchange(f, channel, blocks)

	out, err := f.FetchMedia(context.Background(), channel, uint16(len(blocks)), crc, nil)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if !bytes.Equal(out, original) {
		t.Fatalf("decompressed output differs from original")
	}
}

func TestFetchMediaDeflate(t *testing.T) {
	f := newTestFetcher(t, []string{"1.1.1.1:53"})
	original := bytes.Repeat([]byte("xy "), 250)
	crc := crc32.ChecksumIEEE(original)
	var buf bytes.Buffer
	zw, _ := flate.NewWriter(&buf, flate.BestCompression)
	zw.Write(original)
	zw.Close()
	blocks := protocol.SplitIntoBlocks(withMediaHeader(crc, buf.Bytes(), protocol.MediaCompressionDeflate))

	channel := protocol.MediaChannelStart + 9
	f.exchangeFn = blockMockExchange(f, channel, blocks)
	out, err := f.FetchMedia(context.Background(), channel, uint16(len(blocks)), crc, nil)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if !bytes.Equal(out, original) {
		t.Fatalf("decompressed differs from original")
	}
}

func TestFetchMediaGzip(t *testing.T) {
	f := newTestFetcher(t, []string{"1.1.1.1:53"})
	original := bytes.Repeat([]byte("abc123 "), 200) // compressible
	crc := crc32.ChecksumIEEE(original)
	body := gzipBytes(t, original)
	blocks := protocol.SplitIntoBlocks(withMediaHeader(crc, body, protocol.MediaCompressionGzip))

	channel := protocol.MediaChannelStart + 8
	f.exchangeFn = blockMockExchange(f, channel, blocks)

	out, err := f.FetchMedia(context.Background(), channel, uint16(len(blocks)), crc, nil)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if !bytes.Equal(out, original) {
		t.Fatalf("decompressed output differs from original")
	}
	if len(body) >= len(original) {
		t.Fatalf("compressed body should be smaller than original (got %d vs %d)", len(body), len(original))
	}
}

func TestFetchMediaRejectsNonMediaChannel(t *testing.T) {
	f := newTestFetcher(t, []string{"1.1.1.1:53"})
	_, err := f.FetchMedia(context.Background(), 1, 1, 0, nil)
	if err == nil {
		t.Fatalf("expected error for non-media channel")
	}
}

func TestFetchMediaRejectsBadHash(t *testing.T) {
	f := newTestFetcher(t, []string{"1.1.1.1:53"})
	original := []byte("hello hash mismatch")
	crc := crc32.ChecksumIEEE(original)
	blocks := [][]byte{withMediaHeader(crc, original, protocol.MediaCompressionNone)}
	channel := protocol.MediaChannelStart + 1
	f.exchangeFn = blockMockExchange(f, channel, blocks)

	_, err := f.FetchMedia(context.Background(), channel, 1, 0xDEADBEEF, nil)
	if err != ErrMediaHashMismatch {
		t.Fatalf("err = %v, want ErrMediaHashMismatch", err)
	}
}

func TestFetchMediaBlocksStreamWritesInOrder(t *testing.T) {
	f := newTestFetcher(t, []string{"1.1.1.1:53"})
	blocks := [][]byte{
		[]byte("alpha"),
		[]byte("beta"),
		[]byte("gamma"),
	}
	channel := protocol.MediaChannelStart + 12
	f.exchangeFn = blockMockExchange(f, channel, blocks)

	var got bytes.Buffer
	if err := f.FetchMediaBlocksStream(context.Background(), channel, 0, 3, &got, nil); err != nil {
		t.Fatalf("FetchMediaBlocksStream: %v", err)
	}
	want := append(append(append([]byte{}, blocks[0]...), blocks[1]...), blocks[2]...)
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("got %q, want %q", got.Bytes(), want)
	}
}

func TestFetchMediaBlocksStreamPartialRange(t *testing.T) {
	f := newTestFetcher(t, []string{"1.1.1.1:53"})
	blocks := [][]byte{
		[]byte("first-block"),
		[]byte("second-block"),
		[]byte("third-block"),
	}
	channel := protocol.MediaChannelStart + 13
	f.exchangeFn = blockMockExchange(f, channel, blocks)

	var got bytes.Buffer
	if err := f.FetchMediaBlocksStream(context.Background(), channel, 1, 2, &got, nil); err != nil {
		t.Fatalf("FetchMediaBlocksStream: %v", err)
	}
	want := append(append([]byte{}, blocks[1]...), blocks[2]...)
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("got %q, want %q", got.Bytes(), want)
	}
}
