package client

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ResolverScanner scans CIDR ranges to find working DNS resolvers.
type ResolverScanner struct {
	fetcher     *Fetcher
	concurrency int
	timeout     time.Duration
}

// NewResolverScanner creates a resolver scanner.
func NewResolverScanner(fetcher *Fetcher, concurrency int) *ResolverScanner {
	if concurrency <= 0 {
		concurrency = 50
	}
	return &ResolverScanner{
		fetcher:     fetcher,
		concurrency: concurrency,
		timeout:     3 * time.Second,
	}
}

// ScanCIDR scans a CIDR range for working DNS resolvers.
func (rs *ResolverScanner) ScanCIDR(cidr string, onFound func(ip string)) error {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}

	ips := expandCIDR(ipNet)
	return rs.scanIPs(ips, onFound)
}

// ScanFile scans resolver IPs from a file (one per line, supports CIDR notation).
func (rs *ResolverScanner) ScanFile(path string, onFound func(ip string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var ips []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "/") {
			_, ipNet, err := net.ParseCIDR(line)
			if err != nil {
				log.Printf("[resolver] skip invalid CIDR: %s", line)
				continue
			}
			ips = append(ips, expandCIDR(ipNet)...)
		} else {
			ips = append(ips, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return rs.scanIPs(ips, onFound)
}

// CheckResolver tests if a single resolver works by querying metadata.
func (rs *ResolverScanner) CheckResolver(ip string) bool {
	if !strings.Contains(ip, ":") {
		ip = ip + ":53"
	}

	// Create a new fetcher with only this resolver to avoid copying the lock.
	tmpFetcher := &Fetcher{
		domain:      rs.fetcher.domain,
		queryKey:    rs.fetcher.queryKey,
		responseKey: rs.fetcher.responseKey,
		resolvers:   []string{ip},
		timeout:     rs.timeout,
	}

	_, err := tmpFetcher.FetchBlock(0, 0)
	return err == nil
}

func (rs *ResolverScanner) scanIPs(ips []string, onFound func(ip string)) error {
	if len(ips) == 0 {
		return fmt.Errorf("no IPs to scan")
	}

	var found atomic.Int32
	sem := make(chan struct{}, rs.concurrency)
	var wg sync.WaitGroup

	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if rs.CheckResolver(ip) {
				found.Add(1)
				if onFound != nil {
					onFound(ip)
				}
			}
		}(ip)
	}

	wg.Wait()

	if found.Load() == 0 {
		return fmt.Errorf("no working resolvers found among %d IPs", len(ips))
	}
	return nil
}

// LoadResolversFile loads resolver IPs from a file (one per line).
func LoadResolversFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var resolvers []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		resolvers = append(resolvers, line)
	}
	return resolvers, scanner.Err()
}

func expandCIDR(ipNet *net.IPNet) []string {
	var ips []string
	ip := ipNet.IP.Mask(ipNet.Mask)

	for ip := cloneIP(ip); ipNet.Contains(ip); incIP(ip) {
		// Skip network and broadcast addresses for /24 and smaller
		ones, bits := ipNet.Mask.Size()
		if bits-ones <= 8 {
			last := ip[len(ip)-1]
			if last == 0 || last == 255 {
				continue
			}
		}
		ips = append(ips, ip.String())
	}
	return ips
}

func cloneIP(ip net.IP) net.IP {
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}

func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}
