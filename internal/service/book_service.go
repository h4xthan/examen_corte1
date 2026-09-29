package service

import (
	"context"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

type BookWithReviews struct {
	model.Book
	Reviews []*model.Review `json:"reviews"`
}

type BookService struct {
	books   store.Store
	reviews store.Review
}

func NewBookService(books store.Store, reviews store.Review) *BookService {
	return &BookService{books: books, reviews: reviews}
}

func (s *BookService) ListBooks(ctx context.Context) ([]*model.Book, error) {
	return s.books.GetAllBooks(ctx)
}

func (s *BookService) GetBookWithReviews(ctx context.Context, id int) (*BookWithReviews, error) {
	book, err := s.books.GetBookByID(ctx, id)
	if err != nil {
		return nil, err
	}
	reviews, err := s.reviews.GetReviewsByBookID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &BookWithReviews{Book: *book, Reviews: reviews}, nil
}

func (s *BookService) CreateBook(ctx context.Context, book *model.Book) (*model.Book, error) {
	return s.books.CreateBook(ctx, book)
}

func (s *BookService) UpdateBook(ctx context.Context, id int, book *model.Book) (*model.Book, error) {
	return s.books.UpdateBook(ctx, id, book)
}

func (s *BookService) DeleteBook(ctx context.Context, id int) error {
	return s.books.DeleteBook(ctx, id)
}
