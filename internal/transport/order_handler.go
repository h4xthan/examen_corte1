package transport

import (
	"errors"
	"net/http"

	"dvbs/internal/middleware"
	"dvbs/internal/model"
	"dvbs/internal/service"
)

type OrderHandler struct {
	svc *service.OrderService
}

func NewOrderHandler(svc *service.OrderService) *OrderHandler {
	return &OrderHandler{svc: svc}
}

// List returns orders. It is mounted behind AdminOnly.
//
// A customer has their own listing at /users/{id}/orders semantics; the global
// one hands over every purchase in the shop.
func (h *OrderHandler) List(w http.ResponseWriter, r *http.Request) {
	orders, err := h.svc.ListOrders(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

// ListMine is the customer's own order history.
//
// GET /orders is the administrative listing of everybody's orders (#5), so the
// profile page had nothing to call: it was asking for a route it is not allowed
// to use and rendering an empty history. The scoping is not a filter the caller
// can widen, because the user id comes from the verified session and the query
// itself is already scoped.
func (h *OrderHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	orders, err := h.svc.ListForUser(r.Context(), userID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if orders == nil {
		orders = []*model.Order{}
	}
	writeJSON(w, http.StatusOK, orders)
}

// Get returns one order with its items, to its owner or an admin.
func (h *OrderHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}

	order, err := h.svc.GetOrder(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, order.UserID) {
		return
	}

	items, err := h.svc.ListItems(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &service.OrderWithItems{Order: *order, Items: items})
}

// CreateOrderRequest is the whole writable surface of an order.
//
// The original handler decoded the body into model.Order, so a client could send
// user_id and create an order in somebody else's name, and send total and have
// the shop record a purchase that was never paid for. Both are dropped here: the
// route is admin-only, the owner comes from the session, and the amount is
// computed by the checkout flow rather than declared by a caller.
type CreateOrderRequest struct {
	Status string `json:"status"`
}

// Create is mounted behind AdminOnly. Customers buy through /checkout, which is
// the only path that prices an order, debits a balance and reserves stock.
func (h *OrderHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateOrderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// A customer creating an order for someone is #7. The subject comes from the
	// verified session, never from the body.
	owner, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}

	// A zero total. This route is an administrative convenience, so the only way
	// to get a priced order is through checkout, which computes one.
	created, err := h.svc.CreateOrder(r.Context(), &model.Order{
		UserID: owner,
		Status: req.Status,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *OrderHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}

	existing, err := h.svc.GetOrder(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	// An administrator manages the state of any order; a customer manages none of
	// them. authorizeOwner passes the admin through and answers 404 to anybody
	// else, so the rejection does not confirm that the order exists.
	if middleware.RoleFrom(r.Context()) != model.RoleAdmin {
		writeError(w, http.StatusForbidden, errors.New("only an administrator can change an order's status"))
		return
	}
	if !authorizeOwner(w, r, existing.UserID) {
		return
	}

	var req CreateOrderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Only an administrator moves an order along. A customer used to be able to
	// PUT their own order to "completed", which the review rule trusts and the
	// revenue figure counts, so buying nothing and marking it shipped was two
	// requests. The customer's route to cancelling is DELETE.
	//
	// total and user_id are carried from the stored row. A caller cannot reprice
	// an order or hand it to somebody else by editing it.
	existing.Status = req.Status
	updated, err := h.svc.SetStatus(r.Context(), id, req.Status)
	if err != nil {
		// A status outside the closed set is the caller's mistake. The CHECK
		// constraint rejects it too, but reporting a constraint violation as a
		// 500 would bury a routine bad request in the error log.
		if errors.Is(err, service.ErrOrderStatusInvalid) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *OrderHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}

	existing, err := h.svc.GetOrder(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !authorizeOwner(w, r, existing.UserID) {
		return
	}

	if err := h.svc.DeleteOrder(r.Context(), id); err != nil {
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
