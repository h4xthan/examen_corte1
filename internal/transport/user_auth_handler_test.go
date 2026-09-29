package transport_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signToken mints an HS256 token with the test server's own signing key.
//
// It exists so tests can construct tokens the server should reject, or accept,
// for specific reasons. It is not a way for a test to become an administrator:
// the key here is the test suite's own, and AdminOnly reads the role from the
// database regardless of what the claims say.
func signToken(t *testing.T, claims map[string]any) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims)).
		SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// signTokenAlgNone produces a token whose header declares "alg":"none" and which
// carries no signature at all.
//
// jwt.UnsafeAllowNoneSignatureType is the library's documented way to build one,
// and it is only usable by code that asks for it by name. Producing such a token
// here is the only way to prove the verifier rejects it: putting "alg":"none" in
// the claims body would just be an ordinary signed token with a stray field.
func signTokenAlgNone(t *testing.T, claims map[string]any) string {
	t.Helper()

	signed, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims(claims)).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign alg=none token: %v", err)
	}
	return signed
}

func nowPlus(seconds int64) int64 {
	return time.Now().Add(time.Duration(seconds) * time.Second).Unix()
}

// registerUser creates an account through the public API.
//
// The role argument is kept in the signature so tests read the same way, but it
// is asserted rather than honoured: registration no longer accepts a role, and a
// test that passes one is testing mass assignment.
func registerUser(t *testing.T, email, password, role string) (int64, string) {
	t.Helper()

	body := map[string]any{
		"first_name": "Test",
		"last_name":  "User",
		"email":      email,
		"password":   password,
	}
	if role != "" {
		body["role"] = role
	}

	resp, out := doJSON(t, http.MethodPost, "/auth/register", body)
	if role != "" {
		// The request carried a role, so the expected outcome is a rejection,
		// not a created account.
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("register %s with role=%q: status %d, want 400", email, role, resp.StatusCode)
		}
		return 0, ""
	}

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: status %d, want 201 (body %v)", email, resp.StatusCode, out)
	}
	user, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("register %s: body is %T, want an object", email, out)
	}
	id := idFrom(t, user, "id")
	if id == 0 {
		t.Fatalf("register %s: response has no usable id: %v", email, user)
	}
	createdRole, _ := user["role"].(string)
	return id, createdRole
}

// loginAndGetToken signs in and returns the session token.
//
// The token is read from the session cookie the server sets, not from a response
// body field, so the test drives the same path a browser does. Returning it as
// a string keeps the dozens of existing call sites working: doJSONAuth sends it
// as a bearer credential, which the server accepts for non-browser clients.
func loginAndGetToken(t *testing.T, email, password string) string {
	t.Helper()

	resp, out := doJSON(t, http.MethodPost, "/auth/login", map[string]any{
		"email":    email,
		"password": password,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d, want 200 (body %v)", email, resp.StatusCode, out)
	}

	for _, c := range resp.Cookies() {
		if c.Name == "dvbs_session" && c.Value != "" {
			return c.Value
		}
	}
	t.Fatal("login: no dvbs_session cookie was set")
	return ""
}

func TestRegisterLoginFlow(t *testing.T) {
	email := uniqueTestEmail()

	id, role := registerUser(t, email, "secret-1234-long", "")
	if id == 0 {
		t.Fatal("register: expected generated id")
	}
	if role != "customer" {
		t.Fatalf("register: default role %q, want customer", role)
	}

	token := loginAndGetToken(t, email, "secret-1234-long")

	// The account is confirmed by reading the caller's own profile. The global
	// listing is admin-only now, so it is not available as a check here.
	respUsers, usersOut := doJSONAuth(t, http.MethodGet,
		"/users/"+strconv.FormatInt(id, 10), token, nil)
	if respUsers.StatusCode != http.StatusOK {
		t.Fatalf("GET own profile: status %d, want 200", respUsers.StatusCode)
	}
	found := false
	if usersOut.(map[string]any)["email"] == email {
		found = true
	}
	if !found {
		t.Fatalf("GET /users: registered user %s not listed", email)
	}
}

// TestRegisterMassAssignmentRoleAdmin is the regression for #1.
//
// The original assertion was that a body with "role":"admin" produced an admin.
// registerUser now treats any role argument as a request that must be refused,
// so reaching an account with role=admin here means the vulnerability is back.
func TestRegisterMassAssignmentRoleAdmin(t *testing.T) {
	email := uniqueTestEmail()

	id, role := registerUser(t, email, "secret-1234-long", "admin")
	if id != 0 || role != "" {
		t.Fatalf("register with role=admin created id=%d role=%q; the field must not be settable",
			id, role)
	}

	// The account must be genuinely absent, not merely demoted.
	resp, _ := doJSON(t, http.MethodPost, "/auth/login", map[string]any{
		"email":    email,
		"password": "secret-1234-long",
	})
	if resp.StatusCode == http.StatusOK {
		t.Fatal("an account exists after a registration carrying role=admin")
	}
}

// TestLoginDoesNotEnumerateUsers is the regression for the login half of #4.
//
// A missing account and a wrong password must be indistinguishable, or the
// endpoint becomes a way to test whether an address is registered.
func TestLoginDoesNotEnumerateUsers(t *testing.T) {
	known := uniqueTestEmail()
	registerUser(t, known, "correct-horse-battery", "")
	unknown := uniqueTestEmail()

	respKnown, bodyKnown := doJSON(t, http.MethodPost, "/auth/login", map[string]any{
		"email":    known,
		"password": "wrong-password-here",
	})
	respUnknown, bodyUnknown := doJSON(t, http.MethodPost, "/auth/login", map[string]any{
		"email":    unknown,
		"password": "wrong-password-here",
	})

	if respKnown.StatusCode != respUnknown.StatusCode {
		t.Fatalf("status differs: existing account %d, unknown account %d",
			respKnown.StatusCode, respUnknown.StatusCode)
	}
	if respKnown.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", respKnown.StatusCode)
	}

	knownMsg, _ := bodyKnown.(map[string]any)["error"].(string)
	unknownMsg, _ := bodyUnknown.(map[string]any)["error"].(string)
	if knownMsg != unknownMsg {
		t.Fatalf("error text differs and reveals existence:\n existing: %q\n unknown: %q",
			knownMsg, unknownMsg)
	}
}

func TestUsersRequireToken(t *testing.T) {
	resp, _ := doJSON(t, http.MethodGet, "/users", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /users without token: status %d, want 401", resp.StatusCode)
	}
}

// TestIDORAnyTokenReadsAnyProfile is the regression for #3.
//
// The original assertion was that a token belonging to one account can read
// another account's profile, email included. It passed, because the handler read
// whatever id was in the path. Now a non-owner gets 404.
func TestIDORAnyTokenReadsAnyProfile(t *testing.T) {
	victimEmail := uniqueTestEmail()
	attackerEmail := uniqueTestEmail()

	victimID, _ := registerUser(t, victimEmail, "victim-password-1", "")
	registerUser(t, attackerEmail, "attackerpass", "")
	attackerToken := loginAndGetToken(t, attackerEmail, "attackerpass")

	resp, _ := doJSONAuth(t, http.MethodGet, "/users/"+strconv.FormatInt(victimID, 10), attackerToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker GET victim profile: status %d, want 404", resp.StatusCode)
	}

	// The owner can still read their own.
	own, _ := doJSONAuth(t, http.MethodGet, "/users/"+strconv.FormatInt(victimID, 10),
		loginAndGetToken(t, victimEmail, "victim-password-1"), nil)
	if own.StatusCode != http.StatusOK {
		t.Fatalf("owner GET own profile: status %d, want 200", own.StatusCode)
	}
}

// TestAnyTokenUpdatesAndDeletesAnyUser is the write half of #3, and the
// privilege-escalation chain that came with it: the body asked for
// role=admin and an email the attacker controlled, so one request turned a
// victim into an attacker and then a way in.
func TestAnyTokenUpdatesAndDeletesAnyUser(t *testing.T) {
	victimEmail := uniqueTestEmail()
	attackerEmail := uniqueTestEmail()

	victimID, _ := registerUser(t, victimEmail, "victim-password-1", "")
	_, _ = registerUser(t, attackerEmail, "attackerpass", "")
	attackerToken := loginAndGetToken(t, attackerEmail, "attackerpass")
	victimPath := "/users/" + strconv.FormatInt(victimID, 10)

	// role and password are not in the update DTO at all, so the request is
	// rejected outright rather than silently ignored.
	escalate := map[string]any{
		"id":       victimID,
		"role":     "admin",
		"email":    attackerEmail + "-hijacked.com",
		"password": "ignored",
	}
	resp, _ := doJSONAuth(t, http.MethodPut, victimPath, attackerToken, escalate)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker PUT victim: status %d, want 404", resp.StatusCode)
	}

	respDel, _ := doJSONAuth(t, http.MethodDelete, victimPath, attackerToken, nil)
	if respDel.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker DELETE victim: status %d, want 404", respDel.StatusCode)
	}

	// The victim must be entirely intact: same email, still able to log in.
	respGet, body := doJSONAuth(t, http.MethodGet, victimPath,
		loginAndGetToken(t, victimEmail, "victim-password-1"), nil)
	if respGet.StatusCode != http.StatusOK {
		t.Fatalf("victim profile is gone: status %d", respGet.StatusCode)
	}
	profile, _ := body.(map[string]any)
	if profile["email"] != victimEmail {
		t.Fatalf("victim email was changed to %v, want %v", profile["email"], victimEmail)
	}
	if profile["role"] == "admin" {
		t.Fatal("the victim was escalated to admin")
	}
}

// TestUserUpdateIgnoresRoleAndBalance pins the DTO. Even the legitimate owner
// cannot promote themselves, because role is not an updatable field at all.
func TestUserUpdateIgnoresRoleAndBalance(t *testing.T) {
	email := uniqueTestEmail()
	userID, _ := registerUser(t, email, "owner-password-1", "")
	token := loginAndGetToken(t, email, "owner-password-1")

	resp, out := doJSONAuth(t, http.MethodPut, "/users/"+strconv.FormatInt(userID, 10), token,
		map[string]any{"first_name": "New", "last_name": "Name", "role": "admin", "balance_cents": 999999})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update with role/balance: status %d, want 400 (fields are not in the DTO)", resp.StatusCode)
	}
	if out != nil {
		if m, _ := out.(map[string]any); m != nil {
			if m["balance_cents"] != nil {
				t.Fatalf("response carried a balance: %v", m["balance_cents"])
			}
		}
	}

	// A legitimate update still works.
	resp, out = doJSONAuth(t, http.MethodPut, "/users/"+strconv.FormatInt(userID, 10), token,
		map[string]any{"first_name": "New", "last_name": "Name"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legitimate update: status %d, want 200", resp.StatusCode)
	}
	if m, _ := out.(map[string]any); m != nil && m["first_name"] != "New" {
		t.Fatalf("first_name = %v, want New", m["first_name"])
	}
}

// TestGlobalUserListingIsAdminOnly covers #5 on the users table.
func TestGlobalUserListingIsAdminOnly(t *testing.T) {
	email := uniqueTestEmail()
	registerUser(t, email, "customer-password-1", "")
	customerToken := loginAndGetToken(t, email, "customer-password-1")

	resp, _ := doJSONAuth(t, http.MethodGet, "/users", customerToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("customer GET /users: status %d, want 403", resp.StatusCode)
	}

	// And anonymously, with no cookie and no credential at all.
	anon, _ := doAnon(t, http.MethodGet, "/users", nil)
	if anon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /users: status %d, want 401", anon.StatusCode)
	}
}
