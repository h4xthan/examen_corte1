package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// autoRandomID is a real book id from the TiDB Cloud cluster: 19 digits, and
// eight orders of magnitude past the largest integer a float64 holds exactly.
const autoRandomID = int64(4035225266124144421)

func TestBookIDIsQuotedOnTheWire(t *testing.T) {
	raw, err := json.Marshal(Book{ID: autoRandomID, ISBN: "9780000001"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"id":"4035225266124144421"`) {
		t.Fatalf("the id must travel as a string, got %s", raw)
	}
}

// TestEveryIdentifierSurvivesJSON is the test that would have caught the panel
// reporting a delete that removed nothing.
//
// The chain is Go marshals an int64, the browser's JSON.parse turns it into a
// float64, and 4035225266124144421 is not representable as one. The browser then
// sent back 4035225266076570000, and the update and the delete both addressed a
// book that does not exist. It cost an afternoon of "the panel is broken" before
// anybody checked what the browser was actually holding.
func TestEveryIdentifierSurvivesJSON(t *testing.T) {
	// What a JavaScript runtime does with the value, spelled out in Go because
	// the test cannot run a browser. float64 is the type JSON.parse produces.
	throughBrowser := float64(autoRandomID)
	if int64(throughBrowser) == autoRandomID {
		t.Fatal("this id fits in a float64, so it no longer tests anything")
	}

	// The fields that carry an identifier must all be quoted, and money must
	// not be: a price the panel can add up is worth more than the tidiness of
	// quoting everything.
	assertQuoted(t, Book{ID: autoRandomID}, "id")
	assertQuoted(t, Order{ID: autoRandomID, UserID: autoRandomID}, "id", "user_id")
	assertQuoted(t, OrderItem{ID: autoRandomID, OrderID: autoRandomID, BookID: autoRandomID}, "id", "order_id", "book_id")
	assertQuoted(t, Review{ID: autoRandomID, BookID: autoRandomID, UserID: autoRandomID}, "id", "book_id", "user_id")
	assertQuoted(t, Coupon{ID: autoRandomID}, "id")
	assertQuoted(t, User{ID: autoRandomID}, "id")
	assertQuoted(t, Address{ID: autoRandomID, UserID: autoRandomID}, "id", "user_id")
	assertQuoted(t, PaymentMethod{ID: autoRandomID, UserID: autoRandomID}, "id", "user_id")

	assertBare(t, Book{ID: autoRandomID, PriceCents: 1999, Stock: 3, Pages: 100},
		"price_cents", "stock", "pages")
	assertBare(t, Order{ID: autoRandomID, SubtotalCents: 500, DiscountCents: 100, TotalCents: 400},
		"subtotal_cents", "discount_cents", "total_cents")
}

func assertQuoted(t *testing.T, v any, fields ...string) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if !strings.Contains(string(raw), `"`+f+`":"`) {
			t.Errorf("%T: %s should be quoted, got %s", v, f, raw)
		}
	}
}

func assertBare(t *testing.T, v any, fields ...string) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if !strings.Contains(string(raw), `"`+f+`":`) {
			t.Errorf("%T: %s missing from %s", v, f, raw)
			continue
		}
		if strings.Contains(string(raw), `"`+f+`":"`) {
			t.Errorf("%T: %s must stay a number so the panel can add it up, got %s", v, f, raw)
		}
	}
}
