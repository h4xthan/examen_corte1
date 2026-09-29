package transport_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// checkoutCart buys the given books for the holder of the token.
//
// The body carries only books, quantities and an optional coupon. It used to
// carry user_id, status and total as well, and those three fields are what made
// #6, #7 and #14 possible: the client named the account, named the sum, and the
// discount compounded onto whatever it had sent.
func checkoutCart(t *testing.T, token string, items map[int64]int, couponCode string) (*http.Response, map[string]any) {
	t.Helper()

	lines := make([]map[string]any, 0, len(items))
	for bookID, qty := range items {
		lines = append(lines, map[string]any{"book_id": strconv.FormatInt(bookID, 10), "quantity": qty})
	}
	body := map[string]any{"items": lines}
	if couponCode != "" {
		body["coupon_code"] = couponCode
	}

	resp, out := doJSONAuth(t, http.MethodPost, "/checkout", token, body)
	if out == nil {
		t.Fatalf("POST /checkout: unexpected empty body with status %d", resp.StatusCode)
	}
	return resp, out.(map[string]any)
}

// TestCheckoutComputesTheTotal is #6 inverted.
//
// The old endpoint read `total` from the body and recorded that. A client could
// buy a 19.99 book for 1 cent, and the order row would say so forever. There is
// no total in the request any more: the server multiplies the catalogue price by
// the quantity and the response carries integer cents.
func TestCheckoutComputesTheTotal(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 1999, 10)
	setBalance(t, userID, 100_000)

	resp, out := checkoutCart(t, token, map[int64]int{bookID: 3}, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /checkout: status %d, want 201 (body=%v)", resp.StatusCode, out)
	}

	order := out["order"].(map[string]any)
	if got := int64(order["subtotal_cents"].(float64)); got != 1999*3 {
		t.Fatalf("subtotal_cents = %d, want %d", got, 1999*3)
	}
	if got := int64(order["total_cents"].(float64)); got != 1999*3 {
		t.Fatalf("total_cents = %d, want %d", got, 1999*3)
	}
	if _, present := order["total"]; present {
		t.Fatal("the response still carries a float `total`; money must be integer cents")
	}

	// The balance was debited the same amount, to the cent.
	if got, want := balanceOf(t, userID), int64(100_000-1999*3); got != want {
		t.Fatalf("balance = %d, want %d", got, want)
	}
}

// TestCheckoutRejectsClientSuppliedTotal is the direct assertion: a body with a
// total in it is refused outright rather than quietly ignored, because a 201 with
// a silently dropped field is a response that looks like it was honoured.
func TestCheckoutRejectsClientSuppliedTotal(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 1999, 10)
	setBalance(t, userID, 100_000)

	resp, out := doJSONAuth(t, http.MethodPost, "/checkout", token, map[string]any{
		"items": []map[string]any{{"book_id": strconv.FormatInt(bookID, 10), "quantity": 1}},
		"total": 1,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("checkout with a total: status %d, want 400 (body=%v)", resp.StatusCode, out)
	}
	if got := stockOf(t, bookID); got != 10 {
		t.Fatalf("stock = %d after a rejected body, want 10", got)
	}
}

// TestCheckoutIgnoresUserIDInBody is #7 inverted.
//
// The old request had a user_id field and it was honoured, so a caller could
// spend another customer's balance by name. The subject comes from the verified
// session, and a body that names somebody else is rejected.
func TestCheckoutIgnoresUserIDInBody(t *testing.T) {
	victimID, _, attackerToken := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 1999, 10)
	setBalance(t, victimID, 100_000)

	resp, out := doJSONAuth(t, http.MethodPost, "/checkout", attackerToken, map[string]any{
		"user_id": victimID,
		"items":   []map[string]any{{"book_id": strconv.FormatInt(bookID, 10), "quantity": 1}},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("checkout naming another user: status %d, want 400 (body=%v)", resp.StatusCode, out)
	}
	if got := balanceOf(t, victimID); got != 100_000 {
		t.Fatalf("victim balance = %d, want it untouched at 100000", got)
	}
}

// TestCheckoutAppliesDiscountOnce is #14 inverted for the sequential case.
//
// The coupon is applied to the subtotal the server just computed, and the
// resulting discount is written to the order. The old code subtracted from a
// client-supplied running total and kept the remainder in a process-local map,
// so a second application discounted what the first had already discounted.
func TestCheckoutAppliesDiscountOnce(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 1999, 10)
	code := fmt.Sprintf("ONCE%d", nowUnix()%1000000)
	createCouponAsAdmin(t, code, 50, 10)
	setBalance(t, userID, 100_000)

	resp, out := checkoutCart(t, token, map[int64]int{bookID: 2}, code)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /checkout: status %d, want 201 (body=%v)", resp.StatusCode, out)
	}

	order := out["order"].(map[string]any)
	subtotal := int64(1999 * 2)
	wantDiscount := subtotal / 2

	if got := int64(order["subtotal_cents"].(float64)); got != subtotal {
		t.Fatalf("subtotal_cents = %d, want %d", got, subtotal)
	}
	if got := int64(order["discount_cents"].(float64)); got != wantDiscount {
		t.Fatalf("discount_cents = %d, want %d", got, wantDiscount)
	}
	if got := int64(order["total_cents"].(float64)); got != subtotal-wantDiscount {
		t.Fatalf("total_cents = %d, want %d", got, subtotal-wantDiscount)
	}
	// 50% of 3998 is 1999, so the order is 1999 cents. A compounded second
	// application would have made it 0 or near it.
	if got := int64(order["total_cents"].(float64)); got != 1999 {
		t.Fatalf("total_cents = %d, want 1999", got)
	}

	if used := couponUsedCount(t, code); used != 1 {
		t.Fatalf("used_count = %d, want 1", used)
	}
	if n := couponRedemptionCount(t, code); n != 1 {
		t.Fatalf("redemption rows = %d, want 1", n)
	}

	// The same customer cannot use it again: the unique index refuses.
	resp, _ = checkoutCart(t, token, map[int64]int{bookID: 1}, code)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("second redemption by the same customer: status %d, want 400", resp.StatusCode)
	}
	if used := couponUsedCount(t, code); used != 1 {
		t.Fatalf("used_count after the rejected second attempt = %d, want 1", used)
	}
}

// TestCheckoutIgnoresExpiredCoupon is the old bug inverted: the expiry used to
// never be checked at all.
func TestCheckoutIgnoresExpiredCoupon(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 1999, 10)
	setBalance(t, userID, 100_000)

	code := fmt.Sprintf("EXPIRED%d", nowUnix()%1000000)
	// Inserted directly because the create endpoint validates and there is no
	// reason to be able to mint an already-expired code through the API.
	if err := insertCouponDirect(t, code, 80, 5, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("seed expired coupon: %v", err)
	}

	resp, _ := checkoutCart(t, token, map[int64]int{bookID: 1}, code)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("checkout with an expired coupon: status %d, want 400", resp.StatusCode)
	}
	if used := couponUsedCount(t, code); used != 0 {
		t.Fatalf("used_count = %d, want 0 for an expired coupon", used)
	}
}

func TestCheckoutUnknownCoupon(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 1999, 10)
	setBalance(t, userID, 100_000)

	resp, _ := checkoutCart(t, token, map[int64]int{bookID: 1}, "NOEXISTE")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("checkout with an unknown coupon: status %d, want 400", resp.StatusCode)
	}
}

// TestCouponExhaustedRejectedSequentially keeps the ordinary single-use rule.
func TestCouponExhaustedRejectedSequentially(t *testing.T) {
	code := fmt.Sprintf("ONE%d", nowUnix()%1000000)
	createCouponAsAdmin(t, code, 50, 1)

	// Two different customers, so the per-customer rule is not what stops them.
	for i := 0; i < 2; i++ {
		_, userID, token := setupUsersAndToken(t)
		bookID := createTestBookWithStock(t, 1999, 10)
		setBalance(t, userID, 100_000)

		resp, _ := checkoutCart(t, token, map[int64]int{bookID: 1}, code)
		if i == 0 && resp.StatusCode != http.StatusCreated {
			t.Fatalf("first redemption: status %d, want 201", resp.StatusCode)
		}
		if i == 1 && resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("second redemption by another customer: status %d, want 400", resp.StatusCode)
		}
	}
	if used := couponUsedCount(t, code); used != 1 {
		t.Fatalf("used_count = %d, want 1", used)
	}
}

// TestCouponMaxUsesHoldsUnderConcurrency is #14 inverted.
//
// This test used to assert that the race *reproduced*: that at least two of
// twenty concurrent redemptions of a max_uses=1 coupon succeeded, and it failed
// if the bug stopped happening. It now asserts the opposite — exactly one wins —
// which is the only assertion that is worth keeping.
//
// The mechanism is the conditional UPDATE: `WHERE used_count < max_uses` is part
// of the statement, so the losers update zero rows and abandon the transaction,
// rolling back their stock decrements and their debits with it.
func TestCouponMaxUsesHoldsUnderConcurrency(t *testing.T) {
	code := fmt.Sprintf("RACE%d", nowUnix()%1000000)
	createCouponAsAdmin(t, code, 90, 1)

	const attackers = 20

	// Every attacker needs its own account: a coupon is redeemable once per
	// customer, so reusing one account would test that rule instead of max_uses.
	tokens := make([]string, attackers)
	for i := range tokens {
		_, userID, token := setupUsersAndToken(t)
		bookID := createTestBookWithStock(t, 1999, 100)
		setBalance(t, userID, 100_000)
		lines, _ := json.Marshal(map[string]any{
			"items":       []map[string]any{{"book_id": strconv.FormatInt(bookID, 10), "quantity": 1}},
			"coupon_code": code,
		})
		tokens[i] = token
		_ = lines
	}

	// One shared book would serialise on its stock row and hide the coupon race,
	// so each attacker gets its own copy of the same title.
	bookIDs := make([]int64, attackers)
	for i := range bookIDs {
		bookIDs[i] = createTestBookWithStock(t, 1999, 100)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan int, attackers)

	for i := 0; i < attackers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			body, _ := json.Marshal(map[string]any{
				"items":       []map[string]any{{"book_id": strconv.FormatInt(bookIDs[i], 10), "quantity": 1}},
				"coupon_code": code,
			})
			req, err := http.NewRequest(http.MethodPost, testServer.URL+"/checkout", bytes.NewReader(body))
			if err != nil {
				results <- -1
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+tokens[i])
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results <- -1
				return
			}
			defer resp.Body.Close()
			results <- resp.StatusCode
		}(i)
	}

	close(start)
	wg.Wait()
	close(results)

	successes := 0
	for status := range results {
		if status == http.StatusCreated {
			successes++
		}
	}

	if successes != 1 {
		t.Fatalf("%d of %d concurrent redemptions of a max_uses=1 coupon succeeded, want exactly 1", successes, attackers)
	}
	if used := couponUsedCount(t, code); used != 1 {
		t.Fatalf("used_count = %d, want 1", used)
	}
	if n := couponRedemptionCount(t, code); n != 1 {
		t.Fatalf("redemption rows = %d, want 1", n)
	}
}

// TestBalanceCannotGoNegative is #18 inverted.
//
// The old debit subtracted unconditionally after a balance check performed in Go,
// so two concurrent checkouts both saw enough money and both spent it, leaving a
// negative balance. The debit is now a single statement with the affordability
// test in its WHERE clause, and the loser gets 402.
func TestBalanceCannotGoNegative(t *testing.T) {
	const attempts = 10

	// One account, one balance, enough for exactly one of the books below. Each
	// goroutine uses a different book so nothing serialises on a stock row and
	// the balance really is the only thing under contention.
	_, userID, token := setupUsersAndToken(t)
	bookIDs := make([]int64, attempts)
	for i := range bookIDs {
		bookIDs[i] = createTestBookWithStock(t, 1000, 100)
	}
	setBalance(t, userID, 1000)

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan int, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			resp, _ := checkoutCart(t, token, map[int64]int{bookIDs[i]: 1}, "")
			results <- resp.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	created := 0
	for status := range results {
		if status == http.StatusCreated {
			created++
		}
	}

	if created != 1 {
		t.Fatalf("%d of %d concurrent checkouts succeeded against a balance of 1000 cents, want exactly 1", created, attempts)
	}
	if got := balanceOf(t, userID); got != 0 {
		t.Fatalf("balance = %d cents, want exactly 0 and never negative", got)
	}
}

// TestFailedCheckoutRollsBackEverything is the transaction test.
//
// A checkout that cannot be paid for must leave no trace: no order, no stock
// decrement, no coupon spent. Before there were any transactions in the
// repository, the order was created first and the balance debited second, so an
// insufficient balance left an unpaid order behind.
func TestFailedCheckoutRollsBackEverything(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 5000, 10)
	code := fmt.Sprintf("ROLL%d", nowUnix()%1000000)
	createCouponAsAdmin(t, code, 10, 10)

	// No money at all, so the debit fails and the transaction unwinds. The
	// default balance is 1000, which is less than the 5000 book, but pinning it
	// to zero makes the intent obvious.
	setBalance(t, userID, 0)
	resp, _ := checkoutCart(t, token, map[int64]int{bookID: 1}, code)
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("checkout with no credit: status %d, want 402", resp.StatusCode)
	}

	if got := stockOf(t, bookID); got != 10 {
		t.Fatalf("stock = %d after a failed checkout, want the original 10", got)
	}
	if used := couponUsedCount(t, code); used != 0 {
		t.Fatalf("used_count = %d after a failed checkout, want 0", used)
	}
	if n := couponRedemptionCount(t, code); n != 0 {
		t.Fatalf("redemption rows = %d after a failed checkout, want 0", n)
	}
	if got := balanceOf(t, userID); got != 0 {
		t.Fatalf("balance = %d, want 0", got)
	}
}

// TestCheckoutOutOfStockLeavesNothing makes the same point for the other failure
// mode: the stock reservation happens before the order exists, and it has to be
// undone with everything else.
func TestCheckoutOutOfStockLeavesNothing(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 5000, 1)
	setBalance(t, userID, 100_000)

	resp, _ := checkoutCart(t, token, map[int64]int{bookID: 3}, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("checkout for 3 with stock 1: status %d, want 409", resp.StatusCode)
	}
	if got := stockOf(t, bookID); got != 1 {
		t.Fatalf("stock = %d after the failed checkout, want 1", got)
	}
	if got := balanceOf(t, userID); got != 100_000 {
		t.Fatalf("balance = %d, want it untouched at 100000", got)
	}
}

func TestCheckoutRequiresToken(t *testing.T) {
	resp, _ := doJSON(t, http.MethodPost, "/checkout", map[string]any{
		"items": []map[string]any{{"book_id": 1, "quantity": 1}},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /checkout with no token: status %d, want 401", resp.StatusCode)
	}
}
