package transport

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"dvbs/internal/middleware"
	"dvbs/internal/model"
	"dvbs/internal/service"
	"dvbs/internal/store"
)

type AdminHandler struct {
	svc   *service.AdminService
	audit store.Audit
}

func NewAdminHandler(svc *service.AdminService, audits ...store.Audit) *AdminHandler {
	h := &AdminHandler{svc: svc}
	if len(audits) > 0 {
		h.audit = audits[0]
	}
	return h
}

// logAudit records one row of users/backups operating history. The store is
// optional so the many tests that build the handler straight from a service
// keep working; the server always passes one.
func (h *AdminHandler) logAudit(r *http.Request, entity, action string, entityID int64, details string) {
	if h.audit == nil {
		return
	}
	entry := &model.AuditLog{
		Entity:   entity,
		Action:   action,
		EntityID: &entityID,
		Details:  details,
	}
	if id, ok := middleware.UserIDFrom(r.Context()); ok {
		entry.UserID = &id
	}
	if email := middleware.EmailFrom(r.Context()); email != "" {
		entry.UserEmail = &email
	}
	if err := h.audit.Record(r.Context(), entry); err != nil {
		slog.Warn("audit record failed", "entity", entity, "action", action, "err", err)
	}
}

func (h *AdminHandler) Stats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.svc.Stats(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *AdminHandler) Users(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// Get returns one account for the users interface's "consulta". It is mounted
// behind AdminOnly with no ownership check on purpose: this is a management
// view of the whole directory, which is precisely what the admin is for.
func (h *AdminHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	user, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	// A consultation is as much an event in the users interface as an alta or a
	// baja is, so the history can show who was looked at, and by whom.
	h.logAudit(r, model.AuditEntityUser, model.AuditActionRead, user.ID, user.Email)
	writeJSON(w, http.StatusOK, user)
}

// Create is the users interface's "alta": opening an account from the panel,
// with an operating role when the admin asks for one. Registration cannot do
// that — only this route may mint a capturista or an auditor.
func (h *AdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req service.CreateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	user, err := h.svc.CreateUser(r.Context(), &req)
	switch {
	case errors.Is(err, service.ErrRoleInvalid), errors.Is(err, service.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		handleStoreError(w, err)
	default:
		h.logAudit(r, model.AuditEntityUser, model.AuditActionCreate, user.ID, user.Email)
		writeJSON(w, http.StatusCreated, user)
	}
}

// Update is the users interface's "modificación": the display name and the
// address. Roles go through SetRole.
func (h *AdminHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	var req service.UpdateUserProfileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	user, err := h.svc.UpdateUserProfile(r.Context(), id, &req)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	h.logAudit(r, model.AuditEntityUser, model.AuditActionUpdate, user.ID, user.Email)
	writeJSON(w, http.StatusOK, user)
}

// Delete is the users interface's "baja": the account is taken off, its
// sessions are revoked, and it can no longer log in. The row stays — it has
// orders and reviews attached — so "baja" here never means a cascade of
// removed history, and the interface can still list who was taken off.
func (h *AdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	actor, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	user, err := h.svc.SetUserActive(r.Context(), int(actor), id, false)
	switch {
	case errors.Is(err, service.ErrSelfDeactivation), errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusConflict, err)
	case err != nil:
		handleStoreError(w, err)
	default:
		h.logAudit(r, model.AuditEntityUser, model.AuditActionDelete, user.ID, user.Email)
		w.WriteHeader(http.StatusNoContent)
	}
}

// Activate is the other half of a "baja": putting an account back. The
// deactivated sessions are already dead, so this is just the bit — there is
// no token_version to bump, because nothing was revoked twice.
func (h *AdminHandler) Activate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	actor, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	user, err := h.svc.SetUserActive(r.Context(), int(actor), id, true)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	h.logAudit(r, model.AuditEntityUser, model.AuditActionActive, user.ID, user.Email)
	writeJSON(w, http.StatusOK, user)
}

// Audit is the history behind every interface. entity narrows the view to one
// kind of record ("book", "user" or "backup"); without it, the response is
// everything, newest first.
func (h *AdminHandler) Audit(w http.ResponseWriter, r *http.Request) {
	if h.audit == nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	entity := r.URL.Query().Get("entity")
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := h.audit.ListRecent(r.Context(), entity, limit)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// Reviews is the moderation queue: every review in the shop with the book it is
// about and the author who wrote it.
//
// The join is in the query and the scoping is in the route. There is no
// user_id in the path, so there is nothing for a caller to substitute.
func (h *AdminHandler) Reviews(w http.ResponseWriter, r *http.Request) {
	reviews, err := h.svc.ListReviews(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reviews)
}

// SetRoleRequest is the whole body. Anything else in the request is rejected by
// the strict decoder, so this cannot become a second route to UpdateUser and its
// mass-assignment problem.
type SetRoleRequest struct {
	Role string `json:"role"`
}

// SetRole grants or revokes a role. It is the way to create another
// administrator, or to hand the catalogue over to a capturista or an auditor,
// and it is why the route is mounted behind AdminOnly, which resolves the
// caller's role from the database on every request.
//
// The 409 for SelfDemotion is deliberate: the request is well formed, it just
// conflicts with the current state of the world.
func (h *AdminHandler) SetRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, errUnauthenticated)
		return
	}
	target, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}

	var req SetRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		return
	}

	user, err := h.svc.SetUserRole(r.Context(), int(actor), int(target), req.Role)
	switch {
	case errors.Is(err, service.ErrRoleInvalid):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, service.ErrSelfDemotion):
		writeError(w, http.StatusConflict, err)
	case err != nil:
		handleStoreError(w, err)
	default:
		h.logAudit(r, model.AuditEntityUser, model.AuditActionRole, user.ID, user.Email)
		writeJSON(w, http.StatusOK, user)
	}
}
