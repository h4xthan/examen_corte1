package transport_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// preview asks what a code is worth for a cart, without spending it.
func preview(t *testing.T, token, code string, subtotalCents int64) (int, map[string]any) {
	t.Helper()

	resp, out := doJSONAuth(t, http.MethodPost, "/coupons/code", token, map[string]any{
		"code":           code,
		"subtotal_cents": subtotalCents,
	})
	if out == nil {
		t.Fatalf("POST /coupons/code: empty body with status %d", resp.StatusCode)
	}
	return resp.StatusCode, out.(map[string]any)
}

// firePreview is preview for the concurrency tests, which cannot share a
// *testing.T across goroutines for the recorded helper.
func firePreview(token, code string, subtotalCents int64) (int, map[string]any) {
	body, _ := json.Marshal(map[string]any{"code": code, "subtotal_cents": subtotalCents})
	req, _ := http.NewRequest(http.MethodPost, testServer.URL+"/coupons/code", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1, nil
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

// TestCouponRaceDoesNotCompound is #14 inverted.
//
// The old POST /coupons/code spent the coupon, wrote to a process-local map and
// slept 75ms between its check and its write, all to widen a race on purpose.
// Ten parallel requests therefore each subtracted 20% from a running total, and
// the checkout then charged the compounded figure: 800 became about 280.
//
// The endpoint is now a quote. It spends nothing, holds no state, and cannot
// compound, because there is no running total to compound onto — the discount is
// computed from the cart the server is looking at. The actual spending happens in
// checkout, once, inside a transaction, guarded by a unique index.
//
// Ten parallel previews of the same code all say the same thing, and only one
// checkout ever consumes the use.
func TestCouponRaceDoesNotCompound(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	code := fmt.Sprintf("CMP%d", nowUnix()%1000000)
	createCouponAsAdmin(t, code, 20, 1)

	const subtotalCents int64 = 800_00
	const discountCents int64 = subtotalCents / 5 // 20%

	// Ten parallel quotes, all from one account.
	const attackers = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan map[string]any, attackers)
	for i := 0; i < attackers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, body := firePreview(token, code, subtotalCents)
			results <- body
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	// Every quote is the same figure. A compounding endpoint would return a
	// different, lower one each time, and that is exactly what the old test
	// asserted.
	quotes := 0
	for body := range results {
		if body["status"] != "active" {
			t.Fatalf("quote status %v, want active", body["status"])
		}
		if got := int64(body["discount_cents"].(float64)); got != discountCents {
			t.Fatalf("discount_cents = %d, want %d on every quote", got, discountCents)
		}
		quotes++
	}
	if quotes != attackers {
		t.Fatalf("got %d quotes, want %d", quotes, attackers)
	}

	// A quote spends nothing, so used_count is still zero and the balance is
	// untouched.
	if used := couponUsedCount(t, code); used != 0 {
		t.Fatalf("used_count = %d after quoting only, want 0", used)
	}

	// The single purchase that follows charges the discounted price once.
	bookID := createTestBookWithStock(t, subtotalCents, 10)
	setBalance(t, userID, 10_000_00)
	resp, out := checkoutCart(t, token, map[int64]int{bookID: 1}, code)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /checkout: status %d, want 201 (body=%v)", resp.StatusCode, out)
	}
	order := out["order"].(map[string]any)
	if got := int64(order["discount_cents"].(float64)); got != discountCents {
		t.Fatalf("order discount_cents = %d, want %d", got, discountCents)
	}
	wantTotal := subtotalCents - discountCents
	if got := int64(order["total_cents"].(float64)); got != wantTotal {
		t.Fatalf("order total_cents = %d, want %d", got, wantTotal)
	}
	// The old bug produced a total near 280 from a subtotal of 800. Anything at
	// or below 20% of the subtotal means the discount compounded again.
	if total := int64(order["total_cents"].(float64)); total < subtotalCents/2 {
		t.Fatalf("total_cents = %d, which is below a single 20%% discount; the discount compounded", total)
	}
}

// TestCouponIsRedeemableOncePerCustomer is the per-customer rule.
//
// A code with max_uses high enough for everybody is still one use per customer,
// enforced by the unique index on (coupon_id, user_id). The old "remove applied"
// endpoint let a customer drop the pending discount and redeem again, which is
// why it existed: it undid the only record of the spend.
func TestCouponIsRedeemableOncePerCustomer(t *testing.T) {
	userA, _, tokenA := setupUsersAndToken(t)
	code := fmt.Sprintf("PRU%d", nowUnix()%1000000)
	createCouponAsAdmin(t, code, 20, 50)

	bookA := createTestBookWithStock(t, 1000, 10)
	setBalance(t, userA, 10_000_00)
	respA, _ := checkoutCart(t, tokenA, map[int64]int{bookA: 1}, code)
	if respA.StatusCode != http.StatusCreated {
		t.Fatalf("user A checkout: status %d, want 201", respA.StatusCode)
	}

	// A different customer may use the same code: the limit is per customer as
	// well as global.
	emailB := uniqueTestEmail()
	userB, _ := registerUser(t, emailB, "bpass-long-enough", "")
	tokenB := loginAndGetToken(t, emailB, "bpass-long-enough")
	bookB := createTestBookWithStock(t, 1000, 10)
	setBalance(t, userB, 10_000_00)
	respB, _ := checkoutCart(t, tokenB, map[int64]int{bookB: 1}, code)
	if respB.StatusCode != http.StatusCreated {
		t.Fatalf("user B checkout: status %d, want 201 (a different customer may use the code)", respB.StatusCode)
	}

	// But user A cannot. "Remove applied" is a no-op now and must not reopen it.
	respDel, _ := doJSONAuth(t, http.MethodDelete, "/coupons/applied", tokenA, nil)
	if respDel.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /coupons/applied: status %d, want 204", respDel.StatusCode)
	}

	bookA2 := createTestBookWithStock(t, 1000, 10)
	respAgain, _ := checkoutCart(t, tokenA, map[int64]int{bookA2: 1}, code)
	if respAgain.StatusCode != http.StatusBadRequest {
		t.Fatalf("user A second checkout with the same code: status %d, want 400", respAgain.StatusCode)
	}
	if n := couponRedemptionCount(t, code); n != 2 {
		t.Fatalf("redemption rows = %d, want 2 (one per customer)", n)
	}
}

// TestCouponPreviewReportsExhaustion keeps the ordinary single-use answer.
func TestCouponPreviewReportsExhaustion(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	code := fmt.Sprintf("SEQ%d", nowUnix()%1000000)
	createCouponAsAdmin(t, code, 20, 1)

	bookID := createTestBookWithStock(t, 1000, 10)
	setBalance(t, userID, 10_000_00)

	_, first := preview(t, token, code, 1000)
	if first["status"] != "active" {
		t.Fatalf("first quote status %v, want active", first["status"])
	}

	resp, _ := checkoutCart(t, token, map[int64]int{bookID: 1}, code)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("checkout: status %d, want 201", resp.StatusCode)
	}

	_, second := preview(t, token, code, 1000)
	if second["status"] != "invalid" {
		t.Fatalf("quote after the only use was spent: status %v, want invalid", second["status"])
	}
}

// TestCreditBlocksPurchaseButHonoursOneDiscount is #14's other half inverted.
//
// The old test showed a customer with 1000 cents of credit buying a 5000-cent
// book, because ten parallel redemptions had compounded the price down to about
// 536. The compounding is gone, so a single legitimate discount is the only way
// to get under the limit, and it has to actually be a discount the server
// computed.
func TestCreditBlocksPurchaseButHonoursOneDiscount(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	code := fmt.Sprintf("WAL%d", nowUnix()%1000000)
	createCouponAsAdmin(t, code, 20, 10)

	// A 5000-cent book against a 1000-cent balance.
	bookID := createTestBookWithStock(t, 5000, 10)
	setBalance(t, userID, 1000)

	resp, _ := checkoutCart(t, token, map[int64]int{bookID: 1}, "")
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("checkout without a coupon: status %d, want 402", resp.StatusCode)
	}

	// One real 20% discount brings it to 4000, still above 1000. Ten of them
	// would not have: with compounding the old test got under the limit, and
	// without it nobody can.
	resp, _ = checkoutCart(t, token, map[int64]int{bookID: 1}, code)
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("checkout with one 20%% discount: status %d, want 402 (4000 cents is still over a 1000 balance)", resp.StatusCode)
	}

	if used := couponUsedCount(t, code); used != 0 {
		t.Fatalf("used_count = %d after two rejected checkouts, want 0", used)
	}

	// A discount big enough does go through, and it is spent exactly once.
	bigCode := fmt.Sprintf("HALF%d", nowUnix()%1000000)
	createCouponAsAdmin(t, bigCode, 80, 10)
	resp, out := checkoutCart(t, token, map[int64]int{bookID: 1}, bigCode)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("checkout with an 80%% discount: status %d, want 201 (body=%v)", resp.StatusCode, out)
	}
	order := out["order"].(map[string]any)
	if got, want := int64(order["total_cents"].(float64)), int64(1000); got != want { //nolint
		t.Fatalf("total_cents = %d, want %d", got, want)
	}
	if used := couponUsedCount(t, bigCode); used != 1 {
		t.Fatalf("used_count = %d, want 1", used)
	}
	if got := balanceOf(t, userID); got != 0 {
		t.Fatalf("balance = %d, want exactly 0", got)
	}
}

// TestCheckoutForbidsAQuantityThatInflatesThePrice covers the quantity bound on
// the checkout path, which the old code never checked at all.
func TestCheckoutForbidsAQuantityThatInflatesThePrice(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 1000, 1_000_000)
	setBalance(t, userID, 10_000_00)

	for _, qty := range []int{0, -5, 11, 1_000_000} {
		resp, _ := checkoutCart(t, token, map[int64]int{bookID: qty}, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("quantity %d: status %d, want 400", qty, resp.StatusCode)
		}
	}
	if got := stockOf(t, bookID); got != 1_000_000 {
		t.Fatalf("stock = %d after rejected quantities, want unchanged", got)
	}
}
