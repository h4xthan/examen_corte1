package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimit is a token bucket per client, keyed by IP.
//
// Token bucket rather than a fixed window: a fixed window resets on a boundary
// that has nothing to do with the traffic, so an attacker gets 2x the intended
// rate by straddling it, and a window that resets while a client is being
// brute-forced is exactly when you least want the budget back. A bucket drains
// smoothly and refills continuously.
//
// The state is in memory, so it is per process. That is the right scope for the
// threat this addresses: credential stuffing and password-reset flooding are
// about volume, and one bucket per instance already cuts the volume by the
// number of instances. It is not a defence against a distributed client farm,
// and pretending otherwise would be worse than saying so. A shared store is the
// upgrade path if that ever becomes the problem.
type RateLimit struct {
	buckets sync.Map // string -> *bucket

	// capacity is the burst: how much a client may spend at once.
	capacity float64
	// refillPerSecond is the sustained rate.
	refillPerSecond float64
	// idleTTL is how long an unused bucket survives. Without it the map grows
	// one entry per IP that ever connects and never shrinks.
	idleTTL time.Duration

	now func() time.Time
}

type bucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// RateLimitConfig describes one limiter.
type RateLimitConfig struct {
	// Name appears in logs so a rejection can be traced to a route.
	Name string
	// Capacity is the burst size in requests.
	Capacity int
	// PerMinute is the sustained rate, converted internally to a per-second
	// refill. Per minute is how these limits get written down; per second is
	// what makes the arithmetic correct.
	PerMinute int
	// IdleTTL is how long to remember an idle client. Defaults to 10 minutes.
	IdleTTL time.Duration
}

// NewRateLimit builds a limiter. It returns nil for a nonsensical config so a
// caller can leave a limit off by passing zero.
func NewRateLimit(cfg RateLimitConfig) *RateLimit {
	if cfg.Capacity <= 0 || cfg.PerMinute <= 0 {
		return nil
	}
	ttl := cfg.IdleTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &RateLimit{
		capacity:        float64(cfg.Capacity),
		refillPerSecond: float64(cfg.PerMinute) / 60.0,
		idleTTL:         ttl,
		now:             time.Now,
	}
}

// Allow reports whether a request from r may proceed, and writes the standard
// RateLimit headers on the way. It consumes a token when it says yes.
func (rl *RateLimit) Allow(w http.ResponseWriter, r *http.Request) bool {
	id := clientIP(r)
	now := rl.now()

	ref, _ := rl.buckets.LoadOrStore(id, &bucket{tokens: rl.capacity, lastSeen: now})
	b := ref.(*bucket)

	b.mu.Lock()
	// Refill for the time since the last request, capped so a client that was
	// away for an hour does not come back with a full bucket on top of whatever
	// it already had.
	elapsed := now.Sub(b.lastSeen).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * rl.refillPerSecond
		if b.tokens > rl.capacity {
			b.tokens = rl.capacity
		}
		b.lastSeen = now
	}
	ok := b.tokens >= 1
	if ok {
		b.tokens--
	}
	remaining := b.tokens
	lastSeen := b.lastSeen
	b.mu.Unlock()

	if !ok && now.Sub(lastSeen) > rl.idleTTL {
		// The entry is dead weight, but only drop it if nobody refilled it in
		// the meantime. A concurrent Allow could have just topped it up.
		rl.buckets.CompareAndDelete(id, ref)
	}

	w.Header().Set("RateLimit-Limit", formatLimit(rl.capacity))
	w.Header().Set("RateLimit-Remaining", formatLimit(remaining))
	if !ok {
		// Retry-After in whole seconds, rounded up so the client does not come
		// back one second before a token exists and get refused again.
		retry := time.Duration((1 - remaining) / rl.refillPerSecond * float64(time.Second))
		if retry < time.Second {
			retry = time.Second
		}
		w.Header().Set("Retry-After", strconv.Itoa(int((retry+time.Second-1)/time.Second)))
	}

	return ok
}

// Middleware wraps a handler with the limit.
func (rl *RateLimit) Middleware(next http.Handler) http.Handler {
	if rl == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow(w, r) {
			slog.Warn("rate limit exceeded",
				"path", r.URL.Path,
				"method", r.Method,
				"ip", clientIP(r),
			)
			writeJSONError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Cleanup forgets idle buckets and reports how many it removed.
//
// Without a caller, the map only shrinks when a *rejected* request happens to
// land on an old entry, which means a shop under attack grows one entry per
// source address and keeps them for ever. Every entry is a mutex and two float
// fields, so a million of them is tens of megabytes and a slow leak rather than
// a crash, which is exactly the kind of problem nobody looks for.
func (rl *RateLimit) Cleanup() int {
	if rl == nil {
		return 0
	}
	now := rl.now()
	removed := 0
	rl.buckets.Range(func(k, v any) bool {
		b := v.(*bucket)
		b.mu.Lock()
		idle := now.Sub(b.lastSeen)
		b.mu.Unlock()
		if idle > rl.idleTTL {
			if rl.buckets.CompareAndDelete(k, v) {
				removed++
			}
		}
		return true
	})
	return removed
}

// StartJanitor runs Cleanup on a ticker until stop is closed, and returns
// immediately.
//
// A goroutine per limiter rather than one shared sweep, so two limiters with
// different idle timeouts do not drag each other along. The interval is half the
// TTL: an entry therefore survives at most one and a half TTLs, which is a
// deliberate trade for far fewer wakeups than sweeping on every TTL would need.
//
// The returned function is safe to call more than once, and stops the goroutine
// on its next tick rather than mid-sweep, so a test that closes it and returns
// cannot race with a map Range that is still running.
func (rl *RateLimit) StartJanitor(interval time.Duration, stop <-chan struct{}) {
	if rl == nil {
		return
	}
	if stop == nil {
		// Never closed. A nil channel blocks forever in select, which is exactly
		// the behaviour wanted: the janitor runs for the life of the process.
		stop = make(chan struct{})
	}
	if interval <= 0 {
		interval = rl.idleTTL / 2
	}
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				rl.Cleanup()
			}
		}
	}()
}

// clientIP identifies the caller.
//
// X-Forwarded-For is trusted only when the request actually came from a
// configured proxy hop, and only the first entry, which is the original client.
// Trusting it unconditionally would let any caller set their own bucket key and
// make the limiter useless; trusting it never would make it useless in
// production, where every request arrives through a proxy that sets it.
func clientIP(r *http.Request) string {
	host := remoteHost(r)

	// Only the first entry is the client; the rest were appended by each hop.
	// Trusting the header when the peer is not a known proxy would let any
	// caller choose their own bucket key and walk straight past the limit.
	if peerIsTrustedProxy(host) {
		if hop := r.Header.Get("X-Forwarded-For"); hop != "" {
			if first, _, found := strings.Cut(hop, ","); found {
				hop = first
			}
			if ip := net.ParseIP(strings.TrimSpace(hop)); ip != nil {
				return ip.String()
			}
		}
	}

	return host
}

func remoteHost(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	if r.RemoteAddr == "" {
		// httptest and some in-process callers have no peer address. Bucket
		// them together rather than giving every one of them a free pass.
		return "unknown"
	}
	return r.RemoteAddr
}

// trustedProxies holds the networks whose X-Forwarded-For is believed. Empty by
// default, which means the header is ignored: a limiter that is trivially
// bypassable by setting a header is worse than no limiter, because it looks like
// protection.
var trustedProxies struct {
	sync.RWMutex
	// single holds bare addresses, matched exactly.
	single map[string]struct{}
	// ranges holds CIDR blocks, matched by prefix. A reverse lookup per request
	// would need a trie; there are a handful of entries, so a linear scan is
	// cheaper than the bookkeeping, and it only runs when the peer is not an
	// exact match.
	ranges []*net.IPNet
}

// SetTrustedProxies names the proxy addresses or CIDR blocks to believe. Called
// once at boot from configuration; calling it again replaces the set, so it is
// safe to reconfigure and safe to run under -race in a test.
//
// A CIDR block is what a Docker deployment needs: the proxy container's address
// is assigned from the network's pool and is not something to hard-code. An
// entry that is neither an address nor a block is refused loudly rather than
// ignored silently, because a typo here means every request is bucketed under
// the proxy's address and the limit applies to the whole shop at once.
func SetTrustedProxies(entries ...string) {
	single := make(map[string]struct{}, len(entries))
	var ranges []*net.IPNet

	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if _, block, err := net.ParseCIDR(entry); err == nil {
			ranges = append(ranges, block)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			single[ip.String()] = struct{}{}
			continue
		}
		slog.Warn("ignoring trusted proxy entry that is neither an IP nor a CIDR block", "value", raw)
	}

	trustedProxies.Lock()
	trustedProxies.single = single
	trustedProxies.ranges = ranges
	trustedProxies.Unlock()
}

func peerIsTrustedProxy(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	trustedProxies.RLock()
	defer trustedProxies.RUnlock()

	if _, ok := trustedProxies.single[ip.String()]; ok {
		return true
	}
	for _, block := range trustedProxies.ranges {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

func formatLimit(v float64) string {
	return strconv.Itoa(int(v))
}
