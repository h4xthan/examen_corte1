package store

import (
	"context"
	"database/sql"
	"time"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type ModerationRow struct {
	ID          int64
	BookID      int64
	BookTitle   string
	UserID      int64
	AuthorEmail string
	Rating      int
	Comment     string
	ImageURL    string
	CreatedAt   time.Time
}

type Review interface {
	GetAllReviews(ctx context.Context) ([]*model.Review, error)
	GetReviewByID(ctx context.Context, id int) (*model.Review, error)
	GetReviewsByBookID(ctx context.Context, bookID int) ([]*model.Review, error)
	GetReviewByBookAndUser(ctx context.Context, bookID, userID int) (*model.Review, error)
	CreateReview(ctx context.Context, review *model.Review) (*model.Review, error)
	UpdateReview(ctx context.Context, id int, review *model.Review) (*model.Review, error)
	DeleteReview(ctx context.Context, id int) error
	GetReviewOwnerID(ctx context.Context, id int) (int64, error)
	ListForModeration(ctx context.Context) ([]ModerationRow, error)
	HasPurchasedBook(ctx context.Context, bookID, userID int) (bool, error)
}

type ReviewStore struct {
	q db.Querier
}

func NewReviewStore(q db.Querier) Review {
	return &ReviewStore{q: q}
}

func reviewFromDB(r db.Review) *model.Review {
	return &model.Review{
		ID:        int64(r.ID),
		BookID:    r.BookID,
		UserID:    r.UserID,
		Rating:    int(r.Rating),
		Comment:   r.Comment.String,
		ImageURL:  r.ImageUrl,
		CreatedAt: r.CreatedAt,
	}
}

func (s *ReviewStore) GetAllReviews(ctx context.Context) ([]*model.Review, error) {
	rows, err := s.q.GetAllReviews(ctx)
	if err != nil {
		return nil, err
	}
	reviews := make([]*model.Review, 0, len(rows))
	for _, r := range rows {
		reviews = append(reviews, reviewFromDB(r))
	}
	return reviews, nil
}

func (s *ReviewStore) GetReviewByID(ctx context.Context, id int) (*model.Review, error) {
	r, err := s.q.GetReviewByID(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return reviewFromDB(r), nil
}

func (s *ReviewStore) GetReviewsByBookID(ctx context.Context, bookID int) ([]*model.Review, error) {
	rows, err := s.q.GetReviewsByBookID(ctx, int64(bookID))
	if err != nil {
		return nil, err
	}
	reviews := make([]*model.Review, 0, len(rows))
	for _, r := range rows {
		reviews = append(reviews, reviewFromDB(r))
	}
	return reviews, nil
}

func (s *ReviewStore) GetReviewByBookAndUser(ctx context.Context, bookID, userID int) (*model.Review, error) {
	r, err := s.q.GetReviewByBookAndUser(ctx, db.GetReviewByBookAndUserParams{
		BookID: int64(bookID),
		UserID: int64(userID),
	})
	if err != nil {
		return nil, err
	}
	return reviewFromDB(r), nil
}

func (s *ReviewStore) CreateReview(ctx context.Context, review *model.Review) (*model.Review, error) {
	res, err := s.q.CreateReview(ctx, db.CreateReviewParams{
		BookID:   review.BookID,
		UserID:   review.UserID,
		Rating:   int32(review.Rating),
		Comment:  sql.NullString{String: review.Comment, Valid: review.Comment != ""},
		ImageUrl: review.ImageURL,
	})
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetReviewByID(ctx, int(id))
}

func (s *ReviewStore) UpdateReview(ctx context.Context, id int, review *model.Review) (*model.Review, error) {
	if err := s.q.UpdateReview(ctx, db.UpdateReviewParams{
		ID:       int64(id),
		BookID:   review.BookID,
		UserID:   review.UserID,
		Rating:   int32(review.Rating),
		Comment:  sql.NullString{String: review.Comment, Valid: review.Comment != ""},
		ImageUrl: review.ImageURL,
	}); err != nil {
		return nil, err
	}
	return s.GetReviewByID(ctx, id)
}

func (s *ReviewStore) DeleteReview(ctx context.Context, id int) error {
	return s.q.DeleteReview(ctx, int64(id))
}

func (s *ReviewStore) GetReviewOwnerID(ctx context.Context, id int) (int64, error) {
	return s.q.GetReviewOwnerID(ctx, int64(id))
}

func (s *ReviewStore) ListForModeration(ctx context.Context) ([]ModerationRow, error) {
	rows, err := s.q.ListReviewsForModeration(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ModerationRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, ModerationRow{
			ID:          int64(r.ID),
			BookID:      r.BookID,
			BookTitle:   r.BookTitle,
			UserID:      r.UserID,
			AuthorEmail: r.AuthorEmail,
			Rating:      int(r.Rating),
			Comment:     r.Comment.String,
			ImageURL:    r.ImageUrl,
			CreatedAt:   r.CreatedAt,
		})
	}
	return out, nil
}

func (s *ReviewStore) HasPurchasedBook(ctx context.Context, bookID, userID int) (bool, error) {
	return s.q.HasPurchasedBook(ctx, db.HasPurchasedBookParams{
		BookID: int64(bookID),
		UserID: int64(userID),
	})
}
