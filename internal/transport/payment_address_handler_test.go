package transport_test

import (
	"net/http"
	"strconv"
	"testing"
)

func addCard(t *testing.T, token string, ownerID int64) (*http.Response, map[string]any) {
	t.Helper()

	// 4111111111111111 is a well-known test number that passes the Luhn check.
	// There is no cvv field: the endpoint rejects it, because the application
	// must not store one and pretending to accept it would mislead the client.
	body := map[string]any{
		"card_number":  "4111111111111111",
		"card_holder":  "Owner Name",
		"expiry_month": 12,
		"expiry_year":  2028,
	}
	path := "/users/" + strconv.FormatInt(ownerID, 10) + "/payment-methods"
	resp, out := doJSONAuth(t, http.MethodPost, path, token, body)
	if out == nil {
		t.Fatalf("POST card: unexpected empty body with status %d", resp.StatusCode)
	}
	return resp, out.(map[string]any)
}

// TestPaymentDataExposedInResponses is the regression for #12.
//
// The original assertion was that reading a card back returned the full 16-digit
// number and the CVV in clear, and it passed. PCI DSS forbids storing the CVV
// after authorisation at all, so the column is gone rather than masked, and the
// response is built to expose only what a receipt needs.
func TestPaymentDataExposedInResponses(t *testing.T) {
	// The token belongs to the second account setupUsersAndToken creates, so the
	// cards have to be added under that same id. Pairing the token with the other
	// account's id is exactly the mistake the ownership check is there to catch.
	_, userID, token := setupUsersAndToken(t)

	respCard, card := addCard(t, token, userID)
	if respCard.StatusCode != http.StatusCreated {
		t.Fatalf("POST card: status %d, want 201", respCard.StatusCode)
	}

	cardID := idFrom(t, card, "id")
	respGet, gotOut := doJSONAuth(t, http.MethodGet, "/payment-methods/"+strconv.FormatInt(cardID, 10), token, nil)
	if respGet.StatusCode != http.StatusOK {
		t.Fatalf("GET card: status %d, want 200", respGet.StatusCode)
	}
	got := gotOut.(map[string]any)

	for _, field := range []string{"card_number", "cvv", "pan"} {
		if v, present := got[field]; present && v != nil && v != "" {
			t.Fatalf("response exposed %s = %v; it must not be returned at all", field, v)
		}
	}

	// The last four digits are the one part a receipt legitimately shows, and only
	// when the caller stored one.
	if last4, ok := got["last4"].(string); ok && last4 != "" && len(last4) != 4 {
		t.Fatalf("last4 = %q, want exactly 4 digits", last4)
	}
}

// TestAnyTokenReadsOthersCards is the IDOR half of #12: reading and writing
// another account's stored cards.
func TestAnyTokenReadsOthersCards(t *testing.T) {
	victimID, _, attackerToken := setupUsersAndToken(t)

	respAdd, _ := addCard(t, attackerToken, victimID)
	if respAdd.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker added card to victim account: status %d, want 404", respAdd.StatusCode)
	}

	respList, _ := doJSONAuth(t, http.MethodGet,
		"/users/"+strconv.FormatInt(victimID, 10)+"/payment-methods", attackerToken, nil)
	if respList.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker lists victim cards: status %d, want 404", respList.StatusCode)
	}
}

// TestOwnerCanManageOwnCards is the positive half, so the fix cannot be "lock
// everybody out".
func TestOwnerCanManageOwnCards(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)

	respAdd, card := addCard(t, token, userID)
	if respAdd.StatusCode != http.StatusCreated {
		t.Fatalf("owner adds card: status %d, want 201", respAdd.StatusCode)
	}
	cardID := idFrom(t, card, "id")

	respList, listOut := doJSONAuth(t, http.MethodGet,
		"/users/"+strconv.FormatInt(userID, 10)+"/payment-methods", token, nil)
	if respList.StatusCode != http.StatusOK {
		t.Fatalf("owner lists own cards: status %d, want 200", respList.StatusCode)
	}
	if len(listOut.([]any)) != 1 {
		t.Fatalf("owner sees %d cards, want 1", len(listOut.([]any)))
	}

	respDel, _ := doJSONAuth(t, http.MethodDelete,
		"/payment-methods/"+strconv.FormatInt(cardID, 10), token, nil)
	if respDel.StatusCode != http.StatusNoContent {
		t.Fatalf("owner deletes own card: status %d, want 204", respDel.StatusCode)
	}
}

// TestGlobalListingExposesAllCards is the regression for the global listings.
//
// GET /payment-methods and GET /addresses returned every row in the table to any
// authenticated caller. For the first one that meant every card in the shop, in
// clear, including the CVV. The routes are gone rather than gated, because there
// is no caller that needs them: the frontend lists by user id.
func TestGlobalListingExposesAllCards(t *testing.T) {
	userA, _, tokenA := setupUsersAndToken(t)
	userB, _, tokenB := setupUsersAndToken(t)

	addCard(t, tokenA, userA)
	addCard(t, tokenB, userB)

	for _, path := range []string{"/payment-methods", "/addresses"} {
		t.Run(path, func(t *testing.T) {
			resp, _ := doJSONAuth(t, http.MethodGet, path, tokenA, nil)
			if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("GET %s: status %d; a global listing must not exist", path, resp.StatusCode)
			}
		})
	}
}

// TestAddressesIDORAndMultiPerUser covers two separate faults in one place.
//
// The IDOR: the user id was read from the path and used without checking, so one
// account could create, list and delete another account's addresses.
//
// The multiple-addresses part is not a vulnerability and never was; it records
// that an account is allowed more than one, so the IDOR fix cannot be "only the
// first address is reachable".
func TestAddressesIDORAndMultiPerUser(t *testing.T) {
	// Both accounts are created here rather than through setupUsersAndToken,
	// because this test needs the victim's own credential to prove the fix did
	// not turn into "nobody can manage their address book".
	victimEmail := uniqueTestEmail()
	victimID, _ := registerUser(t, victimEmail, "victim-password-1", "")
	attackerEmail := uniqueTestEmail()
	registerUser(t, attackerEmail, "attackerpass", "")
	attackerToken := loginAndGetToken(t, attackerEmail, "attackerpass")

	addrBody := func(street string) map[string]any {
		return map[string]any{"street": street, "city": "Springfield", "zip": "12345"}
	}
	victimBase := "/users/" + strconv.FormatInt(victimID, 10) + "/addresses"

	// The attacker cannot write into the victim's address book.
	respA1, _ := doJSONAuth(t, http.MethodPost, victimBase, attackerToken, addrBody("Calle 1"))
	if respA1.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker created address for victim: status %d, want 404", respA1.StatusCode)
	}
	respA2, _ := doJSONAuth(t, http.MethodPost, victimBase, attackerToken, addrBody("Calle 2"))
	if respA2.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker created a second address for victim: status %d, want 404", respA2.StatusCode)
	}

	// Nor read it.
	respList, _ := doJSONAuth(t, http.MethodGet, victimBase, attackerToken, nil)
	if respList.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker lists victim addresses: status %d, want 404", respList.StatusCode)
	}

	// The victim keeps both, and manages them, as before.
	ownToken := loginAndGetToken(t, victimEmail, "victim-password-1")
	for _, street := range []string{"Calle 1", "Calle 2"} {
		resp, _ := doJSONAuth(t, http.MethodPost, victimBase, ownToken, addrBody(street))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("victim created %q: status %d, want 201", street, resp.StatusCode)
		}
	}
	ownList, ownOut := doJSONAuth(t, http.MethodGet, victimBase, ownToken, nil)
	if ownList.StatusCode != http.StatusOK {
		t.Fatalf("victim lists own addresses: status %d, want 200", ownList.StatusCode)
	}
	if len(ownOut.([]any)) != 2 {
		t.Fatalf("victim has %d addresses, want 2; multiple addresses are legitimate", len(ownOut.([]any)))
	}
}
func TestFinancialRoutesRequireToken(t *testing.T) {
	// The global listings are gone entirely, so they are 404 whether or not a
	// token is presented. They are not 401 because there is nothing to
	// authenticate against.
	for _, path := range []string{"/payment-methods", "/addresses"} {
		resp, _ := doAnon(t, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("anonymous GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}

	// The routes that do exist still refuse an anonymous caller.
	for _, path := range []string{"/users/1/payment-methods", "/users/1/addresses", "/payment-methods/1", "/addresses/1"} {
		resp, _ := doAnon(t, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous GET %s: status %d, want 401", path, resp.StatusCode)
		}
	}
}

// TestCardNumberIsNeverStored is the strongest form of the #12 fix.
//
// The previous tests checked what a response contains. This one checks the
// database directly, after going through the API, because "the response omits
// the PAN" and "the PAN is not on disk" are different claims and only the second
// one actually protects anybody if the database is ever copied, dumped or
// leaked.
func TestCardNumberIsNeverStored(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)

	resp, _ := addCard(t, token, userID)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST card: status %d, want 201", resp.StatusCode)
	}

	// The columns do not exist at all, so a query naming them fails outright.
	var cnt int
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'payment_methods' AND column_name IN ('card_number', 'cvv', 'card_holder')`,
	).Scan(&cnt); err != nil {
		t.Fatalf("inspect schema: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("%d sensitive columns still exist on payment_methods", cnt)
	}

	// And the PAN does not appear anywhere in the row as text.
	var leaked int
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT count(*) FROM payment_methods WHERE last4 LIKE '%4111111111%' OR brand LIKE '%4111%'`,
	).Scan(&leaked); err != nil {
		t.Fatalf("scan for the pan: %v", err)
	}
	if leaked != 0 {
		t.Fatal("the card number is present in payment_methods")
	}
}

// TestCVVIsRejected documents that the endpoint refuses a cvv rather than
// quietly dropping it. Accepting and discarding would tell the client its data
// had been stored, which is worse than saying no.
func TestCVVIsRejected(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)

	resp, _ := doJSONAuth(t, http.MethodPost,
		"/users/"+strconv.FormatInt(userID, 10)+"/payment-methods", token, map[string]any{
			"card_number":  "4111111111111111",
			"card_holder":  "Owner Name",
			"cvv":          123,
			"expiry_month": 12,
			"expiry_year":  2028,
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST card with cvv: status %d, want 400", resp.StatusCode)
	}
}

// TestCardBrandAndLast4AreKept makes sure the fix did not become "store nothing
// useful": a receipt still needs to say which card and which last four.
func TestCardBrandAndLast4AreKept(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)

	resp, card := addCard(t, token, userID)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST card: status %d, want 201", resp.StatusCode)
	}
	if card["brand"] != "visa" {
		t.Errorf("brand = %v, want visa", card["brand"])
	}
	if card["last4"] != "1111" {
		t.Errorf("last4 = %v, want 1111", card["last4"])
	}
	if card["expiry_month"] != float64(12) || card["expiry_year"] != float64(2028) {
		t.Errorf("expiry = %v/%v, want 12/2028", card["expiry_month"], card["expiry_year"])
	}
}

// TestInvalidCardNumberRejected keeps obviously bad input out of the table, so a
// stored card always corresponds to something that could exist.
func TestInvalidCardNumberRejected(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)

	for _, number := range []string{"4111111111111112", "1234", "", "abcd"} {
		resp, _ := doJSONAuth(t, http.MethodPost,
			"/users/"+strconv.FormatInt(userID, 10)+"/payment-methods", token, map[string]any{
				"card_number":  number,
				"expiry_month": 12,
				"expiry_year":  2028,
			})
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("card %q: status %d, want 400", number, resp.StatusCode)
		}
	}
}
