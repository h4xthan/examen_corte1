package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRecoveryReturnsJSON500 pins the contract that a panicking handler produces
// a parseable 500 rather than a reset connection.
func TestRecoveryReturnsJSON500(t *testing.T) {
	h := Recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/books", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	// The panic value must not leak: it can contain a query, an ID or a
	// connection string, and this is exactly the error text invariant from §4.
	if body["error"] != "internal server error" {
		t.Fatalf("error = %q, want a generic message", body["error"])
	}
	for _, v := range body {
		if v == "boom" {
			t.Fatal("panic value leaked into the response body")
		}
	}
}

// TestRecoveryWithLoggingEmits500 is the regression for the interaction between
// the two middlewares. statusRecorder.WrittenHeader() used to return the
// initialised 200 even before anything was written, so Recovery believed the
// response was already committed and wrote no 500 at all: the client got a 200
// with an empty body while the server logged a panic.
func TestRecoveryWithLoggingEmits500(t *testing.T) {
	handler := Logging(Recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/books", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (panic was masked as a success)", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("body is empty; Recovery skipped the response")
	}
}

// TestRecoveryRespectsCommittedResponse covers the other branch: once a handler
// has written a status, Recovery must stay silent rather than append a second
// status line to the same response.
func TestRecoveryRespectsCommittedResponse(t *testing.T) {
	handler := Logging(Recovery(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("started"))
		panic("too late")
	})))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/books", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 to be preserved", rec.Code)
	}
	if got := rec.Body.String(); got != "started" {
		t.Fatalf("body = %q, want the already-written body untouched", got)
	}
}

// TestRecoveryPassesThroughErrAbortHandler documents that the sentinel used to
// abandon a response is re-panicked instead of being masked as a 500.
func TestRecoveryPassesThroughErrAbortHandler(t *testing.T) {
	h := Recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler to propagate", rec)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/books", nil))
}

func TestStatusRecorderReportsWrittenState(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}

	if got := rec.WrittenHeader(); got != 0 {
		t.Fatalf("WrittenHeader() before any write = %d, want 0", got)
	}
	rec.WriteHeader(http.StatusCreated)
	if got := rec.WrittenHeader(); got != http.StatusCreated {
		t.Fatalf("WrittenHeader() after WriteHeader = %d, want 201", got)
	}
	// A second WriteHeader must not change the recorded status.
	rec.WriteHeader(http.StatusTeapot)
	if got := rec.WrittenHeader(); got != http.StatusCreated {
		t.Fatalf("WrittenHeader() after repeat call = %d, want 201", got)
	}
}

func TestStatusRecorderRecordsImplicitOK(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}

	// Write without an explicit WriteHeader must still record a 200, so the
	// access log does not report a blank status.
	if _, err := rec.Write([]byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := rec.WrittenHeader(); got != http.StatusOK {
		t.Fatalf("WrittenHeader() = %d, want 200", got)
	}
}
