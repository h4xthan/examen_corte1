package transport_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"

	"dvbs/internal/backup"
	"dvbs/internal/config"
	"dvbs/internal/mailer"
	"dvbs/internal/model"
	"dvbs/internal/router"
	"dvbs/internal/service"
	"dvbs/internal/store"
	"dvbs/internal/store/db"
	"dvbs/internal/token"
	"dvbs/internal/transport"
)

// WithTestTransaction runs fn inside a database transaction that is always
// rolled back. It provides a *db.Queries bound to that transaction so that
// all stores created from it see the same isolated snapshot. This gives
// perfect test isolation without truncating tables or relying on unique IDs.
func WithTestTransaction(t *testing.T, fn func(q *db.Queries)) {
	t.Helper()

	ctx := context.Background()
	tx, err := testDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	// Ensure rollback even if the test panics.
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Logf("tx rollback: %v", err)
		}
	}()

	qtx := db.New(tx)
	fn(qtx)
}

// WithTestServerInTransaction builds a complete test server (router + all
// stores) bound to a transaction that is rolled back when fn returns.
// This gives handler tests the same isolation as store tests.
func WithTestServerInTransaction(t *testing.T, fn func(server *httptest.Server)) {
	t.Helper()

	WithTestTransaction(t, func(q *db.Queries) {
		userStore := store.NewUserStore(q)
		orderStore := store.NewOrderStore(q)
		orderItemStore := store.NewOrderItemStore(q)
		reviewStore := store.NewReviewStore(q)
		couponStore := store.NewCouponStore(q)
		paymentMethodStore := store.NewPaymentMethodStore(q)
		addressStore := store.NewAddressStore(q)
		resetTokenStore := store.NewPasswordResetTokenStore(q)
		bookStore := store.NewBookStore(q)
		couponRedemptionStore := store.NewCouponRedemptionStore(q)
		auditStore := store.NewAuditStore(q)

		mailSvc := &recordingMailer{}

		sessions, _ := token.NewManager(testJWTSecret, testSessionTTL)
		authSvc := service.NewAuthService(
			store.NewRunner(testDB), // runner for token ops; not tx-bound but fine for tests
			userStore, resetTokenStore, mailSvc, sessions, bcrypt.MinCost, testResetTTL)
		bookSvc := service.NewBookService(bookStore, reviewStore)
		userSvc := service.NewUserService(userStore)
		orderSvc := service.NewOrderService(orderStore, orderItemStore)
		orderItemSvc := service.NewOrderItemService(store.NewRunner(testDB), orderItemStore, bookStore, orderStore)
		reviewSvc := service.NewReviewService(reviewStore, orderStore)
		couponSvc := service.NewCouponService(couponStore, couponRedemptionStore)
		paymentMethodSvc := service.NewPaymentMethodService(paymentMethodStore)
		addressSvc := service.NewAddressService(addressStore)
		checkoutSvc := service.NewCheckoutService(store.NewRunner(testDB), userStore)
		adminSvc := service.NewAdminService(userStore, orderStore, bookStore, couponStore, paymentMethodStore, reviewStore, bcrypt.MinCost)
		uploadDir, _ := os.MkdirTemp("", "dvbs-uploads")
		uploadHandler := transport.NewUploadHandler(uploadDir)
		backupDir, _ := os.MkdirTemp("", "dvbs-backups")
		backupHandler := transport.NewBackupHandler(backup.New(testDB), backupDir, auditStore)

		handler := router.New(
			router.Deps{Sessions: sessions, Resolver: userStore, CookieSecure: false, DisableCSRF: true},
			transport.NewBookHandler(bookSvc, auditStore),
			transport.NewUserHandler(userSvc),
			transport.NewAuthHandler(authSvc, false, testSessionTTL),
			transport.NewOrderHandler(orderSvc),
			transport.NewOrderItemHandler(orderItemSvc, orderSvc),
			transport.NewReviewHandler(reviewSvc),
			transport.NewCouponHandler(couponSvc),
			transport.NewPaymentMethodHandler(paymentMethodSvc),
			transport.NewAddressHandler(addressSvc),
			transport.NewCheckoutHandler(checkoutSvc),
			transport.NewAdminHandler(adminSvc, auditStore),
			uploadHandler,
			backupHandler,
		)

		server := httptest.NewServer(handler)
		defer server.Close()

		fn(server)
	})
}

var (
	testServer     *httptest.Server
	testBookStore  store.Store
	testUserStore  store.User
	testTokenStore store.PasswordResetToken
	testMail       *recordingMailer
	testCoupon     store.Coupon
	testDB         *sql.DB
)

const (
	// testJWTSecret is a throwaway value used only by the test server. It is not
	// a credential for anything: the suite binds to an ephemeral port and the
	// database is a local development instance.
	testJWTSecret  = "test-only-secret-not-used-anywhere-else-0123456789"
	testSessionTTL = 15 * time.Minute
	testResetTTL   = 30 * time.Minute
)

// recordingMailer captures outbound mail inside the test process.
//
// It deliberately replaces the old GET /inbox endpoint. That endpoint was a
// world-readable mailbox: any authenticated token could list every message
// addressed to attacker@dvbs.net, which is what turned the password-reset flow
// into a one-request account takeover. Tests read the code from here instead,
// so the assertion "the user receives a reset code" survives without shipping
// a read-your-own-mail API in production.
type recordingMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (m *recordingMailer) Send(msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *recordingMailer) Sent() []mailer.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mailer.Message, len(m.sent))
	copy(out, m.sent)
	return out
}

// SentTo returns the messages delivered to a given address, oldest first.
func (m *recordingMailer) SentTo(to string) []mailer.Message {
	var out []mailer.Message
	for _, msg := range m.Sent() {
		if msg.To == to {
			out = append(out, msg)
		}
	}
	return out
}

// Reset clears the recorded messages so one test cannot read another's mail.
func (m *recordingMailer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = nil
}

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "mysql://dvbs:dvbs@localhost:4000/dvbs"
	}
	dsn := config.ParseDSN(url)
	if err := config.RegisterTLSConfig(""); err != nil {
		panic("TLS config: " + err.Error())
	}
	pool, err := sql.Open("mysql", dsn)
	if err != nil {
		panic(err)
	}
	if err := pool.Ping(); err != nil {
		panic("database not reachable: " + err.Error())
	}
	defer pool.Close()

	testDB = pool

	queries := db.New(pool)
	userStore := store.NewUserStore(queries)
	orderStore := store.NewOrderStore(queries)
	orderItemStore := store.NewOrderItemStore(queries)
	reviewStore := store.NewReviewStore(queries)
	couponStore := store.NewCouponStore(queries)
	paymentMethodStore := store.NewPaymentMethodStore(queries)
	addressStore := store.NewAddressStore(queries)
	resetTokenStore := store.NewPasswordResetTokenStore(queries)
	bookStore := store.NewBookStore(queries)
	couponRedemptionStore := store.NewCouponRedemptionStore(queries)
	runner := store.NewRunner(testDB)

	mailSvc := &recordingMailer{}

	testMail = mailSvc
	testTokenStore = resetTokenStore
	testCoupon = couponStore
	testBookStore = bookStore
	testUserStore = userStore

	uploadDir, err := os.MkdirTemp("", "dvbs-uploads")
	if err != nil {
		panic(err)
	}
	backupDir, err := os.MkdirTemp("", "dvbs-backups")
	if err != nil {
		panic(err)
	}

	// The suite drives the real router. The previous version of this file
	// re-declared all 55 routes by hand, so a routing change could leave the
	// tests exercising stale wiring while the server behaved differently.
	sessions, err := token.NewManager(testJWTSecret, testSessionTTL)
	if err != nil {
		panic(err)
	}

	auditStore := store.NewAuditStore(queries)

	handler := router.New(
		router.Deps{Sessions: sessions, Resolver: userStore, CookieSecure: false, DisableCSRF: true},
		transport.NewBookHandler(service.NewBookService(bookStore, reviewStore), auditStore),
		transport.NewUserHandler(service.NewUserService(userStore)),
		transport.NewAuthHandler(
			service.NewAuthService(store.NewRunner(testDB), userStore, resetTokenStore, mailSvc, sessions, bcrypt.MinCost, testResetTTL),
			false,
			testSessionTTL,
		),
		transport.NewOrderHandler(service.NewOrderService(orderStore, orderItemStore)),
		transport.NewOrderItemHandler(service.NewOrderItemService(store.NewRunner(testDB), orderItemStore, bookStore, orderStore), service.NewOrderService(orderStore, orderItemStore)),
		transport.NewReviewHandler(service.NewReviewService(reviewStore, orderStore)),
		transport.NewCouponHandler(service.NewCouponService(couponStore, couponRedemptionStore)),
		transport.NewPaymentMethodHandler(service.NewPaymentMethodService(paymentMethodStore)),
		transport.NewAddressHandler(service.NewAddressService(addressStore)),
		transport.NewCheckoutHandler(service.NewCheckoutService(runner, userStore)),
		transport.NewAdminHandler(service.NewAdminService(userStore, orderStore, bookStore, couponStore, paymentMethodStore, reviewStore, bcrypt.MinCost), auditStore),
		transport.NewUploadHandler(uploadDir),
		transport.NewBackupHandler(backup.New(testDB), backupDir, auditStore),
	)

	if err := seedTestAdmin(); err != nil {
		panic(err)
	}

	testServer = httptest.NewServer(handler)
	defer testServer.Close()

	code := m.Run()
	_ = os.RemoveAll(uploadDir)
	_ = os.RemoveAll(backupDir)
	os.Exit(code)
}

// expireResetToken backdates a reset token so the expiry rule can be exercised
// without waiting out the real TTL, which is 30 minutes.
//
// It is a test-only helper on purpose. Expiry is enforced inside the atomic
// Consume query, so there is no production code path that can be coaxed into
// expiring a token early; the only way to test the rule is to reach past the
// service and move the column, which is what this does.
func expireResetToken(t *testing.T, email, code string) {
	t.Helper()

	tag, err := testDB.ExecContext(t.Context(),
		`UPDATE password_reset_tokens SET expires_at = DATE_SUB(NOW(), INTERVAL 1 MINUTE)
		 WHERE token = ? AND user_id = (SELECT id FROM users WHERE email = ?)`,
		code, email)
	if err != nil {
		t.Fatalf("expire reset token: %v", err)
	}
	if rows, _ := tag.RowsAffected(); rows == 0 {
		t.Fatalf("expire reset token: no row matched code %q for %s", code, email)
	}
}

// uniqueTestEmail hands out an address no other test has used.
//
// It used to be time.Now().UnixNano()%1000000, which collides: the modulus is
// applied to a nanosecond timestamp, so two registrations inside the same
// microsecond got the same address, the second insert hit the unique index, and
// the test failed with a 500 from a duplicate key that had nothing to do with
// what it was checking. A counter cannot collide, and the pid keeps two
// concurrent runs of the suite apart.
var emailSeq atomic.Uint64

func uniqueTestEmail() string {
	return fmt.Sprintf("user_%d_%d@test.com", os.Getpid(), emailSeq.Add(1))
}

// The admin account used by the suite.
//
// Registration cannot produce an admin, which is the whole point of the fix, so
// the harness seeds one directly. Doing it here rather than per test keeps a
// single account whose id and role the tests can rely on, and it is the only
// place the suite reaches past the API to write privileged state.
const (
	testAdminEmail    = "admin_suite@dvbs.test"
	testAdminPassword = "admin-suite-password-1"
)

var testAdminToken string

// It returns an error rather than taking a *testing.T because it runs from
// TestMain, before any test exists.
func seedTestAdmin() error {
	hash, err := bcrypt.GenerateFromPassword([]byte(testAdminPassword), bcrypt.MinCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}

	_, err = testDB.ExecContext(context.Background(), `
		INSERT INTO users (first_name, last_name, email, password_hash, role, balance_cents)
		VALUES ('Admin', 'Suite', ?, ?, ?, 1000000)
		ON DUPLICATE KEY UPDATE password_hash = VALUES(password_hash), role = VALUES(role)`,
		testAdminEmail, string(hash), model.RoleAdmin)
	if err != nil {
		return fmt.Errorf("seed admin: %w", err)
	}
	return nil
}

// adminToken signs in as the seeded admin.
//
// Cached because several tests create books and the suite hashes a bcrypt on
// every login, and because one shared token keeps the assertions readable: these
// tests are about who is allowed to write to the catalogue, not about logging in.
func adminToken(t *testing.T) string {
	t.Helper()

	if testAdminToken != "" {
		return testAdminToken
	}
	testAdminToken = loginAndGetToken(t, testAdminEmail, testAdminPassword)
	return testAdminToken
}

// createBookAsAdmin posts a book with an administrator credential, which is what
// the catalogue write routes now require.
func createBookAsAdmin(t *testing.T, book map[string]any) (int, *http.Response) {
	t.Helper()

	resp, out := doJSONAuth(t, http.MethodPost, "/books", adminToken(t), book)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /books as admin: status %d, want 201 (body %v)", resp.StatusCode, out)
	}
	created, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("POST /books: body is %T, want an object", out)
	}
	return int(idFrom(t, created, "id")), resp
}

// setupUsersAndToken registers two unrelated customers and returns the
// victim's id, the attacker's id and the attacker's bearer token.
//
// Almost every authorisation test needs the same arrangement: a resource that
// belongs to somebody, and a token that does not. Building it once means no test
// can accidentally skip the ownership check by forgetting to authenticate.
func setupUsersAndToken(t *testing.T) (victimID int64, attackerID int64, attackerToken string) {
	t.Helper()

	victimEmail := uniqueTestEmail()
	victimID, _ = registerUser(t, victimEmail, "victim-password-1", "")

	attackerEmail := uniqueTestEmail()
	attackerID, _ = registerUser(t, attackerEmail, "attackerpass", "")
	attackerToken = loginAndGetToken(t, attackerEmail, "attackerpass")

	return victimID, attackerID, attackerToken
}

// requireCheckConstraintsUnenforced documents a platform truth of TiDB Cloud
// Starter, where this suite runs: the server sits behind
// tidb_enable_check_constraint = 0 and there is no way to turn it on — the
// variable is GLOBAL-only and the SET is refused for lack of SUPER /
// SYSTEM_VARIABLES_ADMIN. The CHECK constraints in the migration are therefore
// declarative documentation; the service is the only enforcer, and that is what
// the HTTP-level assertions check. This helper makes the skip reason explicit
// and verified instead of written once in a comment and silently forgotten.
func requireCheckConstraintsUnenforced(t *testing.T) {
	t.Helper()

	var enabled int
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT @@tidb_enable_check_constraint`).Scan(&enabled); err != nil {
		t.Fatalf("read tidb_enable_check_constraint: %v", err)
	}
	if enabled != 0 {
		t.Fatalf("tidb_enable_check_constraint = %d, want 0: the platform enforces CHECKs", enabled)
	}
}

// insertOrderDirect writes an order row straight into the database, bypassing
// the API.
//
// Several tests need an order belonging to a particular customer — a victim whose
// order an attacker then tries to reach — and customers cannot create orders
// themselves any more: POST /orders is admin-only and records an order for
// whoever holds the admin session, while /checkout is the route that creates one
// for the caller. Seeding the row is therefore the only honest way to set this
// up.
//
// It is direct SQL rather than a fixture file because the subject of these tests
// is the API's behaviour, not the shape of the data.
func insertOrderDirect(t *testing.T, userID int64, status string, totalCents int64) int64 {
	t.Helper()

	res, err := testDB.ExecContext(t.Context(),
		`INSERT INTO orders (user_id, status, subtotal_cents, discount_cents, total_cents)
		 VALUES (?, ?, ?, 0, ?)`,
		userID, status, totalCents, totalCents)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("insert order last insert id: %v", err)
	}
	return id
}

// insertOrderItemDirect writes an order line, for the same reason as
// insertOrderDirect.
func insertOrderItemDirect(t *testing.T, orderID, bookID int64, quantity int, unitPriceCents int64) int64 {
	t.Helper()

	res, err := testDB.ExecContext(t.Context(),
		`INSERT INTO order_items (order_id, book_id, quantity, unit_price_cents)
		 VALUES (?, ?, ?, ?)`,
		orderID, bookID, quantity, unitPriceCents)
	if err != nil {
		t.Fatalf("insert order item: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("insert order item last insert id: %v", err)
	}
	return id
}

// creditBalance tops a balance up. A balance is no longer settable through the
// API — the field is not in UpdateUserRequest, and that absence is the fix for
// #18 — so this direct update is the only way a test can afford a purchase.
func creditBalance(t *testing.T, userID int64, cents int64) {
	t.Helper()

	tag, err := testDB.ExecContext(t.Context(),
		`UPDATE users SET balance_cents = balance_cents + ? WHERE id = ?`, cents, userID)
	if err != nil {
		t.Fatalf("credit balance: %v", err)
	}
	if rows, _ := tag.RowsAffected(); rows != 1 {
		t.Fatalf("credit balance: user %d does not exist", userID)
	}
}

func balanceOf(t *testing.T, userID int64) int64 {
	t.Helper()

	var cents int64
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT balance_cents FROM users WHERE id = ?`, userID).Scan(&cents); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return cents
}

func orderTotalOf(t *testing.T, orderID int64) int64 {
	t.Helper()

	var cents int64
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT total_cents FROM orders WHERE id = ?`, orderID).Scan(&cents); err != nil {
		t.Fatalf("read order total: %v", err)
	}
	return cents
}

func couponUsedCount(t *testing.T, code string) int {
	t.Helper()

	var used int
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT used_count FROM coupons WHERE code = ?`, code).Scan(&used); err != nil {
		t.Fatalf("read coupon used_count: %v", err)
	}
	return used
}

func couponRedemptionCount(t *testing.T, code string) int {
	t.Helper()

	var n int
	err := testDB.QueryRowContext(t.Context(),
		`SELECT count(*) FROM coupon_redemptions r JOIN coupons c ON c.id = r.coupon_id WHERE c.code = ?`,
		code).Scan(&n)
	if err != nil {
		t.Fatalf("count redemptions: %v", err)
	}
	return n
}

func orderPath(id int64) string {
	return "/orders/" + strconv.FormatInt(id, 10)
}

func userPath(id int64) string {
	return "/users/" + strconv.FormatInt(id, 10)
}

// loginAsUser authenticates as a user the test seeded, by looking its email up.
func loginAsUser(t *testing.T, userID int64, password string) string {
	t.Helper()
	return loginAndGetToken(t, emailOf(t, userID), password)
}

// stockOf reads a book's remaining stock, for assertions about reservations.
func stockOf(t *testing.T, bookID int64) int {
	t.Helper()

	var stock int
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT stock FROM books WHERE id = ?`, bookID).Scan(&stock); err != nil {
		t.Fatalf("read stock of book %d: %v", bookID, err)
	}
	return stock
}

// emailOf reads an account's email out of the database, so a test can log in as
// a user it registered without threading the address through every helper.
func emailOf(t *testing.T, userID int64) string {
	t.Helper()

	var email string
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
		t.Fatalf("read email of user %d: %v", userID, err)
	}
	return email
}

// createCouponAsAdmin mints a coupon through the admin route, so the test
// exercises the same path an administrator would. Customers cannot reach it —
// that restriction is the fix for #11.
func createCouponAsAdmin(t *testing.T, code string, percent, maxUses int) map[string]any {
	t.Helper()

	resp, out := doJSONAuth(t, http.MethodPost, "/coupons", adminToken(t), map[string]any{
		"code":             code,
		"discount_percent": percent,
		"max_uses":         maxUses,
		"expires_at":       time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create coupon %q: status %d, want 201 (body=%v)", code, resp.StatusCode, out)
	}
	return out.(map[string]any)
}

// insertCouponDirect seeds a coupon row that the API would refuse to create, such
// as one that is already expired. The expiry rule has to be testable, and an
// endpoint that could mint an expired code would not be a fix.
func insertCouponDirect(t *testing.T, code string, percent, maxUses int, expiresAt time.Time) error {
	t.Helper()

	_, err := testDB.ExecContext(t.Context(),
		`INSERT INTO coupons (code, discount_percent, max_uses, used_count, expires_at)
		 VALUES (?, ?, ?, 0, ?)`,
		code, percent, maxUses, expiresAt)
	return err
}

// setBalance puts a balance at an absolute value.
//
// creditBalance cannot be used for this: users.balance_cents has a DEFAULT of
// 1000, so a freshly registered account is not at zero and an "add N" helper
// would leave every assertion with an offset it has to know about.
func setBalance(t *testing.T, userID int64, cents int64) {
	t.Helper()

	// Existence is checked on its own, because the UPDATE cannot tell "no such
	// user" apart from "already at that value": TiDB reports RowsAffected() == 0
	// for an UPDATE that changes nothing, and a fresh account already sits at
	// 1000 cents. The old check read zero rows as a missing user and broke every
	// test that wanted to set the default balance explicitly.
	var exists int
	if err := testDB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM users WHERE id = ?`, userID).Scan(&exists); err != nil {
		t.Fatalf("check user: %v", err)
	}
	if exists == 0 {
		t.Fatalf("set balance: user %d does not exist", userID)
	}
	if _, err := testDB.ExecContext(t.Context(),
		`UPDATE users SET balance_cents = ? WHERE id = ?`, cents, userID); err != nil {
		t.Fatalf("set balance: %v", err)
	}
}

// TestWithTestTransactionRollsBack verifies that the helper actually isolates
// writes by inserting a row inside the transaction and confirming it disappears
// after the function returns.
func TestWithTestTransactionRollsBack(t *testing.T) {
	t.Parallel()

	// First, confirm the table is in a known state from the test's perspective
	// by counting via a direct pool query (outside any transaction).
	var before int64
	err := testDB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM users WHERE email = 'rollback-test@example.com'`).Scan(&before)
	if err != nil {
		t.Fatalf("count before: %v", err)
	}

	WithTestTransaction(t, func(q *db.Queries) {
		// Inside the transaction, insert a user
		_, err := q.CreateUser(context.Background(), db.CreateUserParams{
			FirstName:    "Rollback",
			LastName:     "Test",
			Email:        "rollback-test@example.com",
			PasswordHash: "hash",
			Role:         "customer",
		})
		if err != nil {
			t.Fatalf("CreateUser inside tx: %v", err)
		}

		// The row is visible inside the same transaction
		var inside int64
		inside, err = q.CountUsers(context.Background())
		if err != nil {
			t.Fatalf("CountUsers inside tx: %v", err)
		}
		// Note: CountUsers returns a single row with the count; the sqlc signature
		// returns (int64, error) so we don't use the scanner pattern here.
		// Just verify the insert didn't error.
		_ = inside
	})

	// After the function returns, the transaction has been rolled back.
	// The row should not exist.
	var after int64
	err = testDB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM users WHERE email = 'rollback-test@example.com'`).Scan(&after)
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("rollback failed: count before=%d, after=%d, want equal", before, after)
	}
}

// TestWithTestServerInTransactionRollsBack verifies that a request made
// through the transactional server is isolated: the created user does not
// persist after the transaction rolls back.
func TestWithTestServerInTransactionRollsBack(t *testing.T) {
	t.Parallel()

	var before int64
	err := testDB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM users WHERE email = 'tx-server-test@example.com'`).Scan(&before)
	if err != nil {
		t.Fatalf("count before: %v", err)
	}

	WithTestServerInTransaction(t, func(server *httptest.Server) {
		// Inside the transaction, make a request that creates a user
		body := `{"email":"tx-server-test@example.com","password":"pw1234567890","first_name":"Tx","last_name":"Server"}`
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/auth/register", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("register: status %d, want 201", resp.StatusCode)
		}
	})

	// After rollback, the user is gone
	var after int64
	err = testDB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM users WHERE email = 'tx-server-test@example.com'`).Scan(&after)
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("rollback failed: count before=%d, after=%d, want equal", before, after)
	}
}

// idFrom reads an identifier out of a decoded JSON body.
//
// Identifiers cross the wire **quoted**, and this is not a style choice. TiDB's
// AUTO_RANDOM hands out 19-digit ids such as 8646911284551502322, past the
// largest integer a float64 represents exactly (2^53), so a JSON number loses
// eight significant digits here and in every browser. Money is still a JSON
// number — only identifiers are strings, and that split is the whole point of
// the rule.
//
// A float64 here is a failure, not a fallback: it means the id was serialised as
// a number, so every consumer of that body is working with a rounded value, and
// quietly accepting it would let the bug back in.
func idFrom(t *testing.T, body any, key string) int64 {
	t.Helper()

	obj, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("idFrom: body is %T, want an object", body)
	}
	raw, present := obj[key]
	if !present {
		t.Fatalf("idFrom: body has no %q: %v", key, obj)
	}

	switch v := raw.(type) {
	case string:
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("idFrom: %q is not an integer identifier: %q", key, v)
		}
		return id
	case float64:
		t.Fatalf("idFrom: %q arrived as a JSON number (%v); identifiers must be "+
			"quoted with json:\",string\" or they lose precision past 2^53", key, v)
		return 0
	default:
		t.Fatalf("idFrom: %q is %T, want a quoted identifier", key, raw)
		return 0
	}
}
