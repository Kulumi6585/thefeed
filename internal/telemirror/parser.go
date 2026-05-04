package telemirror

import (
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ParseHTML extracts channel info and recent posts from a t.me/s/ widget.
// The HTML is the rendered output of Google Translate's proxy, which keeps
// the original Telegram class names but rewrites cross-domain URLs onto
// translate.goog so the browser can load them through the same proxy.
func ParseHTML(htmlBody string) (*Channel, []Post, error) {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return nil, nil, err
	}
	return parseChannelInfo(doc), parsePosts(doc), nil
}

func parseChannelInfo(doc *html.Node) *Channel {
	ch := &Channel{}

	if titleEl := findFirstByClass(doc, "tgme_channel_info_header_title"); titleEl != nil {
		if span := findFirstChildElement(titleEl, "span"); span != nil {
			ch.Title = textOf(span)
		} else {
			ch.Title = textOf(titleEl)
		}
	}
	if userEl := findFirstByClass(doc, "tgme_channel_info_header_username"); userEl != nil {
		if a := findFirstChildElement(userEl, "a"); a != nil {
			ch.Username = strings.TrimPrefix(textOf(a), "@")
		}
	}
	if descEl := findFirstByClass(doc, "tgme_channel_info_description"); descEl != nil {
		ch.Description = innerHTML(descEl)
	}
	if header := findFirstByClass(doc, "tgme_channel_info_header"); header != nil {
		if img := findFirstByTag(header, "img"); img != nil {
			ch.Photo = attrOf(img, "src")
		}
	}
	if cnt := findFirstByClass(doc, "tgme_channel_info_counter"); cnt != nil {
		ch.Subscribers = textOf(cnt)
	}
	return ch
}

func parsePosts(doc *html.Node) []Post {
	var posts []Post
	visit(doc, func(n *html.Node) bool {
		if !hasClass(n, "tgme_widget_message_wrap") {
			return true
		}
		if p := parseSinglePost(n); p != nil {
			posts = append(posts, *p)
		}
		return false // posts don't nest
	})
	return posts
}

func parseSinglePost(wrap *html.Node) *Post {
	msg := findFirstByClass(wrap, "tgme_widget_message")
	if msg == nil {
		msg = wrap
	}
	p := &Post{ID: attrOf(msg, "data-post")}

	if owner := findFirstByClass(msg, "tgme_widget_message_owner_name"); owner != nil {
		p.Author = textOf(owner)
	}
	if textEl := findFirstByClass(msg, "tgme_widget_message_text"); textEl != nil {
		p.Text = innerHTML(textEl)
	}

	visit(msg, func(n *html.Node) bool {
		switch {
		case hasClass(n, "tgme_widget_message_photo_wrap"):
			p.Media = append(p.Media, Media{
				Type:  "photo",
				URL:   attrOf(n, "href"),
				Thumb: extractBgImage(attrOf(n, "style")),
			})
		case hasClass(n, "tgme_widget_message_video_player"):
			m := Media{Type: "video", URL: attrOf(n, "href")}
			if t := findFirstByClass(n, "tgme_widget_message_video_thumb"); t != nil {
				m.Thumb = extractBgImage(attrOf(t, "style"))
			}
			if d := findFirstByClass(n, "message_video_duration"); d != nil {
				m.Duration = textOf(d)
			}
			p.Media = append(p.Media, m)
		}
		return true
	})

	if dateEl := findFirstByClass(msg, "tgme_widget_message_date"); dateEl != nil {
		if tag := findFirstByTag(dateEl, "time"); tag != nil {
			if dt := attrOf(tag, "datetime"); dt != "" {
				if parsed, err := time.Parse(time.RFC3339, dt); err == nil {
					p.Time = parsed
				}
			}
		}
	}
	if v := findFirstByClass(msg, "tgme_widget_message_views"); v != nil {
		p.Views = textOf(v)
	}
	if meta := findFirstByClass(msg, "tgme_widget_message_meta"); meta != nil {
		if strings.Contains(strings.ToLower(textOf(meta)), "edited") {
			p.Edited = true
		}
	}

	if p.ID == "" && p.Text == "" && len(p.Media) == 0 {
		return nil
	}
	return p
}

// ===== DOM helpers =====

func visit(n *html.Node, fn func(*html.Node) bool) {
	if !fn(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		visit(c, fn)
	}
}

func hasClass(n *html.Node, class string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, a := range n.Attr {
		if a.Key != "class" {
			continue
		}
		for _, c := range strings.Fields(a.Val) {
			if c == class {
				return true
			}
		}
	}
	return false
}

func attrOf(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func findFirstByClass(root *html.Node, class string) *html.Node {
	var found *html.Node
	visit(root, func(n *html.Node) bool {
		if found != nil {
			return false
		}
		if hasClass(n, class) {
			found = n
			return false
		}
		return true
	})
	return found
}

func findFirstByTag(root *html.Node, tag string) *html.Node {
	var found *html.Node
	visit(root, func(n *html.Node) bool {
		if found != nil {
			return false
		}
		if n.Type == html.ElementNode && n.Data == tag {
			found = n
			return false
		}
		return true
	})
	return found
}

func findFirstChildElement(n *html.Node, tag string) *html.Node {
	if n == nil {
		return nil
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			return c
		}
	}
	return nil
}

func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	visit(n, func(x *html.Node) bool {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		return true
	})
	return strings.TrimSpace(b.String())
}

// innerHTML serialises children only — drops the wrapping element so the
// caller can splice the result into its own container.
func innerHTML(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return ""
		}
	}
	return strings.TrimSpace(b.String())
}

var bgImageRe = regexp.MustCompile(`url\(['"]?([^'")]+)['"]?\)`)

func extractBgImage(style string) string {
	m := bgImageRe.FindStringSubmatch(style)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}
