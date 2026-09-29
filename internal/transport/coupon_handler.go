package transport

import (
	"errors"
	"net/http"
	"time"

	"dvbs/internal/middleware"
	"dvbs/internal/model"
	"dvbs/internal/service"
)

// CouponResponse is the answer to "is this code worth anything to me".
//
// The old version reported original_total, current_total and applications, which
// described the ledger's running arithmetic: how much a customer had knocked off
// so far, across however many times they had applied the same code. Those fields
// are gone with the ledger. A coupon is either usable for an order or it is not,
// and checkout decides.
type CouponResponse struct {
	Status     string `json:"status"` // "active" | "exhausted" | "invalid" | "none"
	Message    string `json:"message"`
	Code       string `json:"code,omitempty"`
	Percent    int    `json:"discount_percent,omitempty"`
	DiscountOn int64  `json:"discount_cents,omitempty"`
}

type CouponHandler struct {
	svc *service.CouponService
}

func NewCouponHandler(svc *service.CouponService) *CouponHandler {
	return &CouponHandler{svc: svc}
}

func (h *CouponHandler) List(w http.ResponseWriter, r *http.Request) {
	coupons, err := h.svc.ListCoupons(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, coupons)
}

func (h *CouponHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid coupon id"))
		return
	}
	coupon, err := h.svc.GetCoupon(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, coupon)
}

// GetByCode is informational and public: it says whether a code exists, without
// spending it. It never reveals who used it or how often.
//
// A code that does not exist and a code that is exhausted are answered
// identically. Distinguishing them would tell an attacker which codes are real,
// which is the enumeration in #4 applied to coupons.
func (h *CouponHandler) GetByCode(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing coupon code"))
		return
	}
	coupon, err := h.svc.GetByCode(r.Context(), code)
	if err != nil {
		writeJSON(w, http.StatusOK, CouponResponse{Status: "invalid", Message: "coupon code not available"})
		return
	}
	if coupon.UsedCount >= coupon.MaxUses {
		writeJSON(w, http.StatusOK, CouponResponse{Status: "exhausted", Message: "coupon code not available"})
		return
	}
	writeJSON(w, http.StatusOK, CouponResponse{
		Status:  "active",
		Message: "coupon code accepted",
		Code:    coupon.Code,
		Percent: coupon.DiscountPercent,
	})
}

// PreviewRequest quotes a code against a cart total without spending anything.
//
// This replaces the old POST /coupons/apply, which incremented used_count, wrote
// to a process-local map and slept for 75ms between its check and its write. The
// sleep was there to make concurrent requests all pass the check, and the
// endpoint existed to serve a cart that has not been bought yet.
type PreviewRequest struct {
	Code          string `json:"code"`
	SubtotalCents int64  `json:"subtotal_cents"`
}

func (h *CouponHandler) Preview(w http.ResponseWriter, r *http.Request) {
	uid, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}

	var req PreviewRequest
	if err := decodeJSON(r, &req); err != nil || req.Code == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing coupon code"))
		return
	}

	coupon, discount, err := h.svc.Preview(r.Context(), uid, req.Code, req.SubtotalCents)
	if err != nil {
		writeJSON(w, http.StatusOK, CouponResponse{Status: "invalid", Message: "coupon code not available"})
		return
	}
	writeJSON(w, http.StatusOK, CouponResponse{
		Status:     "active",
		Message:    "coupon code accepted",
		Code:       coupon.Code,
		Percent:    coupon.DiscountPercent,
		DiscountOn: discount,
	})
}

// Applied always answers "none".
//
// There is no per-customer pending coupon any more, so there is no accumulated
// state to report. The endpoint is kept because the frontend asks for it on
// every cart render, and a 200 with status "none" is a truthful answer rather
// than a 404 the client would have to special-case.
func (h *CouponHandler) Applied(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserIDFrom(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	writeJSON(w, http.StatusOK, CouponResponse{Status: "none", Message: "no coupon applied"})
}

// RemoveApplied is a no-op for the same reason Applied is. It stays so the
// frontend's "remove coupon" button does not have to know the state is gone.
func (h *CouponHandler) RemoveApplied(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserIDFrom(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Create and Update take a DTO rather than model.Coupon, so a caller cannot
// post used_count and invent redemption history. Both are behind AdminOnly.
type CouponRequest struct {
	Code            string `json:"code"`
	DiscountPercent int    `json:"discount_percent"`
	MaxUses         int    `json:"max_uses"`
	ExpiresAt       string `json:"expires_at"`
}

// toModel converts the DTO, parsing the expiry. used_count is not read from the
// request at all: an admin editing a code has no business asserting how many
// times it has been spent.
func (c CouponRequest) toModel() (*model.Coupon, error) {
	expires, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		return nil, service.ErrCouponInvalid
	}
	return &model.Coupon{
		Code:            c.Code,
		DiscountPercent: c.DiscountPercent,
		MaxUses:         c.MaxUses,
		ExpiresAt:       expires,
	}, nil
}

func (h *CouponHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CouponRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	coupon, err := req.toModel()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	created, err := h.svc.CreateCoupon(r.Context(), coupon)
	if err != nil {
		// A validation failure is the caller's mistake, not a server fault, and
		// reporting it as a 500 would both mislead the admin and bury a routine
		// rejection in the error log.
		if errors.Is(err, service.ErrCouponInvalid) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *CouponHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid coupon id"))
		return
	}
	var req CouponRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	coupon, err := req.toModel()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := h.svc.UpdateCoupon(r.Context(), id, coupon)
	if err != nil {
		if errors.Is(err, service.ErrCouponInvalid) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *CouponHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid coupon id"))
		return
	}
	if err := h.svc.DeleteCoupon(r.Context(), id); err != nil {
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
