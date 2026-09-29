package transport

import (
	"errors"
	"net/http"

	"dvbs/internal/middleware"
	"dvbs/internal/service"
)

type CheckoutHandler struct {
	svc *service.CheckoutService
}

func NewCheckoutHandler(svc *service.CheckoutService) *CheckoutHandler {
	return &CheckoutHandler{svc: svc}
}

// Create turns a cart into a paid order.
//
// The body carries only books, quantities and an optional coupon code. The
// previous version also carried user_id, status and total, and it read a running
// total out of the in-memory ledger when no coupon code was supplied. Both meant
// the client, or the process, decided the price:
//
//   - a user_id in the body bought the books in somebody else's name (#7)
//   - a total in the body recorded whatever the client asked for (#6)
//   - the ledger let a parallel burst of redemptions compound the discount to
//     almost nothing, and the compounded price was what got charged (#14)
//
// The handler now reads the customer from the verified session and passes it
// down, and the service builds the total itself inside a transaction.
func (h *CheckoutHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}

	var req service.CheckoutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result, err := h.svc.Checkout(r.Context(), userID, &req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInsufficientCredit):
			writeError(w, http.StatusPaymentRequired, err)
		case errors.Is(err, service.ErrCouponExhausted),
			errors.Is(err, service.ErrCouponAlreadyUsed),
			errors.Is(err, service.ErrCouponInvalid),
			errors.Is(err, service.ErrInvalidQuantity),
			errors.Is(err, service.ErrEmptyCart):
			writeError(w, http.StatusBadRequest, err)
		case errors.Is(err, service.ErrOutOfStock):
			// 409, not 400: the request was well formed, the warehouse is the
			// problem, and a client that retries may well succeed.
			writeError(w, http.StatusConflict, err)
		default:
			// Anything unrecognised is logged server-side and reported as a plain
			// 500, so a driver message never reaches the client.
			handleStoreError(w, err)
		}
		return
	}

	writeJSON(w, http.StatusCreated, result)
}
