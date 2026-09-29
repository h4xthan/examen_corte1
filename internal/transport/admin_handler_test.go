package transport_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// TestAdminRoutesRejectCustomers is the baseline: a real customer must not reach
// the administrative routes.
func TestAdminRoutesRejectCustomers(t *testing.T) {
	email := uniqueTestEmail()
	registerUser(t, email, "some-password-1", "")
	customerToken := loginAndGetToken(t, email, "some-password-1")

	respStats, _ := doJSONAuth(t, http.MethodGet, "/admin/stats", customerToken, nil)
	if respStats.StatusCode != http.StatusForbidden {
		t.Fatalf("customer GET /admin/stats: status %d, want 403", respStats.StatusCode)
	}

	respUsers, _ := doJSONAuth(t, http.MethodGet, "/admin/users", customerToken, nil)
	if respUsers.StatusCode != http.StatusForbidden {
		t.Fatalf("customer GET /admin/users: status %d, want 403", respUsers.StatusCode)
	}
}

// TestForgedTokenGrantsAdminAccess is the regression for the hardcoded signing
// secret.
//
// The original test asserted a 200, and it passed, because the secret was the
// literal string "dvbs-s3cr3t" and the keyfunc handed that same secret to any
// token regardless of the algorithm it declared. The secret now comes from the
// environment and the verifier pins HS256 and validates iss/aud/exp, so a token
// signed with the old constant is just a bad signature.
//
// The forged token here deliberately claims role=admin and points at a user id
// that does not exist, which is the exact shape the original exploit used.
func TestForgedTokenGrantsAdminAccess(t *testing.T) {
	forged := signToken(t, map[string]any{
		"sub":   "999999",
		"email": "forged@evil.com",
		"role":  "admin",
		"iss":   "dvbs",
		"aud":   "dvbs-web",
		"exp":   nowPlus(3600),
		"jti":   "forged",
	})

	respStats, _ := doJSONAuth(t, http.MethodGet, "/admin/stats", forged, nil)
	if respStats.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged admin GET /admin/stats: status %d, want 401", respStats.StatusCode)
	}

	respUsers, _ := doJSONAuth(t, http.MethodGet, "/admin/users", forged, nil)
	if respUsers.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged admin GET /admin/users: status %d, want 401", respUsers.StatusCode)
	}
}

// TestAlgNoneTokenRejected covers the sibling forgery the old keyfunc allowed: a
// token that declares "alg":"none" and carries no signature at all.
func TestAlgNoneTokenRejected(t *testing.T) {
	forged := signTokenAlgNone(t, map[string]any{
		"sub":  "1",
		"role": "admin",
		"exp":  nowPlus(3600),
	})

	resp, _ := doJSONAuth(t, http.MethodGet, "/admin/stats", forged, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("alg=none GET /admin/stats: status %d, want 401", resp.StatusCode)
	}
}

// TestTokenFromAnotherIssuerRejected covers a token minted by a different
// service that happens to share the signing key. iss and aud exist so a key is
// not a global passport.
func TestTokenFromAnotherIssuerRejected(t *testing.T) {
	forged := signToken(t, map[string]any{
		"sub":  "1",
		"role": "admin",
		"iss":  "some-other-service",
		"aud":  "dvbs-web",
		"exp":  nowPlus(3600),
		"jti":  "wrong-iss",
	})

	resp, _ := doJSONAuth(t, http.MethodGet, "/admin/stats", forged, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-issuer GET /admin/stats: status %d, want 401", resp.StatusCode)
	}
}

func TestTokenWithWrongAudienceRejected(t *testing.T) {
	forged := signToken(t, map[string]any{
		"sub":  "1",
		"role": "admin",
		"iss":  "dvbs",
		"aud":  "another-web-app",
		"exp":  nowPlus(3600),
		"jti":  "wrong-aud",
	})

	resp, _ := doJSONAuth(t, http.MethodGet, "/admin/stats", forged, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-audience GET /admin/stats: status %d, want 401", resp.StatusCode)
	}
}

// TestSelfRegisteredAdminAccessesAdminPanel is the regression for mass assignment
// during registration (#1). RegisterRequest no longer has a Role field at all,
// and decodeJSON rejects unknown fields, so a body carrying "role" is refused
// outright rather than quietly ignored.
func TestSelfRegisteredAdminAccessesAdminPanel(t *testing.T) {
	email := uniqueTestEmail()

	body := map[string]any{
		"first_name": "Test",
		"last_name":  "User",
		"email":      email,
		"password":   "ownpass12345",
		"role":       "admin",
	}
	resp, _ := doJSON(t, http.MethodPost, "/auth/register", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("register with role=admin: status %d, want 400 (role is not a settable field)",
			resp.StatusCode)
	}

	// And the account must not exist as a result of the rejected request.
	loginResp, _ := doJSON(t, http.MethodPost, "/auth/login", map[string]any{
		"email":    email,
		"password": "ownpass12345",
	})
	if loginResp.StatusCode == http.StatusOK {
		t.Fatal("a registration carrying role=admin created an account anyway")
	}
}

// TestRoleComesFromDatabaseNotToken is the regression for the admin gate trusting
// the role claim (#16). A token minted with the real secret and a genuine
// subject still does not grant admin, because the role is read from the users
// row rather than from the token.
func TestRoleComesFromDatabaseNotToken(t *testing.T) {
	email := uniqueTestEmail()
	customerID, _ := registerUser(t, email, "some-password-1", "")
	customerToken := loginAndGetToken(t, email, "some-password-1")

	// Sanity: the customer really does have a valid session. Reading their own
	// profile is a route a customer is allowed to call; GET /users is not.
	if resp, _ := doJSONAuth(t, http.MethodGet,
		"/users/"+strconv.FormatInt(customerID, 10), customerToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("customer session should be valid, got %d", resp.StatusCode)
	}

	// A token that claims admin for that same real user. This is only forgeable
	// by someone holding the signing key; the point is that even a validly
	// signed role claim is not what the gate consults.
	escalated := signToken(t, map[string]any{
		"sub":  strconv.FormatInt(customerID, 10),
		"role": "admin",
		"iss":  "dvbs",
		"aud":  "dvbs-web",
		"exp":  nowPlus(3600),
		"jti":  "escalation",
		"ver":  0,
	})

	resp, _ := doJSONAuth(t, http.MethodGet, "/admin/stats", escalated, nil)
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("token with role=admin claim: status %d, want 401 or 403", resp.StatusCode)
	}
}

// TestAdminStatsCountRevenueNotVolume pins the aggregate queries.
//
// The panel used to load every row of every table to take len() of it, and
// Stats summed whatever total the customer typed on the order. The counts are
// now COUNT(*) in the database, which is cheap and cannot drift from reality.
//
// The revenue figure earned its own bug: CountOrders and SumOrderTotals were
// appended to orders.sql badly enough that sqlc bound both names to
// `SELECT count(*) FROM orders`, so the dashboard was rendering the order count
// formatted as money, and the second statement sat below the last query where
// sqlc ignored it. This test fails if the two ever collapse into one again.
func TestAdminStatsCountRevenueNotVolume(t *testing.T) {
	admin := adminToken(t)

	_, userID, _ := setupUsersAndToken(t)

	before := adminStats(t, admin)
	beforeOrders := statNumber(t, before, "orders")
	beforeRevenue := statNumber(t, before, "total_order_value_cents")

	// Two orders with different fates and different amounts. The revenue figure
	// has to move by exactly one of them.
	shipped := insertOrderDirect(t, userID, "shipped", 7000)
	insertOrderDirect(t, userID, "pending", 9999)
	insertOrderDirect(t, userID, "cancelled", 5555)
	insertOrderDirect(t, userID, "refunded", 4444)

	after := adminStats(t, admin)
	afterOrders := statNumber(t, after, "orders")
	afterRevenue := statNumber(t, after, "total_order_value_cents")

	if delta := afterOrders - beforeOrders; delta != 4 {
		t.Errorf("orders count moved by %d, want 4: the aggregate is not counting rows", delta)
	}
	if delta := afterRevenue - beforeRevenue; delta != 7000 {
		t.Errorf("revenue moved by %d cents, want 7000: pending, cancelled and refunded orders are not money taken",
			delta)
	}

	// A cancelled order refunds the customer, so the money leaves the figure
	// again rather than staying in it forever.
	if _, err := testDB.ExecContext(t.Context(),
		`UPDATE orders SET status = 'cancelled' WHERE id = ?`, shipped); err != nil {
		t.Fatalf("cancel the shipped order: %v", err)
	}
	cancelledRevenue := statNumber(t, adminStats(t, admin), "total_order_value_cents")
	if delta := afterRevenue - cancelledRevenue; delta != 7000 {
		t.Errorf("revenue fell by %d cents after a cancellation, want 7000", delta)
	}
}

func adminStats(t *testing.T, token string) map[string]any {
	t.Helper()

	resp, out := doJSONAuth(t, http.MethodGet, "/admin/stats", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/stats: status %d, want 200 (body=%v)", resp.StatusCode, out)
	}
	stats, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("GET /admin/stats returned %T, want an object", out)
	}
	return stats
}

// statNumber reads a figure out of the stats object. The harness decodes JSON
// into map[string]any, so an integer arrives as a float64.
func statNumber(t *testing.T, stats map[string]any, key string) int64 {
	t.Helper()

	switch v := stats[key].(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			t.Fatalf("stat %q is %v, not an integer", key, v)
		}
		return n
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("stat %q = %q, not an integer", key, v)
		}
		return n
	default:
		t.Fatalf("stat %q is %T, want a number", key, v)
		return 0
	}
}

// TestAdminCanChangeARole pins the role endpoint, and the two ways it refuses.
func TestAdminCanChangeARole(t *testing.T) {
	email := uniqueTestEmail()
	userID, _ := registerUser(t, email, "some-password-1", "")
	token := loginAndGetToken(t, email, "some-password-1")
	admin := adminToken(t)

	// A customer cannot promote themselves, and cannot promote anyone else.
	for _, path := range []string{"/admin/users/" + strconv.FormatInt(userID, 10) + "/role"} {
		resp, _ := doJSONAuth(t, http.MethodPut, path, token, map[string]any{"role": "admin"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("customer PUT %s: status %d, want 403", path, resp.StatusCode)
		}
	}
	if roleOfUser(t, userID) != "customer" {
		t.Fatal("the customer was promoted to admin by their own request")
	}

	// An unknown role is refused rather than stored.
	resp, _ := doJSONAuth(t, http.MethodPut,
		"/admin/users/"+strconv.FormatInt(userID, 10)+"/role", admin, map[string]any{"role": "superuser"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT role=superuser: status %d, want 400", resp.StatusCode)
	}

	// An administrator demoting themselves is refused. It is the one change
	// that can lock the last admin out of the panel, and the mistake is one
	// click away in a dropdown.
	resp, _ = doJSONAuth(t, http.MethodPut,
		"/admin/users/"+strconv.FormatInt(userID, 10)+"/role", admin, map[string]any{"role": "staff"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT role=staff: status %d, want 400", resp.StatusCode)
	}

	// The change lands, and it takes effect on the very next request: the old
	// token still carries role=customer, and access is decided by the column.
	resp, _ = doJSONAuth(t, http.MethodPut,
		"/admin/users/"+strconv.FormatInt(userID, 10)+"/role", admin, map[string]any{"role": "admin"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT role=admin: status %d, want 200", resp.StatusCode)
	}
	if got := roleOfUser(t, userID); got != "admin" {
		t.Fatalf("role in the database = %q, want admin", got)
	}

	stats, _ := doJSONAuth(t, http.MethodGet, "/admin/stats", token, nil)
	if stats.StatusCode != http.StatusOK {
		t.Errorf("the promoted user's pre-existing token GET /admin/stats: status %d, want 200", stats.StatusCode)
	}

	// And the reverse: demotion revokes access without waiting for the token to
	// expire.
	resp, _ = doJSONAuth(t, http.MethodPut,
		"/admin/users/"+strconv.FormatInt(userID, 10)+"/role", admin, map[string]any{"role": "customer"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("demoting back: status %d, want 200", resp.StatusCode)
	}
	stats, _ = doJSONAuth(t, http.MethodGet, "/admin/stats", token, nil)
	if stats.StatusCode != http.StatusForbidden {
		t.Errorf("the demoted user's existing token GET /admin/stats: status %d, want 403", stats.StatusCode)
	}
}

func roleOfUser(t *testing.T, userID int64) string {
	t.Helper()

	var role string
	if err := testDB.QueryRowContext(t.Context(), `SELECT role FROM users WHERE id = ?`, userID).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}
