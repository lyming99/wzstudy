package feed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"wzstudy/internal/study"
)

const MaxFeedBytes = 2 << 20

var ErrUnsafe = errors.New("unsafe_feed_address")
var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func PublicIP(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.Zone() != "" || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range reserved {
		if p.Contains(a) {
			return false
		}
	}
	if a.Is6() && !netip.MustParsePrefix("2000::/3").Contains(a) {
		return false
	}
	return true
}
func validateURL(u *url.URL) error {
	if u == nil || !study.ValidURL(u.String()) || strings.Contains(u.Hostname(), "%") || (u.Port() != "" && u.Port() != "80" && u.Port() != "443") {
		return ErrUnsafe
	}
	return nil
}

// The resolver result is checked and the actual socket is pinned to that IP.
// Never use ProxyFromEnvironment: proxies can bypass destination validation.
func SafeClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{Proxy: nil, MaxIdleConns: 16, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 64 << 10, ForceAttemptHTTP2: true}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil || port != "80" && port != "443" {
			return nil, ErrUnsafe
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil {
			return nil, errors.New("feed_dns_failed")
		}
		if len(ips) == 0 {
			return nil, ErrUnsafe
		}
		for _, ip := range ips {
			if !PublicIP(ip) {
				return nil, ErrUnsafe
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Transport: tr, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too_many_feed_redirects")
		}
		if e := validateURL(req.URL); e != nil {
			return e
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return ErrUnsafe
		}
		return nil
	}}
}

type Result struct {
	Items        []study.Content
	NotModified  bool
	ETag         string
	LastModified string
}
type Fetcher struct{ client *http.Client }

func NewFetcher() *Fetcher { return &Fetcher{client: SafeClient()} }
func (f *Fetcher) Fetch(ctx context.Context, s study.Source) (out Result, err error) {
	u, e := url.Parse(s.FeedURL)
	if e != nil {
		return out, ErrUnsafe
	}
	if e = validateURL(u); e != nil {
		return out, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return out, e
	}
	req.Header.Set("User-Agent", "WzStudy/0.1 (+learning-feed-service)")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")
	if s.ETag != "" {
		req.Header.Set("If-None-Match", s.ETag)
	}
	if s.LastModified != "" {
		req.Header.Set("If-Modified-Since", s.LastModified)
	}
	resp, e := f.client.Do(req)
	if e != nil {
		return out, errors.New("feed_request_failed")
	}
	defer resp.Body.Close()
	out.ETag = resp.Header.Get("ETag")
	out.LastModified = resp.Header.Get("Last-Modified")
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		if out.ETag == "" {
			out.ETag = s.ETag
		}
		if out.LastModified == "" {
			out.LastModified = s.LastModified
		}
		return out, nil
	}
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("feed_http_status_%d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, MaxFeedBytes+1))
	if e != nil {
		return out, errors.New("feed_read_failed")
	}
	if len(b) > MaxFeedBytes {
		return out, errors.New("feed_too_large")
	}
	out.Items, e = Parse(b, resp.Request.URL)
	return out, e
}
