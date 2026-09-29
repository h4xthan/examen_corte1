package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"golang.org/x/crypto/bcrypt"

	"dvbs/internal/config"
	"dvbs/internal/model"
	"dvbs/internal/store"
	store_db "dvbs/internal/store/db"
)

const (
	officialCouponCode = "DVBS20"
	adminEmail         = "admin@dvbs.net"
)

// seedBooks deja el catálogo EXACTO en los 200 libros ficticios:
// borra cualquier libro cuyo ISBN no pertenezca al catálogo (con limpieza de FKs)
// e inserta los que falten. Idempotente.
func seedBooks(ctx context.Context, books store.Store, db *sql.DB) {
	existing, err := books.GetAllBooks(ctx)
	if err != nil {
		log.Fatal(err)
	}

	catalog := buildFictionalBooks()
	inCatalog := make(map[string]bool, len(catalog))
	for _, b := range catalog {
		inCatalog[b.isbn] = true
	}

	// Eliminar libros ajenos al catálogo (p. ej. residuos de tests o seeds viejos).
	removed := 0
	for _, b := range existing {
		if inCatalog[b.ISBN] {
			continue
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM order_items WHERE book_id = ?`, b.ID); err != nil {
			log.Fatalf("failed to clear order_items for stray book %q: %v", b.ISBN, err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM reviews WHERE book_id = ?`, b.ID); err != nil {
			log.Fatalf("failed to clear reviews for stray book %q: %v", b.ISBN, err)
		}
		if err := books.DeleteBook(ctx, int(b.ID)); err != nil {
			log.Fatalf("failed to remove stray book %q: %v", b.ISBN, err)
		}
		removed++
	}
	if removed > 0 {
		fmt.Printf("removed %d books outside the fictional catalog\n", removed)
	}

	// Insertar los faltantes.
	existingISBNs := make(map[string]bool, len(existing))
	for _, b := range existing {
		existingISBNs[b.ISBN] = true
	}
	inserted := 0
	for _, b := range catalog {
		if existingISBNs[b.isbn] {
			continue
		}
		created, err := books.CreateBook(ctx, &model.Book{
			Author:     b.author,
			Title:      b.title,
			Pages:      b.pages,
			ISBN:       b.isbn,
			PriceCents: b.priceCents,
			Stock:      b.stock,
		})
		if err != nil {
			log.Fatalf("failed to seed %q: %v", b.title, err)
		}
		inserted++
		_ = created
	}
	fmt.Printf("catalog ready: %d fictional books total (%d inserted, %d removed this run)\n", len(catalog), inserted, removed)
}

func seedOfficialCoupon(ctx context.Context, coupons store.Coupon) {
	all, err := coupons.GetAllCoupons(ctx)
	if err != nil {
		log.Fatal(err)
	}

	for _, c := range all {
		if c.Code != officialCouponCode {
			if err := coupons.DeleteCoupon(ctx, int(c.ID)); err != nil {
				log.Fatalf("failed to remove stray coupon %q: %v", c.Code, err)
			}
			fmt.Printf("removed stray coupon %q\n", c.Code)
		}
	}

	coupon, err := coupons.GetCouponByCode(ctx, officialCouponCode)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		created, err := coupons.CreateCoupon(ctx, &model.Coupon{
			Code:            officialCouponCode,
			DiscountPercent: 20,
			MaxUses:         1,
			ExpiresAt:       time.Now().Add(365 * 24 * time.Hour),
			UsedCount:       0,
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("seeded official coupon %s (20%% off, max_uses=1)\n", created.Code)
	case err != nil:
		log.Fatal(err)
	default:
		coupon.UsedCount = 0
		coupon.ExpiresAt = time.Now().Add(365 * 24 * time.Hour)
		if _, err := coupons.UpdateCoupon(ctx, int(coupon.ID), coupon); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("official coupon %s reset (used_count=0)\n", coupon.Code)
	}
}

func seedAdmin(ctx context.Context, users store.User, cfg *config.Config) {
	if cfg.AdminEmail == "" || cfg.AdminPassword == "" {
		fmt.Println("admin account skipped: set ADMIN_EMAIL and ADMIN_PASSWORD to create it")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), cfg.BcryptCost)
	if err != nil {
		log.Fatal(err)
	}

	existing, err := users.GetUserByEmail(ctx, cfg.AdminEmail)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := users.CreateUser(ctx, &model.User{
			FirstName:    "Admin",
			LastName:     cfg.AdminEmail,
			Email:        cfg.AdminEmail,
			PasswordHash: string(hash),
			Role:         model.RoleAdmin,
		}); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("created admin account %s\n", cfg.AdminEmail)
	case err != nil:
		log.Fatal(err)
	default:
		if existing.Role == model.RoleAdmin {
			fmt.Printf("admin account %s already exists, left untouched\n", existing.Email)
			return
		}
		existing.Role = model.RoleAdmin
		if _, err := users.UpdateUser(ctx, int(existing.ID), existing); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("promoted existing account %s to admin\n", existing.Email)
	}
}

func main() {
	fmt.Println("Starting seed command...")
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	fmt.Println("Config loaded")

	fmt.Println("Creating pool...")
	db, err := config.NewDBPool(cfg)
	if err != nil {
		log.Fatalf("Pool creation failed: %v", err)
	}
	fmt.Println("Pool created successfully")

	ctx := context.Background()

	// Test the connection
	var count int
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(&count)
	if err != nil {
		log.Fatalf("Test query failed: %v", err)
	}
	fmt.Printf("Test query successful: %d\n", count)

	queries := store_db.New(db)
	seedBooks(ctx, store.NewBookStore(queries), db)
	seedOfficialCoupon(ctx, store.NewCouponStore(queries))
	seedAdmin(ctx, store.NewUserStore(queries), cfg)
}
