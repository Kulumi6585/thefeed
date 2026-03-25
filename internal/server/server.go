package server

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

// Config holds server configuration.
type Config struct {
	ListenAddr   string
	Domain       string
	Passphrase   string
	ChannelsFile string
	MaxPadding   int
	Telegram     TelegramConfig
}

// Server orchestrates the DNS server and Telegram reader.
type Server struct {
	cfg  Config
	feed *Feed
}

// New creates a new Server.
func New(cfg Config) (*Server, error) {
	channels, err := loadChannels(cfg.ChannelsFile)
	if err != nil {
		return nil, fmt.Errorf("load channels: %w", err)
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("no channels configured in %s", cfg.ChannelsFile)
	}

	log.Printf("[server] loaded %d channels: %v", len(channels), channels)

	feed := NewFeed(channels)
	return &Server{cfg: cfg, feed: feed}, nil
}

// Run starts both the DNS server and the Telegram reader.
func (s *Server) Run(ctx context.Context) error {
	queryKey, responseKey, err := protocol.DeriveKeys(s.cfg.Passphrase)
	if err != nil {
		return fmt.Errorf("derive keys: %w", err)
	}

	// Handle login-only mode
	if s.cfg.Telegram.LoginOnly {
		reader := NewTelegramReader(s.cfg.Telegram, s.feed.ChannelNames(), s.feed)
		return reader.Run(ctx)
	}

	// Start Telegram reader in background
	reader := NewTelegramReader(s.cfg.Telegram, s.feed.ChannelNames(), s.feed)
	go func() {
		if err := reader.Run(ctx); err != nil {
			log.Printf("[telegram] error: %v", err)
		}
	}()

	// Start DNS server (blocking, respects ctx cancellation)
	maxPad := s.cfg.MaxPadding
	if maxPad == 0 {
		maxPad = protocol.DefaultMaxPadding
	}
	dnsServer := NewDNSServer(s.cfg.ListenAddr, s.cfg.Domain, s.feed, queryKey, responseKey, maxPad)
	return dnsServer.ListenAndServe(ctx)
}

func loadChannels(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var channels []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip @ prefix
		name := strings.TrimPrefix(line, "@")
		channels = append(channels, name)
	}
	return channels, scanner.Err()
}
