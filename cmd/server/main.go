package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"dvbs/internal/backup"
	"dvbs/internal/config"
	"dvbs/internal/dataservice"
	"dvbs/internal/mailer"
	"dvbs/internal/middleware"
	"dvbs/internal/router"
	"dvbs/internal/service"
	"dvbs/internal/store"
	"dvbs/internal/store/db"
	"dvbs/internal/token"
	"dvbs/internal/transport"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	pool, err := config.NewDBPool(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	queries := db.New(pool)

	// The catalogue is served by the Data App, without a SQL fallback. The
	// stock movements stay on `queries`: they have to join the checkout
	// transaction, which an HTTP call cannot. DataServiceBookStore is a
	// composite of the two transports and is the only one that exists now.
	dsClient, err := dataservice.New(cfg.DataService)
	if err != nil {
		log.Fatalf("data service: %v", err)
	}
	log.Printf("books backend: dataservice (%s)", cfg.DataService.BaseURL)
	bookStore := store.NewDataServiceBookStore(dsClient, queries)

	userStore := store.NewUserStore(queries)
	orderStore := store.NewOrderStore(queries)
	orderItemStore := store.NewOrderItemStore(queries)
	reviewStore := store.NewReviewStore(queries)
	couponStore := store.NewCouponStore(queries)
	couponRedemptionStore := store.NewCouponRedemptionStore(queries)
	paymentMethodStore := store.NewPaymentMethodStore(queries)
	addressStore := store.NewAddressStore(queries)
	resetTokenStore := store.NewPasswordResetTokenStore(queries)
	mailSvc := mailer.From(cfg.SMTP)

	sessions, err := token.NewManager(cfg.JWTSecret, cfg.SessionMaxAge)
	if err != nil {
		log.Fatalf("invalid session configuration: %v", err)
	}

	runner := store.NewRunner(pool)
	auditStore := store.NewAuditStore(queries)

	// The two operating roles cannot write to the catalogue without the handler
	// remembering it, or every change would happen off the record.
	bookHandler := transport.NewBookHandler(service.NewBookService(bookStore, reviewStore), auditStore)
	userHandler := transport.NewUserHandler(service.NewUserService(userStore))
	authService := service.NewAuthService(runner, userStore, resetTokenStore, mailSvc, sessions, cfg.BcryptCost, cfg.ResetTokenTTL)
	authHandler := transport.NewAuthHandler(authService, cfg.CookieSecure, cfg.SessionMaxAge)
	orderService := service.NewOrderService(orderStore, orderItemStore)
	orderHandler := transport.NewOrderHandler(orderService)
	checkoutHandler := transport.NewCheckoutHandler(service.NewCheckoutService(runner, userStore))
	orderItemHandler := transport.NewOrderItemHandler(service.NewOrderItemService(runner, orderItemStore, bookStore, orderStore), orderService)
	reviewHandler := transport.NewReviewHandler(service.NewReviewService(reviewStore, orderStore))
	couponHandler := transport.NewCouponHandler(service.NewCouponService(couponStore, couponRedemptionStore))
	paymentMethodHandler := transport.NewPaymentMethodHandler(service.NewPaymentMethodService(paymentMethodStore))
	addressHandler := transport.NewAddressHandler(service.NewAddressService(addressStore))
	adminHandler := transport.NewAdminHandler(service.NewAdminService(userStore, orderStore, bookStore, couponStore, paymentMethodStore, reviewStore, cfg.BcryptCost), auditStore)
	uploadHandler := transport.NewUploadHandler(cfg.UploadDir)

	if err := os.MkdirAll(cfg.BackupDir, 0o750); err != nil {
		log.Fatalf("backup directory: %v", err)
	}
	backupHandler := transport.NewBackupHandler(backup.New(pool), cfg.BackupDir, auditStore)

	var allowedOrigins []string
	if cfg.AppOrigin != "" {
		allowedOrigins = []string{cfg.AppOrigin}
	}

	deps := router.Deps{
		Sessions:       sessions,
		Resolver:       userStore,
		CookieSecure:   cfg.CookieSecure,
		AllowedOrigins: allowedOrigins,
		TrustedProxies: cfg.TrustedProxies,
		AuthLimit: middleware.NewRateLimit(middleware.RateLimitConfig{
			Name:      "auth",
			Capacity:  cfg.AuthRateBurst,
			PerMinute: cfg.AuthRatePerMinute,
		}),
		UploadLimit: middleware.NewRateLimit(middleware.RateLimitConfig{
			Name:      "uploads",
			Capacity:  cfg.UploadRateBurst,
			PerMinute: cfg.UploadRatePerMinute,
		}),
	}

	// The whole API lives under /api so that a static host (Netlify) can proxy a
	// single prefix to this server while the SPA owns every other path. Without
	// the prefix, a page route like /admin collides with an API route and needs
	// an Accept-header hack in the reverse proxy to tell them apart.
	api := router.New(deps, bookHandler, userHandler, authHandler, orderHandler, orderItemHandler, reviewHandler, couponHandler, paymentMethodHandler, addressHandler, checkoutHandler, adminHandler, uploadHandler, backupHandler)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           http.StripPrefix("/api", api),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("server listening on port %s", cfg.Port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
