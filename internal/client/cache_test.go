package client

import (
	"os"
	"testing"
	"time"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

func TestCacheMessages(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(dir)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	msgs := []protocol.Message{
		{ID: 1, Timestamp: 1700000000, Text: "Hello"},
		{ID: 2, Timestamp: 1700000060, Text: "World"},
	}
	if err := cache.PutMessages(1, msgs); err != nil {
		t.Fatalf("PutMessages: %v", err)
	}
	cached := cache.GetMessages(1, 1*time.Hour)
	if cached == nil {
		t.Fatal("expected cached messages")
	}
	if len(cached) != 2 {
		t.Fatalf("got %d messages, want 2", len(cached))
	}
	if cached[0].Text != "Hello" || cached[1].Text != "World" {
		t.Error("cached message text mismatch")
	}
	if cache.GetMessages(2, 1*time.Hour) != nil {
		t.Error("expected nil for uncached channel")
	}
}

func TestCacheMetadata(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(dir)
	if err != nil {
		t.Fatal(err)
	}
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
	cached := cache.GetMetadata(1 * time.Hour)
	if cached == nil {
		t.Fatal("expected cached metadata")
	}
	if cached.Timestamp != 1700000000 {
		t.Errorf("timestamp: got %d, want 1700000000", cached.Timestamp)
	}
	if len(cached.Channels) != 1 || cached.Channels[0].Name != "test" {
		t.Error("metadata channel mismatch")
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
