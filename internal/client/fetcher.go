package client

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

// LogFunc is a callback for logging DNS queries (for debug/TUI).
type LogFunc func(msg string)

// Fetcher fetches feed blocks over DNS.
type Fetcher struct {
	domain      string
	queryKey    [protocol.KeySize]byte
	responseKey [protocol.KeySize]byte
	queryMode   protocol.QueryEncoding

	mu        sync.RWMutex
	resolvers []string
	timeout   time.Duration

	// Rate limiting
	rateMu     sync.Mutex
	queryDelay time.Duration
	lastQuery  time.Time

	// Debug logging
	logFunc LogFunc
}

// NewFetcher creates a new DNS block fetcher.
func NewFetcher(domain, passphrase string, resolvers []string) (*Fetcher, error) {
	qk, rk, err := protocol.DeriveKeys(passphrase)
	if err != nil {
		return nil, fmt.Errorf("derive keys: %w", err)
	}

	return &Fetcher{
		domain:      strings.TrimSuffix(domain, "."),
		queryKey:    qk,
		responseKey: rk,
		queryMode:   protocol.QuerySingleLabel,
		resolvers:   resolvers,
		timeout:     5 * time.Second,
	}, nil
}

// SetRateLimit sets the maximum queries per second (0 = unlimited).
func (f *Fetcher) SetRateLimit(qps float64) {
	if qps <= 0 {
		f.queryDelay = 0
		return
	}
	f.queryDelay = time.Duration(float64(time.Second) / qps)
}

// SetLogFunc sets the debug log callback.
func (f *Fetcher) SetLogFunc(fn LogFunc) {
	f.logFunc = fn
}

// SetQueryMode sets the DNS query encoding mode.
func (f *Fetcher) SetQueryMode(mode protocol.QueryEncoding) {
	f.queryMode = mode
}

func (f *Fetcher) log(format string, args ...any) {
	if f.logFunc != nil {
		f.logFunc(fmt.Sprintf(format, args...))
	}
}

func (f *Fetcher) rateWait() {
	if f.queryDelay <= 0 {
		return
	}
	f.rateMu.Lock()
	defer f.rateMu.Unlock()
	elapsed := time.Since(f.lastQuery)
	if elapsed < f.queryDelay {
		time.Sleep(f.queryDelay - elapsed)
	}
	f.lastQuery = time.Now()
}

// SetResolvers replaces the resolver list.
func (f *Fetcher) SetResolvers(resolvers []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolvers = resolvers
}

// Resolvers returns the current resolver list.
func (f *Fetcher) Resolvers() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	result := make([]string, len(f.resolvers))
	copy(result, f.resolvers)
	return result
}

// FetchBlock fetches a single block from a channel.
func (f *Fetcher) FetchBlock(channel, block uint16) ([]byte, error) {
	f.rateWait()

	qname, err := protocol.EncodeQuery(f.queryKey, channel, block, f.domain, f.queryMode)
	if err != nil {
		return nil, fmt.Errorf("encode query: %w", err)
	}

	f.log("Q ch=%d blk=%d → %s", channel, block, qname)

	resolvers := f.Resolvers()
	if len(resolvers) == 0 {
		return nil, fmt.Errorf("no resolvers configured")
	}

	// Shuffle resolvers to distribute load
	shuffled := make([]string, len(resolvers))
	copy(shuffled, resolvers)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	var lastErr error
	for _, resolver := range shuffled {
		data, err := f.queryResolver(resolver, qname)
		if err != nil {
			lastErr = err
			continue
		}
		return data, nil
	}

	return nil, fmt.Errorf("all resolvers failed, last error: %w", lastErr)
}

// FetchMetadata fetches and parses metadata (channel 0).
func (f *Fetcher) FetchMetadata() (*protocol.Metadata, error) {
	data, err := f.FetchBlock(protocol.MetadataChannel, 0)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata block 0: %w", err)
	}

	meta, err := protocol.ParseMetadata(data)
	if err == nil {
		return meta, nil
	}

	// Metadata might span multiple blocks
	allData := make([]byte, len(data))
	copy(allData, data)

	for blk := uint16(1); blk < 10; blk++ {
		block, fetchErr := f.FetchBlock(protocol.MetadataChannel, blk)
		if fetchErr != nil {
			break
		}
		allData = append(allData, block...)
		meta, parseErr := protocol.ParseMetadata(allData)
		if parseErr == nil {
			return meta, nil
		}
	}

	return nil, fmt.Errorf("could not parse metadata: %w", err)
}

// FetchChannel fetches all blocks for a channel and parses messages.
func (f *Fetcher) FetchChannel(channelNum int, blockCount int) ([]protocol.Message, error) {
	if blockCount <= 0 {
		return nil, nil
	}

	type result struct {
		idx  int
		data []byte
		err  error
	}

	results := make(chan result, blockCount)
	// Limit concurrency to 3 to reduce DNS burst traffic
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup

	for i := 0; i < blockCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			data, err := f.FetchBlock(uint16(channelNum), uint16(idx))
			results <- result{idx: idx, data: data, err: err}
		}(i)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	ordered := make([][]byte, blockCount)
	for r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("fetch block %d: %w", r.idx, r.err)
		}
		ordered[r.idx] = r.data
	}

	var allData []byte
	for _, block := range ordered {
		allData = append(allData, block...)
	}

	return protocol.ParseMessages(allData)
}

func (f *Fetcher) queryResolver(resolver, qname string) ([]byte, error) {
	if !strings.Contains(resolver, ":") {
		resolver = resolver + ":53"
	}

	c := new(dns.Client)
	c.Timeout = f.timeout

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(qname), dns.TypeTXT)
	m.RecursionDesired = true

	resp, _, err := c.Exchange(m, resolver)
	if err != nil {
		return nil, fmt.Errorf("dns exchange with %s: %w", resolver, err)
	}

	if resp.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("dns error from %s: %s", resolver, dns.RcodeToString[resp.Rcode])
	}

	for _, ans := range resp.Answer {
		if txt, ok := ans.(*dns.TXT); ok {
			encoded := strings.Join(txt.Txt, "")
			return protocol.DecodeResponse(f.responseKey, encoded)
		}
	}

	return nil, fmt.Errorf("no TXT record in response from %s", resolver)
}
