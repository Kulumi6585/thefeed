package server

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

// Config holds server configuration.
type Config struct {
	ListenAddr    string
	Domain        string
	Passphrase    string
	ChannelsFile  string
	XAccountsFile string
	XRSSInstances string
	MaxPadding    int
	MsgLimit      int  // max messages per channel (0 = default 15)
	NoTelegram    bool // if true, fetch public channels without Telegram login
	AllowManage   bool // if true, remote channel management and sending via DNS is allowed
	Debug         bool // if true, log every decoded DNS query
	// NoMedia disables downloading and serving image/file media. When set, the
	// server emits the legacy [TAG]\ncaption form for media messages so old
	// clients keep working unchanged.
	NoMedia bool
	// MediaMaxSize is the per-file cap in bytes for cached media. 0 means no
	// cap (not recommended in production).
	MediaMaxSize int64
	// MediaCacheTTL is the cache lifetime in minutes for a single entry. The
	// effective TTL is reset whenever the same upstream id is fetched again.
	MediaCacheTTL int
	// MediaCompression names the compression applied to cached media bytes
	// before they're split into DNS blocks. One of "none", "gzip",
	// "deflate". Empty defaults to "gzip".
	MediaCompression string
	Telegram         TelegramConfig
}

// Server orchestrates the DNS server and Telegram reader.
type Server struct {
	cfg              Config
	feed             *Feed
	reader           *TelegramReader // nil when --no-telegram
	telegramChannels []string
	xAccounts        []string
}

// New creates a new Server.
func New(cfg Config) (*Server, error) {
	channels, err := loadUsernames(cfg.ChannelsFile)
	if err != nil {
		return nil, fmt.Errorf("load channels: %w", err)
	}
	xAccounts, err := loadUsernames(cfg.XAccountsFile)
	if err != nil {
		return nil, fmt.Errorf("load X accounts: %w", err)
	}

	if len(channels) == 0 && len(xAccounts) == 0 {
		return nil, fmt.Errorf("no channels configured in %s and no X accounts configured in %s", cfg.ChannelsFile, cfg.XAccountsFile)
	}

	log.Printf("[server] loaded %d Telegram channels and %d X accounts", len(channels), len(xAccounts))

	feed := NewFeed(append(append([]string{}, channels...), prefixXAccounts(xAccounts)...))
	return &Server{cfg: cfg, feed: feed, telegramChannels: channels, xAccounts: xAccounts}, nil
}

// Run starts both the DNS server and the Telegram reader.
func (s *Server) Run(ctx context.Context) error {
	queryKey, responseKey, err := protocol.DeriveKeys(s.cfg.Passphrase)
	if err != nil {
		return fmt.Errorf("derive keys: %w", err)
	}

	SetMediaDebugLogs(s.cfg.Debug)

	// Configure media cache before any reader starts so the very first fetch
	// cycle can populate it. When --no-media is set we leave Feed.media as
	// nil; the readers fall through to the legacy [TAG]\ncaption form, and
	// Feed.GetBlock rejects media-channel queries with not-found.
	if !s.cfg.NoMedia {
		ttlMin := s.cfg.MediaCacheTTL
		if ttlMin <= 0 {
			ttlMin = 600
		}
		ttl := time.Duration(ttlMin) * time.Minute
		compName := s.cfg.MediaCompression
		if compName == "" {
			compName = "gzip"
		}
		compression, err := protocol.ParseMediaCompressionName(compName)
		if err != nil {
			return fmt.Errorf("--media-compression: %w", err)
		}
		mediaCache := NewMediaCache(MediaCacheConfig{
			MaxFileBytes: s.cfg.MediaMaxSize,
			TTL:          ttl,
			Compression:  compression,
			Logf:         logfMedia,
		})
		s.feed.SetMediaCache(mediaCache)
		log.Printf("[server] media cache enabled: max-size=%d bytes, ttl=%s, compression=%s", s.cfg.MediaMaxSize, ttl, compression)
		go s.runMediaSweep(ctx, mediaCache, ttl)
	} else {
		log.Println("[server] media cache disabled (--no-media)")
	}

	go startLatestVersionTracker(ctx, s.feed)
	var channelCtl channelRefresher

	// Handle login-only mode
	if s.cfg.Telegram.LoginOnly {
		reader := NewTelegramReader(s.cfg.Telegram, s.telegramChannels, s.feed, 15, 1)
		return reader.Run(ctx)
	}

	// Start Telegram reader in background, or public web fetcher in no-login mode.
	if !s.cfg.NoTelegram {
		msgLimit := s.cfg.MsgLimit
		if msgLimit <= 0 {
			msgLimit = 15
		}
		if len(s.telegramChannels) > 0 {
			reader := NewTelegramReader(s.cfg.Telegram, s.telegramChannels, s.feed, msgLimit, 1)
			s.reader = reader
			channelCtl = reader
			go func() {
				log.Println("[telegram] reader goroutine started")
				if err := reader.Run(ctx); err != nil && ctx.Err() == nil {
					log.Printf("[telegram] reader goroutine STOPPED with error: %v", err)
				} else {
					log.Println("[telegram] reader goroutine exited")
				}
			}()
		} else {
			s.feed.SetTelegramLoggedIn(true)
		}
	} else {
		msgLimit := s.cfg.MsgLimit
		if msgLimit <= 0 {
			msgLimit = 15
		}
		publicReader := NewPublicReader(s.telegramChannels, s.feed, msgLimit, 1)
		channelCtl = publicReader
		go func() {
			log.Println("[public] reader goroutine started")
			if err := publicReader.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("[public] reader goroutine STOPPED with error: %v", err)
			} else {
				log.Println("[public] reader goroutine exited")
			}
		}()
		log.Println("[server] running without Telegram login; fetching public channels via t.me")
	}

	var xReader *XPublicReader
	if len(s.xAccounts) > 0 {
		msgLimit := s.cfg.MsgLimit
		if msgLimit <= 0 {
			msgLimit = 15
		}
		xReader = NewXPublicReader(s.xAccounts, s.feed, msgLimit, len(s.telegramChannels)+1, s.cfg.XRSSInstances)
		go func() {
			log.Println("[x] reader goroutine started")
			if err := xReader.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("[x] reader goroutine STOPPED with error: %v", err)
			} else {
				log.Println("[x] reader goroutine exited")
			}
		}()
		log.Printf("[server] enabled X source for %d accounts", len(s.xAccounts))
	}

	// Start DNS server (blocking, respects ctx cancellation)
	maxPad := s.cfg.MaxPadding
	if maxPad == 0 {
		maxPad = protocol.DefaultMaxPadding
	}
	dnsServer := NewDNSServer(s.cfg.ListenAddr, s.cfg.Domain, s.feed, queryKey, responseKey, maxPad, s.reader, s.cfg.AllowManage, s.cfg.ChannelsFile, s.xAccounts, s.cfg.Debug)
	if channelCtl != nil {
		dnsServer.SetChannelRefresher(channelCtl)
	}
	if xReader != nil {
		dnsServer.AddRefresher(xReader)
		dnsServer.SetXReader(xReader)
	}
	return dnsServer.ListenAndServe(ctx)
}

func loadUsernames(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("[server] close usernames file: %v", err)
		}
	}()

	var users []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := strings.TrimPrefix(line, "@")
		users = append(users, name)
	}
	return users, scanner.Err()
}

func prefixXAccounts(accounts []string) []string {
	out := make([]string, len(accounts))
	for i, a := range accounts {
		out[i] = "x/" + a
	}
	return out
}

// runMediaSweep periodically evicts expired entries from the cache. The
// interval is min(ttl/4, 5min) so we don't waste cycles on long-TTL configs
// while still reclaiming slots in time under steady-state churn.
func (s *Server) runMediaSweep(ctx context.Context, cache *MediaCache, ttl time.Duration) {
	if cache == nil {
		return
	}
	interval := ttl / 4
	if interval <= 0 || interval > 5*time.Minute {
		interval = 5 * time.Minute
	}
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cache.Sweep()
		}
	}
}
