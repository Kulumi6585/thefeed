package server

import (
	"context"
	"log"
	"strings"

	"github.com/miekg/dns"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

// DNSServer serves feed data over DNS TXT queries.
type DNSServer struct {
	domain      string
	feed        *Feed
	queryKey    [protocol.KeySize]byte
	responseKey [protocol.KeySize]byte
	listenAddr  string
	maxPadding  int
}

// NewDNSServer creates a DNS server for the given domain.
func NewDNSServer(listenAddr, domain string, feed *Feed, queryKey, responseKey [protocol.KeySize]byte, maxPadding int) *DNSServer {
	return &DNSServer{
		domain:      strings.TrimSuffix(domain, "."),
		feed:        feed,
		queryKey:    queryKey,
		responseKey: responseKey,
		listenAddr:  listenAddr,
		maxPadding:  maxPadding,
	}
}

// ListenAndServe starts the DNS server on UDP, shutting down when ctx is cancelled.
func (s *DNSServer) ListenAndServe(ctx context.Context) error {
	mux := dns.NewServeMux()
	mux.HandleFunc(s.domain+".", s.handleQuery)

	server := &dns.Server{
		Addr:    s.listenAddr,
		Net:     "udp",
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		log.Println("[dns] shutting down...")
		server.Shutdown()
	}()

	log.Printf("[dns] listening on %s (domain: %s)", s.listenAddr, s.domain)
	return server.ListenAndServe()
}

func (s *DNSServer) handleQuery(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true

	if len(r.Question) == 0 {
		w.WriteMsg(m)
		return
	}

	q := r.Question[0]
	if q.Qtype != dns.TypeTXT {
		m.Rcode = dns.RcodeNameError
		w.WriteMsg(m)
		return
	}

	channel, block, err := protocol.DecodeQuery(s.queryKey, q.Name, s.domain)
	if err != nil {
		log.Printf("[dns] decode query: %v", err)
		m.Rcode = dns.RcodeNameError
		w.WriteMsg(m)
		return
	}

	data, err := s.feed.GetBlock(int(channel), int(block))
	if err != nil {
		log.Printf("[dns] get block ch=%d blk=%d: %v", channel, block, err)
		m.Rcode = dns.RcodeNameError
		w.WriteMsg(m)
		return
	}

	encoded, err := protocol.EncodeResponse(s.responseKey, data, s.maxPadding)
	if err != nil {
		log.Printf("[dns] encode response: %v", err)
		m.Rcode = dns.RcodeServerFailure
		w.WriteMsg(m)
		return
	}

	// Split base64 string into 255-byte TXT chunks
	txtParts := splitTXT(encoded)

	m.Answer = append(m.Answer, &dns.TXT{
		Hdr: dns.RR_Header{
			Name:   q.Name,
			Rrtype: dns.TypeTXT,
			Class:  dns.ClassINET,
			Ttl:    1,
		},
		Txt: txtParts,
	})

	w.WriteMsg(m)
}

// splitTXT splits a string into 255-byte chunks for DNS TXT records.
func splitTXT(s string) []string {
	var parts []string
	for len(s) > 255 {
		parts = append(parts, s[:255])
		s = s[255:]
	}
	if len(s) > 0 {
		parts = append(parts, s)
	}
	return parts
}
