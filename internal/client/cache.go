package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

// Cache provides file-based caching for channel data.
type Cache struct {
	dir string
	mu  sync.RWMutex
}

type cachedChannel struct {
	Messages  []protocol.Message `json:"messages"`
	FetchedAt int64              `json:"fetched_at"`
}

type cachedMeta struct {
	Metadata  *protocol.Metadata `json:"metadata"`
	FetchedAt int64              `json:"fetched_at"`
}

// NewCache creates a file cache in the given directory.
func NewCache(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &Cache{dir: dir}, nil
}

// GetMessages returns cached messages for a channel, or nil if expired.
func (c *Cache) GetMessages(channelNum int, maxAge time.Duration) []protocol.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()

	path := c.channelPath(channelNum)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var cached cachedChannel
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil
	}

	if maxAge > 0 && time.Since(time.Unix(cached.FetchedAt, 0)) > maxAge {
		return nil
	}

	return cached.Messages
}

// PutMessages stores messages for a channel.
func (c *Cache) PutMessages(channelNum int, msgs []protocol.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cached := cachedChannel{
		Messages:  msgs,
		FetchedAt: time.Now().Unix(),
	}

	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}

	return os.WriteFile(c.channelPath(channelNum), data, 0600)
}

// GetMetadata returns cached metadata, or nil if expired.
func (c *Cache) GetMetadata(maxAge time.Duration) *protocol.Metadata {
	c.mu.RLock()
	defer c.mu.RUnlock()

	path := filepath.Join(c.dir, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var cached cachedMeta
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil
	}

	if maxAge > 0 && time.Since(time.Unix(cached.FetchedAt, 0)) > maxAge {
		return nil
	}

	return cached.Metadata
}

// PutMetadata stores metadata.
func (c *Cache) PutMetadata(meta *protocol.Metadata) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cached := cachedMeta{
		Metadata:  meta,
		FetchedAt: time.Now().Unix(),
	}

	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(c.dir, "metadata.json"), data, 0600)
}

func (c *Cache) channelPath(channelNum int) string {
	return filepath.Join(c.dir, fmt.Sprintf("channel_%d.json", channelNum))
}
