package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func newTestLimit(capacity, perMinute int) *RateLimit {
	rl := NewRateLimit(RateLimitConfig{
		Name:      "test",
		Capacity:  capacity,
		PerMinute: perMinute,
	})
	// A clock frozen for the duration of the test: these cases are about the
	// burst, and a real one would refill a token mid-test and make the
	// assertion a race against the wall clock.
	frozen := &clock{at: time.Unix(1700000000, 0)}
	rl.now = frozen.now
	return rl
}

// clock is a time source the test moves by hand, so the refill arithmetic can
// be checked without sleeping.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// counter counts how many requests actually reached the wrapped handler, which
// is how a rejection is distinguished from a request that reached the handler
// and happened to be answered 429.
type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) inc() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func testServer(t *testing.T, rl *RateLimit) (*httptest.Server, *counter) {
	t.Helper()

	c := &counter{}
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.inc()
		w.WriteHeader(http.StatusOK)
	}))
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s, c
}

func TestRateLimitStopsABurst(t *testing.T) {
	rl := newTestLimit(5, 60) // 5 burst, one per second
	server, reached := testServer(t, rl)

	for i := 1; i <= 5; i++ {
		resp, err := http.Get(server.URL + "/auth/login")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d, want 200", i, resp.StatusCode)
		}
	}

	resp, err := http.Get(server.URL + "/auth/login")
	if err != nil {
		t.Fatalf("request 6: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("request 6: status %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Error("Retry-After is missing, so the client cannot know when to come back")
	}
	if got := resp.Header.Get("RateLimit-Limit"); got != "5" {
		t.Errorf("RateLimit-Limit = %q, want 5", got)
	}
	// The refusal happened in the limiter, not downstream: the handler saw
	// exactly the five requests that were allowed through.
	if n := reached.get(); n != 5 {
		t.Fatalf("handler reached %d times, want 5", n)
	}
}

// TestRateLimitRefillsOverTime is what a token bucket buys over a fixed window.
func TestRateLimitRefillsOverTime(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	rl := NewRateLimit(RateLimitConfig{Name: "test", Capacity: 2, PerMinute: 60})
	rl.now = c.now // one per second
	server, _ := testServer(t, rl)

	get := func(want int) {
		t.Helper()
		resp, err := http.Get(server.URL + "/auth/login")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("status %d, want %d", resp.StatusCode, want)
		}
	}

	get(http.StatusOK)
	get(http.StatusOK)
	get(http.StatusTooManyRequests)

	// Four seconds later the bucket has four whole tokens in it.
	c.advance(4 * time.Second)
	get(http.StatusOK)
	get(http.StatusOK)
	get(http.StatusTooManyRequests)
}

// TestRateLimitIsPerClient is the point of bucketing by IP.
func TestRateLimitIsPerClient(t *testing.T) {
	rl := newTestLimit(2, 60)
	server, _ := testServer(t, rl)

	// httptest serves every request from 127.0.0.1, so per-IP bucketing is
	// exercised through the header path instead: a trusted proxy is the only
	// way two clients appear, and that is exactly the production case.
	SetTrustedProxies("127.0.0.1")
	t.Cleanup(func() { SetTrustedProxies() })

	from := func(ip string) int {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/auth/login", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request from %s: %v", ip, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	for i := 0; i < 2; i++ {
		if got := from("203.0.113.1"); got != http.StatusOK {
			t.Fatalf("client 1 request %d: %d, want 200", i+1, got)
		}
	}
	if got := from("203.0.113.1"); got != http.StatusTooManyRequests {
		t.Fatalf("client 1 request 3: %d, want 429", got)
	}

	// A different client is unaffected by the first one's spending.
	if got := from("203.0.113.2"); got != http.StatusOK {
		t.Fatalf("client 2 first request: %d, want 200", got)
	}
}

// TestRateLimitIgnoresSpoofedForwardedFor is the reason trustedProxy exists. If
// the header were believed from any peer, a caller could rotate the value and
// get an unlimited supply of buckets.
func TestRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	rl := newTestLimit(2, 60)
	server, _ := testServer(t, rl)
	SetTrustedProxies() // no proxy is trusted
	t.Cleanup(func() { SetTrustedProxies() })

	from := func(ip string) int {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/auth/login", nil)
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := from("203.0.113.1"); got != http.StatusOK {
		t.Fatalf("first: %d, want 200", got)
	}
	// The header is ignored, so this is the same bucket as the call above even
	// though the value changed.
	if got := from("198.51.100.9"); got != http.StatusOK {
		t.Fatalf("second: %d, want 200 (same real client)", got)
	}
	if got := from("203.0.113.1"); got != http.StatusTooManyRequests {
		t.Fatalf("third: %d, want 429 (the header did not buy a new bucket)", got)
	}
}

func TestNewRateLimitRejectsNonsense(t *testing.T) {
	for _, cfg := range []RateLimitConfig{
		{Name: "zero capacity", Capacity: 0, PerMinute: 10},
		{Name: "negative capacity", Capacity: -1, PerMinute: 10},
		{Name: "zero rate", Capacity: 10, PerMinute: 0},
		{Name: "negative rate", Capacity: 10, PerMinute: -1},
	} {
		if rl := NewRateLimit(cfg); rl != nil {
			t.Errorf("%s: got a limiter, want nil so the limit is off by accident rather than by design", cfg.Name)
		}
	}
}

func TestNilRateLimitIsTransparent(t *testing.T) {
	var rl *RateLimit
	called := false
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	if h == nil {
		t.Fatal("Middleware on a nil limiter returned nil")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("a nil limiter must pass the request through")
	}
}

func TestRateLimitIdleEntriesAreForgotten(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	rl := NewRateLimit(RateLimitConfig{Name: "test", Capacity: 1, PerMinute: 60, IdleTTL: time.Minute})
	rl.now = c.now
	server, _ := testServer(t, rl)

	SetTrustedProxies("127.0.0.1")
	t.Cleanup(func() { SetTrustedProxies() })

	hit := func(ip string) {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/auth/login", nil)
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
	}

	for i := 0; i < 50; i++ {
		hit("203.0.113." + strconv.Itoa(i))
	}
	countBuckets(t, rl, 50)

	// Every one of those is now idle past the TTL, so cleanup empties the map.
	// Without it the limiter's memory grows with the number of addresses that
	// have ever connected.
	c.advance(2 * time.Minute)
	rl.Cleanup()
	countBuckets(t, rl, 0)
}

// TestJanitorSweepsIdleBuckets covers the wiring rather than the sweep.
//
// Cleanup is straightforward; what can go wrong is nobody calling it, and that
// is a wiring bug invisible in a unit test of Cleanup alone. The janitor is
// started from main with a real ticker, so this drives it through a fake clock
// and asserts the map does empty without anyone asking.
func TestJanitorSweepsIdleBuckets(t *testing.T) {
	// The test client is on loopback and identifies its callers with
	// X-Forwarded-For, so loopback has to be a trusted hop for twenty distinct
	// addresses to become twenty buckets. Without it they would all collapse
	// into one and the test would be measuring the wrong thing.
	SetTrustedProxies("127.0.0.0/8")
	t.Cleanup(func() { SetTrustedProxies() })
	c := &clock{at: time.Unix(1700000000, 0)}
	rl := NewRateLimit(RateLimitConfig{Name: "test", Capacity: 2, PerMinute: 60, IdleTTL: time.Minute})
	if rl == nil {
		t.Fatal("NewRateLimit returned nil for a valid config")
	}
	rl.now = c.now

	server := httptest.NewServer(rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	defer server.Close()

	for i := 0; i < 20; i++ {
		req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
		req.Header.Set("X-Forwarded-For", "198.51.100."+strconv.Itoa(i))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
	}
	countBuckets(t, rl, 20)

	// The janitor ticks on its own schedule, so the test has to wait for it
	// rather than assume a single pass already happened.
	stop := make(chan struct{})
	defer close(stop)
	rl.StartJanitor(time.Millisecond, stop)

	// Past the idle TTL, the entries go away on their own.
	c.advance(2 * time.Minute)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := countBucketsSoft(rl); n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("buckets = %d after the idle TTL, want 0: the janitor never swept", countBucketsSoft(rl))
}

func countBucketsSoft(rl *RateLimit) int {
	n := 0
	rl.buckets.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// TestTrustedProxyAcceptsACIDRBlock is the Docker case.
//
// A compose deployment cannot know the proxy container's address in advance, so
// the configuration is a network block. Before this, SetTrustedProxies only
// understood bare addresses and dropped a block on the floor with a warning,
// which left every request arriving from the proxy's own IP and sharing one
// bucket: a single client could exhaust the login limit for the whole shop.
func TestTrustedProxyAcceptsACIDRBlock(t *testing.T) {
	SetTrustedProxies("172.16.0.0/12", "10.0.0.7")
	t.Cleanup(func() { SetTrustedProxies() })

	for _, tc := range []struct {
		peer string
		want bool
	}{
		{"172.16.0.2", true},
		{"172.31.255.254", true},
		{"10.0.0.7", true},
		{"10.0.0.8", false},
		{"192.168.0.1", false},
	} {
		if got := peerIsTrustedProxy(tc.peer); got != tc.want {
			t.Errorf("peerIsTrustedProxy(%q) = %v, want %v", tc.peer, got, tc.want)
		}
	}

	// And the header is only believed for a peer inside a block.
	c := &clock{at: time.Unix(1700000000, 0)}
	rl := NewRateLimit(RateLimitConfig{Name: "test", Capacity: 5, PerMinute: 60})
	rl.now = c.now

	bucketFor := func(peer, forwarded string) string {
		req := &http.Request{RemoteAddr: peer + ":5000", Header: http.Header{}}
		req.Header.Set("X-Forwarded-For", forwarded)
		return clientIP(req)
	}

	if got := bucketFor("172.20.0.3", "203.0.113.9"); got != "203.0.113.9" {
		t.Errorf("through a trusted proxy the client IP is %q, want the forwarded one", got)
	}
	if got := bucketFor("192.168.1.5", "203.0.113.9"); got != "192.168.1.5" {
		t.Errorf("through an untrusted peer the client IP is %q, want the peer address", got)
	}
}

func countBuckets(t *testing.T, rl *RateLimit, want int) {
	t.Helper()

	got := 0
	rl.buckets.Range(func(_, _ any) bool {
		got++
		return true
	})
	if got != want {
		t.Fatalf("buckets = %d, want %d", got, want)
	}
}
