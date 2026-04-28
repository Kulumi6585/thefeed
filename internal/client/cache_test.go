package client

import (
	"os"
	"testing"
	"time"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

func TestCacheMergeAndPut_Basic(t *testing.T) {
	cache, _ := NewCache(t.TempDir())
	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Hello"},
		{ID: 2, Timestamp: 1700000060, Text: "World"},
	}
	result, err := cache.MergeAndPut("testchan", msgs)
	if err != nil {
		t.Fatalf("MergeAndPut: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(result.Messages))
	}
	if result.Messages[0].Text != "Hello" || result.Messages[1].Text != "World" {
		t.Error("message text mismatch")
	}
}

func TestCacheMergeAndPut_Accumulates(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	// First batch
	cache.MergeAndPut("chan", []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Msg1"},
		{ID: 2, Timestamp: 1700000001, Text: "Msg2"},
	})

	// Second batch — new messages, no overlap
	result, err := cache.MergeAndPut("chan", []protocol.Message{
		{ID: 5, Timestamp: 1700000010, Text: "Msg5"},
		{ID: 6, Timestamp: 1700000011, Text: "Msg6"},
	})
	if err != nil {
		t.Fatalf("MergeAndPut second: %v", err)
	}
	if len(result.Messages) != 4 {
		t.Fatalf("accumulated: got %d messages, want 4", len(result.Messages))
	}
	if result.Messages[0].ID != 1 || result.Messages[3].ID != 6 {
		t.Errorf("order wrong: %v", result.Messages)
	}
}

func TestCacheMergeAndPut_FreshWinsOnConflict(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	cache.MergeAndPut("chan", []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Old"},
	})
	result, _ := cache.MergeAndPut("chan", []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "New"},
	})
	if len(result.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(result.Messages))
	}
	if result.Messages[0].Text != "New" {
		t.Errorf("fresh message should win conflict, got %q", result.Messages[0].Text)
	}
}

func TestCacheMergeAndPut_Cap200(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	msgs := make([]protocol.Message, 250)
	for i := range msgs {
		msgs[i] = protocol.Message{ID: uint32(i + 1), Timestamp: uint32(1700000000 + i), Text: "msg"}
	}
	result, err := cache.MergeAndPut("chan", msgs)
	if err != nil {
		t.Fatalf("MergeAndPut: %v", err)
	}
	if len(result.Messages) != 200 {
		t.Fatalf("cap: got %d messages, want 200", len(result.Messages))
	}
	// Newest 200 should be kept (IDs 51–250)
	if result.Messages[0].ID != 51 {
		t.Errorf("first retained ID = %d, want 51", result.Messages[0].ID)
	}
}

func TestCacheGetMessages_Basic(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	// Missing channel → nil
	if cache.GetMessages("missing") != nil {
		t.Error("expected nil for uncached channel")
	}

	// After put → returns data
	cache.MergeAndPut("chan", []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Hi"},
	})
	result := cache.GetMessages("chan")
	if result == nil {
		t.Fatal("expected cached result")
	}
	if len(result.Messages) != 1 || result.Messages[0].Text != "Hi" {
		t.Errorf("cached message mismatch: %v", result.Messages)
	}
}

func TestCacheGetMessages_StaleFileRemoved(t *testing.T) {
	dir := t.TempDir()
	cache, _ := NewCache(dir)

	cache.MergeAndPut("old", []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "stale"},
	})

	// Manually backdate the file modification time past the 7-day TTL.
	path := cache.channelPath("old")
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if cache.GetMessages("old") != nil {
		t.Error("expected nil for expired cache file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("stale file should be removed by GetMessages")
	}
}

func TestCacheCleanup(t *testing.T) {
	dir := t.TempDir()
	cache, _ := NewCache(dir)

	cache.MergeAndPut("fresh", []protocol.Message{{ID: 1, Timestamp: 1700000000, Text: "ok"}})
	cache.MergeAndPut("stale", []protocol.Message{{ID: 2, Timestamp: 1700000001, Text: "old"}})

	// Backdate the stale file.
	stalePath := cache.channelPath("stale")
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := cache.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Error("stale file should be removed by Cleanup")
	}
	freshPath := cache.channelPath("fresh")
	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("fresh file should not be removed: %v", err)
	}
}

func TestCacheChannelPath_SanitisesName(t *testing.T) {
	cache, _ := NewCache(t.TempDir())
	cases := []struct {
		name string
		want string // suffix after "ch_", before ".json"
	}{
		{"news", "ch_news.json"},
		{"my-channel", "ch_my-channel.json"},
		{"chan/evil", "ch_chan_evil.json"},
		{"", "ch_unknown.json"},
		{"with spaces", "ch_with_spaces.json"},
	}
	for _, c := range cases {
		p := cache.channelPath(c.name)
		base := p[len(cache.dir)+1:]
		if base != c.want {
			t.Errorf("channelPath(%q) = %q, want %q", c.name, base, c.want)
		}
	}
}

func TestCacheGapDetection(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "a"},
		{ID: 2, Timestamp: 1700000001, Text: "b"},
		// Gap of 2 here (IDs 3,4 missing)
		{ID: 5, Timestamp: 1700000005, Text: "e"},
		{ID: 6, Timestamp: 1700000006, Text: "f"},
		{ID: 7, Timestamp: 1700000007, Text: "g"},
		{ID: 8, Timestamp: 1700000008, Text: "h"},
		{ID: 9, Timestamp: 1700000009, Text: "i"},
		{ID: 10, Timestamp: 1700000010, Text: "j"},
		{ID: 11, Timestamp: 1700000011, Text: "k"},
		{ID: 12, Timestamp: 1700000012, Text: "l"},
	}
	result, _ := cache.MergeAndPut("gapchan", msgs)

	if len(result.Gaps) == 0 {
		t.Fatal("expected at least one gap")
	}
	g := result.Gaps[0]
	if g.AfterID != 2 || g.BeforeID != 5 || g.Count != 2 {
		t.Errorf("gap = %+v, want AfterID=2 BeforeID=5 Count=2", g)
	}
}

func TestCacheGapDetection_NoGapWhenFewMessages(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "a"},
		// big gap
		{ID: 100, Timestamp: 1700000001, Text: "b"},
	}
	result, _ := cache.MergeAndPut("tiny", msgs)
	if len(result.Gaps) != 0 {
		t.Error("expected no gaps when < 10 messages")
	}
}

func TestCacheGapDetection_AlbumNoFalsePositive(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	// 10 sequential messages where ID=5 is a 2-image album: it absorbs ID 6
	// (a real Telegram behaviour). The next message is ID 7. Without the
	// album-aware fix, the gap detector would flag a missing ID 6.
	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "a"},
		{ID: 2, Timestamp: 1700000001, Text: "b"},
		{ID: 3, Timestamp: 1700000002, Text: "c"},
		{ID: 4, Timestamp: 1700000003, Text: "d"},
		{ID: 5, Timestamp: 1700000004, Text: "[IMAGE]100:0:0:0:abcd1234:img1.jpg\n[IMAGE]200:0:0:0:abcd5678:img2.jpg\nalbum caption"},
		// ID 6 is absorbed into the album above; the feed jumps to 7.
		{ID: 7, Timestamp: 1700000005, Text: "e"},
		{ID: 8, Timestamp: 1700000006, Text: "f"},
		{ID: 9, Timestamp: 1700000007, Text: "g"},
		{ID: 10, Timestamp: 1700000008, Text: "h"},
		{ID: 11, Timestamp: 1700000009, Text: "i"},
	}
	result, _ := cache.MergeAndPut("albumchan", msgs)

	if len(result.Gaps) != 0 {
		t.Errorf("album-absorbed sibling should not be flagged as a gap, got %+v", result.Gaps)
	}
}

func TestCacheGapDetection_AlbumWithRealGap(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	// 3-image album at ID=5 absorbs IDs 6,7. A real gap of IDs 8,9 follows
	// before ID=10. The detector should report a single 2-message gap.
	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "a"},
		{ID: 2, Timestamp: 1700000001, Text: "b"},
		{ID: 3, Timestamp: 1700000002, Text: "c"},
		{ID: 4, Timestamp: 1700000003, Text: "d"},
		{ID: 5, Timestamp: 1700000004, Text: "[IMAGE]100:0:0:0:aaaaaaaa:1.jpg\n[IMAGE]200:0:0:0:bbbbbbbb:2.jpg\n[IMAGE]300:0:0:0:cccccccc:3.jpg\ncap"},
		// IDs 6,7 absorbed; IDs 8,9 truly missing; resume at 10.
		{ID: 10, Timestamp: 1700000010, Text: "e"},
		{ID: 11, Timestamp: 1700000011, Text: "f"},
		{ID: 12, Timestamp: 1700000012, Text: "g"},
		{ID: 13, Timestamp: 1700000013, Text: "h"},
		{ID: 14, Timestamp: 1700000014, Text: "i"},
		{ID: 15, Timestamp: 1700000015, Text: "j"},
	}
	result, _ := cache.MergeAndPut("albumgap", msgs)

	if len(result.Gaps) != 1 {
		t.Fatalf("expected exactly one gap, got %+v", result.Gaps)
	}
	g := result.Gaps[0]
	if g.AfterID != 7 || g.BeforeID != 10 || g.Count != 2 {
		t.Errorf("gap = %+v, want AfterID=7 BeforeID=10 Count=2", g)
	}
}

func TestCacheGapDetection_AlbumWithReplyPrefix(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	// [REPLY]:42 prefix before the media headers should still let albumSpan
	// count the headers correctly.
	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "a"},
		{ID: 2, Timestamp: 1700000001, Text: "b"},
		{ID: 3, Timestamp: 1700000002, Text: "c"},
		{ID: 4, Timestamp: 1700000003, Text: "d"},
		{ID: 5, Timestamp: 1700000004, Text: "[REPLY]:42\n[IMAGE]100:0:0:0:aaaaaaaa:1.jpg\n[IMAGE]200:0:0:0:bbbbbbbb:2.jpg\nreplied caption"},
		// ID 6 absorbed.
		{ID: 7, Timestamp: 1700000005, Text: "e"},
		{ID: 8, Timestamp: 1700000006, Text: "f"},
		{ID: 9, Timestamp: 1700000007, Text: "g"},
		{ID: 10, Timestamp: 1700000008, Text: "h"},
		{ID: 11, Timestamp: 1700000009, Text: "i"},
	}
	result, _ := cache.MergeAndPut("replychan", msgs)

	if len(result.Gaps) != 0 {
		t.Errorf("album with reply prefix should not produce false gaps, got %+v", result.Gaps)
	}
}

func TestAlbumSpan(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"plain text", "hello world", 0},
		{"single image legacy", "[IMAGE]\ncaption", 1},
		{"single image downloadable", "[IMAGE]100:0:0:0:abcd1234:f.jpg\ncap", 1},
		{"two images", "[IMAGE]100:0:0:0:aa:1.jpg\n[IMAGE]200:0:0:0:bb:2.jpg\ncap", 2},
		{"three mixed", "[IMAGE]1:0:0:0:aa:a.jpg\n[VIDEO]2:0:0:0:bb:b.mp4\n[FILE]3:0:0:0:cc:c.pdf\nx", 3},
		{"with reply prefix", "[REPLY]:99\n[IMAGE]100:0:0:0:aa:1.jpg\n[IMAGE]200:0:0:0:bb:2.jpg\ncap", 2},
		{"reply only no media", "[REPLY]:99\nhello", 0},
		{"caption that mentions a tag", "look at this [IMAGE] thing", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := albumSpan(c.text); got != c.want {
				t.Errorf("albumSpan(%q) = %d, want %d", c.text, got, c.want)
			}
		})
	}
}

func TestCacheGapDetection_LargeGapIgnored(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	msgs := make([]protocol.Message, 10)
	for i := range msgs {
		msgs[i] = protocol.Message{ID: uint32(i + 1), Timestamp: uint32(1700000000 + i), Text: "x"}
	}
	msgs[9] = protocol.Message{ID: 2000, Timestamp: 1700001000, Text: "far"} // gap > 500
	result, _ := cache.MergeAndPut("bigchan", msgs)

	for _, g := range result.Gaps {
		if g.Count > 500 {
			t.Errorf("gap > 500 should be ignored, got %+v", g)
		}
	}
}

func TestNewMessagesResult(t *testing.T) {
	result := NewMessagesResult(nil)
	if result == nil {
		t.Fatal("expected non-nil result for nil input")
	}
	if result.Messages == nil {
		t.Error("messages should be empty slice, not nil")
	}

	msgs := []protocol.Message{
		{ID: 3, Timestamp: 1700000002, Text: "c"},
		{ID: 1, Timestamp: 1700000000, Text: "a"},
	}
	result2 := NewMessagesResult(msgs)
	// Should be sorted by ID
	if result2.Messages[0].ID != 1 {
		t.Errorf("first message should have ID 1, got %d", result2.Messages[0].ID)
	}
}

func TestCacheDirCreation(t *testing.T) {
	dir := t.TempDir() + "/sub/dir"
	_, err := NewCache(dir)
	if err != nil {
		t.Fatalf("NewCache should create dirs: %v", err)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("cache dir should be created")
	}
}

func TestCacheMetadata(t *testing.T) {
	cache, _ := NewCache(t.TempDir())

	meta := &protocol.Metadata{
		Marker:    [3]byte{1, 2, 3},
		Timestamp: 1700000000,
		Channels: []protocol.ChannelInfo{
			{Name: "test", Blocks: 5, LastMsgID: 100},
		},
	}
	if err := cache.PutMetadata(meta); err != nil {
		t.Fatalf("PutMetadata: %v", err)
	}
	// metadata.json should exist
	if _, err := os.Stat(cache.dir + "/metadata.json"); err != nil {
		t.Errorf("metadata.json missing: %v", err)
	}
}
