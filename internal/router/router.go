package router

import (
	"net/http"

	"dvbs/internal/middleware"
	"dvbs/internal/model"
	"dvbs/internal/token"
	"dvbs/internal/transport"
)

// Deps carries the pieces the middleware chain needs, so the handler list does
// not have to grow another positional argument every time authentication or CSRF
// changes shape.
type Deps struct {
	Sessions     *token.Manager
	Resolver     middleware.SessionResolver
	CookieSecure bool

	// AllowedOrigins is the CORS allowlist. Empty means same-origin only.
	AllowedOrigins []string
	// TrustedProxies are the peer addresses whose X-Forwarded-For is believed,
	// for the rate limiter's per-IP buckets.
	TrustedProxies []string
	// AuthLimit caps the credential endpoints. Nil disables it.
	AuthLimit *middleware.RateLimit
	// UploadLimit caps image uploads. Nil disables it.
	UploadLimit *middleware.RateLimit
	// Done stops the background janitors. Nil means they run until the process
	// exits, which is what a server wants; a test passes a channel it closes.
	Done <-chan struct{}
	// DisableCSRF skips the CSRF middleware. Intended for tests.
	DisableCSRF bool
}

func New(deps Deps, bookHandler *transport.BookHandler, userHandler *transport.UserHandler, authHandler *transport.AuthHandler, orderHandler *transport.OrderHandler, orderItemHandler *transport.OrderItemHandler, reviewHandler *transport.ReviewHandler, couponHandler *transport.CouponHandler, paymentMethodHandler *transport.PaymentMethodHandler, addressHandler *transport.AddressHandler, checkoutHandler *transport.CheckoutHandler, adminHandler *transport.AdminHandler, uploadHandler *transport.UploadHandler, backupHandler *transport.BackupHandler) http.Handler {
	mux := http.NewServeMux()

	// Health check — public, no auth, no CSRF. Returns 200 if DB is reachable.
	if deps.Resolver != nil {
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
			if err := deps.Resolver.Ping(r.Context()); err != nil {
				http.Error(w, "unhealthy", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
	} else {
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
	}

	auth := middleware.Auth(deps.Sessions, deps.Resolver, middleware.SessionCookieName, deps.CookieSecure)
	// optAuth resolves a session when one is present and lets anonymous traffic
	// through. Fronting the public catalogue reads lets the operating history
	// attribute an auditor's consultations without turning the shop into a wall.
	optAuth := middleware.OptionalAuth(deps.Sessions, deps.Resolver, middleware.SessionCookieName)
	admin := func(h http.HandlerFunc) http.Handler { return auth(middleware.AdminOnly(h)) }

	// Today there is one administrator, and AdminOnly is enough. The college
	// brief opens the panel to two operating roles who are not administrators:
	// a capturista who manages the catalogue and an auditor who only reads it.
	// This is the vector they arrive on, so the middleware set is spelled out
	// here rather than reused through a helper: every route below reads like a
	// sentence with its own subjects.
	staff := func(h http.HandlerFunc) http.Handler {
		return auth(middleware.RequireRoles(model.RoleAdmin, model.RoleCapturista)(h))
	}
	panel := func(h http.HandlerFunc) http.Handler {
		return auth(middleware.RequireRoles(model.RoleAdmin, model.RoleCapturista, model.RoleAuditor)(h))
	}

	// The catalogue is public to read. Writing to it is not: an anonymous
	// POST /books was six of the original routes with no auth at all, which let
	// anyone rewrite the shop. Now the writing roles are the admin and the
	// capturista, and the auditor — who is there to inspect, not to change —
	// gets no write route at all.
	mux.Handle("GET /books", optAuth(http.HandlerFunc(bookHandler.List)))
	mux.Handle("GET /books/{id}", optAuth(http.HandlerFunc(bookHandler.Get)))
	mux.Handle("POST /books", staff(bookHandler.Create))
	mux.Handle("PUT /books/{id}", staff(bookHandler.Update))
	mux.Handle("DELETE /books/{id}", staff(bookHandler.Delete))

	mux.HandleFunc("GET /uploads/{name}", uploadHandler.Serve)
	mux.Handle("POST /uploads", auth(http.HandlerFunc(uploadHandler.Create)))

	// The four credential endpoints are where an unauthenticated attacker can
	// spend unbounded work: hashing a password is deliberately slow, so
	// /auth/login is a CPU amplifier, and /auth/forgot-password sends mail.
	// Both were open to any number of requests per second from any address.
	login := http.Handler(http.HandlerFunc(authHandler.Login))
	register := http.Handler(http.HandlerFunc(authHandler.Register))
	forgot := http.Handler(http.HandlerFunc(authHandler.ForgotPassword))
	reset := http.Handler(http.HandlerFunc(authHandler.ResetPassword))
	if deps.AuthLimit != nil {
		login = deps.AuthLimit.Middleware(login)
		register = deps.AuthLimit.Middleware(register)
		forgot = deps.AuthLimit.Middleware(forgot)
		reset = deps.AuthLimit.Middleware(reset)
	}
	mux.Handle("POST /auth/register", register)
	mux.Handle("POST /auth/login", login)
	mux.HandleFunc("POST /auth/logout", authHandler.Logout)
	mux.Handle("POST /auth/forgot-password", forgot)
	mux.Handle("POST /auth/reset-password", reset)

	// Unauthenticated on purpose: this is how a client with no cookie yet
	// obtains the token it must echo on every state-changing request.
	mux.HandleFunc("GET /auth/csrf", middleware.CSRFToken(deps.CookieSecure))

	// Global listings are admin-only. A customer has no business enumerating
	// every account or every order in the system, and the frontend uses
	// /users/me and the caller's own orders instead.
	mux.Handle("GET /users", admin(http.HandlerFunc(userHandler.List)))
	mux.Handle("GET /users/me", auth(http.HandlerFunc(userHandler.Me)))
	mux.Handle("GET /users/{id}", auth(http.HandlerFunc(userHandler.Get)))
	mux.Handle("PUT /users/{id}", auth(http.HandlerFunc(userHandler.Update)))
	mux.Handle("DELETE /users/{id}", auth(http.HandlerFunc(userHandler.Delete)))

	mux.Handle("GET /orders", admin(http.HandlerFunc(orderHandler.List)))
	// The caller's own history. Registered before /orders/{id} out of caution, not
	// necessity: the more specific literal pattern wins on its own.
	mux.Handle("GET /orders/me", auth(http.HandlerFunc(orderHandler.ListMine)))
	mux.Handle("GET /orders/{id}", auth(http.HandlerFunc(orderHandler.Get)))
	mux.Handle("PUT /orders/{id}", auth(http.HandlerFunc(orderHandler.Update)))
	mux.Handle("DELETE /orders/{id}", auth(http.HandlerFunc(orderHandler.Delete)))

	// POST /orders is not a customer route. A customer buys through /checkout,
	// and the only reason to reach this one is to record an order by hand, which
	// is an administrative act. It used to accept user_id and total from the
	// body, so any caller could open an order in another account's name and
	// record any sum as paid (#6, #7).
	mux.Handle("POST /orders", admin(http.HandlerFunc(orderHandler.Create)))

	mux.Handle("POST /checkout", auth(http.HandlerFunc(checkoutHandler.Create)))

	mux.Handle("GET /orders/{id}/items", auth(http.HandlerFunc(orderItemHandler.ListByOrder)))
	mux.Handle("POST /orders/{id}/items", auth(http.HandlerFunc(orderItemHandler.AddToOrder)))
	mux.Handle("GET /order-items/{id}", auth(http.HandlerFunc(orderItemHandler.Get)))
	mux.Handle("PUT /order-items/{id}", auth(http.HandlerFunc(orderItemHandler.Update)))
	mux.Handle("DELETE /order-items/{id}", auth(http.HandlerFunc(orderItemHandler.Delete)))

	mux.Handle("GET /books/{id}/reviews", auth(http.HandlerFunc(reviewHandler.ListByBook)))
	mux.Handle("POST /books/{id}/reviews", auth(http.HandlerFunc(reviewHandler.AddToBook)))
	mux.Handle("GET /reviews/{id}", auth(http.HandlerFunc(reviewHandler.Get)))
	mux.Handle("PUT /reviews/{id}", auth(http.HandlerFunc(reviewHandler.Update)))
	mux.Handle("DELETE /reviews/{id}", auth(http.HandlerFunc(reviewHandler.Delete)))

	// Reading the catalogue of codes is harmless; minting, editing and deleting
	// them is not. Every authenticated customer used to be able to create a
	// coupon with discount_percent 100 and redeem it on their own order (#11).
	mux.Handle("GET /coupons", auth(http.HandlerFunc(couponHandler.List)))
	mux.Handle("GET /coupons/code/{code}", auth(http.HandlerFunc(couponHandler.GetByCode)))
	mux.Handle("GET /coupons/applied", auth(http.HandlerFunc(couponHandler.Applied)))
	mux.Handle("DELETE /coupons/applied", auth(http.HandlerFunc(couponHandler.RemoveApplied)))
	mux.Handle("GET /coupons/{id}", auth(http.HandlerFunc(couponHandler.Get)))

	// POST /coupons/code used to spend the coupon and write to a process-local
	// map. It now quotes the discount against a cart total and spends nothing;
	// the coupon is spent by checkout, once, inside a transaction.
	mux.Handle("POST /coupons/code", auth(http.HandlerFunc(couponHandler.Preview)))

	mux.Handle("POST /coupons", admin(http.HandlerFunc(couponHandler.Create)))
	mux.Handle("PUT /coupons/{id}", admin(http.HandlerFunc(couponHandler.Update)))
	mux.Handle("DELETE /coupons/{id}", admin(http.HandlerFunc(couponHandler.Delete)))

	mux.Handle("GET /users/{id}/payment-methods", auth(http.HandlerFunc(paymentMethodHandler.ListByUser)))
	mux.Handle("POST /users/{id}/payment-methods", auth(http.HandlerFunc(paymentMethodHandler.AddToUser)))
	// There is no GET /payment-methods and no GET /addresses. Both returned
	// every row in the table to any authenticated caller, which for the first one
	// meant every stored card in the shop. The frontend lists by user id.
	mux.Handle("GET /payment-methods/{id}", auth(http.HandlerFunc(paymentMethodHandler.Get)))
	mux.Handle("PUT /payment-methods/{id}", auth(http.HandlerFunc(paymentMethodHandler.Update)))
	mux.Handle("DELETE /payment-methods/{id}", auth(http.HandlerFunc(paymentMethodHandler.Delete)))

	mux.Handle("GET /users/{id}/addresses", auth(http.HandlerFunc(addressHandler.ListByUser)))
	mux.Handle("POST /users/{id}/addresses", auth(http.HandlerFunc(addressHandler.AddToUser)))
	mux.Handle("GET /addresses/{id}", auth(http.HandlerFunc(addressHandler.Get)))
	mux.Handle("PUT /addresses/{id}", auth(http.HandlerFunc(addressHandler.Update)))
	mux.Handle("DELETE /addresses/{id}", auth(http.HandlerFunc(addressHandler.Delete)))

	mux.Handle("GET /admin/stats", admin(adminHandler.Stats))

	// The users interface: a management view of the whole directory, so none of
	// it is an ownership check — it is precisely what the admin is for. The
	// customers' own /users routes stay next to them, opened to the session
	// that owns the row.
	mux.Handle("GET /admin/users", admin(adminHandler.Users))
	mux.Handle("POST /admin/users", admin(adminHandler.Create))
	mux.Handle("GET /admin/users/{id}", admin(adminHandler.Get))
	mux.Handle("PUT /admin/users/{id}", admin(adminHandler.Update))
	// Baja and its mirror. Their numbering is unnatural on purpose: "baja" the
	// account defers nothing and returns 204, "activate" returns the row, and
	// reading POST /admin/users/{id} as "make it work again" reads better than
	// inventing a verb the panel never shows.
	mux.Handle("DELETE /admin/users/{id}", admin(adminHandler.Delete))
	mux.Handle("POST /admin/users/{id}/activate", admin(adminHandler.Activate))
	// Granting and revoking roles. Behind the same guard as the rest of the
	// panel, and AdminOnly reads the caller's role from the database rather than
	// from the session claim, so a forged token does not reach it.
	mux.Handle("PUT /admin/users/{id}/role", admin(adminHandler.SetRole))

	// The operating history. It is the one panel route the auditor and the
	// capturista can read besides the catalogue, because an auditor is paid to
	// look at trails and a capturista at their own ledger. Only the panels that
	// record it can read it back.
	mux.Handle("GET /admin/audit", panel(adminHandler.Audit))

	// The moderation queue. Admin-only because the rows carry author email
	// addresses, which the public book page has no use for.
	mux.Handle("GET /admin/reviews", admin(adminHandler.Reviews))

	// A full logical dump of the database. Admin-only and POST, and that is the
	// whole access control: the dump carries every password hash and every reset
	// token, so it must not be reachable by a customer, and it must not be a GET
	// that a link preview or a browser extension could set off by accident.
	// The backups interface — listing and downloading the dumps already taken —
	// is mounted next to it and shares the same guard.
	mux.Handle("POST /admin/backup", admin(http.HandlerFunc(backupHandler.Create)))
	mux.Handle("GET /admin/backups", admin(http.HandlerFunc(backupHandler.List)))
	mux.Handle("GET /admin/backups/{name}", admin(http.HandlerFunc(backupHandler.Download)))

	// Order matters. CSRF sits outside Auth because it must see the session
	// cookie that the browser attaches, and it must run before any handler acts
	// on the request.
	//
	// Auth is applied per route above, never to the mux as a whole. Wrapping the
	// mux would put the session requirement in front of /auth/login itself, so
	// nobody could ever obtain a session in the first place.
	// The rate limiter has to know which peers are allowed to speak for a
	// client before it keys its buckets.
	middleware.SetTrustedProxies(deps.TrustedProxies...)

	// Sweep the limiter's buckets on a ticker. Without this the map only loses
	// an entry when a rejected request happens to land on an old one, so a shop
	// that is being scanned grows one entry per source address and keeps it.
	// A closed channel stops the goroutine, which matters for tests and would
	// matter more for a graceful shutdown once there is one.
	for _, rl := range []*middleware.RateLimit{deps.AuthLimit, deps.UploadLimit} {
		rl.StartJanitor(0, deps.Done)
	}

	return wrapCSRF(deps, middleware.Logging(middleware.Recovery(
		middleware.CORS(deps.AllowedOrigins)(mux))))
}
func wrapCSRF(deps Deps, h http.Handler) http.Handler {
	if !deps.DisableCSRF {
		return middleware.CSRF(deps.CookieSecure)(h)
	}
	return h
}
