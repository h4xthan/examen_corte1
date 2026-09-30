package transport_test

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// createTestBook creates a book with a given price in cents.
func createTestBook(t *testing.T, priceCents int64) int64 {
	t.Helper()
	return createTestBookWithStock(t, priceCents, 5)
}

func createTestBookWithStock(t *testing.T, priceCents int64, stock int) int64 {
	t.Helper()

	book := map[string]any{
		"author":      "Author",
		"title":       "Title",
		"pages":       100,
		"isbn":        fmt.Sprintf("I-%d", time.Now().UnixNano()%1000000),
		"price_cents": priceCents,
		"stock":       stock,
	}
	id, _ := createBookAsCapturista(t, book)
	return int64(id)
}

// addItem posts a line to an order.
//
// It deliberately takes no unit price. The endpoint has no such field any more:
// the old one did, and accepting it is what let a client decide what it was
// charged (#8). A test that wants to try tampering sends the field itself and
// expects a 400.
func addItem(t *testing.T, token string, orderID, bookID int64, qty int) (*http.Response, map[string]any) {
	t.Helper()

	// The id crosses the wire as text (§3.8): every identifier is json:",string",
	// and a raw number in a ,string field is a 400.
	body := map[string]any{
		"book_id":  strconv.FormatInt(bookID, 10),
		"quantity": qty,
	}
	resp, out := doJSONAuth(t, http.MethodPost, orderPath(orderID)+"/items", token, body)
	if out == nil {
		t.Fatalf("POST item: unexpected empty body with status %d", resp.StatusCode)
	}
	return resp, out.(map[string]any)
}

func TestOrderDetailEmbedsItems(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, userID, "pending", 1999)
	bookID := createTestBook(t, 1999)
	addItem(t, token, orderID, bookID, 1)

	resp, out := doJSONAuth(t, http.MethodGet, orderPath(orderID), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", orderPath(orderID), resp.StatusCode)
	}
	order := out.(map[string]any)
	items, ok := order["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected embedded items array of len 1, got %v", order["items"])
	}
	item := items[0].(map[string]any)
	if idFrom(t, item, "book_id") != bookID {
		t.Fatalf("embedded item book_id %v, want %v", item["book_id"], bookID)
	}
	// The field is unit_price_cents now. A float `unit_price` would reintroduce
	// the money-as-floating-point bug the migration removed.
	if int64(item["unit_price_cents"].(float64)) != 1999 {
		t.Fatalf("embedded unit_price_cents %v, want 1999", item["unit_price_cents"])
	}
}

// TestAddItemIgnoresClientPrice is #8 inverted.
//
// The old endpoint decoded the whole order item from the body, so a client sent
// unit_price: 1 and got a line priced at one cent against a book that cost
// twenty. The field is gone from the request, so sending it is a 400 rather than
// a cheap purchase.
func TestAddItemIgnoresClientPrice(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, userID, "pending", 1999)
	bookID := createTestBook(t, 1999)

	resp, _ := doJSONAuth(t, http.MethodPost, orderPath(orderID)+"/items", token, map[string]any{
		"book_id":    bookID,
		"quantity":   1,
		"unit_price": 1,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST item with unit_price: status %d, want 400", resp.StatusCode)
	}

	// And the line that does get created is priced from the catalogue.
	ok, item := addItem(t, token, orderID, bookID, 1)
	if ok.StatusCode != http.StatusCreated {
		t.Fatalf("POST item: status %d, want 201", ok.StatusCode)
	}
	if got := int64(item["unit_price_cents"].(float64)); got != 1999 {
		t.Fatalf("unit_price_cents = %d, want the catalogue price 1999", got)
	}
}

// TestAddItemValidatesQuantityAndKeepsStock checks that a rejected line does not
// take stock with it. The old handler inserted whatever quantity it was given and
// never touched stock at all, so a negative quantity was a refund and a huge one
// was a way to exhaust a title.
func TestAddItemValidatesQuantityAndKeepsStock(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, userID, "pending", 0)
	bookID := createTestBookWithStock(t, 500, 5)

	for _, qty := range []int{0, -1, 11, 1000000} {
		resp, _ := addItem(t, token, orderID, bookID, qty)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("quantity %d: status %d, want 400", qty, resp.StatusCode)
		}
	}

	if got := stockOf(t, bookID); got != 5 {
		t.Fatalf("stock = %d after rejected lines, want the original 5", got)
	}

	// A valid line does take its stock, which is the behaviour the old code was
	// missing entirely.
	resp, _ := addItem(t, token, orderID, bookID, 2)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST item quantity 2: status %d, want 201", resp.StatusCode)
	}
	if got := stockOf(t, bookID); got != 3 {
		t.Fatalf("stock = %d after taking 2, want 3", got)
	}
}

// TestAddItemRefusesMoreThanStock makes the reservation conditional.
//
// Two customers racing for the last copy is the case the conditional UPDATE
// exists for: without it both read stock = 1, both passed the check and both
// inserted, leaving stock at -1.
func TestAddItemRefusesMoreThanStock(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, userID, "pending", 0)
	bookID := createTestBookWithStock(t, 500, 1)

	resp, _ := addItem(t, token, orderID, bookID, 1)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first line: status %d, want 201", resp.StatusCode)
	}

	resp, _ = addItem(t, token, orderID, bookID, 1)
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("second line for the last copy was accepted; stock went negative")
	}
	if got := stockOf(t, bookID); got != 0 {
		t.Fatalf("stock = %d, want 0", got)
	}
}

// TestItemIDORIsRefused is #9 inverted.
//
// The old GET /order-items/{id} looked the item up by id alone. Any token could
// read any line of any order, which reveals what books people bought and how
// much they paid.
func TestItemIDORIsRefused(t *testing.T) {
	victimID, _, attackerToken := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, victimID, "pending", 1999)
	bookID := createTestBook(t, 1999)
	itemID := insertOrderItemDirect(t, orderID, bookID, 1, 1999)

	// The owner can read their own line.
	ownerToken := loginAndGetToken(t, emailOf(t, victimID), "victim-password-1")
	resp, _ := doJSONAuth(t, http.MethodGet, "/order-items/"+strconv.FormatInt(itemID, 10), ownerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner GET item: status %d, want 200", resp.StatusCode)
	}

	resp, _ = doJSONAuth(t, http.MethodGet, "/order-items/"+strconv.FormatInt(itemID, 10), attackerToken, nil)
	// 404, not 403: a 403 would confirm the item exists, which is the same leak
	// in a thinner wrapper.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker GET item: status %d, want 404", resp.StatusCode)
	}
}

// TestCannotAddOrDeleteItemsInAnotherOrder is #9 inverted for writes.
func TestCannotAddOrDeleteItemsInAnotherOrder(t *testing.T) {
	victimID, _, attackerToken := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, victimID, "pending", 0)
	bookID := createTestBookWithStock(t, 500, 5)

	resp, _ := doJSONAuth(t, http.MethodPost, orderPath(orderID)+"/items", attackerToken, map[string]any{
		"book_id":  bookID,
		"quantity": 1,
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker POST item into another order: status %d, want 404", resp.StatusCode)
	}
	if got := stockOf(t, bookID); got != 5 {
		t.Fatalf("stock = %d after a rejected injection, want 5", got)
	}

	itemID := insertOrderItemDirect(t, orderID, bookID, 1, 500)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		resp, _ := doJSONAuth(t, method, "/order-items/"+strconv.FormatInt(itemID, 10), attackerToken, map[string]any{
			"quantity": 5,
		})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("attacker %s item: status %d, want 404", method, resp.StatusCode)
		}
	}
}

// TestListItemsRequiresOwnership stops the collection endpoint leaking too.
func TestListItemsRequiresOwnership(t *testing.T) {
	victimID, _, attackerToken := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, victimID, "pending", 0)
	bookID := createTestBook(t, 500)
	insertOrderItemDirect(t, orderID, bookID, 1, 500)

	resp, _ := doJSONAuth(t, http.MethodGet, orderPath(orderID)+"/items", attackerToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker GET items: status %d, want 404", resp.StatusCode)
	}
}

// TestStockAndOrderLineMoveTogether is the atomicity of the line endpoints.
//
// The reservation and the row write used to be two statements. A failure on the
// second one — and a foreign key is the easy case, because order_id is a real
// constraint — left a copy counted out of stock with no line to explain it, and
// the only thing that ever gives stock back is a line being edited or deleted.
// The order was never completed, so nothing would ever look for the missing
// copy again.
//
// The orphan order id is the cheapest way to make the second statement fail for
// real, rather than mocking it.
func TestStockAndOrderLineMoveTogether(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	bookID := createTestBookWithStock(t, 1200, 3)

	// An order id that does not exist, so the item insert violates the foreign
	// key after the stock has already been taken.
	missingOrderID := int64(9_000_000 + time.Now().UnixNano()%1_000_000)

	resp, _ := addItem(t, token, missingOrderID, bookID, 2)
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("an item was created against an order that does not exist")
	}

	if got := stockOf(t, bookID); got != 3 {
		t.Errorf("stock = %d after a failed line, want 3: the reservation was not rolled back with the insert", got)
	}

	// And the same book can still be bought normally, which is the part that
	// matters in production: the inventory is not quietly short.
	orderID := insertOrderDirect(t, userID, "pending", 0)
	if resp, _ := addItem(t, token, orderID, bookID, 2); resp.StatusCode != http.StatusCreated {
		t.Fatalf("adding the line to a real order: status %d, want 201", resp.StatusCode)
	}
	if got := stockOf(t, bookID); got != 1 {
		t.Errorf("stock = %d after a successful line, want 1", got)
	}
}

// TestEditingALineReturnsExactlyTheDifference covers the delta arithmetic, and
// the case it is easy to get wrong: lowering a quantity must not manufacture
// stock out of nothing.
func TestEditingALineReturnsExactlyTheDifference(t *testing.T) {
	_, userID, token := setupUsersAndToken(t)
	orderID := insertOrderDirect(t, userID, "pending", 0)
	bookID := createTestBookWithStock(t, 900, 10)
	addItem(t, token, orderID, bookID, 3)
	if got := stockOf(t, bookID); got != 7 {
		t.Fatalf("stock = %d after reserving 3, want 7", got)
	}

	var itemID int64
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT id FROM order_items WHERE order_id = ?`, orderID).Scan(&itemID); err != nil {
		t.Fatalf("read the line: %v", err)
	}
	// Update and delete hang off /order-items/{id}: the line's own identity, not
	// the order's, so there is no path component to disagree about.
	itemPath := "/order-items/" + strconv.FormatInt(itemID, 10)

	// Raise it: two more copies are reserved.
	resp, _ := doJSONAuth(t, http.MethodPut, itemPath, token, map[string]any{"quantity": 5})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("raising the quantity: status %d, want 200", resp.StatusCode)
	}
	if got := stockOf(t, bookID); got != 5 {
		t.Errorf("stock = %d after raising 3 to 5, want 5", got)
	}

	// Lower it. The difference is 3, so exactly three copies come back: 5 + 3.
	// Returning the whole reserved five instead would put phantom stock on
	// sale, and returning none of them would strand it.
	resp, _ = doJSONAuth(t, http.MethodPut, itemPath, token, map[string]any{"quantity": 2})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lowering the quantity: status %d, want 200", resp.StatusCode)
	}
	if got := stockOf(t, bookID); got != 8 {
		t.Errorf("stock = %d after lowering 5 to 2, want 8", got)
	}

	// Deleting the line gives back what is left, once.
	resp, _ = doJSONAuth(t, http.MethodDelete, itemPath, token, nil)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("deleting the line: status %d, want 204 or 200", resp.StatusCode)
	}
	if got := stockOf(t, bookID); got != 10 {
		t.Errorf("stock = %d after deleting the line, want the original 10", got)
	}
}
