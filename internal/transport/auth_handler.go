package transport

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"dvbs/internal/middleware"
	"dvbs/internal/service"
)

type AuthHandler struct {
	svc          *service.AuthService
	cookieSecure bool
	sessionTTL   time.Duration
}

func NewAuthHandler(svc *service.AuthService, cookieSecure bool, sessionTTL time.Duration) *AuthHandler {
	return &AuthHandler{svc: svc, cookieSecure: cookieSecure, sessionTTL: sessionTTL}
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req service.RegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	user, err := h.svc.Register(r.Context(), &req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrWeakPassword):
			writeError(w, http.StatusBadRequest, err)
		case errors.Is(err, service.ErrAccountExists):
			writeError(w, http.StatusConflict, err)
		default:
			handleStoreError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req service.LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	signed, user, err := h.svc.Login(r.Context(), &req)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			// One status and one body for both "no such user" and "wrong
			// password". Distinguishing them is user enumeration.
			writeError(w, http.StatusUnauthorized, service.ErrInvalidCredentials)
			return
		}
		handleStoreError(w, err)
		return
	}

	// The session lives in an httpOnly cookie. The token is not in the response
	// body any more: a body is readable by JavaScript, and this credential is
	// the thing an XSS is after.
	middleware.SetSessionCookie(w, signed, h.sessionTTL, h.cookieSecure)

	// Kept in the body for non-browser clients, which cannot use a cookie jar
	// conveniently. The browser ignores it; see web/src/api/client.js.
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// Logout clears the session cookie.
//
// The client is expected to call it on sign-out rather than only deleting local
// state, otherwise the cookie is still a live credential in the browser.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	middleware.ClearSessionCookie(w, h.cookieSecure)
	writeJSON(w, http.StatusOK, map[string]string{"message": "sesión cerrada"})
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req ForgotPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// The error is deliberately not inspected for a "user not found" case. The
	// service returns nil in that situation so the response cannot be used to
	// enumerate accounts; anything that does go wrong is still logged.
	if err := h.svc.ForgotPassword(r.Context(), req.Email); err != nil {
		slog.Error("forgot-password failed", "error", err)
		handleStoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "si el email existe, se envió un correo con el código de recuperación",
	})
}

type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req ResetPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	user, err := h.svc.ResetPassword(r.Context(), req.Token, req.NewPassword)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrWeakPassword):
			writeError(w, http.StatusBadRequest, err)
		case errors.Is(err, service.ErrResetTokenInvalid):
			// Unknown, expired and already-used tokens are indistinguishable.
			// Telling them apart would confirm that a guessed token once existed.
			writeError(w, http.StatusBadRequest, err)
		default:
			handleStoreError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "password updated", "user": user})
}
