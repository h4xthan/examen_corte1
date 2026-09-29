package transport

import (
	"net/http"

	"dvbs/internal/middleware"
	"dvbs/internal/service"
)

type UserHandler struct {
	svc *service.UserService
}

func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// List returns every account. It is mounted behind AdminOnly, so the handler
// itself does not repeat the role check.
//
// A global user listing is only safe when the caller is an administrator: it
// hands over every email address in the system, and email addresses are exactly
// what the password-reset endpoint and the login endpoint would then be probed
// with.
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// Get returns one account, which has to be the caller's own unless they are an
// admin.
func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	if !authorizeOwner(w, r, int64(id)) {
		return
	}

	user, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// Me returns the caller's own account, the route the shop frontend uses to
// bootstrap the session (cart, profile, gate). It is a /users/{id} read where
// the id comes from the authenticated session, not from a client that can only
// know it by asking.
func (h *UserHandler) Me(w http.ResponseWriter, r *http.Request) {
	id, _ := middleware.UserIDFrom(r.Context())
	if id == 0 {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}

	user, err := h.svc.GetUser(r.Context(), int(id))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// Update changes the caller's own profile. The body is a DTO, so role and
// balance_cents are not addressable.
func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	if !authorizeOwner(w, r, int64(id)) {
		return
	}

	var req service.UpdateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	updated, err := h.svc.UpdateUser(r.Context(), id, &req)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	if !authorizeOwner(w, r, int64(id)) {
		return
	}

	if err := h.svc.DeleteUser(r.Context(), id); err != nil {
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
