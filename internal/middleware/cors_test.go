package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	allowedOrigin = "https://dvbs.example"
	otherOrigin   = "https://evil.example"
)

func corsServer(t *testing.T, allowed ...string) *httptest.Server {
	t.Helper()

	h := CORS(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return server
}

func doReq(t *testing.T, method, url, origin string, headers map[string]string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Access-Control-Request-Method", "POST")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestCORSAllowsOnlyTheConfiguredOrigin is #15 inverted.
//
// The old middleware answered Access-Control-Allow-Origin: * to every origin,
// which let any page on the internet read authenticated API responses using the
// visitor's cookies. Now the header names the one origin that is allowed.
func TestCORSAllowsOnlyTheConfiguredOrigin(t *testing.T) {
	server := corsServer(t, allowedOrigin)

	resp := doReq(t, http.MethodGet, server.URL+"/books", allowedOrigin, nil)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, allowedOrigin)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true; a credentialed request needs it", got)
	}
	if got := resp.Header.Values("Vary"); len(got) == 0 {
		t.Fatal("Vary is missing, so a shared cache could serve one origin's headers to another")
	}
}

func TestCORSDeniesAnUnlistedOrigin(t *testing.T) {
	server := corsServer(t, allowedOrigin)

	resp := doReq(t, http.MethodGet, server.URL+"/books", otherOrigin, nil)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q for an unlisted origin, want no header at all", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials = %q for an unlisted origin, want none", got)
	}
	// The request is still served: a cross-origin form post can reach us no
	// matter what this header says. CORS is a read barrier, and pretending
	// otherwise would be a lie.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the request is served; the browser is what blocks the read)", resp.StatusCode)
	}
}

// TestCORSPreflightFromAnUnlistedOriginIsRefused checks the half that does
// short-circuit: a preflight only exists to ask permission, so no is a real
// answer and the browser never sends the request that follows.
func TestCORSPreflightFromAnUnlistedOriginIsRefused(t *testing.T) {
	server := corsServer(t, allowedOrigin)

	resp := doReq(t, http.MethodOptions, server.URL+"/users", otherOrigin, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("preflight status = %d, want 403 for an unlisted origin", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("preflight Access-Control-Allow-Origin = %q, want none", got)
	}
}

func TestCORSPreflightFromTheAllowedOrigin(t *testing.T) {
	server := corsServer(t, allowedOrigin)

	resp := doReq(t, http.MethodOptions, server.URL+"/users", allowedOrigin,
		map[string]string{"Access-Control-Request-Headers": "Content-Type, X-CSRF-Token"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("preflight Access-Control-Allow-Origin = %q, want %q", got, allowedOrigin)
	}

	methods := resp.Header.Get("Access-Control-Allow-Methods")
	for _, want := range []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"} {
		if !containsToken(methods, want) {
			t.Errorf("Access-Control-Allow-Methods = %q, missing %s", methods, want)
		}
	}
	if containsToken(methods, "*") {
		t.Error("Access-Control-Allow-Methods contains a wildcard, which the spec rejects on a credentialed request")
	}

	headers := resp.Header.Get("Access-Control-Allow-Headers")
	if !containsToken(headers, CSRFHeader) {
		t.Errorf("Access-Control-Allow-Headers = %q, missing %s", headers, CSRFHeader)
	}
	if !containsToken(headers, "Authorization") {
		t.Errorf("Access-Control-Allow-Headers = %q, missing Authorization", headers)
	}
}

// TestCORSPreflightDoesNotReachTheHandler keeps the old test's useful assertion:
// a preflight must not be mistaken for the real request.
func TestCORSPreflightDoesNotReachTheHandler(t *testing.T) {
	// A handler that answers 200 with a body. If the preflight reached it the
	// status would be 200 rather than 204.
	h := CORS([]string{allowedOrigin})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(h)
	defer server.Close()

	resp := doReq(t, http.MethodOptions, server.URL+"/users", allowedOrigin, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204 (the wrapped handler must not run)", resp.StatusCode)
	}
}

// TestCORSWithoutAnAllowlistDeniesEverything is the safe default: an unset
// APP_ORIGIN must not degrade into "any origin".
func TestCORSWithoutAnAllowlistDeniesEverything(t *testing.T) {
	server := corsServer(t)

	resp := doReq(t, http.MethodGet, server.URL+"/books", allowedOrigin, nil)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q with an empty allowlist, want none", got)
	}
}

// TestCORSWithNoOriginHeaderIsUntouched covers curl and same-origin requests:
// there is nothing to negotiate, and the handler must see the request.
func TestCORSWithNoOriginHeaderIsUntouched(t *testing.T) {
	server := corsServer(t, allowedOrigin)

	resp := doReq(t, http.MethodGet, server.URL+"/books", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q on a request with no Origin, want none", got)
	}
}

// containsToken reports whether a comma-separated header lists value.
func containsToken(header, value string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.TrimSpace(part) == value {
			return true
		}
	}
	return false
}
