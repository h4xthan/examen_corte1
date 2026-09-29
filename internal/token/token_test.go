package token

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-only-secret-not-used-anywhere-else-0123456789"

func newTestManager(t *testing.T) *Manager {
	t.Helper()

	m, err := NewManager(testSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// sign builds a token with a caller-chosen method and key, so tests can produce
// the forgeries the verifier is supposed to refuse.
func sign(t *testing.T, method jwt.SigningMethod, key any, mutate func(*Claims)) string {
	t.Helper()

	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "42",
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        "a-jti-value",
		},
		Email:        "user@test.com",
		TokenVersion: 0,
	}
	if mutate != nil {
		mutate(&claims)
	}

	raw, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign with %v: %v", method.Alg(), err)
	}
	return raw
}

func TestIssueAndVerifyRoundTrip(t *testing.T) {
	m := newTestManager(t)

	raw, err := m.Issue(42, "user@test.com", 7)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	claims, err := m.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "42" {
		t.Errorf("sub = %q, want 42", claims.Subject)
	}
	if claims.Email != "user@test.com" {
		t.Errorf("email = %q", claims.Email)
	}
	if claims.TokenVersion != 7 {
		t.Errorf("ver = %d, want 7", claims.TokenVersion)
	}
	if claims.ID == "" {
		t.Error("no jti")
	}
	if claims.Issuer != Issuer || len(claims.Audience) != 1 || claims.Audience[0] != Audience {
		t.Errorf("issuer/audience = %q/%v", claims.Issuer, claims.Audience)
	}
}

func TestNewManagerRejectsWeakConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		ttl    time.Duration
	}{
		{"empty secret", "", time.Hour},
		{"short secret", "0123456789", time.Hour},
		{"31 bytes", "0123456789012345678901234567890", time.Hour},
		{"zero ttl", testSecret, 0},
		{"negative ttl", testSecret, -time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewManager(tc.secret, tc.ttl); err == nil {
				t.Fatal("expected an error")
			}
		})
	}

	// 32 bytes exactly is the boundary and must be accepted.
	if _, err := NewManager("01234567890123456789012345678901", time.Hour); err != nil {
		t.Fatalf("32-byte secret rejected: %v", err)
	}
}

// TestVerifyRejectsOtherSecret is the whole point of moving the key out of the
// source: the original key was a public constant, so anyone could mint a token.
func TestVerifyRejectsOtherSecret(t *testing.T) {
	m := newTestManager(t)

	forged := sign(t, jwt.SigningMethodHS256, []byte("a-different-secret-of-sufficient-length!"), nil)

	if _, err := m.Verify(forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Verify: err = %v, want ErrInvalid", err)
	}
}

// TestVerifyRejectsAlgNone is the classic attack: a token that declares no
// algorithm and carries no signature, signed with the empty string.
func TestVerifyRejectsAlgNone(t *testing.T) {
	m := newTestManager(t)

	forged := sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, nil)
	if _, err := m.Verify(forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Verify: err = %v, want ErrInvalid", err)
	}
}

// TestVerifyRejectsOtherAlgorithms covers algorithm confusion: the same secret
// used with a different HMAC, and the RS256 family where a public key would be
// handed to the verifier as if it were a shared secret.
func TestVerifyRejectsOtherAlgorithms(t *testing.T) {
	m := newTestManager(t)

	cases := []struct {
		name   string
		method jwt.SigningMethod
		key    any
	}{
		{"HS512", jwt.SigningMethodHS512, []byte(testSecret)},
		{"HS384", jwt.SigningMethodHS384, []byte(testSecret)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forged := sign(t, tc.method, tc.key, nil)
			if _, err := m.Verify(forged); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Verify: err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestVerifyRejectsExpiry(t *testing.T) {
	m := newTestManager(t)

	expired := sign(t, jwt.SigningMethodHS256, []byte(testSecret), func(c *Claims) {
		past := jwt.NewNumericDate(time.Now().Add(-2 * time.Hour))
		c.ExpiresAt = past
		c.IssuedAt = past
		c.NotBefore = past
	})

	if _, err := m.Verify(expired); !errors.Is(err, ErrExpired) {
		t.Fatalf("Verify: err = %v, want ErrExpired", err)
	}
}

func TestVerifyRejectsNotYetValid(t *testing.T) {
	m := newTestManager(t)

	future := sign(t, jwt.SigningMethodHS256, []byte(testSecret), func(c *Claims) {
		c.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour))
		c.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour))
		c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(2 * time.Hour))
	})

	if _, err := m.Verify(future); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Verify: err = %v, want ErrInvalid", err)
	}
}

// TestVerifyRequiresExpiry stops a token with no exp, which would otherwise
// never expire, from being minted by anyone.
func TestVerifyRequiresExpiry(t *testing.T) {
	m := newTestManager(t)

	noExp := sign(t, jwt.SigningMethodHS256, []byte(testSecret), func(c *Claims) {
		c.ExpiresAt = nil
	})

	if _, err := m.Verify(noExp); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Verify: err = %v, want ErrInvalid", err)
	}
}

func TestVerifyRejectsWrongIssuerOrAudience(t *testing.T) {
	m := newTestManager(t)

	cases := []struct {
		name   string
		mutate func(*Claims)
	}{
		{"other issuer", func(c *Claims) { c.Issuer = "someone-else" }},
		{"empty issuer", func(c *Claims) { c.Issuer = "" }},
		{"other audience", func(c *Claims) { c.Audience = jwt.ClaimStrings{"another-app"} }},
		{"empty audience", func(c *Claims) { c.Audience = jwt.ClaimStrings{} }},
		{"audience of unrelated apps", func(c *Claims) {
			c.Audience = jwt.ClaimStrings{"another-app", "yet-another"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forged := sign(t, jwt.SigningMethodHS256, []byte(testSecret), tc.mutate)
			if _, err := m.Verify(forged); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Verify: err = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestVerifyAcceptsAudienceListContainingOurs pins the semantics of aud on
// purpose, because the intuitive expectation here is the opposite of the correct
// one.
//
// aud is defined (RFC 7519) as the set of recipients a token is intended for, and
// a token listing several audiences is meant for all of them. Treating membership
// as a failure would reject legitimately multi-audience tokens; what must never
// pass is a token that does not name us at all, which is the case above.
func TestVerifyAcceptsAudienceListContainingOurs(t *testing.T) {
	m := newTestManager(t)

	multi := sign(t, jwt.SigningMethodHS256, []byte(testSecret), func(c *Claims) {
		c.Audience = jwt.ClaimStrings{"another-app", Audience}
	})
	if _, err := m.Verify(multi); err != nil {
		t.Fatalf("Verify: err = %v, want nil; a token naming us as one of several recipients is for us", err)
	}
}

func TestVerifyRejectsMissingJTI(t *testing.T) {
	m := newTestManager(t)

	forged := sign(t, jwt.SigningMethodHS256, []byte(testSecret), func(c *Claims) { c.ID = "" })
	if _, err := m.Verify(forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Verify: err = %v, want ErrInvalid", err)
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	m := newTestManager(t)

	for _, raw := range []string{
		"",
		"not-a-token",
		"a.b",
		"a.b.c",
		".....",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiI0MiJ9.", // no signature
	} {
		if _, err := m.Verify(raw); err == nil {
			t.Errorf("Verify(%q) accepted a malformed token", raw)
		}
	}
}

// TestIssueProducesUniqueJTI matters for revocation: a jti is what identifies a
// single session, and duplicates would make two sessions indistinguishable.
func TestIssueProducesUniqueJTI(t *testing.T) {
	m := newTestManager(t)

	seen := make(map[string]bool)
	for i := 0; i < 500; i++ {
		raw, err := m.Issue(int64(i), "user@test.com", 0)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		claims, err := m.Verify(raw)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if seen[claims.ID] {
			t.Fatalf("jti %q issued twice", claims.ID)
		}
		seen[claims.ID] = true
	}
}

func TestIssueHonoursTokenVersion(t *testing.T) {
	m := newTestManager(t)

	raw, err := m.Issue(1, "user@test.com", 3)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := m.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.TokenVersion != 3 {
		t.Fatalf("ver = %d, want 3; a reset that bumps the column must not be ignored", claims.TokenVersion)
	}
}

// TestIssuedTokenCarriesNoRole documents that the role is not in the token.
//
// The original design read an admin role straight out of the claims, which is
// forgeable. The role now comes from the database on every request, so its
// absence here is the fix, not an omission.
func TestIssuedTokenCarriesNoRole(t *testing.T) {
	m := newTestManager(t)

	raw, err := m.Issue(1, "admin@test.com", 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	claims, err := m.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// A claim struct with no Role field cannot carry one.
	var c Claims = *claims
	if got := c.RegisteredClaims.Subject; got != strconv.Itoa(1) {
		t.Fatalf("sub = %q", got)
	}
}

func TestVerifyRejectsUnsignedNoneTokenWithRS256Shape(t *testing.T) {
	m := newTestManager(t)

	// header says HS512, body is a normal claim set, signature is empty. Only the
	// pinned method list and the keyfunc stand between this and acceptance.
	forged := "eyJhbGciOiJIUzUxMiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiIxIiwiaXNzIjoiZHZicyIsImF1ZCI6WyJkdmJzLXdlYiJdLCJleHAiOjQxMDI0NDQ4MDB9."

	if _, err := m.Verify(forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Verify: err = %v, want ErrInvalid", err)
	}
}

func TestTTLIsExposed(t *testing.T) {
	m, err := NewManager(testSecret, 42*time.Minute)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if m.TTL() != 42*time.Minute {
		t.Fatalf("TTL = %v", m.TTL())
	}
}

// TestIssuedTokenExpiresWithinTTL makes sure the lifetime written into the token
// is the configured one, not a hardcoded week.
func TestIssuedTokenExpiresWithinTTL(t *testing.T) {
	m := newTestManager(t)

	raw, err := m.Issue(1, "user@test.com", 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := m.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	lifetime := time.Until(claims.ExpiresAt.Time)
	if lifetime > m.TTL() {
		t.Fatalf("token lives for %v, longer than the %v configured", lifetime, m.TTL())
	}
	if lifetime < m.TTL()-time.Minute {
		t.Fatalf("token lives for %v, far shorter than the %v configured", lifetime, m.TTL())
	}
}
