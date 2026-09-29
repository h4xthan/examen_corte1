package store

import (
	"context"
	"database/sql"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type Store interface {
	GetAllBooks(ctx context.Context) ([]*model.Book, error)
	GetBookByID(ctx context.Context, id int) (*model.Book, error)
	CreateBook(ctx context.Context, book *model.Book) (*model.Book, error)
	UpdateBook(ctx context.Context, id int, book *model.Book) (*model.Book, error)
	DeleteBook(ctx context.Context, id int) error
	DecrementBookStock(ctx context.Context, id int, qty int) (int64, error)
	IncrementBookStock(ctx context.Context, id int, qty int) error
	CountBooks(ctx context.Context) (int64, error)
}

type BookStore struct {
	q db.Querier
}

func NewBookStore(q db.Querier) Store {
	return &BookStore{q: q}
}

func bookFromDB(r db.Book) *model.Book {
	return &model.Book{
		ID:            int64(r.ID),
		Author:        r.Author,
		Title:         r.Title,
		Pages:         int(r.Pages),
		ISBN:          r.Isbn,
		PriceCents:    int64(r.PriceCents),
		Stock:         int(r.Stock),
		URLCoverImage: r.UrlCoverImage.String,
	}
}

func (s *BookStore) GetAllBooks(ctx context.Context) ([]*model.Book, error) {
	rows, err := s.q.GetAllBooks(ctx)
	if err != nil {
		return nil, err
	}
	books := make([]*model.Book, 0, len(rows))
	for _, r := range rows {
		books = append(books, bookFromDB(r))
	}
	return books, nil
}

func (s *BookStore) GetBookByID(ctx context.Context, id int) (*model.Book, error) {
	r, err := s.q.GetBookByID(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return bookFromDB(r), nil
}

func (s *BookStore) CreateBook(ctx context.Context, book *model.Book) (*model.Book, error) {
	res, err := s.q.CreateBook(ctx, db.CreateBookParams{
		Author:        book.Author,
		Title:         book.Title,
		Pages:         int32(book.Pages),
		Isbn:          book.ISBN,
		PriceCents:    book.PriceCents,
		Stock:         int32(book.Stock),
		UrlCoverImage: sql.NullString{String: book.URLCoverImage, Valid: book.URLCoverImage != ""},
	})
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetBookByID(ctx, int(id))
}

func (s *BookStore) UpdateBook(ctx context.Context, id int, book *model.Book) (*model.Book, error) {
	if err := s.q.UpdateBook(ctx, db.UpdateBookParams{
		ID:            int64(id),
		Author:        book.Author,
		Title:         book.Title,
		Pages:         int32(book.Pages),
		Isbn:          book.ISBN,
		PriceCents:    book.PriceCents,
		Stock:         int32(book.Stock),
		UrlCoverImage: sql.NullString{String: book.URLCoverImage, Valid: book.URLCoverImage != ""},
	}); err != nil {
		return nil, err
	}
	return s.GetBookByID(ctx, id)
}

// DeleteBook removes the book, or says that there was nothing to remove.
//
// A DELETE that matches no rows is not an error in SQL, and with the query
// declared as :exec there was no way to tell it from one that did the work. The
// panel therefore reported a book as deleted that it had not deleted, with no
// error anywhere to give it away. The query returns the row count, and zero rows
// becomes sql.ErrNoRows, which handleStoreError already turns into a 404.
func (s *BookStore) DeleteBook(ctx context.Context, id int) error {
	n, err := s.q.DeleteBook(ctx, int64(id))
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *BookStore) DecrementBookStock(ctx context.Context, id int, qty int) (int64, error) {
	// The same double-argument trap as DebitUserBalanceIfSufficient: the query
	// subtracts qty and requires stock >= qty in the WHERE. The second field is
	// that floor; leaving it zero turned the guard into `stock >= 0` and a
	// seller could exceed its stock on every checkout.
	return s.q.DecrementBookStock(ctx, db.DecrementBookStockParams{
		ID:      int64(id),
		Stock:   int32(qty),
		Stock_2: int32(qty),
	})
}

func (s *BookStore) IncrementBookStock(ctx context.Context, id int, qty int) error {
	return s.q.IncrementBookStock(ctx, db.IncrementBookStockParams{
		ID:    int64(id),
		Stock: int32(qty),
	})
}

func (s *BookStore) CountBooks(ctx context.Context) (int64, error) {
	count, err := s.q.CountBooks(ctx)
	return count, err
}
