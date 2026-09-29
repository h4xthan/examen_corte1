package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"dvbs/internal/dataservice"
	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

// The endpoint paths of the book Data App. They are paths, not methods: the
// verb is chosen by Data Service from what each one was deployed as, which is
// why the create and the update look inverted next to the routes the browser
// calls. See endpoints.txt, and the table in AGENTS.md.
const (
	endpointBooks = "books"
	endpointBook  = "book"
)

// DataServiceBookStore is the store the catalogue is served from. There is no
// SQL fallback any more: the Data App is the catalogue, and "the SQL backend"
// exists only in tests and in cmd/seed's seeding.
//
// It is a composite of two transports, and the seam is not arbitrary. The
// catalogue is read-mostly and single-statement, so it goes over Data Service
// and the app stops needing a pooled connection for it. The stock movements do
// not: DecrementBookStock is called from inside the checkout transaction, next
// to the balance debit and the order row, and a Data Service call is its own
// connection with its own transaction. Sending it over HTTP would decrement the
// stock in one commit while the order and the debit live in another, and a
// rollback of the second would leave the first applied: inventory disappearing
// with no sale behind it. So the two stock methods stay on the querier they are
// handed, and in a transaction that querier is the transaction's own.
//
// The consequence worth writing down: this type is only correct when the
// querier passed in is the same one the caller is inside. Constructing it with
// the pool and then calling the stock methods outside a transaction would work
// but would not be atomic with anything, which is the mistake the interface
// invites.
type DataServiceBookStore struct {
	crud  *dataservice.Client
	stock db.Querier
}

// NewDataServiceBookStore returns a book store whose catalogue goes through the
// Data App in crud and whose stock movements use stock.
//
// stock is a db.Querier rather than a store.Store on purpose: inside
// NewQueriers it is the transaction, and that is the only thing that makes the
// stock methods safe.
func NewDataServiceBookStore(crud *dataservice.Client, stock db.Querier) Store {
	return &DataServiceBookStore{crud: crud, stock: stock}
}

// bookFromRow maps one Data Service row onto the model.
//
// Every value arrives as text, so a number that fails to parse is reported
// rather than dropped: a book whose price_cents came back unreadable must not
// be listed as if it cost nothing.
func bookFromRow(r dataservice.Row) (*model.Book, error) {
	pages, err := r.Int("pages")
	if err != nil {
		return nil, err
	}
	priceCents, err := r.Int64("price_cents")
	if err != nil {
		return nil, err
	}
	stock, err := r.Int("stock")
	if err != nil {
		return nil, err
	}
	id, err := r.Int64("id")
	if err != nil {
		return nil, err
	}

	cover, _ := r.NullableStr("url_cover_image")
	return &model.Book{
		ID:            id,
		Author:        r.Str("author"),
		Title:         r.Str("title"),
		Pages:         pages,
		ISBN:          r.Str("isbn"),
		PriceCents:    priceCents,
		Stock:         stock,
		URLCoverImage: cover,
	}, nil
}

func (s *DataServiceBookStore) GetAllBooks(ctx context.Context) ([]*model.Book, error) {
	rows, err := s.crud.Query(ctx, http.MethodGet, endpointBooks, nil)
	if err != nil {
		return nil, err
	}
	books := make([]*model.Book, 0, len(rows))
	for i, r := range rows {
		book, err := bookFromRow(r)
		if err != nil {
			return nil, fmt.Errorf("books: row %d: %w", i, err)
		}
		books = append(books, book)
	}
	return books, nil
}

func (s *DataServiceBookStore) GetBookByID(ctx context.Context, id int) (*model.Book, error) {
	return s.getBook(ctx, id, "")
}

// getBook reads one book by id or by ISBN.
//
// The endpoint takes both keys and matches on whichever is not empty, so both are
// always sent. A statement that references a parameter which was not sent is
// refused, so choosing per call which one to include would make the endpoint's
// parameter declarations load-bearing. The unused one travels as "" and the
// endpoint's NULLIF turns it into NULL, which matches neither column.
//
// A zero id is that unused key, and it is sent as "" rather than as "0" on
// purpose: "0" would work only because no AUTO_RANDOM id is ever zero, which is
// a property of the id generator and not of the lookup.
func (s *DataServiceBookStore) getBook(ctx context.Context, id int, isbn string) (*model.Book, error) {
	key := itoa(id)
	if id == 0 {
		key = ""
	}
	row, err := s.crud.QueryOne(ctx, http.MethodGet, endpointBook,
		map[string]any{"id": key, "isbn": isbn})
	if err != nil {
		return nil, err
	}
	return bookFromRow(row)
}

// CreateBook inserts through the endpoint deployed as PUT and then reads the row
// back.
//
// The read-back is not an optimisation, it is the only way to learn the id. The
// table uses AUTO_RANDOM, so the id does not exist until the statement runs, and
// a TiDB 8.5 INSERT cannot hand it back: RETURNING is a syntax error on this
// version, and a write endpoint replies with the row count and no rows even when
// its script ends in a SELECT. ISBN is UNIQUE and the caller supplies it, so it
// is the key the row can be read back by.
func (s *DataServiceBookStore) CreateBook(ctx context.Context, book *model.Book) (*model.Book, error) {
	res, err := s.crud.Exec(ctx, http.MethodPut, endpointBooks, bookParams(book))
	if err != nil {
		return nil, err
	}
	if res.RowAffect != 1 {
		return nil, errInsertWroteNothing
	}
	return s.getBook(ctx, 0, book.ISBN)
}

// errInsertWroteNothing is what a create that changed no row says.
//
// Returning a book with a zero id would be worse than an error: the panel would
// list a new entry, and the next edit of it would be sent to whichever row
// actually holds zero.
var errInsertWroteNothing = errors.New("books: the insert endpoint reported no written row, " +
	"so the stored book cannot be read back")

// UpdateBook writes through the endpoint deployed as POST.
//
// An id that does not exist is not an error at the endpoint: the UPDATE matches
// nothing and reports success. Reading the row back is what turns that into
// ErrNoRows, so the panel gets a 404 for a book that is gone instead of a 200
// for an edit that never happened.
func (s *DataServiceBookStore) UpdateBook(ctx context.Context, id int, book *model.Book) (*model.Book, error) {
	params := bookParams(book)
	params["id"] = itoa(id)
	if _, err := s.crud.Query(ctx, http.MethodPost, endpointBooks, params); err != nil {
		return nil, err
	}
	return s.GetBookByID(ctx, id)
}

// DeleteBook removes the book. A book that still appears in an order comes back
// as a foreign key rejection, which the transport layer answers with 409 rather
// than the 500 it used to be.
//
// It asks for row_affect and refuses to claim a delete that did not happen. The
// endpoint answers a DELETE of an id that is not there with 200 and
// row_affect: 0, and this used to forward that as a plain success, so the panel
// reported 204, said the book was gone, and the book came back on the next
// reload. That is the worst shape a delete can have: it is indistinguishable
// from working, and the only evidence is the row that is still there. A missing
// book is sql.ErrNoRows, which handleStoreError already turns into 404.
func (s *DataServiceBookStore) DeleteBook(ctx context.Context, id int) error {
	res, err := s.crud.Exec(ctx, http.MethodDelete, endpointBooks, map[string]any{"id": itoa(id)})
	if err != nil {
		return err
	}
	if res.RowAffect != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// DecrementBookStock and IncrementBookStock stay on SQL. See the type comment
// for why they cannot go over HTTP.

func (s *DataServiceBookStore) DecrementBookStock(ctx context.Context, id int, qty int) (int64, error) {
	return s.stock.DecrementBookStock(ctx, db.DecrementBookStockParams{
		ID:      int64(id),
		Stock:   int32(qty),
		Stock_2: int32(qty),
	})
}

func (s *DataServiceBookStore) IncrementBookStock(ctx context.Context, id int, qty int) error {
	return s.stock.IncrementBookStock(ctx, db.IncrementBookStockParams{
		ID:    int64(id),
		Stock: int32(qty),
	})
}

// CountBooks is an aggregate over the whole table, not part of the catalogue
// read path, and it stays on SQL like the stock methods do.
//
// The dashboard's other counts have to come from the pool anyway, so routing
// this one over HTTP would add an endpoint to deploy and to keep correct for no
// gain. s.stock is the same querier the stock methods use, which is the pool
// when this store is built at startup.
func (s *DataServiceBookStore) CountBooks(ctx context.Context) (int64, error) {
	return s.stock.CountBooks(ctx)
}

// bookParams is the parameter map of the insert and the update.
//
// Money goes as an int64 and stock as an int rather than as their names: the
// endpoint declares them as integer parameters, and a value sent as a string is
// either rejected or silently coerced to zero.
//
// The cover is always sent, even when it is empty, and the endpoint turns that
// into a NULL with NULLIF. Leaving it out was the obvious alternative and it
// does not work: a statement referencing a parameter that was not sent is
// refused, so omitting it would make an empty cover depend on the operator
// having marked the parameter optional. The empty string is the same value the
// sqlc-backed store writes as NULL, so the two agree on what a missing cover
// means.
func bookParams(book *model.Book) map[string]any {
	return map[string]any{
		"author":          book.Author,
		"title":           book.Title,
		"pages":           int64(book.Pages),
		"isbn":            book.ISBN,
		"price_cents":     book.PriceCents,
		"stock":           int64(book.Stock),
		"url_cover_image": book.URLCoverImage,
	}
}

// itoa renders an id as text for the parameter map.
//
// Ids are declared as string parameters because the key column is an
// AUTO_RANDOM BIGINT, which is wider than the range a float64 represents
// exactly. That is also why they come back as text. Sending one as a JSON
// number is not a detail: 2083456789012345 is not representable exactly as a
// double, and the row that gets read back would not be the row that was
// written.
func itoa(id int) string { return strconv.FormatInt(int64(id), 10) }
