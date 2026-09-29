package transport

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"dvbs/internal/middleware"
	"dvbs/internal/model"
	"dvbs/internal/service"
	"dvbs/internal/store"
)

type BookHandler struct {
	svc   *service.BookService
	audit store.Audit
}

func NewBookHandler(svc *service.BookService, audits ...store.Audit) *BookHandler {
	h := &BookHandler{svc: svc}
	if len(audits) > 0 {
		h.audit = audits[0]
	}
	return h
}

// logAudit records one row of catalogue operating history when an audit store is
// wired in. The store is optional so the many tests that build the handler
// straight from a service keep working; cmd/server always passes one.
func (h *BookHandler) logAudit(r *http.Request, action string, entityID int64, details string) {
	if h.audit == nil {
		return
	}
	entry := &model.AuditLog{
		Entity:   model.AuditEntityBook,
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
		slog.Warn("audit record failed", "entity", model.AuditEntityBook, "action", action, "err", err)
	}
}

// isPanelRole reports whether the caller is one of the operating roles whose
// catalogue consultations the history records. A customer browsing a book is
// normal traffic, not an operation a panel should log like a change.
func isPanelRole(role string) bool {
	switch role {
	case model.RoleAdmin, model.RoleCapturista, model.RoleAuditor:
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func parseID(r *http.Request) (int, error) {
	return strconv.Atoi(r.PathValue("id"))
}

// handleStoreError maps a store failure onto a status.
//
// A missing row is a 404, a rejected uniqueness or reference is a 409, and
// anything else is a 500 whose detail stays in the log. The 409 cases used to
// arrive as 500s, which told the admin panel that a duplicate ISBN or a book
// with orders in it was the server's fault. They are the caller's problem to
// resolve, and the panel already has a message for each.
func handleStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if store.IsDuplicate(err) {
		writeError(w, http.StatusConflict, errors.New("that ISBN is already used by another book"))
		return
	}
	if store.IsForeignKey(err) {
		writeError(w, http.StatusConflict, errors.New("this book appears in an order and cannot be deleted"))
		return
	}
	writeError(w, http.StatusInternalServerError, err)
}

// maxBodyBytes caps every JSON body. The only endpoint that legitimately
// exceeds this is POST /uploads, which is multipart and handled separately.
const maxBodyBytes = 1 << 20 // 1 MiB

// decodeJSON strictly decodes a request body. DisallowUnknownFields matters for
// more than tidiness: without it, extra keys like "role" or "balance_cents"
// decode silently into the target struct and become a mass-assignment hole.
func decodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errRequestTooLarge
		}
		slog.Warn("rejected malformed body", "path", r.URL.Path, "err", err)
		return errInvalidJSON
	}
	// Reject trailing content so `{"a":1}{"b":2}` cannot smuggle a second
	// document past the decoder.
	if dec.More() {
		return errInvalidJSON
	}
	return nil
}

func (h *BookHandler) List(w http.ResponseWriter, r *http.Request) {
	books, err := h.svc.ListBooks(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, books)
}

func (h *BookHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	book, err := h.svc.GetBookWithReviews(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	// A consultation by one of the operating roles is an event the catalogue
	// history shows. Public shoppers are not logged individually; that would
	// drown the history in everyone who ever opened a page.
	if isPanelRole(middleware.RoleFrom(r.Context())) {
		h.logAudit(r, model.AuditActionRead, int64(id), book.Title)
	}
	writeJSON(w, http.StatusOK, book)
}

// BookRequest is the whole body of a book write.
//
// The handler used to decode model.Book straight from the request, so `id` was a
// field a caller could set. These routes are admin-only, which limits the blast
// radius, but "admin may do anything" is not a reason to let a form post an
// arbitrary primary key: the panel sends the fields it means to send, and
// DisallowUnknownFields turns anything else into a 400 instead of a silent no-op.
type BookRequest struct {
	Author        string `json:"author"`
	Title         string `json:"title"`
	Pages         int    `json:"pages"`
	ISBN          string `json:"isbn"`
	PriceCents    int64  `json:"price_cents"`
	Stock         int    `json:"stock"`
	URLCoverImage string `json:"url_cover_image"`
}

func (b BookRequest) toModel() (*model.Book, error) {
	if strings.TrimSpace(b.Title) == "" {
		return nil, errors.New("title is required")
	}
	if strings.TrimSpace(b.Author) == "" {
		return nil, errors.New("author is required")
	}
	if b.Pages < 0 {
		return nil, errors.New("pages cannot be negative")
	}
	if b.PriceCents < 0 {
		return nil, errors.New("price_cents cannot be negative")
	}
	if b.Stock < 0 {
		return nil, errors.New("stock cannot be negative")
	}
	return &model.Book{
		Author:        strings.TrimSpace(b.Author),
		Title:         strings.TrimSpace(b.Title),
		Pages:         b.Pages,
		ISBN:          strings.TrimSpace(b.ISBN),
		PriceCents:    b.PriceCents,
		Stock:         b.Stock,
		URLCoverImage: strings.TrimSpace(b.URLCoverImage),
	}, nil
}

func (h *BookHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req BookRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	book, err := req.toModel()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	created, err := h.svc.CreateBook(r.Context(), book)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	h.logAudit(r, model.AuditActionCreate, created.ID, created.Title)
	writeJSON(w, http.StatusCreated, created)
}

func (h *BookHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	var req BookRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	book, err := req.toModel()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := h.svc.UpdateBook(r.Context(), id, book)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	h.logAudit(r, model.AuditActionUpdate, int64(id), updated.Title)
	writeJSON(w, http.StatusOK, updated)
}

func (h *BookHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidPathID)
		return
	}
	// The history shows the title of a low. Best-effort: if the read fails the
	// delete still proceeds, and the row is recorded with just its id.
	title := ""
	if book, err := h.svc.GetBookWithReviews(r.Context(), id); err == nil {
		title = book.Title
	}
	if err := h.svc.DeleteBook(r.Context(), id); err != nil {
		handleStoreError(w, err)
		return
	}
	h.logAudit(r, model.AuditActionDelete, int64(id), title)
	w.WriteHeader(http.StatusNoContent)
}
