package controlplane

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type loginAttempt struct {
	count   int
	resetAt time.Time
}

type loginLimiter struct {
	mu           sync.Mutex
	window       time.Duration
	accountLimit int
	ipLimit      int
	now          func() time.Time
	accounts     map[string]loginAttempt
	ips          map[string]loginAttempt
}

func newLoginLimiter(config Config) *loginLimiter {
	return &loginLimiter{
		window:       config.LoginAttemptWindow,
		accountLimit: config.LoginMaxAttemptsPerAccount,
		ipLimit:      config.LoginMaxAttemptsPerIP,
		now:          func() time.Time { return time.Now().UTC() },
		accounts:     make(map[string]loginAttempt),
		ips:          make(map[string]loginAttempt),
	}
}

func (l *loginLimiter) allow(account, ip string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.cleanExpired(now)
	retryAfter := time.Duration(0)
	if attempt := l.accounts[account]; attempt.count >= l.accountLimit && now.Before(attempt.resetAt) {
		retryAfter = attempt.resetAt.Sub(now)
	}
	if attempt := l.ips[ip]; attempt.count >= l.ipLimit && now.Before(attempt.resetAt) && attempt.resetAt.Sub(now) > retryAfter {
		retryAfter = attempt.resetAt.Sub(now)
	}
	return retryAfter, retryAfter > 0
}

func (l *loginLimiter) recordFailure(account, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.cleanExpired(now)
	l.accounts[account] = nextLoginAttempt(l.accounts[account], now, l.window)
	l.ips[ip] = nextLoginAttempt(l.ips[ip], now, l.window)
}

func (l *loginLimiter) clearAccount(account string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.accounts, account)
}

func (l *loginLimiter) cleanExpired(now time.Time) {
	for key, attempt := range l.accounts {
		if !now.Before(attempt.resetAt) {
			delete(l.accounts, key)
		}
	}
	for key, attempt := range l.ips {
		if !now.Before(attempt.resetAt) {
			delete(l.ips, key)
		}
	}
}

func nextLoginAttempt(attempt loginAttempt, now time.Time, window time.Duration) loginAttempt {
	if !now.Before(attempt.resetAt) {
		return loginAttempt{count: 1, resetAt: now.Add(window)}
	}
	attempt.count++
	return attempt
}

func requestClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	direct, err := netip.ParseAddr(host)
	if err == nil && trustedProxy(direct, trusted) {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
			if client, err := netip.ParseAddr(forwarded); err == nil {
				return client.String()
			}
		}
	}
	if err == nil {
		return direct.String()
	}
	return host
}

func trustedProxy(address netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
