package web

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	mediaCacheFileExt    = ".cache"
	mediaCacheMaxMime    = 200
	mediaCacheMaxFileExt = 1 << 26 // 64 MiB hard cap per cached file
)

// mediaDiskCache stores downloaded media blobs on disk so multiple devices
// connected to the same client/server share the cost of one DNS-tunnelled
// fetch. Entries are content-addressed by (size, crc32) and reaped after
// ttl based on file mtime.
//
// File format: each entry is a single file
//
//	<size>_<crc8hex>.cache
//
// containing:
//
//	2 bytes BE  — mime length
//	N bytes     — mime utf8
//	rest        — raw file bytes
type mediaDiskCache struct {
	dir string
	ttl time.Duration
	mu  sync.Mutex
}

func newMediaDiskCache(dir string, ttl time.Duration) (*mediaDiskCache, error) {
	if dir == "" {
		return nil, errors.New("media cache dir is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &mediaDiskCache{dir: dir, ttl: ttl}, nil
}

func (c *mediaDiskCache) keyFile(size int64, crc uint32) string {
	return filepath.Join(c.dir, fmt.Sprintf("%d_%08x%s", size, crc, mediaCacheFileExt))
}

// Get returns the cached body and mime type if present and not expired.
// Touching mtime on hit so the entry stays alive while it's in use.
func (c *mediaDiskCache) Get(size int64, crc uint32) (body []byte, mime string, ok bool) {
	if size <= 0 || crc == 0 {
		return nil, "", false
	}
	path := c.keyFile(size, crc)
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", false
	}
	if c.ttl > 0 && time.Since(info.ModTime()) > c.ttl {
		_ = os.Remove(path)
		return nil, "", false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 2 {
		return nil, "", false
	}
	mimeLen := int(binary.BigEndian.Uint16(data[:2]))
	if mimeLen > mediaCacheMaxMime || 2+mimeLen > len(data) {
		return nil, "", false
	}
	mime = string(data[2 : 2+mimeLen])
	body = data[2+mimeLen:]
	if int64(len(body)) != size {
		// Corrupt or partial write — treat as miss.
		return nil, "", false
	}
	_ = os.Chtimes(path, time.Now(), time.Now())
	return body, mime, true
}

// Put writes the body+mime atomically to the cache.
func (c *mediaDiskCache) Put(size int64, crc uint32, body []byte, mime string) error {
	if size <= 0 || crc == 0 || int64(len(body)) != size {
		return errors.New("media cache: invalid put")
	}
	if len(body) > mediaCacheMaxFileExt {
		return errors.New("media cache: body too large")
	}
	if len(mime) > mediaCacheMaxMime {
		mime = mime[:mediaCacheMaxMime]
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	path := c.keyFile(size, crc)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	header := make([]byte, 2)
	binary.BigEndian.PutUint16(header, uint16(len(mime)))
	if _, err := f.Write(header); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write([]byte(mime)); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Cleanup removes entries older than ttl. Returns the count removed.
func (c *mediaDiskCache) Cleanup() int {
	if c.ttl <= 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return 0
	}
	now := time.Now()
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), mediaCacheFileExt) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > c.ttl {
			if os.Remove(filepath.Join(c.dir, e.Name())) == nil {
				removed++
			}
		}
	}
	return removed
}

// Clear deletes every cached entry. Returns the count removed.
func (c *mediaDiskCache) Clear() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), mediaCacheFileExt) {
			continue
		}
		if os.Remove(filepath.Join(c.dir, e.Name())) == nil {
			removed++
		}
	}
	return removed
}
