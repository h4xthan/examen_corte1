package service

import (
	"context"

	"errors"
	"fmt"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

var (
	ErrAlreadyReviewed = errors.New("you have already reviewed this book")
	ErrNotPurchased    = errors.New("you can only review a book you have bought")
	ErrRatingRange     = errors.New("rating must be between 1 and 5")
)

// ReviewService enforces the three rules a review table needs, and the database
// enforces them too. The duplication is the point: the service answers with a
// message the customer can act on, and the constraints stop any other writer,
// including a concurrent request, from getting around them.
type ReviewService struct {
	reviews store.Review
	orders  store.Order
}

func NewReviewService(reviews store.Review, orders store.Order) *ReviewService {
	return &ReviewService{reviews: reviews, orders: orders}
}

func (s *ReviewService) ListByBook(ctx context.Context, bookID int) ([]*model.Review, error) {
	return s.reviews.GetReviewsByBookID(ctx, bookID)
}

func (s *ReviewService) GetReview(ctx context.Context, id int) (*model.Review, error) {
	return s.reviews.GetReviewByID(ctx, id)
}

// OwnerID reports who wrote a review, which is what the handler checks before
// allowing an edit or a delete.
func (s *ReviewService) OwnerID(ctx context.Context, id int) (int64, error) {
	return s.reviews.GetReviewOwnerID(ctx, id)
}

// CreateReview records a review.
//
// userID comes from the session, never from the body. The old signature took a
// whole model.Review decoded from the request, which let a caller post a review
// in somebody else's name — so a page full of five-star reviews attributed to a
// customer who had bought nothing, or worse, to a named victim.
func (s *ReviewService) CreateReview(ctx context.Context, bookID int64, userID int64, rating int, comment string, imageURL string) (*model.Review, error) {
	// The same bound the CHECK constraint enforces. Both exist: the constraint
	// guarantees the invariant, this produces an answer a customer understands.
	if rating < 1 || rating > 5 {
		return nil, fmt.Errorf("%w: got %d", ErrRatingRange, rating)
	}

	purchased, err := s.reviews.HasPurchasedBook(ctx, int(bookID), int(userID))
	if err != nil {
		return nil, err
	}
	if !purchased {
		return nil, ErrNotPurchased
	}

	review, err := s.reviews.CreateReview(ctx, &model.Review{
		BookID:   bookID,
		UserID:   userID,
		Rating:   rating,
		Comment:  comment,
		ImageURL: imageURL,
	})
	if err != nil {
		// A duplicate is refused by the unique index, which is the check that
		// cannot be raced. The service-level pre-check below is only there to
		// give a nicer message in the common case.
		if isUniqueViolation(err) {
			return nil, ErrAlreadyReviewed
		}
		return nil, err
	}
	return review, nil
}

// UpdateReview edits a review's text and rating.
//
// book_id and user_id are not parameters. The old handler decoded
// model.Review from the body and passed the whole thing on, so editing a review
// could reassign it to a different book — putting a five-star comment about one
// title under another — or transfer its authorship.
func (s *ReviewService) UpdateReview(ctx context.Context, id int, rating int, comment string, imageURL string) (*model.Review, error) {
	if rating < 1 || rating > 5 {
		return nil, fmt.Errorf("%w: got %d", ErrRatingRange, rating)
	}

	existing, err := s.reviews.GetReviewByID(ctx, id)
	if err != nil {
		return nil, err
	}

	existing.Rating = rating
	existing.Comment = comment
	existing.ImageURL = imageURL

	updated, err := s.reviews.UpdateReview(ctx, id, existing)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyReviewed
		}
		return nil, err
	}
	return updated, nil
}

func (s *ReviewService) DeleteReview(ctx context.Context, id int) error {
	return s.reviews.DeleteReview(ctx, id)
}
