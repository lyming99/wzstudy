package feed

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"wzstudy/internal/study"
)

func TestPublicIP(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1", "240.0.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1", "64:ff9b::a00:1", "2002:7f00:1::"} {
		if PublicIP(netip.MustParseAddr(raw)) {
			t.Fatalf("allowed %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !PublicIP(netip.MustParseAddr(raw)) {
			t.Fatalf("blocked public %s", raw)
		}
	}
}
func TestSafeClientBlocksLocalDestinations(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/feed", "http://localhost/feed", "http://[::1]/feed", "http://169.254.169.254/feed", "http://example.com:8080/feed", "file:///etc/passwd", "https://user:pass@example.com/feed"} {
		if _, e := NewFetcher().Fetch(context.Background(), study.Source{FeedURL: raw}); e == nil {
			t.Fatalf("allowed %s", raw)
		}
	}
	req, _ := http.NewRequest("GET", "http://example.com/feed", nil)
	prev, _ := http.NewRequest("GET", "https://example.com/feed", nil)
	if e := SafeClient().CheckRedirect(req, []*http.Request{prev}); e == nil {
		t.Fatal("allowed HTTPS downgrade")
	}
}
func TestParseRSSAndAtom(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed")
	rss := `<?xml version="1.0"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><item><guid>stable-1</guid><title><![CDATA[<b>学习 Go</b>]]></title><link>/go</link><description><![CDATA[<script>alert(1)</script><p>摘要</p>]]></description><content:encoded><![CDATA[<p>正文 &amp; 更多</p>]]></content:encoded><pubDate>Fri, 23 Oct 2026 08:00:00 +0000</pubDate><category>Go</category></item></channel></rss>`
	rows, e := Parse([]byte(rss), base)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	v := rows[0]
	if v.ExternalID != "stable-1" || v.Title != "学习 Go" || strings.Contains(v.Summary, "alert") || v.Content != "正文 & 更多" || v.URL != "https://example.com/go" || v.PublishedAt == nil {
		t.Fatalf("%+v", v)
	}
	atom := `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>urn:lesson:1</id><title type="html">&lt;b&gt;Atom&lt;/b&gt;</title><link rel="alternate" href="/atom"/><summary>摘要</summary><content type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml"><p>内容</p></div></content><updated>2026-10-23T08:00:00Z</updated><author><name>作者</name></author><category term="学习"/></entry></feed>`
	rows, e = Parse([]byte(atom), base)
	if e != nil || len(rows) != 1 || rows[0].Title != "Atom" || rows[0].Content != "内容" || rows[0].SourceUpdatedAt == nil || rows[0].Author != "作者" {
		t.Fatal(rows, e)
	}
}
func TestInvalidFeeds(t *testing.T) {
	for _, raw := range []string{`<html/>`, `<rss><channel><item><title>no identity</title></item></channel></rss>`, `<!DOCTYPE x [<!ENTITY a SYSTEM "file:///etc/passwd">]><rss/>`, `<rss><channel><item><guid>x</guid><title>x</title></item><item><guid>x</guid><title>duplicate</title></item></channel></rss>`, `<rss>`, string([]byte{255})} {
		if _, e := Parse([]byte(raw), nil); e == nil {
			t.Fatalf("accepted invalid feed %s", raw)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConditionalRequestsAndSizeLimit(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Request: r}
		response.Header.Set("ETag", "tag")
		body := `<rss><channel/></rss>`
		if r.Header.Get("If-None-Match") == "tag" {
			response.StatusCode = 304
			body = ""
		}
		if r.URL.Path == "/large" {
			body = strings.Repeat("x", MaxFeedBytes+1)
		}
		response.Body = io.NopCloser(strings.NewReader(body))
		return response, nil
	})
	f := &Fetcher{client: &http.Client{Transport: transport}}
	result, e := f.Fetch(context.Background(), study.Source{FeedURL: "https://example.com/feed"})
	if e != nil || result.ETag != "tag" {
		t.Fatal(result, e)
	}
	result, e = f.Fetch(context.Background(), study.Source{FeedURL: "https://example.com/feed", ETag: "tag"})
	if e != nil || !result.NotModified || result.ETag != "tag" {
		t.Fatal(result, e)
	}
	if _, e = f.Fetch(context.Background(), study.Source{FeedURL: "https://example.com/large"}); e == nil {
		t.Fatal("size limit ignored")
	}
}

type fakeFetcher struct{ calls int }

func (f *fakeFetcher) Fetch(ctx context.Context, s study.Source) (Result, error) {
	f.calls++
	return Result{Items: []study.Content{{ExternalID: "lesson", Title: "lesson"}}, ETag: "v1"}, nil
}
func TestWorkerCommitsAndDeduplicates(t *testing.T) {
	service := study.NewService(study.NewMemoryStore())
	source, _, e := service.CreateSource(context.Background(), study.SourceInput{Slug: "worker", Name: "worker", Kind: "rss", FeedURL: "https://example.com/rss", AutoPublish: true})
	if e != nil {
		t.Fatal(e)
	}
	fetcher := &fakeFetcher{}
	w := Worker{Service: service, Fetcher: fetcher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	first, e := w.Once(context.Background(), source.ID, true)
	if e != nil || first.Created != 1 {
		t.Fatal(first, e)
	}
	second, e := w.Once(context.Background(), source.ID, true)
	if e != nil || second.Unchanged != 1 {
		t.Fatal(second, e)
	}
	page, e := service.Changes(context.Background(), 0, 100, nil)
	if e != nil || len(page.Changes) != 1 {
		t.Fatal(page, e)
	}
	source, e = service.Source(context.Background(), source.ID)
	if e != nil || source.LastSuccessAt == nil || source.ETag != "v1" || source.PollToken != "" {
		t.Fatal(source, e)
	}
	job, e := service.ClaimPoll(context.Background(), source.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	unchanged, e := service.CompletePoll(context.Background(), job, nil, true, "v1", "", nil)
	if e != nil || unchanged.Items == nil || len(unchanged.Items) != 0 {
		t.Fatal("304 must return an empty item array", unchanged, e)
	}
}
