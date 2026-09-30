package transport_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// uniqueISBN hands out a fresh catalogue ISBN per call. The shared database
// keeps every row a previous run created, so a test hard-coding an ISBN breaks
// on the second execution: this is a 13-digit string that has never been used.
var isbnSeq uint64

func uniqueISBN() string {
	n := atomic.AddUint64(&isbnSeq, 1)
	return "9780000" + fmt.Sprintf("%06d", (n+uint64(os.Getpid()))%1000000)
}

// staffUser creates an account and promotes it to an operating role through the
// admin route, then returns a live session for it. Registration alone could
// never produce a capturista or an auditor — that is the point of the fix — so
// the only way a test can speak as one is the route the panel itself uses.
func staffUser(t *testing.T, role string) (int64, string) {
	t.Helper()
	email := uniqueTestEmail()
	id, _ := registerUser(t, email, "staff-pass-1234567890", "")
	resp, out := doJSONAuth(t, http.MethodPut, "/admin/users/"+strconv.FormatInt(id, 10)+"/role",
		adminToken(t), map[string]any{"role": role})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("grant role %s to user %d: status %d, want 200 (body %v)", role, id, resp.StatusCode, out)
	}
	return id, loginAndGetToken(t, email, "staff-pass-1234567890")
}

// TestCapturistaAndAuditorReachTheCatalogueByRole is the access control the
// college brief asked for: the capturista manages the catalogue, the auditor
// only reads it, and the admin — who used to capture books too — was taken off
// the catalogue writes. The capturista is therefore the only role that writes,
// and the role comes from the users table on every request.
func TestCapturistaAndAuditorReachTheCatalogueByRole(t *testing.T) {
	capturistaID, capturistaToken := staffUser(t, "capturista")
	auditorID, auditorToken := staffUser(t, "auditor")
	_ = capturistaID
	_ = auditorID

	resp, out := doJSONAuth(t, http.MethodPost, "/books", capturistaToken, map[string]any{
		"author":          "Autor de roles",
		"title":           "Libro de roles",
		"pages":           240,
		"isbn":            uniqueISBN(),
		"price_cents":     1999,
		"stock":           5,
		"url_cover_image": "",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("capturista POST /books: status %d, want 201 (body %v)", resp.StatusCode, out)
	}
	bookID := idFrom(t, out.(map[string]any), "id")

	// Admin is refused the write with the same 403 an auditor gets: the admin
	// can read the catalogue, but capturing it is the capturista's job.
	resp, out = doJSONAuth(t, http.MethodPost, "/books", adminToken(t), map[string]any{
		"author": "Autor de roles", "title": "No", "pages": 1,
		"isbn": uniqueISBN(), "price_cents": 1, "stock": 1,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("admin POST /books: status %d, want 403 (body %v)", resp.StatusCode, out)
	}

	// The auditor is refused the write too, with the same 403 a customer gets:
	// nothing in the response edges around whether the route exists.
	resp, out = doJSONAuth(t, http.MethodPost, "/books", auditorToken, map[string]any{
		"author": "Autor de roles", "title": "Otro", "pages": 1,
		"isbn": uniqueISBN(), "price_cents": 1, "stock": 1,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("auditor POST /books: status %d, want 403 (body %v)", resp.StatusCode, out)
	}

	// But the auditor and the admin can read the book the capturista put up.
	resp, out = doJSONAuth(t, http.MethodGet, "/books/"+strconv.FormatInt(bookID, 10), auditorToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auditor GET /books/{id}: status %d, want 200 (body %v)", resp.StatusCode, out)
	}
	resp, out = doJSONAuth(t, http.MethodGet, "/books/"+strconv.FormatInt(bookID, 10), adminToken(t), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin GET /books/{id}: status %d, want 200 (body %v)", resp.StatusCode, out)
	}

	// An anonymous caller has never been able to write; the guard did not regress.
	// Sent without a session and without cookies, so no browser state can leak
	// into the credential.
	anonReq, _ := newJSONRequest(t, http.MethodPost, "/books", map[string]any{
		"title": "anon", "author": "x", "isbn": uniqueISBN(), "pages": 1,
		"price_cents": 1, "stock": 1,
	}, "")
	anonResp, err := http.DefaultClient.Do(anonReq)
	if err != nil {
		t.Fatalf("anonymous POST /books: %v", err)
	}
	anonResp.Body.Close()
	if anonResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous POST /books: status %d, want 401", anonResp.StatusCode)
	}
}

// TestAuditTrailsPanelOperations checks that the operating history records the
// interfaces' events: a catalogue write, an auditor's consultation — and that a
// customer browsing the catalogue is not noise in that history.
func TestAuditTrailsPanelOperations(t *testing.T) {
	title := "Titulo auditoria " + uniqueTestEmail()

	_, capturistaToken := staffUser(t, "capturista")
	resp, out := doJSONAuth(t, http.MethodPost, "/books", capturistaToken, map[string]any{
		"author":          "Autor auditoria",
		"title":           title,
		"pages":           100,
		"isbn":            uniqueISBN(),
		"price_cents":     1000,
		"stock":           2,
		"url_cover_image": "",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("capturista POST /books: status %d (body %v)", resp.StatusCode, out)
	}
	bookID := idFrom(t, out.(map[string]any), "id")
	bookPath := strconv.FormatInt(bookID, 10)

	// An auditor consulting the book adds three "read" rows; a customer
	// consulting the same book must not add any.
	auditorID, auditorToken := staffUser(t, "auditor")
	customerID, _ := registerUser(t, uniqueTestEmail(), "customer-pass-1234567890", "")
	customerToken := loginAndGetToken(t, emailOf(t, customerID), "customer-pass-1234567890")

	for i := 0; i < 3; i++ {
		if resp, _ := doJSONAuth(t, http.MethodGet, "/books/"+bookPath, auditorToken, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("auditor GET book: status %d", resp.StatusCode)
		}
	}
	if resp, _ := doJSONAuth(t, http.MethodGet, "/books/"+bookPath, customerToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("customer GET book: status %d", resp.StatusCode)
	}

	resp, out = doJSONAuth(t, http.MethodGet, "/admin/audit?entity=book&limit=500", adminToken(t), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/audit: status %d, want 200 (body %v)", resp.StatusCode, out)
	}
	rows := out.([]any)

	foundCreate := false
	readCount := 0
	for _, r := range rows {
		row := r.(map[string]any)
		if row["entity"] != "book" {
			continue
		}
		entityID, _ := strconv.ParseInt(row["entity_id"].(string), 10, 64)
		if entityID != bookID {
			continue
		}
		switch row["action"] {
		case "create":
			if row["details"] == title {
				foundCreate = true
			}
		case "read":
			if row["user_email"] == emailOf(t, auditorID) {
				readCount++
			}
			if row["user_email"] == emailOf(t, customerID) {
				t.Error("a customer's catalogue consultation was recorded as an operation")
			}
		}
	}
	if !foundCreate {
		t.Error("the history does not record the alta of the book")
	}
	if readCount != 3 {
		t.Errorf("the history shows %d auditor reads of the book, want 3", readCount)
	}
}

// TestAuditIsReachableByEveryPanelRole checks the panel history route's own
// access: an admin, a capturista and an auditor may read it, a customer may
// not.
func TestAuditIsReachableByEveryPanelRole(t *testing.T) {
	for _, role := range []string{"admin", "capturista", "auditor"} {
		t.Run(role, func(t *testing.T) {
			var token string
			if role == "admin" {
				token = adminToken(t)
			} else {
				_, token = staffUser(t, role)
			}
			resp, _ := doJSONAuth(t, http.MethodGet, "/admin/audit?entity=book&limit=5", token, nil)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s GET /admin/audit: status %d, want 200", role, resp.StatusCode)
			}
		})
	}

	customerID, _ := registerUser(t, uniqueTestEmail(), "customer-pass-1234567890", "")
	customerToken := loginAndGetToken(t, emailOf(t, customerID), "customer-pass-1234567890")
	resp, _ := doJSONAuth(t, http.MethodGet, "/admin/audit?entity=book", customerToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("customer GET /admin/audit: status %d, want 403", resp.StatusCode)
	}
}

// TestDisabledAccountIsBarredEverywhere covers the "baja": once an
// administrator takes an account off, its sessions stop working immediately and
// it cannot log in again, and the refusal is indistinguishable from a wrong
// password so an attacker cannot enumerate disabled addresses.
func TestDisabledAccountIsBarredEverywhere(t *testing.T) {
	email := uniqueTestEmail()
	id, _ := registerUser(t, email, "baja-pass-1234567890", "")
	liveToken := loginAndGetToken(t, email, "baja-pass-1234567890")

	// Before the baja the session works.
	resp, _ := doJSONAuth(t, http.MethodGet, "/users/"+strconv.FormatInt(id, 10), liveToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET own profile before baja: status %d, want 200", resp.StatusCode)
	}

	// The baja.
	resp, _ = doJSONAuth(t, http.MethodDelete, "/admin/users/"+strconv.FormatInt(id, 10), adminToken(t), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("baja: status %d, want 204", resp.StatusCode)
	}

	// The session that was alive a moment ago dies on its next request.
	resp, _ = doJSONAuth(t, http.MethodGet, "/users/"+strconv.FormatInt(id, 10), liveToken, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a disabled account's live session: status %d, want 401", resp.StatusCode)
	}

	// Login is refused with the uniform credential error, not a hint that the
	// address exists.
	resp, out := doJSON(t, http.MethodPost, "/auth/login", map[string]any{
		"email":    email,
		"password": "baja-pass-1234567890",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("login of a disabled account: status %d, want 401", resp.StatusCode)
	}
	if msg, ok := out.(map[string]any)["error"].(string); !ok || !strings.Contains(msg, "credentials") {
		t.Errorf("login of a disabled account leaked a different error: %v", out)
	}
}

// TestUsersInterfaceLifecycle runs the users interface end to end: an admin
// opens an account with an operating role, edits its profile, consults it, takes
// it off, lets it back in, and moves it between the four roles.
func TestUsersInterfaceLifecycle(t *testing.T) {
	email := uniqueTestEmail()

	resp, out := doJSONAuth(t, http.MethodPost, "/admin/users", adminToken(t), map[string]any{
		"first_name": "Alta",
		"last_name":  "Usuario",
		"email":      email,
		"password":   "alta-pass-1234567890",
		"role":       "capturista",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("alta: status %d, want 201 (body %v)", resp.StatusCode, out)
	}
	user := out.(map[string]any)
	id := idFrom(t, user, "id")
	if user["role"] != "capturista" {
		t.Errorf("alta: role %q, want capturista", user["role"])
	}
	if user["is_active"] != true {
		t.Errorf("alta: is_active %v, want true", user["is_active"])
	}
	idPath := "/admin/users/" + strconv.FormatInt(id, 10)

	// Modificación: the name and the address.
	resp, out = doJSONAuth(t, http.MethodPut, idPath, adminToken(t), map[string]any{
		"first_name": "Editado",
		"last_name":  "Ahora",
		"email":      email,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("modificación: status %d, want 200 (body %v)", resp.StatusCode, out)
	}

	// Consulta: the single row the panel shows when asked for one account.
	resp, out = doJSONAuth(t, http.MethodGet, idPath, adminToken(t), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("consulta: status %d, want 200", resp.StatusCode)
	}
	if got := out.(map[string]any)["first_name"]; got != "Editado" {
		t.Errorf("consulta: first_name %v, want Editado", got)
	}

	// Baja and reactivación.
	resp, _ = doJSONAuth(t, http.MethodDelete, idPath, adminToken(t), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("baja: status %d, want 204", resp.StatusCode)
	}
	resp, out = doJSONAuth(t, http.MethodPost, idPath+"/activate", adminToken(t), map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reactivación: status %d, want 200 (body %v)", resp.StatusCode, out)
	}
	if out.(map[string]any)["is_active"] != true {
		t.Errorf("reactivación: is_active %v, want true", out.(map[string]any)["is_active"])
	}

	// Asignación de permisos: move the account between the four roles.
	for _, role := range []string{"admin", "auditor", "customer", "capturista"} {
		resp, _ := doJSONAuth(t, http.MethodPut, idPath+"/role", adminToken(t), map[string]any{"role": role})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("role %s: status %d, want 200", role, resp.StatusCode)
		}
	}

	// An admin cannot take its own account off: that is the one change that
	// could leave a shop with no way back in.
	meResp, meOut := doJSONAuth(t, http.MethodGet, "/users/me", adminToken(t), nil)
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /users/me as admin: status %d", meResp.StatusCode)
	}
	meID := idFrom(t, meOut.(map[string]any), "id")
	resp, _ = doJSONAuth(t, http.MethodDelete, "/admin/users/"+strconv.FormatInt(meID, 10), adminToken(t), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("self-baja: status %d, want 409", resp.StatusCode)
	}
}

// TestBackupsInterface creates a dump as the panel does, confirms the backups
// interface lists it and downloads it back — and confirms a customer sees none
// of it.
func TestBackupsInterface(t *testing.T) {
	resp, body := dumpAsAdmin(t, adminToken(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /admin/backup: status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "CREATE TABLE `users`") {
		t.Error("the dump does not contain the schema")
	}
	posted := backupNameFrom(resp)
	if posted == "" {
		t.Fatal("the backup POST returned no filename")
	}

	// The backups interface lists the dump the POST just wrote to disk.
	resp, out := doJSONAuth(t, http.MethodGet, "/admin/backups", adminToken(t), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/backups: status %d, want 200", resp.StatusCode)
	}
	rows := out.([]any)
	if len(rows) == 0 {
		t.Fatal("the backups interface listed no dumps")
	}
	found := false
	for _, r := range rows {
		if r.(map[string]any)["name"] == posted {
			found = true
		}
	}
	if !found {
		t.Fatalf("the dump %q was created but not listed", posted)
	}

	// Download it back and check it is a real file, not an error wearing one.
	req, _ := newJSONRequest(t, http.MethodGet, "/admin/backups/"+posted, nil, adminToken(t))
	dlResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/backups/%s: %v", posted, err)
	}
	defer dlResp.Body.Close()
	dlBody, err := io.ReadAll(dlResp.Body)
	if err != nil {
		t.Fatalf("read downloaded dump: %v", err)
	}
	if dlResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/backups/{name}: status %d, want 200", dlResp.StatusCode)
	}
	if !strings.Contains(string(dlBody), "CREATE TABLE `users`") {
		t.Error("the downloaded dump is not the backup file")
	}

	// A non-admin is refused the interface.
	customerID, _ := registerUser(t, uniqueTestEmail(), "customer-pass-1234567890", "")
	customerToken := loginAndGetToken(t, emailOf(t, customerID), "customer-pass-1234567890")
	resp, _ = doJSONAuth(t, http.MethodGet, "/admin/backups", customerToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("customer GET /admin/backups: status %d, want 403", resp.StatusCode)
	}

	// A name that is not a dump filename is a 404, not a traversal attempt.
	resp, _ = doJSONAuth(t, http.MethodGet, "/admin/backups/..%2F..%2Fetc%2Fpasswd", adminToken(t), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /admin/backups/{bad-name}: status %d, want 404", resp.StatusCode)
	}
}

// TestAuditorReadsEverythingWritesNothing is the role matrix in one pass: the
// auditor is the window over the whole shop, so every panel reading and both
// global listings answer 200 — order detail included — while every writing
// surface answers 403, including downloading a backup dump, which is credential
// material and stays admin-only. Books follow their own matrix and were closed
// to the admin too: capturing the catalogue is the capturista's job alone.
func TestAuditorReadsEverythingWritesNothing(t *testing.T) {
	_, auditorToken := staffUser(t, "auditor")

	// An order belonging to a customer, so the auditor reads somebody else's.
	bookID := createTestBook(t, 1500)
	_, buyerID, buyerToken := setupUsersAndToken(t)
	buyBook(t, buyerToken, buyerID, bookID, 1)

	for _, path := range []string{
		"/users",
		"/orders",
		"/admin/stats",
		"/admin/users",
		"/admin/users/" + strconv.FormatInt(buyerID, 10),
		"/admin/reviews",
		"/admin/backups",
	} {
		resp, _ := doJSONAuth(t, http.MethodGet, path, auditorToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("auditor GET %s: status %d, want 200", path, resp.StatusCode)
		}
	}

	// The order detail of another customer's order is a read too.
	ordersResp, ordersOut := doJSONAuth(t, http.MethodGet, "/orders", auditorToken, nil)
	if ordersResp.StatusCode != http.StatusOK {
		t.Fatalf("auditor GET /orders: status %d, want 200", ordersResp.StatusCode)
	}
	var orderID int64
	for _, o := range ordersOut.([]any) {
		row := o.(map[string]any)
		if uid, _ := row["user_id"].(string); uid == strconv.FormatInt(buyerID, 10) {
			orderID, _ = strconv.ParseInt(row["id"].(string), 10, 64)
			break
		}
	}
	if orderID == 0 {
		t.Fatal("the buyer's order is missing from the global listing")
	}
	resp, _ := doJSONAuth(t, http.MethodGet, "/orders/"+strconv.FormatInt(orderID, 10), auditorToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("auditor GET /orders/{id}: status %d, want 200", resp.StatusCode)
	}

	// A dump exists, so the download denial below is a real denial, not a 404
	// wearing one.
	dumpResp, _ := dumpAsAdmin(t, adminToken(t))
	if dumpResp.StatusCode != http.StatusOK {
		t.Fatalf("seed dump: status %d, want 200", dumpResp.StatusCode)
	}
	dumpName := backupNameFrom(dumpResp)
	if dumpName == "" {
		t.Fatal("seed dump returned no filename")
	}
	resp, _ = doJSONAuth(t, http.MethodGet, "/admin/backups/"+dumpName, auditorToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("auditor GET /admin/backups/{name}: status %d, want 403", resp.StatusCode)
	}

	// Every writing surface is locked: books to the capturista, all other writes
	// to an admin, and the auditor reaches none of them. (Order writes are
	// owner-scoped, so the auditor gets the same 404 anyone who is not the owner
	// gets: no existence oracle.)
	bookPath := "/books/" + strconv.FormatInt(bookID, 10)
	for _, w := range []struct{ method, path string }{
		{http.MethodPut, bookPath},
		{http.MethodDelete, bookPath},
		{http.MethodPost, "/books"},
		{http.MethodPost, "/coupons"},
		{http.MethodPost, "/admin/users"},
		{http.MethodPut, "/admin/users/" + strconv.FormatInt(buyerID, 10) + "/role"},
		{http.MethodPost, "/admin/backup"},
	} {
		resp, out := doJSONAuth(t, w.method, w.path, auditorToken, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("auditor %s %s: status %d, want 403 (body %v)", w.method, w.path, resp.StatusCode, out)
		}
	}
}

// backupNameFrom reads the download filename out of a POST /admin/backup
// response header.
func backupNameFrom(resp *http.Response) string {
	parts := strings.Split(resp.Header.Get("Content-Disposition"), "filename=")
	if len(parts) < 2 {
		return ""
	}
	return strings.Trim(parts[len(parts)-1], `"`)
}

// keep the imported context alive for call sites in this file's pattern
var _ = context.Background
var _ = backupNameFrom
