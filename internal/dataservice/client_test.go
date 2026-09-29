package dataservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient points a client at a test server.
//
// It builds the struct directly rather than going through New so the tests can
// use the plain http of httptest. New keeps its https requirement, which is a
// production invariant and not something a test should be allowed to relax.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return &Client{
		baseURL:    srv.URL,
		publicKey:  "pub",
		privateKey: "priv",
		http:       srv.Client(),
	}
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, env Envelope) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(env); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func okEnvelope(rows []Row) Envelope {
	var env Envelope
	env.Type = "sql_endpoint"
	env.Data.Rows = rows
	env.Data.Result = Result{Code: 200, Message: "Query OK", RowCount: len(rows)}
	return env
}

// A writing endpoint answers with the number of rows it changed and no rows, so
// that count is the only evidence the statement did anything. Query discards it,
// and a create that has to read the row back needs to know the insert landed.
func TestExecReturnsTheRowCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := okEnvelope(nil)
		env.Data.Result = Result{Code: 200, Message: "Query OK, 1 row affected", RowAffect: 1}
		writeEnvelope(t, w, env)
	}))
	defer srv.Close()

	res, err := newTestClient(t, srv).Exec(context.Background(), http.MethodPut, "books", nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.RowAffect != 1 {
		t.Fatalf("want 1 row written, got %+v", res)
	}
}

// A write that changed nothing is not a failure at the SQL layer, so it arrives as
// a success with a zero count. Reporting it is the caller's job, and it can only
// do that if the count survives.
func TestExecReportsAWriteThatChangedNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := okEnvelope(nil)
		env.Data.Result = Result{Code: 200, Message: "Query OK, 0 rows affected", RowAffect: 0}
		writeEnvelope(t, w, env)
	}))
	defer srv.Close()

	res, err := newTestClient(t, srv).Exec(context.Background(), http.MethodPost, "books", nil)
	if err != nil {
		t.Fatalf("a write that changed no row is not a transport failure: %v", err)
	}
	if res.RowAffect != 0 {
		t.Fatalf("want 0, got %+v", res)
	}
}

func TestQueryReturnsRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, okEnvelope([]Row{{"id": "1", "title": "Clean Code"}}))
	}))
	defer srv.Close()

	rows, err := newTestClient(t, srv).Query(context.Background(), http.MethodGet, "books", nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0].Str("title") != "Clean Code" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

// Data Service answers HTTP 200 and reports the failure in the body. A client
// that trusted the status would call this a success, so the error code inside
// the envelope is what decides.
func TestHTTP200WithErrorCodeIsStillAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := okEnvelope(nil)
		env.Data.Result = Result{Code: 1146, Message: "table not found"}
		writeEnvelope(t, w, env)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Query(context.Background(), http.MethodGet, "books", nil)
	if err == nil {
		t.Fatal("Query reported success for a failed statement")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if apiErr.Code != 1146 {
		t.Fatalf("want code 1146, got %d", apiErr.Code)
	}
	if apiErr.Status != http.StatusOK {
		t.Fatalf("the status really was 200, and that is the point: got %d", apiErr.Status)
	}
}

func TestUnauthorisedKeepsItsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := okEnvelope(nil)
		env.Data.Result = Result{Code: 401, Message: "auth failed"}
		w.WriteHeader(http.StatusUnauthorized)
		writeEnvelope(t, w, env)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Query(context.Background(), http.MethodGet, "books", nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("want a 401 *Error, got %v", err)
	}
}

// A missing book must stay a missing book: sql.ErrNoRows is the sentinel the
// transport layer already turns into 404.
func TestQueryOneEmptyResultIsNoRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, okEnvelope(nil))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).QueryOne(context.Background(), http.MethodGet, "book", map[string]any{"id": "999"})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want sql.ErrNoRows, got %v", err)
	}
}

// Data Service decides where parameters travel from the verb alone, so the
// client has to follow: a GET cannot carry a body and a POST cannot use the
// query string.
func TestGetSendsParametersInTheQueryString(t *testing.T) {
	var gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		writeEnvelope(t, w, okEnvelope([]Row{{"id": "7"}}))
	}))
	defer srv.Close()

	params := map[string]any{"id": 7, "title": "Clean Code"}
	if _, err := newTestClient(t, srv).Query(context.Background(), http.MethodGet, "book", params); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !strings.Contains(gotQuery, "id=7") {
		t.Errorf("id missing from query: %q", gotQuery)
	}
	if gotBody != "" {
		t.Errorf("GET carried a body: %q", gotBody)
	}
}

func TestPostSendsParametersAsJSONBody(t *testing.T) {
	var gotQuery, gotBody, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotContentType = r.Header.Get("Content-Type")
		writeEnvelope(t, w, okEnvelope([]Row{{"auto_increment_id": "1"}}))
	}))
	defer srv.Close()

	// A JSON number has to stay a number: the endpoint declares these as integer
	// parameters and quotes what it is given as a string.
	params := map[string]any{"pages": 300, "price_cents": int64(2999), "isbn": "978-0"}
	if _, err := newTestClient(t, srv).Query(context.Background(), http.MethodPut, "books", params); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("POST put parameters in the query string: %q", gotQuery)
	}
	if gotContentType != "application/json" {
		t.Errorf("want a JSON content type, got %q", gotContentType)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(gotBody), &decoded); err != nil {
		t.Fatalf("body is not JSON: %q", gotBody)
	}
	if got := decoded["pages"]; got != float64(300) {
		t.Errorf("pages should stay numeric, got %#v", got)
	}
	if got := decoded["price_cents"]; got != float64(2999) {
		t.Errorf("price_cents should stay numeric, got %#v", got)
	}
}

func TestRequestsCarryBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	var gotOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		writeEnvelope(t, w, okEnvelope(nil))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).Query(context.Background(), http.MethodGet, "books", nil); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !gotOK || gotUser != "pub" || gotPass != "priv" {
		t.Fatalf("credentials did not arrive: ok=%v user=%q pass=%q", gotOK, gotUser, gotPass)
	}
}

// A gateway can answer with HTML and no envelope. "status 502" is the useful
// fact; a JSON parse error would hide it.
func TestNonJSONFailureReportsTheStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>502 Bad Gateway</html>")
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Query(context.Background(), http.MethodGet, "books", nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("the status is missing from %q", err)
	}
}

func TestUnsupportedMethodIsRejectedBeforeTheCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should have been made")
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Query(context.Background(), http.MethodPatch, "books", nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported method") {
		t.Fatalf("want an unsupported method error, got %v", err)
	}
}

func TestCancelledContextStopsTheCall(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := newTestClient(t, srv).Query(ctx, http.MethodGet, "books", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestNewRejectsAPlaintextBaseURL(t *testing.T) {
	// The credentials are base64, not encrypted. Over http they are readable by
	// anything on the path, so this is a configuration error worth failing on.
	_, err := New(Config{BaseURL: "http://example.test/api", PublicKey: "p", PrivateKey: "s"})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("want an https complaint, got %v", err)
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New(Config{BaseURL: "https://example.test/api"}); err == nil {
		t.Fatal("want an error for missing keys")
	}
}

func TestNewTrimsATrailingSlash(t *testing.T) {
	c, err := New(Config{BaseURL: "https://example.test/api/endpoint/", PublicKey: "p", PrivateKey: "s"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if strings.HasSuffix(c.baseURL, "/") {
		t.Fatalf("trailing slash left in %q", c.baseURL)
	}
}

func TestNewDefaultsTheTimeout(t *testing.T) {
	c, err := New(Config{BaseURL: "https://example.test/api", PublicKey: "p", PrivateKey: "s"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.http.Timeout != defaultTimeout {
		t.Fatalf("want the default timeout, got %v", c.http.Timeout)
	}
}

// Every value comes back as text, so a column of numbers is the normal case
// rather than an odd one.
func TestRowCoercion(t *testing.T) {
	row := Row{"id": "2083456789012345", "pages": "300", "url_cover_image": nil, "stock": ""}

	id, err := row.Int64("id")
	if err != nil {
		t.Fatalf("Int64: %v", err)
	}
	if id != 2083456789012345 {
		t.Fatalf("an AUTO_RANDOM id must survive intact, got %d", id)
	}

	pages, err := row.Int("pages")
	if err != nil || pages != 300 {
		t.Fatalf("Int: %d, %v", pages, err)
	}

	if s, ok := row.NullableStr("url_cover_image"); ok || s != "" {
		t.Fatalf("a null column must read as absent, got %q %v", s, ok)
	}
	if _, ok := row.NullableStr("stock"); ok {
		t.Fatal("an empty string is present, unlike null")
	}
}

// A column that is null where a number is required is a real fault, and
// returning 0 for it would be how a book ends up costing nothing.
func TestRowInt64RefusesNullAndGarbage(t *testing.T) {
	row := Row{"pages": nil, "title": "Clean Code"}
	if _, err := row.Int64("pages"); err == nil {
		t.Fatal("a null column must not read as 0")
	}
	if _, err := row.Int64("title"); err == nil {
		t.Fatal("text must not read as an integer")
	}
	if _, err := row.Int64("absent"); err == nil {
		t.Fatal("a missing column must not read as 0")
	}
}

func TestErrorClassifiesTheCodesTheAppCaresAbout(t *testing.T) {
	if !(&Error{Code: codeDuplicateEntry}).IsDuplicate() {
		t.Error("1062 should read as a duplicate")
	}
	// 1451 is the one a book delete produces, and 1452 the one an insert into a
	// child table would. A store that only knows 1452 turns the first into a 500.
	if !(&Error{Code: codeParentHasChildren}).IsForeignKey() {
		t.Error("1451 should read as a foreign key rejection")
	}
	if !(&Error{Code: codeChildHasNoParent}).IsForeignKey() {
		t.Error("1452 should read as a foreign key rejection")
	}
	if (&Error{Code: 500}).IsDuplicate() || (&Error{Code: 500}).IsForeignKey() {
		t.Error("an unrelated code must not be classified")
	}
}

func TestErrorMessageCarriesBothChannels(t *testing.T) {
	err := &Error{Method: "DELETE", Path: "books", Status: 200, Code: 1452, Message: "a foreign key constraint fails"}
	msg := err.Error()
	for _, want := range []string{"DELETE", "books", "200", "1452"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q missing from %q", want, msg)
		}
	}
}
