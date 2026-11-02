package httpapi

import (
	"net"
	"sync"
	"time"
)

type collectionTriggerWindow struct {
	start time.Time
	count int
}
type collectionTriggerLimiter struct {
	mu     sync.Mutex
	ips    map[string]collectionTriggerWindow
	global collectionTriggerWindow
}

// Bound unauthenticated work as well as accepted events. Do not trust arbitrary
// X-Forwarded-For headers. Reverse proxies should apply their own public limits.
func (l *collectionTriggerLimiter) allow(remote string, now time.Time) bool {
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		ip = remote
	}
	if len(ip) > 64 {
		ip = ip[:64]
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global.start.IsZero() || now.Sub(l.global.start) >= time.Minute {
		l.global = collectionTriggerWindow{start: now}
	}
	if l.global.count >= 600 {
		return false
	}
	l.global.count++
	if l.ips == nil {
		l.ips = map[string]collectionTriggerWindow{}
	}
	bucket, found := l.ips[ip]
	if !found && len(l.ips) >= 1024 {
		for key, value := range l.ips {
			if now.Sub(value.start) >= time.Minute {
				delete(l.ips, key)
			}
		}
		if len(l.ips) >= 1024 {
			return false
		}
	}
	if bucket.start.IsZero() || now.Sub(bucket.start) >= time.Minute {
		bucket = collectionTriggerWindow{start: now}
	}
	if bucket.count >= 60 {
		return false
	}
	bucket.count++
	l.ips[ip] = bucket
	return true
}
