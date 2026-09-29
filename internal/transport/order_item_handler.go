package transport

import (
	"database/sql"
	"errors"
	"net/http"

	"dvbs/internal/service"
	"dvbs/internal/store"
)

type OrderItemHandler struct {
	svc    *service.OrderItemService
	orders *service.OrderService
}

func NewOrderItemHandler(svc *service.OrderItemService, orders *service.OrderService) *OrderItemHandler {
	return &OrderItemHandler{svc: svc, orders: orders}
}

// errInvalidItemID is one message for a bad path id, reused so a caller cannot
// probe the difference between "not a number" and "no such item".
var errInvalidItemID = errors.New("invalid order item id")

// AddItemRequest is the entire writable surface of an order line.
//
// The previous handler decoded model.OrderItem straight from the body, so the
// client supplied unit_price and got to choose what it was charged, and it
// supplied order_id and book_id, so it could attach a line to somebody else's
// order. Only book_id and quantity are accepted now. unit_price is not a field:
// decodeJSON rejects unknown fields, so sending one is a 400 rather than a
// silently ignored value that looks like it was honoured.
type AddItemRequest struct {
	BookID   int64 `json:"book_id,string"`
	Quantity int   `json:"quantity"`
}

// handleItemError maps the service's sentinel errors onto status codes and
// leaves everything else to handleStoreError.
//
// This has to be explicit. The service refuses a bad quantity with a domain
// error, and passing that to handleStoreError would report a client mistake as a
// server fault: a 500 for something the caller can fix, and a stack of noise in
// the logs for a rejected request.
func handleItemError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidQuantity):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, service.ErrOutOfStock):
		// The request was well formed and the warehouse is the problem, so 409
		// rather than 400: a client that waits and retries may succeed.
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, store.ErrNoRows):
		writeError(w, http.StatusNotFound, errNotFound)
	default:
		handleStoreError(w, err)
	}
}

func (h *OrderItemHandler) ListByOrder(w http.ResponseWriter, r *http.Request) {
	orderID, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}

	// The order is loaded first so the caller can be checked against it. Doing
	// it the other way round would return the contents of any order by number.
	order, err := h.orders.GetOrder(r.Context(), orderID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, order.UserID) {
		return
	}

	items, err := h.svc.ListByOrder(r.Context(), orderID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *OrderItemHandler) AddToOrder(w http.ResponseWriter, r *http.Request) {
	orderID, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}

	order, err := h.orders.GetOrder(r.Context(), orderID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, order.UserID) {
		return
	}

	var req AddItemRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	created, err := h.svc.AddItem(r.Context(), orderID, req.BookID, req.Quantity)
	if err != nil {
		handleItemError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *OrderItemHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidItemID)
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

	item, err := h.svc.GetItem(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// UpdateItemRequest carries only what may change. book_id and order_id are not
// here on purpose: moving a line to another order, or to another book without
// re-pricing it, is both a takeover and an accounting trick.
type UpdateItemRequest struct {
	Quantity int `json:"quantity"`
}

func (h *OrderItemHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidItemID)
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

	var req UpdateItemRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	updated, err := h.svc.UpdateItem(r.Context(), id, req.Quantity)
	if err != nil {
		handleItemError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *OrderItemHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidItemID)
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

	if err := h.svc.DeleteItem(r.Context(), id); err != nil {
		handleItemError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
