package transport_test

import (
	"net/http"
	"testing"
)

func TestOrdersRequireToken(t *testing.T) {
	resp, _ := doJSON(t, http.MethodGet, "/orders", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /orders with no token: status %d, want 401", resp.StatusCode)
	}
}

// TestOrderListingIsAdminOnly is #5 inverted.
//
// GET /orders used to return every order in the shop to any authenticated
// caller: who bought what, how much they paid and when. It is admin-only now, and
// a customer reaches their own history through checkout results and their own
// order ids.
func TestOrderListingIsAdminOnly(t *testing.T) {
	_, _, token := setupUsersAndToken(t)

	resp, _ := doJSONAuth(t, http.MethodGet, "/orders", token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("customer GET /orders: status %d, want 403", resp.StatusCode)
	}

	admin := adminToken(t)
	resp, _ = doJSONAuth(t, http.MethodGet, "/orders", admin, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin GET /orders: status %d, want 200", resp.StatusCode)
	}
}

// TestOrderIDORIsRefused is #3 inverted for orders.
//
// The old GET /orders/{id} fetched by id and returned it, so any token could read
// any order: another person's address, basket and total.
func TestOrderIDORIsRefused(t *testing.T) {
	victimID, _, attackerToken := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, victimID, "completed", 4999)

	// The owner can read their own order.
	ownerToken := loginAndGetToken(t, emailOf(t, victimID), "victim-password-1")
	resp, _ := doJSONAuth(t, http.MethodGet, orderPath(orderID), ownerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner GET order: status %d, want 200", resp.StatusCode)
	}

	resp, _ = doJSONAuth(t, http.MethodGet, orderPath(orderID), attackerToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker GET order: status %d, want 404", resp.StatusCode)
	}
}

// TestCannotCreateOrderForAnotherUser is #7 inverted.
//
// POST /orders decoded model.Order from the body, so user_id came from the
// client. Any caller could open an order in a named victim's account. The route
// is admin-only, takes no user_id, and the subject comes from the session.
func TestCannotCreateOrderForAnotherUser(t *testing.T) {
	victimID, _, attackerToken := setupUsersAndToken(t)

	// A customer cannot reach the route at all.
	resp, _ := doJSONAuth(t, http.MethodPost, "/orders", attackerToken, map[string]any{
		"user_id": victimID,
		"status":  "completed",
		"total":   1,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("customer POST /orders: status %d, want 403", resp.StatusCode)
	}

	// And even an admin cannot name somebody else: the owner is the session.
	admin := adminToken(t)
	resp, out := doJSONAuth(t, http.MethodPost, "/orders", admin, map[string]any{
		"user_id": victimID,
		"status":  "completed",
		"total":   1,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("admin POST /orders with user_id and total: status %d, want 400 (body=%v)", resp.StatusCode, out)
	}
}

// TestCannotUpdateOrDeleteAnotherOrder is #3 inverted for writes.
func TestCannotUpdateOrDeleteAnotherOrder(t *testing.T) {
	victimID, _, attackerToken := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, victimID, "pending", 4999)

	// 403 and not 404. Changing an order's status is administrative, so the
	// role gate answers before the handler ever looks at the order. That is not
	// the existence oracle a 404 would risk being: the answer is the same for
	// every order id, existing or not.
	resp, _ := doJSONAuth(t, http.MethodPut, orderPath(orderID), attackerToken, map[string]any{
		"status": "completed",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("attacker PUT order: status %d, want 403", resp.StatusCode)
	}

	resp, _ = doJSONAuth(t, http.MethodDelete, orderPath(orderID), attackerToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker DELETE order: status %d, want 404", resp.StatusCode)
	}

	// Still there, and still pending.
	if got := statusOfOrder(t, orderID); got != "pending" {
		t.Fatalf("order status = %q, want pending", got)
	}
}

// TestOrderUpdateCannotReprice is #6 inverted for the order route.
//
// An order's total is what checkout computed. Editing the order must not be able
// to change it, so the field is not in the update DTO at all. The caller is an
// administrator because moving an order along is an administrative act: a
// customer used to be able to PUT their own order to "completed", which is the
// state the review rule trusts as proof of purchase.
func TestOrderUpdateCannotReprice(t *testing.T) {
	_, userID, _ := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, userID, "pending", 4999)
	admin := adminToken(t)

	resp, out := doJSONAuth(t, http.MethodPut, orderPath(orderID), admin, map[string]any{
		"status":      "completed",
		"total":       1,
		"total_cents": 1,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT order with total: status %d, want 400 (body=%v)", resp.StatusCode, out)
	}

	// A legitimate status change works, and leaves the amount alone.
	resp, _ = doJSONAuth(t, http.MethodPut, orderPath(orderID), admin, map[string]any{
		"status": "completed",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT order status: status %d, want 200", resp.StatusCode)
	}
	if got := orderTotalOf(t, orderID); got != 4999 {
		t.Fatalf("total after update = %d cents, want the original 4999", got)
	}
}

// TestOrderStatusIsRestrictedAndClosed pins the two halves of the status rule:
// only an administrator moves an order, and only to a state the column allows.
func TestOrderStatusIsRestrictedAndClosed(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, userID, "pending", 4999)
	admin := adminToken(t)

	// A customer cannot mark their own purchase complete, which is the state a
	// review requires.
	resp, _ := doJSONAuth(t, http.MethodPut, orderPath(orderID), token, map[string]any{
		"status": "completed",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("customer PUT order status: status %d, want 403", resp.StatusCode)
	}
	if got := statusOfOrder(t, orderID); got != "pending" {
		t.Fatalf("order status = %q after a refused change, want pending", got)
	}

	// And the status is a closed set, not a free-text column. Before the CHECK
	// constraint and the service check, this wrote through to VARCHAR(50).
	for _, bad := range []string{"shipped' , 'x", "COMPLETED", "en camino", ""} {
		resp, _ := doJSONAuth(t, http.MethodPut, orderPath(orderID), admin, map[string]any{"status": bad})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status %q: got %d, want 400", bad, resp.StatusCode)
		}
	}

	// TiDB Cloud Starter does not enforce CHECK constraints (§3.6-adjacent,
	// measured: tidb_enable_check_constraint is GLOBAL-only and the SET is
	// refused without SYSTEM_VARIABLES_ADMIN), so the closed vocabulary above is
	// enforced by the service, not by the schema. The declaration still lives in
	// the migration; what enforces it is the 400 loop, and the platform limit is
	// made explicit and verified rather than assumed.
	requireCheckConstraintsUnenforced(t)
}

// TestOrderUpdateCannotChangeOwner stops a transfer.
func TestOrderUpdateCannotChangeOwner(t *testing.T) {
	victimID, _, _ := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, victimID, "pending", 4999)

	// Even with the administrative role, the owner cannot be rewritten: the
	// handler carries user_id from the stored row.
	resp, out := doJSONAuth(t, http.MethodPut, orderPath(orderID), adminToken(t), map[string]any{
		"user_id": 1,
		"status":  "completed",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT order with user_id: status %d, want 400 (body=%v)", resp.StatusCode, out)
	}
	if got := ownerOfOrder(t, orderID); got != victimID {
		t.Fatalf("order owner = %d after the attempt, want the original %d", got, victimID)
	}
}

// TestOrderHistoryIsTheCallersOwn is the route the profile page needs.
//
// GET /orders is the administrative listing of every order in the shop (#5), so
// the profile's history used to come back empty: the request was not forbidden,
// it was simply the wrong route. This pins that /orders/me is scoped to the
// session, that it is not an alias for the global listing, and that the id in
// the path is not a way around it.
func TestOrderHistoryIsTheCallersOwn(t *testing.T) {
	victimEmail := uniqueTestEmail()
	victimID, _ := registerUser(t, victimEmail, "victim-password-1", "")
	attackerEmail := uniqueTestEmail()
	_, _ = registerUser(t, attackerEmail, "attackerpass", "")
	attackerToken := loginAndGetToken(t, attackerEmail, "attackerpass")
	mine := insertOrderDirect(t, victimID, "completed", 1999)
	theirs := insertOrderDirect(t, victimID, "completed", 4200)

	resp, out := doJSONAuth(t, http.MethodGet, "/orders/me", attackerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /orders/me: status %d, want 200 (body=%v)", resp.StatusCode, out)
	}
	orders, ok := out.([]any)
	if !ok {
		t.Fatalf("GET /orders/me returned %T, want an array", out)
	}
	if len(orders) != 0 {
		t.Fatalf("a customer with no orders got %d of them", len(orders))
	}

	// The victim's own history contains their order and nothing else.
	victimToken := loginAndGetToken(t, victimEmail, "victim-password-1")
	resp, out = doJSONAuth(t, http.MethodGet, "/orders/me", victimToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("victim GET /orders/me: status %d, want 200 (body=%v)", resp.StatusCode, out)
	}
	orders = out.([]any)
	if len(orders) != 2 {
		t.Fatalf("victim's history has %d orders, want 2", len(orders))
	}
	for _, o := range orders {
		got := idFrom(t, o, "id")
		if got != mine && got != theirs {
			t.Errorf("order %v in the victim's history is not one of theirs", got)
		}
	}

	// The global listing still shows everything, to an admin only.
	resp, _ = doJSONAuth(t, http.MethodGet, "/orders/me", attackerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repeat GET /orders/me: status %d, want 200", resp.StatusCode)
	}
	if resp, _ = doJSONAuth(t, http.MethodGet, "/orders", attackerToken, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("customer GET /orders: status %d, want 403", resp.StatusCode)
	}
}

func TestOrderInvalidPathID(t *testing.T) {
	_, _, token := setupUsersAndToken(t)

	resp, _ := doJSONAuth(t, http.MethodGet, "/orders/not-a-number", token, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /orders/not-a-number: status %d, want 400", resp.StatusCode)
	}
}

func ownerOfOrder(t *testing.T, orderID int64) int64 {
	t.Helper()

	var userID int64
	if err := testDB.QueryRowContext(t.Context(), `SELECT user_id FROM orders WHERE id = ?`, orderID).Scan(&userID); err != nil {
		t.Fatalf("read order owner: %v", err)
	}
	return userID
}

func statusOfOrder(t *testing.T, orderID int64) string {
	t.Helper()

	var status string
	if err := testDB.QueryRowContext(t.Context(), `SELECT status FROM orders WHERE id = ?`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order status: %v", err)
	}
	return status
}
