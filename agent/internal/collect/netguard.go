package collect

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var errBlockedAddress = errors.New("目标地址不允许访问")

// guardedDial resolves the host once, rejects every non-public address and then
// dials the verified IP. Dialing the IP prevents DNS rebinding between the check
// and the connection.
func guardedDial(allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(resolved) == 0 {
			return nil, errors.New("无法解析目标主机")
		}
		for _, candidate := range resolved {
			if !allowedIP(candidate.IP, allowPrivate) {
				return nil, errBlockedAddress
			}
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(ctx, network, net.JoinHostPort(resolved[0].IP.String(), port))
	}
}

func allowedIP(ip net.IP, allowPrivate bool) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.IsLoopback() {
		return allowPrivate
	}
	if ip.IsPrivate() {
		return allowPrivate
	}
	if len(ip) == net.IPv6len && ip.To4() == nil && ip[0]&0xfe == 0xfc {
		return allowPrivate
	}
	return true
}

func newHTTPClient(allowPrivate bool) *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			DialContext:           guardedDial(allowPrivate),
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			MaxIdleConns:          4,
		},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("重定向次数过多")
			}
			if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
				return errors.New("仅允许 http/https 重定向")
			}
			return nil
		},
	}
}

func validateTarget(raw string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, errors.New("url 必须是绝对的 http(s) 地址")
	}
	if target.User != nil {
		return nil, errors.New("url 不能包含凭据")
	}
	return target, nil
}
