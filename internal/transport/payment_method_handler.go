package transport

import (
	"errors"
	"net/http"

	"dvbs/internal/model"
	"dvbs/internal/service"
)

type PaymentMethodHandler struct {
	svc *service.PaymentMethodService
}

func NewPaymentMethodHandler(svc *service.PaymentMethodService) *PaymentMethodHandler {
	return &PaymentMethodHandler{svc: svc}
}

func (h *PaymentMethodHandler) ListByUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	// The user id is in the path, so it is the caller's to prove, not ours to
	// trust. Without this, reading another account's stored cards was one path
	// segment away.
	if !authorizeOwner(w, r, int64(userID)) {
		return
	}

	methods, err := h.svc.ListByUser(r.Context(), userID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, methods)
}

// cardRequest is the only shape in which a card number is ever accepted.
//
// The field disappears the moment the request has been decoded: nothing
// downstream can read it, because nothing downstream receives it. The handler
// derives brand and last4 and then throws the rest away, so the number exists in
// memory for the length of one request and in no database row ever.
//
// There is deliberately no cvv field. decodeJSON rejects unknown fields, so a
// client that sends one gets a 400 rather than having it silently accepted and
// dropped, which would leave the caller thinking it was stored.
type cardRequest struct {
	CardNumber  string `json:"card_number"`
	CardHolder  string `json:"card_holder"`
	ExpiryMonth int    `json:"expiry_month"`
	ExpiryYear  int    `json:"expiry_year"`
}

// luhn validates the check digit, so a typo is caught before the card is
// recorded as real. This is input validation, not a substitute for an actual
// authorisation against a gateway: the application never charges anybody.
func luhn(number string) bool {
	sum, double := 0, false
	for i := len(number) - 1; i >= 0; i-- {
		d := int(number[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// cardBrand is a coarse IIN prefix check, enough to label a stored card.
func cardBrand(number string) string {
	switch {
	case len(number) == 0:
		return "unknown"
	case number[0] == '4':
		return "visa"
	case number[0] == '5':
		return "mastercard"
	case number[0] == '3':
		return "amex"
	case number[0] == '6':
		return "discover"
	default:
		return "unknown"
	}
}

func (h *PaymentMethodHandler) AddToUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	if !authorizeOwner(w, r, int64(userID)) {
		return
	}

	var req cardRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	digits := make([]byte, 0, len(req.CardNumber))
	for i := 0; i < len(req.CardNumber); i++ {
		if req.CardNumber[i] >= '0' && req.CardNumber[i] <= '9' {
			digits = append(digits, req.CardNumber[i])
		}
	}
	number := string(digits)

	if len(number) < 12 || len(number) > 19 || !luhn(number) {
		writeError(w, http.StatusBadRequest, errors.New("invalid card number"))
		return
	}
	if req.ExpiryMonth < 1 || req.ExpiryMonth > 12 || req.ExpiryYear < 2000 || req.ExpiryYear > 2100 {
		writeError(w, http.StatusBadRequest, errors.New("invalid expiry"))
		return
	}

	created, err := h.svc.Create(r.Context(), &model.PaymentMethod{
		UserID: int64(userID),
		Brand:  cardBrand(number),
		Last4:  number[len(number)-4:],
		// Expiry, and nothing else from the request.
		ExpiryMonth: req.ExpiryMonth,
		ExpiryYear:  req.ExpiryYear,
		// CardHolder is read to validate the shape of the body and then dropped.
		// It is personal data a receipt does not need.
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *PaymentMethodHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	pm, err := h.svc.Get(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	// The owner is only known after the row is read, so the lookup happens first.
	// It does not leak anything: a caller who is neither owner nor admin gets the
	// same 404 whether or not the row exists.
	if !authorizeOwner(w, r, pm.UserID) {
		return
	}
	writeJSON(w, http.StatusOK, pm)
}

func (h *PaymentMethodHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	existing, err := h.svc.Get(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, existing.UserID) {
		return
	}

	var req cardRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.ExpiryMonth < 1 || req.ExpiryMonth > 12 || req.ExpiryYear < 2000 || req.ExpiryYear > 2100 {
		writeError(w, http.StatusBadRequest, errors.New("invalid expiry"))
		return
	}

	// A card already on file is updated through its stored brand and last4, so a
	// request that omits card_number does not erase them.
	updated, err := h.svc.Update(r.Context(), id, &model.PaymentMethod{
		ID:          int64(id),
		UserID:      existing.UserID,
		Brand:       existing.Brand,
		Last4:       existing.Last4,
		ExpiryMonth: req.ExpiryMonth,
		ExpiryYear:  req.ExpiryYear,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *PaymentMethodHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	existing, err := h.svc.Get(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, existing.UserID) {
		return
	}

	if err := h.svc.Delete(r.Context(), id); err != nil {
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
