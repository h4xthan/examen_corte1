package transport_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"sync"
	"testing"
	"time"
)

// browser is a minimal stand-in for a browser: it keeps cookies between
// requests and echoes the CSRF token back in a header.
//
// The old suite sent every request with no cookies at all, which is only
// possible while authentication lives in a header. Now that a session is a
// cookie, a test that did not model the cookie jar would silently stop
// exercising the code path a real user takes.
type browser struct {
	mu   sync.Mutex
	http *http.Client
	csrf string
}

// browsers holds one browser per test, so state cannot leak between tests while
// existing call sites keep the same doJSON/doJSONAuth shape.
var browsers sync.Map

func browserFor(t *testing.T) *browser {
	t.Helper()

	key := t.Name()
	if existing, ok := browsers.Load(key); ok {
		return existing.(*browser)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	b := &browser{http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	actual, _ := browsers.LoadOrStore(key, b)
	return actual.(*browser)
}

func (b *browser) do(t *testing.T, method, path string, body any) (*http.Response, any) {
	t.Helper()

	// A client with no token yet cannot satisfy the CSRF check, so it obtains one
	// first. This is the handshake a real browser performs on page load.
	if b.csrf == "" {
		b.bootstrap(t)
	}

	if !isSafeMethod(method) {
		b.mu.Lock()
		token := b.csrf
		b.mu.Unlock()
		if token == "" {
			t.Fatalf("%s %s: no CSRF token available", method, path)
		}
	}

	req, _ := newJSONRequest(t, method, path, body, "")
	b.mu.Lock()
	if !isSafeMethod(method) {
		req.Header.Set("X-CSRF-Token", b.csrf)
	}
	b.mu.Unlock()
	return b.send(t, req)
}

func (b *browser) bootstrap(t *testing.T) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/auth/csrf", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		t.Fatalf("GET /auth/csrf: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/csrf: status %d, want 200", resp.StatusCode)
	}

	var payload struct {
		Token string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode csrf response: %v", err)
	}
	if payload.Token == "" {
		t.Fatal("GET /auth/csrf returned an empty token")
	}

	b.mu.Lock()
	b.csrf = payload.Token
	b.mu.Unlock()
}

func (b *browser) send(t *testing.T, req *http.Request) (*http.Response, any) {
	t.Helper()

	resp, err := b.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	return resp, decodeBody(t, req, resp)
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func newJSONRequest(t *testing.T, method, path string, body any, token string) (*http.Request, *bytes.Reader) {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, testServer.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, reader
}

func decodeBody(t *testing.T, req *http.Request, resp *http.Response) any {
	t.Helper()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	var out any
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		t.Fatalf("%s %s: decode response: %v", req.Method, req.URL.Path, err)
	}
	return out
}

func doJSON(t *testing.T, method, path string, body any) (*http.Response, any) {
	t.Helper()
	return doJSONAuth(t, method, path, "", body)
}

// doJSONAuth dispatches on which credential the caller wants to present.
//
// With a token it is a non-browser client: the request is sent statelessly, with
// no cookies, because the cookie jar belongs to whichever user last logged in on
// this test's browser. Sending both would be ambiguous, and the server prefers
// the cookie, so a test asking to act as one user while the jar holds another
// would silently act as the wrong one. Bearer requests are also exempt from
// CSRF, which is the trade-off the server accepts for that client model.
//
// Without a token it goes through the per-test browser, cookies and all.
func doJSONAuth(t *testing.T, method, path, token string, body any) (*http.Response, any) {
	t.Helper()

	if token == "" {
		return browserFor(t).do(t, method, path, body)
	}

	req, _ := newJSONRequest(t, method, path, body, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	return resp, decodeBody(t, req, resp)
}

func TestBooksEndToEnd(t *testing.T) {
	book := map[string]any{
		"author":          "Test Author",
		"title":           "Test Title",
		"pages":           123,
		"isbn":            fmt.Sprintf("T-%d", time.Now().UnixNano()%1000000),
		"price_cents":     1500,
		"stock":           7,
		"url_cover_image": "https://example.com/x.jpg",
	}

	id, _ := createBookAsCapturista(t, book)
	if id == 0 {
		t.Fatal("POST /books: expected generated id")
	}
	path := "/books/" + strconv.Itoa(id)
	capturista := capturistaToken(t)

	resp, gotAny := doJSON(t, http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
	got := gotAny.(map[string]any)
	if got["title"] != "Test Title" {
		t.Fatalf("GET %s: title %v, want Test Title", path, got["title"])
	}

	resp, list := doJSON(t, http.MethodGet, "/books", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /books: status %d, want 200", resp.StatusCode)
	}
	items := list.([]any)
	if len(items) < 1 {
		t.Fatal("GET /books: expected at least one book")
	}

	// The whole lifecycle is the capturista's: the admin reads the manual path
	// at the end, but no longer writes the catalogue.
	book["title"] = "Updated Title"
	resp, updatedAny := doJSONAuth(t, http.MethodPut, path, capturista, book)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s: status %d, want 200", path, resp.StatusCode)
	}
	updated := updatedAny.(map[string]any)
	if updated["title"] != "Updated Title" {
		t.Fatalf("PUT %s: title %v, want Updated Title", path, updated["title"])
	}

	resp, _ = doJSONAuth(t, http.MethodDelete, path, capturista, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE %s: status %d, want 204", path, resp.StatusCode)
	}

	resp, _ = doJSON(t, http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted %s: status %d, want 404", path, resp.StatusCode)
	}
}

func TestBooksInvalidID(t *testing.T) {
	resp, _ := doJSON(t, http.MethodGet, "/books/notanumber", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /books/notanumber: status %d, want 400", resp.StatusCode)
	}
}

func TestBooksNotFound(t *testing.T) {
	resp, _ := doJSON(t, http.MethodGet, "/books/99999999", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /books/99999999: status %d, want 404", resp.StatusCode)
	}
}

// doAnon sends a request with no cookies and no credential.
//
// It cannot be doJSON with an empty token: that path goes through the per-test
// browser, which holds whatever session a previous login in the same test
// established. A test that wants to assert "no credential" has to actually have
// no credential, so this uses a stateless client that never sends a cookie.
func doAnon(t *testing.T, method, path string, body any) (*http.Response, any) {
	t.Helper()

	req, _ := newJSONRequest(t, method, path, body, "")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	return resp, decodeBody(t, req, resp)
}
