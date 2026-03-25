package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sartoopjj/thefeed/internal/client"
	"github.com/sartoopjj/thefeed/internal/protocol"
	"github.com/sartoopjj/thefeed/internal/tui"
	"github.com/sartoopjj/thefeed/internal/version"
)

func main() {
	domain := flag.String("domain", "", "DNS domain (e.g., t.example.com)")
	key := flag.String("key", "", "Encryption passphrase")
	resolvers := flag.String("resolvers", "", "Comma-separated resolver IPs or path to resolvers file")
	scanPath := flag.String("scan", "", "File with IPs/CIDRs to scan for resolvers, or a single CIDR (e.g., 8.8.8.0/24)")
	cacheDir := flag.String("cache", "", "Cache directory (default: ~/.thefeed/cache)")
	scanWorkers := flag.Int("scan-workers", 50, "Concurrent scanner workers")
	rateLimit := flag.Float64("rate", 0, "Max DNS queries per second (0 = unlimited)")
	queryMode := flag.String("query-mode", "single", "DNS query encoding: single (base32) or double (hex)")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("thefeed-client %s (commit: %s, built: %s)\n", version.Version, version.Commit, version.Date)
		os.Exit(0)
	}

	if *domain == "" {
		*domain = os.Getenv("THEFEED_DOMAIN")
	}
	if *key == "" {
		*key = os.Getenv("THEFEED_KEY")
	}

	if *domain == "" || *key == "" {
		fmt.Fprintln(os.Stderr, "Error: --domain and --key are required")
		flag.Usage()
		os.Exit(1)
	}

	if *cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Get home dir: %v", err)
		}
		*cacheDir = filepath.Join(home, ".thefeed", "cache")
	}

	cache, err := client.NewCache(*cacheDir)
	if err != nil {
		log.Fatalf("Create cache: %v", err)
	}

	resolverList := parseResolvers(*resolvers)

	fetcher, err := client.NewFetcher(*domain, *key, resolverList)
	if err != nil {
		log.Fatalf("Create fetcher: %v", err)
	}

	// Set query encoding mode
	if *queryMode == "double" {
		fetcher.SetQueryMode(protocol.QueryDoubleLabel)
	}

	// Set rate limit
	if *rateLimit > 0 {
		fetcher.SetRateLimit(*rateLimit)
		fmt.Printf("Rate limit: %.1f queries/sec\n", *rateLimit)
	}

	// Scan for resolvers (supports file with IPs/CIDRs or a single CIDR)
	if *scanPath != "" {
		var mu sync.Mutex
		var found []string

		scanner := client.NewResolverScanner(fetcher, *scanWorkers)

		// Check if it's a file
		if _, statErr := os.Stat(*scanPath); statErr == nil {
			fmt.Printf("Scanning resolvers from file %s...\n", *scanPath)
			err := scanner.ScanFile(*scanPath, func(ip string) {
				mu.Lock()
				found = append(found, ip)
				mu.Unlock()
				fmt.Printf("  Found: %s\n", ip)
			})
			if err != nil {
				fmt.Printf("Scan warning: %v\n", err)
			}
		} else if strings.Contains(*scanPath, "/") {
			// Treat as CIDR
			fmt.Printf("Scanning %s for DNS resolvers...\n", *scanPath)
			err := scanner.ScanCIDR(*scanPath, func(ip string) {
				mu.Lock()
				found = append(found, ip)
				mu.Unlock()
				fmt.Printf("  Found: %s\n", ip)
			})
			if err != nil {
				fmt.Printf("Scan warning: %v\n", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Error: --scan value %q is not a file or CIDR\n", *scanPath)
			os.Exit(1)
		}

		if len(found) > 0 {
			all := append(resolverList, found...)
			fetcher.SetResolvers(all)
			fmt.Printf("Using %d resolvers\n", len(all))
		}
	}

	if len(fetcher.Resolvers()) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no resolvers available. Use --resolvers or --scan")
		os.Exit(1)
	}

	if err := tui.Run(fetcher, cache); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}

func parseResolvers(input string) []string {
	if input == "" {
		return nil
	}

	if _, err := os.Stat(input); err == nil {
		resolvers, err := client.LoadResolversFile(input)
		if err != nil {
			log.Printf("Warning: could not load resolvers file %s: %v", input, err)
		} else {
			return resolvers
		}
	}

	var resolvers []string
	for _, r := range strings.Split(input, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			resolvers = append(resolvers, r)
		}
	}
	return resolvers
}
