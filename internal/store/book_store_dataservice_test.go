package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"dvbs/internal/dataservice"
	"dvbs/internal/model"
)

// These tests pin the mapping between the admin panel's routes and the Data App
// endpoints, which is the part of this integration nobody can read off the
// schema: the verbs are inverted, every value comes back as text, and the ids
// are too wide to survive a float64.

// dsRequest is one call the store made, as the Data App saw it.
type dsRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   map[string]any
}

// fakeDataService stands in for the deployed Data App.
type fakeDataService struct {
	t     *testing.T
	mu    sync.Mutex
	calls []dsRequest
	reply func(dsRequest) dataservice.Envelope
}

func (f *fakeDataService) serve(w http.ResponseWriter, r *http.Request) {
	call := dsRequest{Method: r.Method, Path: strings.TrimPrefix(r.URL.Path, "/")}

	var env dataservice.Envelope
	env.Type = "sql_endpoint"
	env.Data.Result = dataservice.Result{Code: 200, Message: "Query OK"}

	if r.URL.RawQuery != "" {
		call.Query = r.URL.Query()
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		if err := json.NewDecoder(r.Body).Decode(&call.Body); err != nil {
			f.t.Errorf("decode body of %s %s: %v", r.Method, r.URL.Path, err)
		}
	}

	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()

	if f.reply != nil {
		env = f.reply(call)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(env); err != nil {
		f.t.Errorf("encode reply: %v", err)
	}
}

func (f *fakeDataService) recorded() []dsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dsRequest(nil), f.calls...)
}

func rowsReply(rows ...dataservice.Row) dataservice.Envelope {
	var env dataservice.Envelope
	env.Type = "sql_endpoint"
	env.Data.Rows = rows
	env.Data.Result = dataservice.Result{Code: 200, Message: "Query OK", RowCount: len(rows)}
	return env
}

// affectedReply is what a writing endpoint answers with: the row count and no
// rows. Data Service reports the result of the first statement of the script, so
// this is what an INSERT gets even when its script ends in a SELECT.
func affectedReply(n int) dataservice.Envelope {
	env := rowsReply()
	env.Data.Result = dataservice.Result{Code: 200, Message: "Query OK", RowAffect: n}
	return env
}

func failingReply(code int, message string) dataservice.Envelope {
	env := rowsReply()
	env.Data.Result = dataservice.Result{Code: code, Message: message}
	return env
}

// bookRow is one catalogue row as Data Service sends it: everything is text, and
// the nullable cover is null.
func bookRow(id, title string) dataservice.Row {
	return dataservice.Row{
		"id": id, "author": "Robert C. Martin", "title": title, "pages": "464",
		"isbn": "978-0132350884", "price_cents": "2999", "stock": "12",
		"url_cover_image": nil,
	}
}

// newDataServiceStore wires a book store to a fake Data App and to the real
// querier, which is the composite the server builds.
func newDataServiceStore(t *testing.T, reply func(dsRequest) dataservice.Envelope) (*DataServiceBookStore, *fakeDataService) {
	t.Helper()
	fake := &fakeDataService{t: t, reply: reply}
	// TLS rather than plain http, so the client is built through New and the
	// https requirement is exercised on the way to a test too.
	srv := httptest.NewTLSServer(http.HandlerFunc(fake.serve))
	t.Cleanup(srv.Close)

	c, err := dataservice.New(dataservice.Config{
		BaseURL:    srv.URL,
		PublicKey:  "pub",
		PrivateKey: "priv",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("build data service client: %v", err)
	}
	return NewDataServiceBookStore(c, testQueries).(*DataServiceBookStore), fake
}

func sampleBook() *model.Book {
	return &model.Book{
		Author: "Robert C. Martin", Title: "Clean Code", Pages: 464,
		ISBN: "978-0132350884", PriceCents: 2999, Stock: 12,
	}
}

func TestDataServiceListMapsEveryColumn(t *testing.T) {
	s, _ := newDataServiceStore(t, func(dsRequest) dataservice.Envelope {
		return rowsReply(bookRow("2083456789012345", "Clean Code"))
	})

	books, err := s.GetAllBooks(t.Context())
	if err != nil {
		t.Fatalf("GetAllBooks: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("want 1 book, got %d", len(books))
	}
	b := books[0]
	if b.ID != 2083456789012345 {
		t.Errorf("an AUTO_RANDOM id must arrive intact, got %d", b.ID)
	}
	if b.Title != "Clean Code" || b.Author != "Robert C. Martin" {
		t.Errorf("text columns did not map: %+v", b)
	}
	if b.Pages != 464 || b.Stock != 12 {
		t.Errorf("integer columns came back as text: %+v", b)
	}
	if b.PriceCents != 2999 {
		t.Errorf("money must be cents, got %d", b.PriceCents)
	}
	// A null cover is an empty string, which is what the sqlc-backed store
	// returns for a NULL column, so the two transports agree on the model.
	if b.URLCoverImage != "" {
		t.Errorf("a null cover should be empty, got %q", b.URLCoverImage)
	}
}

// A column that cannot be read is a failure, not a zero. Reporting a book at
// price 0 because price_cents came back empty is the kind of bug that only
// shows up on an invoice.
func TestDataServiceListRefusesToInventANumber(t *testing.T) {
	s, _ := newDataServiceStore(t, func(dsRequest) dataservice.Envelope {
		row := bookRow("1", "Clean Code")
		row["price_cents"] = nil
		return rowsReply(row)
	})

	_, err := s.GetAllBooks(t.Context())
	if err == nil {
		t.Fatal("want an error for a null price")
	}
	if !strings.Contains(err.Error(), "price_cents") {
		t.Errorf("the error should name the column, got %v", err)
	}
}

// The id is declared as a string parameter because AUTO_RANDOM ids are wider
// than an exact float64. A number would be rounded on the way out, and the row
// read back would not be the row written.
func TestDataServiceGetSendsTheIdAsExactText(t *testing.T) {
	s, fake := newDataServiceStore(t, func(dsRequest) dataservice.Envelope {
		return rowsReply(bookRow("2083456789012345", "Clean Code"))
	})

	if _, err := s.GetBookByID(t.Context(), 2083456789012345); err != nil {
		t.Fatalf("GetBookByID: %v", err)
	}
	calls := fake.recorded()
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if calls[0].Method != http.MethodGet || calls[0].Path != "book" {
		t.Errorf("want GET book, got %s %s", calls[0].Method, calls[0].Path)
	}
	if got := calls[0].Query.Get("id"); got != "2083456789012345" {
		t.Errorf("the id must be exact text, got %q", got)
	}
	// The lookup endpoint is declared with both keys, so the unused one has to
	// travel too: a statement referencing a parameter that was not sent is
	// refused.
	if got := calls[0].Query.Get("isbn"); got != "" {
		t.Errorf("the unused isbn should travel as an empty string, got %q", got)
	}
}

func TestDataServiceGetMissingBookIsNoRows(t *testing.T) {
	s, _ := newDataServiceStore(t, func(dsRequest) dataservice.Envelope { return rowsReply() })

	_, err := s.GetBookByID(t.Context(), 42)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want sql.ErrNoRows so the handler answers 404, got %v", err)
	}
}

// The create is the endpoint deployed as PUT and the update the one deployed as
// POST. That inversion is Data Service's, and getting it wrong would write a
// duplicate ISBN on every create.
//
// The create is two calls: the insert, then the read-back. A write endpoint
// answers with a row count and no rows, so the second call is the only way to
// learn an AUTO_RANDOM id.
func TestDataServiceCreateUsesPutThenReadsBackByIsbn(t *testing.T) {
	s, fake := newDataServiceStore(t, func(call dsRequest) dataservice.Envelope {
		switch call.Method {
		case http.MethodPut:
			return affectedReply(1)
		default:
			return rowsReply(bookRow("2083456789012345", "Clean Code"))
		}
	})

	created, err := s.CreateBook(t.Context(), sampleBook())
	if err != nil {
		t.Fatalf("CreateBook: %v", err)
	}
	if created.ID != 2083456789012345 || created.ISBN != "978-0132350884" {
		t.Errorf("the stored row should come back, got %+v", created)
	}

	calls := fake.recorded()
	if len(calls) != 2 {
		t.Fatalf("want the insert and the read-back, got %+v", calls)
	}
	if calls[0].Method != http.MethodPut || calls[0].Path != "books" {
		t.Fatalf("the write should be a PUT books, got %+v", calls[0])
	}
	if calls[1].Method != http.MethodGet || calls[1].Path != "book" {
		t.Fatalf("the read-back should be a GET book, got %+v", calls[1])
	}
	// The read-back is keyed on the ISBN, with the id sent as the inert empty
	// string. It is the ISBN the caller chose, so it needs no round trip to learn.
	if got := calls[1].Query.Get("isbn"); got != "978-0132350884" {
		t.Errorf("the read-back should be keyed on isbn, got %#v", got)
	}
	if got := calls[1].Query.Get("id"); got != "" {
		t.Errorf("the unused id should travel as an empty string, got %#v", got)
	}

	body := calls[0].Body
	if body["title"] != "Clean Code" {
		t.Errorf("title did not travel: %#v", body)
	}
	// Numbers must stay numbers: these are integer parameters, and a quoted one
	// is either refused or read as 0.
	for _, name := range []string{"pages", "stock", "price_cents"} {
		if _, ok := body[name].(float64); !ok {
			t.Errorf("%s should be a JSON number, got %#v", name, body[name])
		}
	}
	if body["price_cents"].(float64) != 2999 {
		t.Errorf("price_cents is money: %#v", body["price_cents"])
	}
	// The cover travels even when it is empty. A statement that references a
	// parameter which was never sent is refused, so omitting it would make an
	// empty cover depend on how the endpoint's parameters were declared.
	if got, sent := body["url_cover_image"]; !sent || got != "" {
		t.Errorf("an empty cover should travel as an empty string, got %#v (sent=%v)", got, sent)
	}
}

func TestDataServiceCreateSendsACoverWhenThereIsOne(t *testing.T) {
	s, fake := newDataServiceStore(t, func(call dsRequest) dataservice.Envelope {
		if call.Method == http.MethodPut {
			return affectedReply(1)
		}
		return rowsReply(bookRow("1", "Clean Code"))
	})

	book := sampleBook()
	book.URLCoverImage = "https://cdn.test/clean.png"
	if _, err := s.CreateBook(t.Context(), book); err != nil {
		t.Fatalf("CreateBook: %v", err)
	}
	if got := fake.recorded()[0].Body["url_cover_image"]; got != "https://cdn.test/clean.png" {
		t.Errorf("the cover did not travel: %#v", got)
	}
}

// An insert that changed no row has produced nothing to read back, and the caller
// must not be handed a book with a zero id: the panel would list it, and the next
// edit would be sent to whichever row actually holds zero.
func TestDataServiceCreateThatWroteNothingIsLoud(t *testing.T) {
	s, fake := newDataServiceStore(t, func(dsRequest) dataservice.Envelope { return affectedReply(0) })

	_, err := s.CreateBook(t.Context(), sampleBook())
	if err == nil {
		t.Fatal("want an error, not a book with no id")
	}
	if calls := fake.recorded(); len(calls) != 1 {
		t.Errorf("nothing was written, so nothing should be read back, got %d calls", len(calls))
	}
}

func TestDataServiceUpdateUsesPostThenRereads(t *testing.T) {
	s, fake := newDataServiceStore(t, func(call dsRequest) dataservice.Envelope {
		switch call.Method {
		case http.MethodPost:
			return rowsReply()
		default:
			return rowsReply(bookRow("7", "Clean Code"))
		}
	})

	updated, err := s.UpdateBook(t.Context(), 7, sampleBook())
	if err != nil {
		t.Fatalf("UpdateBook: %v", err)
	}
	if updated.ID != 7 {
		t.Errorf("want the updated book, got %+v", updated)
	}

	calls := fake.recorded()
	if len(calls) != 2 {
		t.Fatalf("want the write and the read-back, got %d calls", len(calls))
	}
	if calls[0].Method != http.MethodPost || calls[0].Path != "books" {
		t.Errorf("want POST books, got %s %s", calls[0].Method, calls[0].Path)
	}
	if got := calls[0].Body["id"]; got != "7" {
		t.Errorf("the id belongs in the body as text, got %#v", got)
	}
	if calls[1].Method != http.MethodGet || calls[1].Query.Get("id") != "7" {
		t.Errorf("want a read-back of the same row, got %s %s", calls[1].Method, calls[1].Query.Encode())
	}
}

// An UPDATE that matches nothing reports success, so the read-back is the only
// thing that turns it into a 404. Without it, deleting a book in one tab and
// saving it in another would return 200 for an edit that never happened.
func TestDataServiceUpdateOfAMissingBookIsNoRows(t *testing.T) {
	s, _ := newDataServiceStore(t, func(dsRequest) dataservice.Envelope { return rowsReply() })

	_, err := s.UpdateBook(t.Context(), 999, sampleBook())
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want sql.ErrNoRows, got %v", err)
	}
}

func TestDataServiceDeleteUsesDeleteWithTheIdAsText(t *testing.T) {
	s, fake := newDataServiceStore(t, func(dsRequest) dataservice.Envelope { return affectedReply(1) })

	if err := s.DeleteBook(t.Context(), 2083456789012345); err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	calls := fake.recorded()
	if len(calls) != 1 || calls[0].Method != http.MethodDelete {
		t.Fatalf("want a single DELETE, got %+v", calls)
	}
	if got := calls[0].Query.Get("id"); got != "2083456789012345" {
		t.Errorf("the id must be exact text, got %q", got)
	}
}

// A delete that matched nothing must not be reported as a delete.
//
// The DELETE endpoint answers 200 with row_affect: 0 for an id that is not
// there, and this used to be forwarded as a success. The panel showed "deleted",
// answered 204, and the book was still in the catalogue on the next load. There
// was no error anywhere to explain it, because from the caller's side nothing
// had failed. sql.ErrNoRows is what handleStoreError turns into a 404.
func TestDataServiceDeleteOfAMissingBookIsNotFound(t *testing.T) {
	s, _ := newDataServiceStore(t, func(dsRequest) dataservice.Envelope { return affectedReply(0) })

	err := s.DeleteBook(t.Context(), 424242)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a delete that removed nothing must be ErrNoRows, got %v", err)
	}
	if IsForeignKey(err) || IsDuplicate(err) {
		t.Errorf("a missing book is neither a conflict nor a duplicate: %v", err)
	}
}

// order_items points at books with ON DELETE RESTRICT. Deleting a book that
// still appears in an order is 1451, not 1452, and it has to be recognisable as
// a conflict or the panel is told the server broke.
func TestDataServiceDeleteOfAReferencedBookIsAConflict(t *testing.T) {
	s, _ := newDataServiceStore(t, func(dsRequest) dataservice.Envelope {
		return failingReply(1451, "a foreign key constraint fails")
	})

	err := s.DeleteBook(t.Context(), 7)
	if err == nil {
		t.Fatal("want the rejection to surface")
	}
	if !IsForeignKey(err) {
		t.Errorf("want a foreign key conflict, got %v", err)
	}
	if IsDuplicate(err) {
		t.Error("a foreign key failure is not a duplicate")
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Error("a rejected delete is not a missing row")
	}
}

func TestDataServiceDuplicateIsbnIsAConflict(t *testing.T) {
	s, _ := newDataServiceStore(t, func(dsRequest) dataservice.Envelope {
		return failingReply(1062, "Duplicate entry")
	})

	_, err := s.CreateBook(t.Context(), sampleBook())
	if !IsDuplicate(err) {
		t.Fatalf("want a duplicate conflict, got %v", err)
	}
	if IsForeignKey(err) {
		t.Error("a duplicate is not a foreign key failure")
	}
}

// The two stock methods run inside the checkout transaction, next to the balance
// debit. If either one travelled over HTTP it would commit on its own and a
// rollback of the rest would leave stock decremented with no sale behind it, so
// this asserts they never leave the process.
func TestStockNeverTravelsOverHTTP(t *testing.T) {
	s, fake := newDataServiceStore(t, func(dsRequest) dataservice.Envelope {
		// If a stock method were routed here, this reply would satisfy it and the
		// assertions below would pass for the wrong reason. Fail the call instead.
		return failingReply(9999, "a stock movement must not go through Data Service")
	})

	const missingID = 1 << 60

	affected, err := s.DecrementBookStock(t.Context(), missingID, 1)
	if err != nil {
		t.Fatalf("DecrementBookStock: %v", err)
	}
	// Zero rows because no such book exists, which is exactly what the checkout
	// service reads as "not enough stock".
	if affected != 0 {
		t.Fatalf("want 0 rows for a book that does not exist, got %d", affected)
	}

	if err := s.IncrementBookStock(t.Context(), missingID, 1); err != nil {
		t.Fatalf("IncrementBookStock: %v", err)
	}

	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("stock went out over HTTP: %+v", calls)
	}
}

// The dashboard's counts come from the pool with the rest of the stats, so this
// one does too. An aggregate is not worth a deployed endpoint.
func TestCountBooksStaysOnSQL(t *testing.T) {
	s, fake := newDataServiceStore(t, nil)

	count, err := s.CountBooks(t.Context())
	if err != nil {
		t.Fatalf("CountBooks: %v", err)
	}
	if count < 0 {
		t.Fatalf("a count cannot be negative, got %d", count)
	}
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("the count went out over HTTP: %+v", calls)
	}
}

func TestDataServiceStorePropagatesACancelledContext(t *testing.T) {
	s, _ := newDataServiceStore(t, nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.GetAllBooks(ctx); err == nil {
		t.Fatal("a cancelled context must not come back as a book list")
	}
}
