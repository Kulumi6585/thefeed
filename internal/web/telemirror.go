// Optional, removable backup feed. Keep this file isolated from the rest
// of the web package so deleting it (plus the routes / clear-cache wiring
// in web.go and the BEGIN/END telemirror block in static/index.html) is
// enough to drop the feature.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sartoopjj/thefeed/internal/telemirror"
)

type telemirrorHub struct {
	client *telemirror.Client
	cache  *telemirror.Cache
	store  *telemirror.Store

	mu         sync.Mutex
	refreshing map[string]chan struct{}
}

func newTelemirrorHub(dataDir string) *telemirrorHub {
	return &telemirrorHub{
		client:     telemirror.NewClient(),
		cache:      telemirror.NewCache(filepath.Join(dataDir, "telemirror")),
		store:      telemirror.NewStore(dataDir),
		refreshing: make(map[string]chan struct{}),
	}
}

func (h *telemirrorHub) handleChannels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list := h.store.List()
		type entry struct {
			Username string `json:"username"`
			Pinned   bool   `json:"pinned"`
		}
		out := make([]entry, 0, len(list))
		for _, u := range list {
			out = append(out, entry{Username: u, Pinned: telemirror.IsDefault(u)})
		}
		writeJSON(w, map[string]any{"channels": out})

	case http.MethodPost:
		var req struct {
			Username string `json:"username"`
			Action   string `json:"action"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		switch req.Action {
		case "add":
			if err := h.store.Add(req.Username); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		case "remove":
			if err := h.store.Remove(req.Username); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		default:
			http.Error(w, "unknown action", 400)
			return
		}
		writeJSON(w, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleChannel serves /api/telemirror/channel/<username>[?refresh=1].
// Stale-while-revalidate: serve cached content immediately, refresh in
// the background when it's older than FreshTTL.
func (h *telemirrorHub) handleChannel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	username := telemirror.SanitizeUsername(strings.TrimPrefix(r.URL.Path, "/api/telemirror/channel/"))
	if username == "" {
		http.Error(w, "missing username", 400)
		return
	}
	forceRefresh := r.URL.Query().Get("refresh") == "1"

	cached, fresh := h.cache.Get(username)
	if cached != nil && fresh && !forceRefresh {
		writeJSON(w, rewriteImageURLs(cached))
		return
	}
	if cached != nil && !forceRefresh {
		go func() { _, _ = h.refresh(username) }()
		writeJSON(w, rewriteImageURLs(cached))
		return
	}
	res, err := h.refresh(username)
	if err != nil {
		if cached != nil {
			writeJSON(w, rewriteImageURLs(cached))
			return
		}
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, rewriteImageURLs(res))
}

// rewriteImageURLs returns a copy of the result with image URLs pointed
// at our /api/telemirror/img proxy. Only the bytes-bearing fields are
// rewritten (Channel.Photo, Media.Thumb). Media.URL is the POST
// permalink — rewriting it as an image was the "weird URL" bug:
// clicking a photo fetched the post HTML through the image proxy.
func rewriteImageURLs(in *telemirror.FetchResult) *telemirror.FetchResult {
	if in == nil {
		return nil
	}
	cp := *in
	cp.Channel.Photo = proxyImgURL(cp.Channel.Photo)
	cp.Posts = make([]telemirror.Post, len(in.Posts))
	for i, p := range in.Posts {
		p.Media = append([]telemirror.Media(nil), p.Media...)
		for j := range p.Media {
			// Leave Media.URL alone (it's a permalink, not bytes).
			p.Media[j].Thumb = proxyImgURL(p.Media[j].Thumb)
		}
		cp.Posts[i] = p
	}
	return &cp
}

// proxyImgURL routes an image URL through our /api/telemirror/img
// endpoint, applying the translate.goog hostname rewrite when the
// source is a Telegram CDN. Google's Translate proxy doesn't rewrite
// inline background-image URLs, so the parser hands us the original
// cdn4.telegram.org / cdn4.telesco.pe URLs and we have to translate
// them ourselves before fetching through fronting.
func proxyImgURL(raw string) string {
	rewritten := translateGoogify(raw)
	if rewritten == "" {
		return raw
	}
	return "/api/telemirror/img?u=" + url.QueryEscape(rewritten)
}

// translateGoogify maps a CDN URL to its Google-Translate-proxied form.
// Returns "" if the URL is something we shouldn't proxy at all.
//
// Examples:
//
//	https://cdn4.telegram.org/file/abc.jpg
//	  → https://cdn4-telegram-org.translate.goog/file/abc.jpg
//	https://cdn1.telesco.pe/file/x.jpg
//	  → https://cdn1-telesco-pe.translate.goog/file/x.jpg
//	https://cdn4-telegram-org.translate.goog/file/abc.jpg
//	  → unchanged (already on translate.goog)
func translateGoogify(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "https://") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Host)
	switch {
	case strings.HasSuffix(host, ".translate.goog"):
		// Already on translate.goog — proxy directly.
		return raw
	case strings.HasSuffix(host, ".telegram.org"),
		strings.HasSuffix(host, ".telesco.pe"),
		host == "t.me":
		// Replace every "." with "-" and append ".translate.goog".
		// Same scheme Google's proxy uses.
		u.Host = strings.ReplaceAll(host, ".", "-") + ".translate.goog"
		return u.String()
	}
	return ""
}

// isProxiableHost — only the translate.goog form. Used by the /api/telemirror/img
// handler to validate the `u` query parameter, after the JSON
// rewrite has converted Telegram CDN URLs to their .translate.goog equivalent.
func isProxiableHost(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return strings.HasSuffix(host, ".translate.goog")
}

// handleImg proxies a single image (or other binary) URL through the
// same fronting path used for the channel widget.
func (h *telemirrorHub) handleImg(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	raw := r.URL.Query().Get("u")
	if raw == "" {
		http.Error(w, "missing u", 400)
		return
	}
	if !isProxiableHost(raw) {
		http.Error(w, "host not allowed", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	body, ctype, err := h.client.FetchURL(ctx, raw)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	// If the upstream returned HTML or some non-image content (a Google
	// error page, captcha, etc.), surface a 502 instead of relaying it.
	// The browser fires onerror reliably on a 502, so the avatar / image
	// fallback in the JS kicks in cleanly.
	low := strings.ToLower(ctype)
	if !strings.HasPrefix(low, "image/") && !strings.HasPrefix(low, "video/") && !strings.HasPrefix(low, "audio/") && low != "" {
		http.Error(w, "upstream not an image: "+ctype, 502)
		return
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
}

// refresh fetches and parses a channel, coalescing concurrent calls for
// the same username so we don't hit the upstream more than once at a time.
func (h *telemirrorHub) refresh(username string) (*telemirror.FetchResult, error) {
	username = strings.ToLower(telemirror.SanitizeUsername(username))
	if username == "" {
		return nil, telemirror.ErrEmptyUsername
	}

	h.mu.Lock()
	if ch, ok := h.refreshing[username]; ok {
		h.mu.Unlock()
		<-ch
		if r, _ := h.cache.Get(username); r != nil {
			return r, nil
		}
		return nil, fmt.Errorf("telemirror: concurrent refresh did not produce a result")
	}
	done := make(chan struct{})
	h.refreshing[username] = done
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.refreshing, username)
		h.mu.Unlock()
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	body, err := h.client.FetchHTML(ctx, username)
	if err != nil {
		return nil, err
	}
	chInfo, posts, err := telemirror.ParseHTML(body)
	if err != nil {
		return nil, err
	}
	// Reject "successful" responses that have no widget content — usually
	// a captcha / rate-limit / soft-error page returned with status 200.
	if len(posts) == 0 && chInfo.Title == "" && chInfo.Description == "" {
		return nil, fmt.Errorf("telemirror: empty widget for %q", username)
	}
	if chInfo.Username == "" {
		chInfo.Username = username
	}
	res := &telemirror.FetchResult{Channel: *chInfo, Posts: posts}
	_ = h.cache.Put(username, res)
	return res, nil
}

func (h *telemirrorHub) ClearCache() { h.cache.Clear() }
