package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// cookieFrom reads a cookie the handler under test set.
func cookieFrom(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// okHandler records that it ran, so a test can tell a rejection from a pass.
func okHandler(ran *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ran = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestCSRFIssuesTokenOnSafeRequest(t *testing.T) {
	var ran bool
	h := CSRF(false)(okHandler(&ran))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/books", nil))

	if !ran {
		t.Fatal("handler did not run")
	}
	c := cookieFrom(t, rec, CSRFCookieName)
	if c == nil {
		t.Fatal("no CSRF cookie was issued; the client could never make a POST")
	}
	if len(c.Value) < 32 {
		t.Fatalf("CSRF token %q is only %d characters", c.Value, len(c.Value))
	}
}

// seedToken performs the priming GET and returns the token the middleware issued.
//
// It deliberately uses its own handler instance: the priming request also passes
// through the middleware, and a shared "did it run" flag would already be set by
// the time the test made its real assertion.
func seedToken(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()

	ignored := false
	seed := CSRF(false)(okHandler(&ignored))
	_ = seed
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/books", nil))
	c := cookieFrom(t, rec, CSRFCookieName)
	if c == nil {
		t.Fatal("priming GET did not issue a CSRF token")
	}
	return c
}

// TestCSRFDoesNotOverwriteExistingToken matters because rotating the token on
// every response would break a page holding two tabs: the second response would
// invalidate the token the first tab already read.
func TestCSRFDoesNotOverwriteExistingToken(t *testing.T) {
	var ran bool
	h := CSRF(false)(okHandler(&ran))

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/books", nil))
	issued := cookieFrom(t, first, CSRFCookieName)
	if issued == nil {
		t.Fatal("no token on first request")
	}

	req := httptest.NewRequest(http.MethodGet, "/books", nil)
	req.AddCookie(issued)
	second := httptest.NewRecorder()
	h.ServeHTTP(second, req)

	if c := cookieFrom(t, second, CSRFCookieName); c != nil {
		t.Fatal("an existing CSRF token was replaced")
	}
}

// TestUnsafeMethodWithoutTokenRejected is the core of the defence: a
// state-changing request with no token is refused.
func TestUnsafeMethodWithoutTokenRejected(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			var ran bool
			h := CSRF(false)(okHandler(&ran))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, "/checkout", nil))

			if ran {
				t.Fatal("handler ran without a CSRF token")
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestSafeMethodsSkipCSRF(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			var ran bool
			h := CSRF(false)(okHandler(&ran))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, "/books", nil))

			if !ran {
				t.Fatalf("%s was rejected; only state-changing methods need a token", method)
			}
		})
	}
}

// TestMatchingTokenAccepted is the positive case, including the cookie-plus-header
// combination a browser actually produces.
func TestMatchingTokenAccepted(t *testing.T) {
	h := CSRF(false)(okHandler(new(bool)))
	issued := seedToken(t, h)

	ran := false
	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.AddCookie(issued)
	req.Header.Set(CSRFHeader, issued.Value)

	rec := httptest.NewRecorder()
	CSRF(false)(okHandler(&ran)).ServeHTTP(rec, req)

	if !ran {
		t.Fatal("handler did not run with a matching token")
	}
}

func TestMismatchedTokenRejected(t *testing.T) {
	issued := seedToken(t, CSRF(false)(okHandler(new(bool))))

	ran := false
	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.AddCookie(issued)
	req.Header.Set(CSRFHeader, issued.Value+"x")

	rec := httptest.NewRecorder()
	CSRF(false)(okHandler(&ran)).ServeHTTP(rec, req)

	if ran {
		t.Fatal("handler ran with a token that does not match the cookie")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestHeaderWithoutCookieRejected covers an attacker who sets the header but
// cannot set the cookie on our origin. The comparison is only meaningful when
// both halves are present.
func TestHeaderWithoutCookieRejected(t *testing.T) {
	var ran bool
	h := CSRF(false)(okHandler(&ran))

	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.Header.Set(CSRFHeader, "attacker-chosen-value")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if ran {
		t.Fatal("handler ran with a header token and no cookie")
	}
}

// TestBearerRequestsAreExempt documents the deliberate carve-out: a token the
// browser does not store or attach cannot be ridden on by another site.
func TestBearerRequestsAreExempt(t *testing.T) {
	var ran bool
	h := CSRF(false)(okHandler(&ran))

	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.Header.Set("Authorization", "Bearer some-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !ran {
		t.Fatal("a bearer-authenticated request was rejected by CSRF")
	}
}

func TestEmptyBearerIsNotAnExemption(t *testing.T) {
	var ran bool
	h := CSRF(false)(okHandler(&ran))

	// "Bearer " with nothing after it is not a credential, and must not be
	// treated as an exemption by a client that sets the header carelessly.
	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.Header.Set("Authorization", "Bearer ")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if ran {
		t.Fatal("an empty Bearer header was treated as an authenticated request")
	}
}

func TestCSRFTokenIsUnpredictable(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		v, err := NewCSRFToken()
		if err != nil {
			t.Fatalf("NewCSRFToken: %v", err)
		}
		if seen[v] {
			t.Fatalf("token %q was generated twice", v)
		}
		seen[v] = true
	}
}

func TestSetSessionCookieIsHttpOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "a-session-token", time.Hour, true)

	c := cookieFrom(t, rec, SessionCookieName)
	if c == nil {
		t.Fatal("no session cookie was set")
	}
	if !c.HttpOnly {
		t.Error("session cookie is not HttpOnly; script on the page can read it")
	}
	if !c.Secure {
		t.Error("session cookie is not Secure despite secure=true")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
}

// TestClearSessionCookieMatchesSetAttributes is a real trap: a cookie is
// identified by name, domain and path, so clearing with different attributes
// leaves the original in place and the session stays live after logout.
func TestClearSessionCookieMatchesSetAttributes(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec, true)

	c := cookieFrom(t, rec, SessionCookieName)
	if c == nil {
		t.Fatal("no cookie in the clearing response")
	}
	if c.Value != "" {
		t.Errorf("Value = %q, want empty", c.Value)
	}
	if c.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative so the browser deletes it", c.MaxAge)
	}
	if !c.HttpOnly {
		t.Error("clearing cookie is not HttpOnly, so it does not replace the original")
	}
	if !c.Secure {
		t.Error("clearing cookie is not Secure")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
}

// TestCSRFCookieIsReadable documents the asymmetry that makes the scheme work:
// the session cookie is hidden from script, the CSRF cookie is not.
func TestCSRFCookieIsReadable(t *testing.T) {
	rec := httptest.NewRecorder()
	SetCSRFCookie(rec, "a-csrf-token", true, time.Hour)

	c := cookieFrom(t, rec, CSRFCookieName)
	if c == nil {
		t.Fatal("no CSRF cookie was set")
	}
	if c.HttpOnly {
		t.Error("CSRF cookie is HttpOnly; the frontend could never read it to set the header")
	}
}

func TestCSRFRejectsGarbageTokens(t *testing.T) {
	// Values an attacker might try to smuggle in. None of them may be accepted,
	// and none may panic the comparison.
	cases := []struct {
		name   string
		cookie string
		header string
	}{
		{"empty header", "abc", ""},
		{"empty cookie", "", "abc"},
		{"both empty", "", ""},
		{"header only prefix", "abc", "ab"},
		{"unicode", "ábc", "ábc"},
		{"very long", strings.Repeat("a", 4096), strings.Repeat("a", 4095) + "b"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ran bool
			h := CSRF(false)(okHandler(&ran))

			req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: tc.cookie})
			}
			if tc.header != "" {
				req.Header.Set(CSRFHeader, tc.header)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if ran {
				t.Fatal("handler ran with a mismatched token")
			}
		})
	}
}

// TestCSRFTokenEndpointIsUsable is the handshake the whole scheme depends on: a
// caller with no cookie can obtain a token, and the token it reads back is the
// one the browser is holding, so a following POST succeeds.
func TestCSRFTokenEndpointIsUsable(t *testing.T) {
	rec := httptest.NewRecorder()
	CSRFToken(false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/csrf", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	cookie := cookieFrom(t, rec, CSRFCookieName)
	if cookie == nil {
		t.Fatal("no cookie set")
	}

	var body struct {
		Token string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Token != cookie.Value {
		t.Fatalf("body token %q does not match cookie %q", body.Token, cookie.Value)
	}

	// And the token it handed out is accepted on a state-changing request.
	ran := false
	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.AddCookie(cookie)
	req.Header.Set(CSRFHeader, body.Token)
	post := httptest.NewRecorder()
	CSRF(false)(okHandler(&ran)).ServeHTTP(post, req)
	if !ran {
		t.Fatalf("the issued token was rejected; status %d", post.Code)
	}
}

// TestCSRFTokenEndpointIsStable stops the server from handing out a fresh token
// on every poll, which would invalidate the token a page already read.
func TestCSRFTokenEndpointIsStable(t *testing.T) {
	first := httptest.NewRecorder()
	CSRFToken(false).ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/auth/csrf", nil))
	issued := cookieFrom(t, first, CSRFCookieName)
	if issued == nil {
		t.Fatal("no cookie on first call")
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/csrf", nil)
	req.AddCookie(issued)
	second := httptest.NewRecorder()
	CSRFToken(false).ServeHTTP(second, req)

	if c := cookieFrom(t, second, CSRFCookieName); c != nil {
		t.Fatal("an existing token was replaced")
	}
}

func TestCSRFTokenEndpointRejectsWrites(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			CSRFToken(false).ServeHTTP(rec, httptest.NewRequest(method, "/auth/csrf", nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
		})
	}
}

// TestCSRFTokenBodyIsNotCached stops a shared cache from replaying one visitor's
// token to another, which would let a third party satisfy the check.
func TestCSRFTokenBodyIsNotCached(t *testing.T) {
	rec := httptest.NewRecorder()
	CSRFToken(false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/csrf", nil))

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}
