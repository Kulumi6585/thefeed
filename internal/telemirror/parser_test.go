package telemirror

import (
	"strings"
	"testing"
)

const sampleChannel = `<!DOCTYPE html><html><body>
<div class="tgme_channel_info">
  <div class="tgme_channel_info_header">
    <i class="tgme_page_photo_image"><img src="https://cdn4-telegram-org.translate.goog/file/abc.jpg"/></i>
    <div class="tgme_channel_info_header_title"><span dir="auto">Sample Channel</span></div>
    <div class="tgme_channel_info_header_username"><a href="https://t-me.translate.goog/sample">@sample</a></div>
  </div>
  <div class="tgme_channel_info_description">channel <b>description</b> with <a href="https://example.com">link</a></div>
  <div class="tgme_channel_info_counter"><span class="counter_value">12.3K</span> <span class="counter_type">subscribers</span></div>
</div>

<div class="tgme_widget_message_wrap">
  <div class="tgme_widget_message" data-post="sample/123">
    <a class="tgme_widget_message_owner_name" href="#"><span dir="auto">Sample Channel</span></a>
    <div class="tgme_widget_message_text">first <b>post</b> body</div>
    <a class="tgme_widget_message_photo_wrap" href="https://t-me.translate.goog/sample/123" style="background-image:url('https://cdn4.translate.goog/photo.jpg')"></a>
    <div class="tgme_widget_message_footer">
      <span class="tgme_widget_message_views">1.2K</span>
      <a class="tgme_widget_message_date" href="#"><time datetime="2026-04-30T12:34:56+00:00">Apr 30</time></a>
      <span class="tgme_widget_message_meta">edited</span>
    </div>
  </div>
</div>

<div class="tgme_widget_message_wrap">
  <div class="tgme_widget_message" data-post="sample/124">
    <div class="tgme_widget_message_text">second post</div>
    <a class="tgme_widget_message_video_player" href="https://t-me.translate.goog/sample/124">
      <i class="tgme_widget_message_video_thumb" style="background-image:url(https://cdn4.translate.goog/vid.jpg)"></i>
      <time class="message_video_duration">0:42</time>
    </a>
  </div>
</div>
</body></html>`

func TestParseHTMLChannelHeader(t *testing.T) {
	ch, _, err := ParseHTML(sampleChannel)
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	if ch.Title != "Sample Channel" {
		t.Errorf("title = %q, want %q", ch.Title, "Sample Channel")
	}
	if ch.Username != "sample" {
		t.Errorf("username = %q, want %q", ch.Username, "sample")
	}
	if !strings.Contains(ch.Description, "description") {
		t.Errorf("description = %q, missing 'description'", ch.Description)
	}
	if ch.Photo == "" {
		t.Errorf("photo url empty")
	}
	if !strings.Contains(ch.Subscribers, "12.3K") {
		t.Errorf("subscribers = %q, missing '12.3K'", ch.Subscribers)
	}
}

func TestParseHTMLPosts(t *testing.T) {
	_, posts, err := ParseHTML(sampleChannel)
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(posts))
	}

	p1 := posts[0]
	if p1.ID != "sample/123" {
		t.Errorf("post 1 id = %q", p1.ID)
	}
	if !strings.Contains(p1.Text, "first") {
		t.Errorf("post 1 text = %q", p1.Text)
	}
	if len(p1.Media) != 1 || p1.Media[0].Type != "photo" {
		t.Errorf("post 1 media = %+v, want one photo", p1.Media)
	}
	if p1.Media[0].Thumb == "" {
		t.Errorf("post 1 photo thumb missing")
	}
	if p1.Views != "1.2K" {
		t.Errorf("post 1 views = %q", p1.Views)
	}
	if !p1.Edited {
		t.Errorf("post 1 should be marked edited")
	}
	if p1.Time.IsZero() {
		t.Errorf("post 1 time not parsed")
	}

	p2 := posts[1]
	if p2.ID != "sample/124" {
		t.Errorf("post 2 id = %q", p2.ID)
	}
	if len(p2.Media) != 1 || p2.Media[0].Type != "video" {
		t.Errorf("post 2 media = %+v, want one video", p2.Media)
	}
	if p2.Media[0].Duration != "0:42" {
		t.Errorf("post 2 duration = %q", p2.Media[0].Duration)
	}
	if p2.Media[0].Thumb == "" {
		t.Errorf("post 2 video thumb missing")
	}
}

func TestParseHTMLEmpty(t *testing.T) {
	ch, posts, err := ParseHTML("<html></html>")
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	if ch == nil {
		t.Fatal("channel nil")
	}
	if len(posts) != 0 {
		t.Errorf("posts = %d, want 0", len(posts))
	}
}

func TestSanitizeUsername(t *testing.T) {
	cases := []struct{ in, want string }{
		{"@VahidOnline", "VahidOnline"},
		{"  @VahidOnline  ", "VahidOnline"},
		{"https://t.me/networkti", "networkti"},
		{"t.me/s/networkti", "networkti"},
		{"networkti?embed=1", "networkti"},
		{"bad name with spaces", "badnamewithspaces"},
		{"خبر", ""},
		{"", ""},
		{strings.Repeat("a", 64), strings.Repeat("a", 32)},
	}
	for _, c := range cases {
		if got := SanitizeUsername(c.in); got != c.want {
			t.Errorf("SanitizeUsername(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsDefault(t *testing.T) {
	for _, want := range DefaultChannels {
		if !IsDefault(want) {
			t.Errorf("IsDefault(%q) = false", want)
		}
		if !IsDefault(strings.ToLower(want)) {
			t.Errorf("IsDefault(%q) case-sensitive miss", strings.ToLower(want))
		}
	}
	if IsDefault("not_a_default") {
		t.Errorf("IsDefault returned true for unknown channel")
	}
}

func TestStoreAddRemove(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if err := s.Add("@MyChan"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Adding a default is a no-op (success, no duplicate).
	if err := s.Add(DefaultChannels[0]); err != nil {
		t.Errorf("Add default: %v", err)
	}
	list := s.List()
	if len(list) < len(DefaultChannels)+1 {
		t.Fatalf("list = %v, want defaults + MyChan", list)
	}
	// Defaults pinned at the front.
	for i, want := range DefaultChannels {
		if list[i] != want {
			t.Errorf("list[%d] = %q, want %q", i, list[i], want)
		}
	}
	// MyChan present and at the end.
	if list[len(list)-1] != "MyChan" {
		t.Errorf("last entry = %q, want MyChan", list[len(list)-1])
	}

	// Removing a default is rejected.
	if err := s.Remove(DefaultChannels[0]); err != ErrPinnedChannel {
		t.Errorf("Remove default = %v, want ErrPinnedChannel", err)
	}
	if err := s.Remove("MyChan"); err != nil {
		t.Errorf("Remove: %v", err)
	}
	list = s.List()
	for _, u := range list {
		if strings.EqualFold(u, "MyChan") {
			t.Errorf("MyChan still present after Remove: %v", list)
		}
	}
}

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)

	if r, _ := c.Get("nope"); r != nil {
		t.Errorf("Get on missing returned non-nil: %+v", r)
	}

	res := &FetchResult{
		Channel: Channel{Username: "test", Title: "Test"},
		Posts:   []Post{{ID: "test/1", Text: "hi"}},
	}
	if err := c.Put("test", res); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, fresh := c.Get("test")
	if got == nil {
		t.Fatal("Get returned nil after Put")
	}
	if !fresh {
		t.Errorf("expected fresh entry right after Put")
	}
	if got.Channel.Title != "Test" || len(got.Posts) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	c.Clear()
	if got, _ := c.Get("test"); got != nil {
		t.Errorf("Get after Clear returned %+v", got)
	}
}
