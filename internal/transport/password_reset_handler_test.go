package transport_test

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func forgotPassword(t *testing.T, email string) (*http.Response, map[string]any) {
	t.Helper()

	resp, out := doJSON(t, http.MethodPost, "/auth/forgot-password", map[string]any{"email": email})
	if out == nil {
		t.Fatalf("forgot-password %s: unexpected empty body with status %d", email, resp.StatusCode)
	}
	return resp, out.(map[string]any)
}

func resetPassword(t *testing.T, code, newPassword string) *http.Response {
	t.Helper()

	resp, _ := doJSON(t, http.MethodPost, "/auth/reset-password", map[string]any{
		"token":        code,
		"new_password": newPassword,
	})
	return resp
}

// resetCodePattern matches the token the mailer was asked to deliver.
//
// It is deliberately not "six digits": the old code was a 6-digit value from
// math/rand, and a test that still looked for six digits would keep asserting
// the shape of the vulnerability. The token is now 32 bytes of crypto/rand in
// base64url.
var resetCodePattern = regexp.MustCompile(`[A-Za-z0-9_-]{40,}`)

// latestCodeFromMail returns the reset code from the mail the service actually
// sent, read in-process. It used to come from GET /inbox, an endpoint readable
// by any authenticated token, which was the last link of the account-takeover
// chain.
func latestCodeFromMail(t *testing.T, email string) string {
	t.Helper()

	messages := testMail.SentTo(email)
	if len(messages) == 0 {
		t.Fatalf("no email was sent to %s", email)
	}
	body := messages[len(messages)-1].Body
	code := resetCodePattern.FindString(body)
	if code == "" {
		t.Fatalf("no reset token found in email body: %q", body)
	}
	return code
}

func TestForgotPasswordFlowWorks(t *testing.T) {
	email := uniqueTestEmail()
	registerUser(t, email, "old-password-1", "")

	resp, out := forgotPassword(t, email)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forgot-password: status %d, want 200", resp.StatusCode)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "correo") {
		t.Fatalf("expected a message pointing at the mailbox, got %v", out)
	}

	if len(testMail.SentTo(email)) != 1 {
		t.Fatalf("expected exactly 1 email, got %d", len(testMail.SentTo(email)))
	}

	code := latestCodeFromMail(t, email)
	if resp := resetPassword(t, code, "new-password-22"); resp.StatusCode != http.StatusOK {
		t.Fatalf("reset-password: status %d, want 200", resp.StatusCode)
	}

	if token := loginAndGetToken(t, email, "new-password-22"); token == "" {
		t.Fatal("login with the new password failed")
	}
}

// TestResetTokenIsNotSixDigits is the regression for the weak random source.
//
// The original code was fmt.Sprintf("%06d", rand.Intn(1000000)) from math/rand:
// one million values from a deterministic sequence, and the endpoint had no rate
// limit, so a reset could be brute-forced in minutes. The token is now 32 bytes
// from crypto/rand, and TestResetTokenIsUnguessable pins the size.
func TestResetTokenIsNotSixDigits(t *testing.T) {
	email := uniqueTestEmail()
	registerUser(t, email, "old-password-1", "")

	forgotPassword(t, email)
	code := latestCodeFromMail(t, email)

	if sixDigitsOnly(code) {
		t.Fatalf("reset token %q is six digits; the weak generator is back", code)
	}
	if len(code) < 40 {
		t.Fatalf("reset token %q is only %d characters, want at least 40", code, len(code))
	}
}

func sixDigitsOnly(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// TestResetTokensAreUnique makes sure two consecutive requests do not produce the
// same token, which a predictable generator would eventually do.
func TestResetTokensAreUnique(t *testing.T) {
	email := uniqueTestEmail()
	registerUser(t, email, "old-password-1", "")

	seen := make(map[string]bool)
	for i := 0; i < 5; i++ {
		forgotPassword(t, email)
		code := latestCodeFromMail(t, email)
		if seen[code] {
			t.Fatalf("reset token %q was issued twice", code)
		}
		seen[code] = true
	}
}

// TestExpiredTokenIsRejected covers the case the original tests asserted the
// opposite of: expiry was written to the column and never read.
func TestExpiredTokenIsRejected(t *testing.T) {
	email := uniqueTestEmail()
	registerUser(t, email, "old-password-1", "")

	forgotPassword(t, email)
	code := latestCodeFromMail(t, email)

	// Expire it directly rather than waiting out the TTL.
	expireResetToken(t, email, code)

	resp := resetPassword(t, code, "new-password-33")
	if resp.StatusCode == http.StatusOK {
		t.Fatal("an expired reset token was accepted; expires_at is not being checked")
	}

	// And the password must not have changed.
	if token := loginAndGetToken(t, email, "old-password-1"); token == "" {
		t.Fatal("the old password stopped working after a rejected reset")
	}
}

// TestTokenReusableAfterReset is the regression for single use. Redemption is a
// single atomic UPDATE, so a second attempt matches zero rows.
func TestTokenReusableAfterReset(t *testing.T) {
	email := uniqueTestEmail()
	registerUser(t, email, "old-password-1", "")
	forgotPassword(t, email)
	code := latestCodeFromMail(t, email)

	if resp := resetPassword(t, code, "new-password-44"); resp.StatusCode != http.StatusOK {
		t.Fatalf("first reset: status %d, want 200", resp.StatusCode)
	}

	resp := resetPassword(t, code, "another-password-5")
	if resp.StatusCode == http.StatusOK {
		t.Fatal("the same reset token was accepted twice")
	}
}

// TestMultipleTokensAllValid becomes TestNewRequestRevokesPrevious: asking for a
// new code must invalidate the one the user already has, because that is exactly
// when they expect the old one to stop working.
func TestNewRequestRevokesPrevious(t *testing.T) {
	email := uniqueTestEmail()
	userID, _ := registerUser(t, email, "old-password-1", "")

	forgotPassword(t, email)
	first := latestCodeFromMail(t, email)

	forgotPassword(t, email)
	second := latestCodeFromMail(t, email)

	if first == second {
		t.Fatal("two requests produced the same token")
	}

	if resp := resetPassword(t, first, "new-password-66"); resp.StatusCode == http.StatusOK {
		t.Fatal("the superseded token still works; requesting a new code must revoke the old one")
	}

	if resp := resetPassword(t, second, "new-password-66"); resp.StatusCode != http.StatusOK {
		t.Fatalf("the current token was rejected: status %d, want 200", resp.StatusCode)
	}

	// Only one live token should remain attributable to this user.
	live := 0
	tokens, err := testTokenStore.GetAllPasswordResetTokens(t.Context())
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	for _, tok := range tokens {
		if tok.UserID == userID && tok.Usable(time.Now()) {
			live++
		}
	}
	if live > 1 {
		t.Fatalf("%d live reset tokens for one user, want at most 1", live)
	}
}

// TestForgotPasswordEnumeratesEmails is the regression for #4 on this endpoint.
// Both cases must look identical from outside.
func TestForgotPasswordEnumeratesEmails(t *testing.T) {
	email := uniqueTestEmail()
	registerUser(t, email, "some-password-1", "")

	respUnknown, bodyUnknown := forgotPassword(t, "unknown_"+uniqueTestEmail())
	respKnown, bodyKnown := forgotPassword(t, email)

	if respUnknown.StatusCode != respKnown.StatusCode {
		t.Fatalf("status reveals existence: unknown %d, known %d",
			respUnknown.StatusCode, respKnown.StatusCode)
	}
	if respKnown.StatusCode != http.StatusOK {
		t.Fatalf("known email: status %d, want 200", respKnown.StatusCode)
	}

	unknownMsg, _ := bodyUnknown["message"].(string)
	knownMsg, _ := bodyKnown["message"].(string)
	if unknownMsg != knownMsg {
		t.Fatalf("body reveals existence:\n unknown: %q\n known:   %q", unknownMsg, knownMsg)
	}
}

// TestOldJWTValidAfterReset becomes TestResetRevokesLiveSessions: a user who
// resets their password because they think it was stolen needs the other party's
// session to stop working immediately, not at token expiry.
func TestResetRevokesLiveSessions(t *testing.T) {
	email := uniqueTestEmail()
	userID, _ := registerUser(t, email, "before-reset-1", "")
	oldToken := loginAndGetToken(t, email, "before-reset-1")

	// The session works to begin with.
	if resp, _ := doJSONAuth(t, http.MethodGet,
		"/users/"+strconv.FormatInt(userID, 10), oldToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("setup: session should be valid, got %d", resp.StatusCode)
	}

	forgotPassword(t, email)
	if resp := resetPassword(t, latestCodeFromMail(t, email), "after-reset-22"); resp.StatusCode != http.StatusOK {
		t.Fatalf("reset: status %d, want 200", resp.StatusCode)
	}

	resp, _ := doJSONAuth(t, http.MethodGet,
		"/users/"+strconv.FormatInt(userID, 10), oldToken, nil)
	if resp.StatusCode == http.StatusOK {
		t.Fatal("the pre-reset session still works; token_version is not being checked")
	}
}

// TestResetCodeIsNotReadableOverHTTP is the first link of the old takeover chain
// that is closed. The original chain was: hijack the victim's email (IDOR),
// request a reset for it, read the code from GET /inbox, reset the password,
// then log in as the victim. The inbox was a world-readable mailbox, so any
// authenticated token could complete the chain in three requests.
//
// Asserting the route is gone is the regression: restoring a readable mailbox,
// in any form, re-opens the whole chain. The remaining links (the IDOR that
// sets the email, and the token reuse/expiry flaws) are pinned separately in
// their own phase.
func TestResetCodeIsNotReadableOverHTTP(t *testing.T) {
	victimEmail := uniqueTestEmail()
	registerUser(t, victimEmail, "victim-pass-1", "")
	attackerEmail := uniqueTestEmail()
	registerUser(t, attackerEmail, "attacker-pw-1", "")
	attackerToken := loginAndGetToken(t, attackerEmail, "attacker-pw-1")

	// Trigger a real reset so that a message genuinely exists to leak.
	respForgot, _ := forgotPassword(t, victimEmail)
	if respForgot.StatusCode != http.StatusOK {
		t.Fatalf("forgot-password: status %d, want 200", respForgot.StatusCode)
	}

	for _, path := range []string{"/inbox", "/emails", "/mail", "/auth/inbox"} {
		t.Run(path, func(t *testing.T) {
			resp, _ := doJSONAuth(t, http.MethodGet, path, attackerToken, nil)
			if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("GET %s returned %d; a readable mailbox must not exist", path, resp.StatusCode)
			}
		})
	}

	// An unauthenticated caller must not find it either.
	respAnon, _ := doJSON(t, http.MethodGet, "/inbox", nil)
	if respAnon.StatusCode != http.StatusNotFound && respAnon.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("anonymous GET /inbox returned %d, want 404", respAnon.StatusCode)
	}

	// Sanity check: the code did leave the system, so the assertions above are
	// rejecting a real delivery and not passing because nothing was sent.
	if len(testMail.SentTo(victimEmail)) == 0 {
		t.Fatal("no reset mail was sent; the test is not exercising a real leak")
	}
}

// TestResetIsAtomicUnderConcurrentRedemption covers the transaction the reset
// moved into, from the outside.
//
// Consume, write the hash and bump token_version were three statements. The
// failure that matters is a partial one: the token spent, the password changed,
// and the old sessions still valid — which is exactly what a victim who resets
// because they believe the account was taken is trying to prevent. Wrapping
// them in a transaction means the observable rule is simpler and testable: of
// any number of simultaneous redemptions with the same code, exactly one sets
// the password and invalidates the sessions, and the rest are told the code is
// not valid.
func TestResetIsAtomicUnderConcurrentRedemption(t *testing.T) {
	email := uniqueTestEmail()
	registerUser(t, email, "old-password-1", "")
	forgotPassword(t, email)
	code := latestCodeFromMail(t, email)

	const attempts = 8
	var wg sync.WaitGroup
	statuses := make([]int, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = resetPassword(t, code, "new-password-9").StatusCode
		}(i)
	}
	wg.Wait()

	wins := 0
	for i, s := range statuses {
		switch s {
		case http.StatusOK:
			wins++
		case http.StatusBadRequest, http.StatusUnauthorized:
			// The loser of the race is told the code is not valid, which is the
			// right answer: from the second request onwards it genuinely is not.
		default:
			t.Errorf("attempt %d: status %d, want 200 or 400", i, s)
			t.Logf("first statuses: %v", statuses)
		}
	}
	if wins != 1 {
		t.Errorf("%d of %d simultaneous redemptions of the same code succeeded, want exactly 1", wins, attempts)
	}

	// The password changed, and the old one does not work. If the transaction
	// had rolled back the hash but not the token, this is where it would show.
	if resp, _ := doJSON(t, http.MethodPost, "/auth/login", map[string]any{
		"email": email, "password": "new-password-9",
	}); resp.StatusCode != http.StatusOK {
		t.Errorf("login with the new password: status %d, want 200", resp.StatusCode)
	}
	if resp, _ := doJSON(t, http.MethodPost, "/auth/login", map[string]any{
		"email": email, "password": "old-password-1",
	}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("login with the old password: status %d, want 401", resp.StatusCode)
	}
}
