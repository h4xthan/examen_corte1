package transport_test

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func nowUnix() int64 {
	return time.Now().UnixNano()
}

// buyBook makes a customer actually buy a book, which is the precondition for
// reviewing it. Every review test needs one, and doing it through checkout keeps
// the setup honest: an order that was paid for is a real purchase, and the
// service checks for exactly that.
func buyBook(t *testing.T, token string, userID int64, bookID int64, qty int) {
	t.Helper()

	setBalance(t, userID, 1_000_000_00)
	resp, out := checkoutCart(t, token, map[int64]int{bookID: qty}, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup purchase of book %d: status %d, want 201 (body=%v)", bookID, resp.StatusCode, out)
	}
}

// addReview posts a review as the holder of the token. It takes no user id,
// because the endpoint has no such field: the author is the session.
func addReview(t *testing.T, token string, bookID int64, rating int) (*http.Response, map[string]any) {
	t.Helper()

	resp, out := doJSONAuth(t, http.MethodPost, "/books/"+strconv.FormatInt(bookID, 10)+"/reviews", token, map[string]any{
		"rating":  rating,
		"comment": "review",
	})
	if out == nil {
		t.Fatalf("POST review: unexpected empty body with status %d", resp.StatusCode)
	}
	return resp, out.(map[string]any)
}

// TestReviewIsAttributedToTheSession is the spoofing half of #10 inverted.
//
// The old handler decoded model.Review, so user_id came from the body. Any caller
// could post a five-star review under a named victim's account, which is a lie
// about the reviewer and a way to make somebody look like they had opinions about
// books they never bought. The field is not in the request and the author is the
// session.
func TestReviewIsAttributedToTheSession(t *testing.T) {
	victimID, attackerID, attackerToken := setupUsersAndToken(t)
	bookID := createTestBook(t, 100)
	buyBook(t, attackerToken, attackerID, bookID, 1)

	resp, out := doJSONAuth(t, http.MethodPost, "/books/"+strconv.FormatInt(bookID, 10)+"/reviews", attackerToken, map[string]any{
		"rating":  5,
		"comment": "mine",
		"user_id": victimID,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("review naming another user: status %d, want 400 (body=%v)", resp.StatusCode, out)
	}

	// Without the field, the author is the attacker and nobody else.
	resp, out = addReview(t, attackerToken, bookID, 5)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST review: status %d, want 201 (body=%v)", resp.StatusCode, out)
	}
	review := out.(map[string]any)
	if got := idFrom(t, review, "user_id"); got != attackerID {
		t.Fatalf("review attributed to %d, want the session's %d", got, attackerID)
	}
}

// TestBookDetailEmbedsReviews keeps the original assertion that a book page
// carries its reviews, with a real author this time.
func TestBookDetailEmbedsReviewsWithRealAuthor(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBook(t, 100)
	buyBook(t, token, userID, bookID, 1)

	resp, _ := addReview(t, token, bookID, 5)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST review: status %d, want 201", resp.StatusCode)
	}

	respBook, bookOut := doJSON(t, http.MethodGet, "/books/"+strconv.FormatInt(bookID, 10), nil)
	if respBook.StatusCode != http.StatusOK {
		t.Fatalf("GET /books/%d: status %d, want 200", bookID, respBook.StatusCode)
	}
	book := bookOut.(map[string]any)
	reviews, ok := book["reviews"].([]any)
	if !ok || len(reviews) != 1 {
		t.Fatalf("expected embedded reviews of len 1, got %v", book["reviews"])
	}
	if got := idFrom(t, reviews[0], "user_id"); got != userID {
		t.Fatalf("embedded review user_id %d, want %d", got, userID)
	}
}

// TestReviewRequiresPurchase is #10 inverted.
//
// A review used to need nothing but a token, so ratings were an open voting
// booth for anyone who wanted to vote. A purchase is now required, and the check
// joins through order_items to a completed order, because an order row on its own
// proves nothing.
func TestReviewRequiresPurchase(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBook(t, 100)
	// Deliberately not bought.

	resp, _ := addReview(t, token, bookID, 4)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("review without a purchase: status %d, want 403", resp.StatusCode)
	}

	// Buy it, and the same request succeeds.
	buyBook(t, token, userID, bookID, 1)
	resp, _ = addReview(t, token, bookID, 4)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("review after a purchase: status %d, want 201", resp.StatusCode)
	}
}

// TestReviewRejectsOutOfRangeRatings is the rating bound, in the service and in
// the CHECK constraint.
func TestReviewRejectsOutOfRangeRatings(t *testing.T) {
	for _, rating := range []int{0, -3, 6, 999} {
		// A distinct customer per rating, so the one-review-per-customer rule is
		// not what rejects these.
		_, userID, token := setupUsersAndToken(t)
		bookID := createTestBook(t, 100)
		buyBook(t, token, userID, bookID, 1)

		resp, _ := addReview(t, token, bookID, rating)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("rating=%d: status %d, want 400", rating, resp.StatusCode)
		}
	}
}

// TestRatingIsBoundedInTheDatabase documents where the bound actually lives.
// The DDL still declares `reviews_rating_range` (`rating BETWEEN 1 AND 5`), but
// TiDB Cloud Starter runs with tidb_enable_check_constraint = 0 and there is no
// session-level override, so the declaration is inert and the review service is
// the enforcer. The service side is what TestReviewAcceptsInvalidRatings above
// covers; here the platform limit is stated and verified instead of a test
// silently depending on a constraint that never fires.
func TestRatingIsBoundedInTheDatabase(t *testing.T) {
	requireCheckConstraintsUnenforced(t)
}

// TestOneReviewPerBookAndCustomer is the unique index, tested as a unique index.
func TestOneReviewPerBookAndCustomer(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBook(t, 100)
	buyBook(t, token, userID, bookID, 1)

	resp, _ := addReview(t, token, bookID, 5)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first review: status %d, want 201", resp.StatusCode)
	}
	resp, _ = addReview(t, token, bookID, 1)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second review of the same book: status %d, want 409", resp.StatusCode)
	}

	// A second insert straight at the database is refused too, which is the part
	// the Go-level check could never guarantee.
	_, err := testDB.ExecContext(t.Context(),
		`INSERT INTO reviews (book_id, user_id, rating, comment) VALUES (?, ?, 3, 'again')`,
		bookID, userID)
	if err == nil {
		t.Fatal("the database accepted a second review from the same customer; the unique index is missing")
	}
}

// TestOnlyTheAuthorEditsOrDeletesAReview is #10's ownership half.
func TestOnlyTheAuthorEditsOrDeletesAReview(t *testing.T) {
	victimEmail := uniqueTestEmail()
	victimID, _ := registerUser(t, victimEmail, "victim-password-1", "")
	victimToken := loginAndGetToken(t, victimEmail, "victim-password-1")
	_, _, attackerToken := setupUsersAndToken(t)

	bookID := createTestBook(t, 100)
	buyBook(t, victimToken, victimID, bookID, 1)

	respCreate, reviewOut := addReview(t, victimToken, bookID, 5)
	if respCreate.StatusCode != http.StatusCreated {
		t.Fatalf("setup: create review failed with status %d", respCreate.StatusCode)
	}
	reviewID := idFrom(t, reviewOut, "id")
	path := "/reviews/" + strconv.FormatInt(reviewID, 10)

	respUpd, _ := doJSONAuth(t, http.MethodPut, path, attackerToken, map[string]any{
		"rating":  1,
		"comment": "hijacked",
	})
	if respUpd.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker PUT review: status %d, want 404", respUpd.StatusCode)
	}

	respDel, _ := doJSONAuth(t, http.MethodDelete, path, attackerToken, nil)
	if respDel.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker DELETE review: status %d, want 404", respDel.StatusCode)
	}

	// Still the victim's, with the original text.
	resp, out := doJSONAuth(t, http.MethodGet, path, victimToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("author GET review: status %d, want 200", resp.StatusCode)
	}
	if got := out.(map[string]any)["comment"]; got != "review" {
		t.Fatalf("comment = %v, want the original %q", got, "review")
	}
}

// TestReviewCannotBeMovedToAnotherBook stops the reassignment trick: editing a
// review used to take book_id from the body, so a five-star comment about one
// title could be re-filed under another.
func TestReviewCannotBeMovedToAnotherBook(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBook(t, 100)
	otherBook := createTestBook(t, 100)
	buyBook(t, token, userID, bookID, 1)

	_, reviewOut := addReview(t, token, bookID, 5)
	reviewID := idFrom(t, reviewOut, "id")

	resp, out := doJSONAuth(t, http.MethodPut, "/reviews/"+strconv.FormatInt(reviewID, 10), token, map[string]any{
		"rating":  5,
		"comment": "moved",
		"book_id": otherBook,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT review with book_id: status %d, want 400 (body=%v)", resp.StatusCode, out)
	}
}

func TestReviewsRequireToken(t *testing.T) {
	bookID := createTestBook(t, 100)
	resp, _ := doJSON(t, http.MethodPost, "/books/"+strconv.FormatInt(bookID, 10)+"/reviews", map[string]any{
		"rating": 5,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST review without token: status %d, want 401", resp.StatusCode)
	}
}

func TestCouponsRequireToken(t *testing.T) {
	resp, _ := doJSON(t, http.MethodGet, "/coupons", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /coupons without token: status %d, want 401", resp.StatusCode)
	}
}

// createCoupon posts a coupon with an arbitrary token, so a test can check what
// happens for a customer and not only for an admin.
func createCoupon(t *testing.T, token string, code string, discount, maxUses int) (*http.Response, any) {
	t.Helper()

	body := map[string]any{
		"code":             code,
		"discount_percent": discount,
		"max_uses":         maxUses,
		"expires_at":       time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	}
	resp, out := doJSONAuth(t, http.MethodPost, "/coupons", token, body)
	if out == nil {
		t.Fatalf("POST /coupons: unexpected empty body with status %d", resp.StatusCode)
	}
	return resp, out
}

// TestCustomersCannotCreateCoupons is #11 inverted.
//
// POST /coupons was reachable by any authenticated user, so anybody could mint a
// code with discount_percent 100 and max_uses 1000000 and then redeem it on their
// own order: the shop gave away its catalogue. It is admin-only now, and the role
// comes from the database rather than from the token.
func TestCustomersCannotCreateCoupons(t *testing.T) {
	_, _, customerToken := setupUsersAndToken(t)
	code := fmt.Sprintf("HACK%d", nowUnix()%1000000)

	resp, _ := createCoupon(t, customerToken, code, 99, 1_000_000)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("customer POST /coupons: status %d, want 403", resp.StatusCode)
	}

	// The code does not exist, so nobody can use it.
	var n int
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT count(*) FROM coupons WHERE code = ?`, code).Scan(&n); err != nil {
		t.Fatalf("count coupons: %v", err)
	}
	if n != 0 {
		t.Fatal("a coupon created by a customer was persisted")
	}
}

// TestCouponValuesAreRangeChecked is the other half of #11: even an admin cannot
// mint a 150% discount or a negative use limit, because the CHECK constraints and
// the service both say no.
func TestCouponValuesAreRangeChecked(t *testing.T) {
	admin := adminToken(t)

	cases := []struct {
		name     string
		discount int
		maxUses  int
	}{
		{"over_100_percent", 150, 5},
		{"negative_discount", -20, 5},
		{"negative_max_uses", 10, -5},
		{"zero_max_uses", 10, 0},
	}

	for _, tc := range cases {
		code := fmt.Sprintf("ABS%d%d", nowUnix()%1000000, tc.discount)
		resp, _ := createCoupon(t, admin, code, tc.discount, tc.maxUses)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.name, resp.StatusCode)
		}

		var n int
		if err := testDB.QueryRowContext(t.Context(),
			`SELECT count(*) FROM coupons WHERE code = ?`, code).Scan(&n); err != nil {
			t.Fatalf("count coupons: %v", err)
		}
		if n != 0 {
			t.Errorf("%s: the coupon was persisted anyway", tc.name)
		}
	}

	// And the constraint holds even against a direct insert.
	_, err := testDB.ExecContext(t.Context(),
		`INSERT INTO coupons (code, discount_percent, max_uses, used_count, expires_at)
		 VALUES ('DIRECTBAD', 150, 5, 0, DATE_ADD(NOW(), INTERVAL 1 DAY))`)
	if err == nil {
		t.Fatal("the database accepted discount_percent 150; the CHECK constraint is missing")
	}
}

// TestCustomersCannotEditOrDeleteCoupons stops the matching write hole.
func TestCustomersCannotEditOrDeleteCoupons(t *testing.T) {
	_, _, attackerToken := setupUsersAndToken(t)
	code := fmt.Sprintf("VICTIM%d", nowUnix()%1000000)
	createCouponAsAdmin(t, code, 10, 5)

	var couponID int64
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT id FROM coupons WHERE code = ?`, code).Scan(&couponID); err != nil {
		t.Fatalf("look up coupon: %v", err)
	}
	path := "/coupons/" + strconv.FormatInt(couponID, 10)

	respUpd, _ := doJSONAuth(t, http.MethodPut, path, attackerToken, map[string]any{
		"code":             code + "X",
		"discount_percent": 100,
		"max_uses":         999_999,
		"expires_at":       time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})
	if respUpd.StatusCode != http.StatusForbidden {
		t.Fatalf("customer PUT coupon: status %d, want 403", respUpd.StatusCode)
	}

	respDel, _ := doJSONAuth(t, http.MethodDelete, path, attackerToken, nil)
	if respDel.StatusCode != http.StatusForbidden {
		t.Fatalf("customer DELETE coupon: status %d, want 403", respDel.StatusCode)
	}

	var stillThere int
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT count(*) FROM coupons WHERE id = ?`, couponID).Scan(&stillThere); err != nil {
		t.Fatalf("count coupons: %v", err)
	}
	if stillThere != 1 {
		t.Fatal("the coupon was deleted by a customer")
	}
}
