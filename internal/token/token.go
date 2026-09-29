// Package token issues and verifies the session tokens.
//
// Both halves live in one place on purpose. The original code signed tokens in
// the service and verified them in the middleware, each with its own copy of the
// secret and its own idea of which algorithms were acceptable. That is how a
// keyfunc ends up handing the raw signing key to a token that asks for a
// different algorithm, and how a hardcoded constant becomes the root of trust.
// One Manager means one secret, one algorithm, one issuer, one audience.
package token

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// Issuer and Audience pin who a token is for. Without them a token minted
	// for a different service sharing the same signing key would be accepted here.
	Issuer   = "dvbs"
	Audience = "dvbs-web"
)

// Errors are deliberately coarse: the caller answers 401 regardless, and a
// detailed reason would tell an attacker which part of the forgery attempt was
// close to working.
var (
	ErrInvalid = errors.New("invalid token")
	ErrExpired = errors.New("expired token")
)

type Claims struct {
	jwt.RegisteredClaims
	Email        string `json:"email"`
	TokenVersion int    `json:"ver"`
}

type Manager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewManager returns a Manager bound to secret. A short secret is rejected
// rather than padded, because a weak key is a real problem and silently
// accepting one here would just move the failure to production.
func NewManager(secret string, ttl time.Duration) (*Manager, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("jwt secret must be at least 32 bytes, got %d", len(secret))
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("session ttl must be positive, got %v", ttl)
	}
	return &Manager{
		secret: []byte(secret),
		ttl:    ttl,
		now:    time.Now,
	}, nil
}

// Issue mints a token for a user. tokenVersion is copied into the token so the
// middleware can reject sessions issued before a password change.
func (m *Manager) Issue(userID int64, email string, tokenVersion int) (string, error) {
	now := m.now()
	jti, err := randomJTI()
	if err != nil {
		return "", err
	}

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", userID),
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        jti,
		},
		Email:        email,
		TokenVersion: tokenVersion,
	}

	// SigningMethodHS256 is pinned at construction, so the algorithm cannot be
	// chosen by the token being verified.
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// Verify parses and validates a token.
//
// jwt.WithValidMethods is the important one: the keyfunc below only runs after
// the library has checked that the token's alg is on that list, which closes the
// "alg: none" and algorithm-confusion families of forgery.
func (m *Manager) Verify(raw string) (*Claims, error) {
	var claims Claims

	parsed, err := jwt.ParseWithClaims(raw, &claims,
		func(*jwt.Token) (any, error) { return m.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, ErrInvalid
	}
	if !parsed.Valid {
		return nil, ErrInvalid
	}
	if claims.ID == "" {
		// A token without a jti cannot be traced or individually revoked, and
		// its absence means it was not minted by Issue.
		return nil, ErrInvalid
	}
	return &claims, nil
}

// TTL exposes the configured lifetime so the transport layer can set the cookie
// Max-Age to the same value the token itself enforces.
func (m *Manager) TTL() time.Duration { return m.ttl }

func randomJTI() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate jti: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
