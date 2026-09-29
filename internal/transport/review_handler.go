package transport

import (
	"errors"
	"net/http"

	"dvbs/internal/middleware"
	"dvbs/internal/service"
)

type ReviewHandler struct {
	svc *service.ReviewService
}

func NewReviewHandler(svc *service.ReviewService) *ReviewHandler {
	return &ReviewHandler{svc: svc}
}

// errInvalidReviewID is one message for a bad path id, so a caller cannot tell a
// malformed request from a missing row.
var errInvalidReviewID = errors.New("invalid review id")

// CreateReviewRequest is the writable surface of a review.
//
// The old handler decoded model.Review, so the body carried user_id and book_id.
// book_id is now taken from the path and user_id from the session, which closes
// the impersonation: previously any caller could post a review in another
// customer's name, which is both a lie about the reviewer and a way to make a
// named account look like it had opinions about books it never bought.
type CreateReviewRequest struct {
	Rating   int    `json:"rating"`
	Comment  string `json:"comment"`
	ImageURL string `json:"image_url"`
}

func (h *ReviewHandler) ListByBook(w http.ResponseWriter, r *http.Request) {
	bookID, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	reviews, err := h.svc.ListByBook(r.Context(), bookID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reviews)
}

func (h *ReviewHandler) AddToBook(w http.ResponseWriter, r *http.Request) {
	bookID, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}

	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}

	var req CreateReviewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	created, err := h.svc.CreateReview(r.Context(), int64(bookID), userID, req.Rating, req.Comment, req.ImageURL)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAlreadyReviewed):
			writeError(w, http.StatusConflict, err)
		case errors.Is(err, service.ErrNotPurchased):
			// 403 rather than 404: the book plainly exists, the caller simply
			// has not bought it, and hiding that would be a lie about the
			// catalogue rather than a protection.
			writeError(w, http.StatusForbidden, err)
		case errors.Is(err, service.ErrRatingRange):
			writeError(w, http.StatusBadRequest, err)
		default:
			handleStoreError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *ReviewHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidReviewID)
		return
	}
	review, err := h.svc.GetReview(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func (h *ReviewHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidReviewID)
		return
	}

	// Only the author edits a review. The owner is read from the row, so this is
	// a fact about the database rather than something the request can assert.
	ownerID, err := h.svc.OwnerID(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, ownerID) {
		return
	}

	var req CreateReviewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	updated, err := h.svc.UpdateReview(r.Context(), id, req.Rating, req.Comment, req.ImageURL)
	if err != nil {
		if errors.Is(err, service.ErrRatingRange) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *ReviewHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidReviewID)
		return
	}

	ownerID, err := h.svc.OwnerID(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, ownerID) {
		return
	}

	if err := h.svc.DeleteReview(r.Context(), id); err != nil {
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
