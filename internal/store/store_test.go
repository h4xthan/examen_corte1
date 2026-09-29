package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"dvbs/internal/model"
)

// These mirror the fix in internal/transport: a timestamp is not unique. Two
// rows created in the same nanosecond insert against the unique index and the
// test fails with a duplicate-key error that has nothing to do with what it was
// checking.
var uniqueCounter atomic.Uint64

func uniqueSuffix() string {
	return fmt.Sprintf("%d_%d", os.Getpid(), uniqueCounter.Add(1))
}

func uniqueEmail() string {
	return fmt.Sprintf("user_%s@test.com", uniqueSuffix())
}

func uniqueISBN() string {
	return fmt.Sprintf("I-%s", uniqueSuffix())
}

func newTestUser() *model.User {
	return &model.User{
		FirstName:    "Test",
		LastName:     "User",
		Email:        uniqueEmail(),
		PasswordHash: "hash",
		Role:         "customer",
	}
}

func TestUserStoreCRUD(t *testing.T) {
	store := NewUserStore(testQueries)

	user := newTestUser()
	created, err := store.CreateUser(t.Context(), user)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreateUser: expected generated ID")
	}

	got, err := store.GetUserByID(t.Context(), int(created.ID))
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Email != user.Email {
		t.Fatalf("GetUserByID: got email %q, want %q", got.Email, user.Email)
	}

	byEmail, err := store.GetUserByEmail(t.Context(), user.Email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if byEmail.ID != created.ID {
		t.Fatalf("GetUserByEmail: got ID %d, want %d", byEmail.ID, created.ID)
	}

	all, err := store.GetAllUsers(t.Context())
	if err != nil {
		t.Fatalf("GetAllUsers: %v", err)
	}
	if len(all) < 1 {
		t.Fatal("GetAllUsers: expected at least one user")
	}

	created.FirstName = "Updated"
	updated, err := store.UpdateUser(t.Context(), int(created.ID), created)
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.FirstName != "Updated" {
		t.Fatalf("UpdateUser: got %q, want %q", updated.FirstName, "Updated")
	}

	if err := store.DeleteUser(t.Context(), int(created.ID)); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
}

func TestBookStoreCRUD(t *testing.T) {
	store := NewBookStore(testQueries)

	book := &model.Book{
		Author:        "Author",
		Title:         "Title",
		Pages:         100,
		ISBN:          uniqueISBN(),
		PriceCents:    1999,
		Stock:         10,
		URLCoverImage: "https://example.com/cover.jpg",
	}

	created, err := store.CreateBook(t.Context(), book)
	if err != nil {
		t.Fatalf("CreateBook: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreateBook: expected generated ID")
	}

	got, err := store.GetBookByID(t.Context(), int(created.ID))
	if err != nil {
		t.Fatalf("GetBookByID: %v", err)
	}
	if got.URLCoverImage != book.URLCoverImage {
		t.Fatalf("GetBookByID: got %q, want %q", got.URLCoverImage, book.URLCoverImage)
	}

	all, err := store.GetAllBooks(t.Context())
	if err != nil {
		t.Fatalf("GetAllBooks: %v", err)
	}
	if len(all) < 1 {
		t.Fatal("GetAllBooks: expected at least one book")
	}

	created.PriceCents = 2500
	updated, err := store.UpdateBook(t.Context(), int(created.ID), created)
	if err != nil {
		t.Fatalf("UpdateBook: %v", err)
	}
	if updated.PriceCents != 2500 {
		t.Fatalf("UpdateBook: got %d, want 2500", updated.PriceCents)
	}

	if err := store.DeleteBook(t.Context(), int(created.ID)); err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
}

func TestCouponStoreCRUD(t *testing.T) {
	store := NewCouponStore(testQueries)

	coupon := &model.Coupon{
		Code:            fmt.Sprintf("CODE%s", uniqueSuffix()),
		DiscountPercent: 10,
		MaxUses:         5,
		ExpiresAt:       time.Now().Add(24 * time.Hour),
		UsedCount:       0,
	}

	created, err := store.CreateCoupon(t.Context(), coupon)
	if err != nil {
		t.Fatalf("CreateCoupon: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreateCoupon: expected generated ID")
	}

	got, err := store.GetCouponByID(t.Context(), int(created.ID))
	if err != nil {
		t.Fatalf("GetCouponByID: %v", err)
	}
	if got.Code != coupon.Code {
		t.Fatalf("GetCouponByID: got %q, want %q", got.Code, coupon.Code)
	}

	all, err := store.GetAllCoupons(t.Context())
	if err != nil {
		t.Fatalf("GetAllCoupons: %v", err)
	}
	if len(all) < 1 {
		t.Fatal("GetAllCoupons: expected at least one coupon")
	}

	created.DiscountPercent = 20
	updated, err := store.UpdateCoupon(t.Context(), int(created.ID), created)
	if err != nil {
		t.Fatalf("UpdateCoupon: %v", err)
	}
	if updated.DiscountPercent != 20 {
		t.Fatalf("UpdateCoupon: got %d, want 20", updated.DiscountPercent)
	}

	if err := store.DeleteCoupon(t.Context(), int(created.ID)); err != nil {
		t.Fatalf("DeleteCoupon: %v", err)
	}
}

func TestAddressStoreCRUD(t *testing.T) {
	userStore := NewUserStore(testQueries)
	store := NewAddressStore(testQueries)

	user, err := userStore.CreateUser(t.Context(), newTestUser())
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defer userStore.DeleteUser(t.Context(), int(user.ID))

	address := &model.Address{
		UserID: user.ID,
		Street: "Main St 123",
		City:   "Springfield",
		Zip:    "12345",
	}

	created, err := store.CreateAddress(t.Context(), address)
	if err != nil {
		t.Fatalf("CreateAddress: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreateAddress: expected generated ID")
	}

	got, err := store.GetAddressByID(t.Context(), int(created.ID))
	if err != nil {
		t.Fatalf("GetAddressByID: %v", err)
	}
	if got.Street != address.Street {
		t.Fatalf("GetAddressByID: got %q, want %q", got.Street, address.Street)
	}

	byUser, err := store.GetAddressesByUserID(t.Context(), int(user.ID))
	if err != nil {
		t.Fatalf("GetAddressesByUserID: %v", err)
	}
	if len(byUser) != 1 || byUser[0].ID != created.ID {
		t.Fatalf("GetAddressesByUserID: got %d addresses, want 1 with ID %d", len(byUser), created.ID)
	}

	all, err := store.GetAllAddresses(t.Context())
	if err != nil {
		t.Fatalf("GetAllAddresses: %v", err)
	}
	if len(all) < 1 {
		t.Fatal("GetAllAddresses: expected at least one address")
	}

	created.City = "Shelbyville"
	updated, err := store.UpdateAddress(t.Context(), int(created.ID), created)
	if err != nil {
		t.Fatalf("UpdateAddress: %v", err)
	}
	if updated.City != "Shelbyville" {
		t.Fatalf("UpdateAddress: got %q, want %q", updated.City, "Shelbyville")
	}

	if err := store.DeleteAddress(t.Context(), int(created.ID)); err != nil {
		t.Fatalf("DeleteAddress: %v", err)
	}
}

func TestOrderStoreCRUD(t *testing.T) {
	userStore := NewUserStore(testQueries)
	store := NewOrderStore(testQueries)

	user, err := userStore.CreateUser(t.Context(), newTestUser())
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defer userStore.DeleteUser(t.Context(), int(user.ID))

	order := &model.Order{
		UserID: user.ID,
		Status: "pending",
		// 99.50 as an integer count of cents. The literal used to be 99.50 in a
		// float64 field, which is the exact expression of #6: money in binary
		// floating point.
		TotalCents: 9950,
	}

	created, err := store.CreateOrder(t.Context(), order)
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreateOrder: expected generated ID")
	}
	if created.TotalCents != 9950 {
		t.Fatalf("CreateOrder: got total %v cents, want 9950", created.TotalCents)
	}

	got, err := store.GetOrderByID(t.Context(), int(created.ID))
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if got.Status != order.Status {
		t.Fatalf("GetOrderByID: got %q, want %q", got.Status, order.Status)
	}

	all, err := store.GetAllOrders(t.Context())
	if err != nil {
		t.Fatalf("GetAllOrders: %v", err)
	}
	if len(all) < 1 {
		t.Fatal("GetAllOrders: expected at least one order")
	}

	created.Status = "shipped"
	updated, err := store.UpdateOrder(t.Context(), int(created.ID), created)
	if err != nil {
		t.Fatalf("UpdateOrder: %v", err)
	}
	if updated.Status != "shipped" {
		t.Fatalf("UpdateOrder: got %q, want %q", updated.Status, "shipped")
	}

	if err := store.DeleteOrder(t.Context(), int(created.ID)); err != nil {
		t.Fatalf("DeleteOrder: %v", err)
	}
}

func TestOrderItemStoreCRUD(t *testing.T) {
	userStore := NewUserStore(testQueries)
	bookStore := NewBookStore(testQueries)
	orderStore := NewOrderStore(testQueries)
	store := NewOrderItemStore(testQueries)

	user, err := userStore.CreateUser(t.Context(), newTestUser())
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defer userStore.DeleteUser(t.Context(), int(user.ID))

	book, err := bookStore.CreateBook(t.Context(), &model.Book{
		Author:     "A",
		Title:      "B",
		Pages:      1,
		ISBN:       uniqueISBN(),
		PriceCents: 100,
		Stock:      5,
	})
	if err != nil {
		t.Fatalf("CreateBook: %v", err)
	}
	defer bookStore.DeleteBook(t.Context(), int(book.ID))

	order, err := orderStore.CreateOrder(t.Context(), &model.Order{UserID: user.ID, Status: "pending", TotalCents: 100})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	defer orderStore.DeleteOrder(t.Context(), int(order.ID))

	item := &model.OrderItem{
		OrderID:        order.ID,
		BookID:         book.ID,
		Quantity:       2,
		UnitPriceCents: 100,
	}

	created, err := store.CreateOrderItem(t.Context(), item)
	if err != nil {
		t.Fatalf("CreateOrderItem: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreateOrderItem: expected generated ID")
	}

	got, err := store.GetOrderItemByID(t.Context(), int(created.ID))
	if err != nil {
		t.Fatalf("GetOrderItemByID: %v", err)
	}
	if got.Quantity != 2 {
		t.Fatalf("GetOrderItemByID: got %d, want 2", got.Quantity)
	}

	byOrder, err := store.GetOrderItemsByOrderID(t.Context(), int(order.ID))
	if err != nil {
		t.Fatalf("GetOrderItemsByOrderID: %v", err)
	}
	if len(byOrder) != 1 {
		t.Fatalf("GetOrderItemsByOrderID: got %d items, want 1", len(byOrder))
	}

	all, err := store.GetAllOrderItems(t.Context())
	if err != nil {
		t.Fatalf("GetAllOrderItems: %v", err)
	}
	if len(all) < 1 {
		t.Fatal("GetAllOrderItems: expected at least one item")
	}

	created.Quantity = 3
	updated, err := store.UpdateOrderItem(t.Context(), int(created.ID), created)
	if err != nil {
		t.Fatalf("UpdateOrderItem: %v", err)
	}
	if updated.Quantity != 3 {
		t.Fatalf("UpdateOrderItem: got %d, want 3", updated.Quantity)
	}

	if err := store.DeleteOrderItem(t.Context(), int(created.ID)); err != nil {
		t.Fatalf("DeleteOrderItem: %v", err)
	}
}

func TestPaymentMethodStoreCRUD(t *testing.T) {
	userStore := NewUserStore(testQueries)
	store := NewPaymentMethodStore(testQueries)

	user, err := userStore.CreateUser(t.Context(), newTestUser())
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defer userStore.DeleteUser(t.Context(), int(user.ID))

	// There is no card number, no CVV and no holder here, and that is the test.
	// The store's CreatePaymentMethod takes brand and last4, so there is no
	// parameter through which a full card number could be persisted. The columns
	// no longer exist either, so this compiles only because the model cannot
	// express a PAN at all.
	pm := &model.PaymentMethod{
		UserID:      user.ID,
		Brand:       "visa",
		Last4:       "1111",
		ExpiryMonth: 12,
		ExpiryYear:  2028,
	}

	created, err := store.CreatePaymentMethod(t.Context(), pm)
	if err != nil {
		t.Fatalf("CreatePaymentMethod: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreatePaymentMethod: expected generated ID")
	}

	got, err := store.GetPaymentMethodByID(t.Context(), int(created.ID))
	if err != nil {
		t.Fatalf("GetPaymentMethodByID: %v", err)
	}
	if got.Last4 != pm.Last4 || got.Brand != pm.Brand {
		t.Fatalf("GetPaymentMethodByID: got %s/%s, want %s/%s", got.Brand, got.Last4, pm.Brand, pm.Last4)
	}
	if got.ExpiryMonth != 12 || got.ExpiryYear != 2028 {
		t.Fatalf("GetPaymentMethodByID: expiry %d/%d, want 12/2028", got.ExpiryMonth, got.ExpiryYear)
	}

	byUser, err := store.GetPaymentMethodsByUserID(t.Context(), int(user.ID))
	if err != nil {
		t.Fatalf("GetPaymentMethodsByUserID: %v", err)
	}
	if len(byUser) != 1 {
		t.Fatalf("GetPaymentMethodsByUserID: got %d, want 1", len(byUser))
	}

	// The card must not be visible to anyone else. This replaces an assertion
	// that the global listing returned a row, which was asserting the
	// capability rather than the property: the query it exercised is gone.
	other, err := userStore.CreateUser(t.Context(), newTestUser())
	if err != nil {
		t.Fatalf("CreateUser (second account): %v", err)
	}
	defer userStore.DeleteUser(t.Context(), int(other.ID))

	notMine, err := store.GetPaymentMethodsByUserID(t.Context(), int(other.ID))
	if err != nil {
		t.Fatalf("GetPaymentMethodsByUserID (second account): %v", err)
	}
	for _, m := range notMine {
		if m.UserID == user.ID {
			t.Fatalf("another account's card list contains user %d's card", user.ID)
		}
	}

	// The owner is not part of the update, so a card cannot be moved to another
	// account by editing it.
	created.ExpiryMonth = 3
	updated, err := store.UpdatePaymentMethod(t.Context(), int(created.ID), created)
	if err != nil {
		t.Fatalf("UpdatePaymentMethod: %v", err)
	}
	if updated.ExpiryMonth != 3 {
		t.Fatalf("UpdatePaymentMethod: expiry month %d, want 3", updated.ExpiryMonth)
	}
	if updated.UserID != user.ID {
		t.Fatalf("UpdatePaymentMethod: owner changed to %d, want %d", updated.UserID, user.ID)
	}

	if err := store.DeletePaymentMethod(t.Context(), int(created.ID)); err != nil {
		t.Fatalf("DeletePaymentMethod: %v", err)
	}
}

func TestPasswordResetTokenStoreCRUD(t *testing.T) {
	userStore := NewUserStore(testQueries)
	store := NewPasswordResetTokenStore(testQueries)

	user, err := userStore.CreateUser(t.Context(), newTestUser())
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defer userStore.DeleteUser(t.Context(), int(user.ID))

	tok := &model.PasswordResetToken{
		UserID:    user.ID,
		Token:     fmt.Sprintf("token_%d", time.Now().UnixNano()),
		ExpiresAt: time.Now().Add(time.Hour),
	}

	created, err := store.CreatePasswordResetToken(t.Context(), tok)
	if err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreatePasswordResetToken: expected generated ID")
	}

	got, err := store.GetPasswordResetTokenByID(t.Context(), int(created.ID))
	if err != nil {
		t.Fatalf("GetPasswordResetTokenByID: %v", err)
	}
	if got.Token != tok.Token {
		t.Fatalf("GetPasswordResetTokenByID: got %q, want %q", got.Token, tok.Token)
	}

	byToken, err := store.GetPasswordResetTokenByToken(t.Context(), tok.Token)
	if err != nil {
		t.Fatalf("GetPasswordResetTokenByToken: %v", err)
	}
	if byToken.ID != created.ID {
		t.Fatalf("GetPasswordResetTokenByToken: got ID %d, want %d", byToken.ID, created.ID)
	}

	all, err := store.GetAllPasswordResetTokens(t.Context())
	if err != nil {
		t.Fatalf("GetAllPasswordResetTokens: %v", err)
	}
	if len(all) < 1 {
		t.Fatal("GetAllPasswordResetTokens: expected at least one token")
	}

	created.ExpiresAt = time.Now().Add(2 * time.Hour)
	updated, err := store.UpdatePasswordResetToken(t.Context(), int(created.ID), created)
	if err != nil {
		t.Fatalf("UpdatePasswordResetToken: %v", err)
	}
	if !updated.ExpiresAt.After(tok.ExpiresAt) {
		t.Fatal("UpdatePasswordResetToken: expires_at not updated")
	}

	if err := store.DeletePasswordResetToken(t.Context(), int(created.ID)); err != nil {
		t.Fatalf("DeletePasswordResetToken: %v", err)
	}
}

func TestReviewStoreCRUD(t *testing.T) {
	userStore := NewUserStore(testQueries)
	bookStore := NewBookStore(testQueries)
	store := NewReviewStore(testQueries)

	user, err := userStore.CreateUser(t.Context(), newTestUser())
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defer userStore.DeleteUser(t.Context(), int(user.ID))

	book, err := bookStore.CreateBook(t.Context(), &model.Book{
		Author:     "A",
		Title:      "B",
		Pages:      1,
		ISBN:       uniqueISBN(),
		PriceCents: 100,
		Stock:      5,
	})
	if err != nil {
		t.Fatalf("CreateBook: %v", err)
	}
	defer bookStore.DeleteBook(t.Context(), int(book.ID))

	review := &model.Review{
		BookID:  book.ID,
		UserID:  user.ID,
		Rating:  5,
		Comment: "Great book",
	}

	created, err := store.CreateReview(t.Context(), review)
	if err != nil {
		t.Fatalf("CreateReview: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("CreateReview: expected generated ID")
	}

	got, err := store.GetReviewByID(t.Context(), int(created.ID))
	if err != nil {
		t.Fatalf("GetReviewByID: %v", err)
	}
	if got.Rating != 5 {
		t.Fatalf("GetReviewByID: got %d, want 5", got.Rating)
	}

	byBook, err := store.GetReviewsByBookID(t.Context(), int(book.ID))
	if err != nil {
		t.Fatalf("GetReviewsByBookID: %v", err)
	}
	if len(byBook) != 1 {
		t.Fatalf("GetReviewsByBookID: got %d, want 1", len(byBook))
	}

	all, err := store.GetAllReviews(t.Context())
	if err != nil {
		t.Fatalf("GetAllReviews: %v", err)
	}
	if len(all) < 1 {
		t.Fatal("GetAllReviews: expected at least one review")
	}

	created.Comment = "Updated comment"
	updated, err := store.UpdateReview(t.Context(), int(created.ID), created)
	if err != nil {
		t.Fatalf("UpdateReview: %v", err)
	}
	if updated.Comment != "Updated comment" {
		t.Fatalf("UpdateReview: got %q, want %q", updated.Comment, "Updated comment")
	}

	if err := store.DeleteReview(t.Context(), int(created.ID)); err != nil {
		t.Fatalf("DeleteReview: %v", err)
	}
}

// A DELETE that matches no rows is not an error in SQL, so a store whose query
// returns no row count cannot tell "deleted" from "there was nothing there".
// Every one of these stores used to answer 204 and the panel said "borrado" for
// rows that were never there, with no error anywhere to notice. sql.ErrNoRows
// is what the transport already turns into a 404.
func TestDeletingSomethingThatIsNotThereIsNotFound(t *testing.T) {
	// Past every id TiDB has issued so far, and not an id that exists.
	const absentID = 1 << 40

	t.Run("book", func(t *testing.T) {
		err := NewBookStore(testQueries).DeleteBook(t.Context(), absentID)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("DeleteBook of a missing book: got %v, want sql.ErrNoRows", err)
		}
	})

	t.Run("coupon", func(t *testing.T) {
		err := NewCouponStore(testQueries).DeleteCoupon(t.Context(), absentID)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("DeleteCoupon of a missing coupon: got %v, want sql.ErrNoRows", err)
		}
	})

	t.Run("user", func(t *testing.T) {
		err := NewUserStore(testQueries).DeleteUser(t.Context(), absentID)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("DeleteUser of a missing user: got %v, want sql.ErrNoRows", err)
		}
	})

	// The same delete that does match a row has to keep working, or this test
	// would be satisfied by a store that never deletes anything.
	t.Run("and a row that is there still goes", func(t *testing.T) {
		users := NewUserStore(testQueries)
		created, err := users.CreateUser(t.Context(), newTestUser())
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		if err := users.DeleteUser(t.Context(), int(created.ID)); err != nil {
			t.Fatalf("DeleteUser of an existing user: %v", err)
		}
		if _, err := users.GetUserByID(t.Context(), int(created.ID)); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetUserByID after delete: got %v, want sql.ErrNoRows", err)
		}
	})
}

// The column was in the schema and in the model the whole time; the row-to-model
// conversion just did not copy it. Nothing failed, because the zero time is a
// perfectly valid time.Time: it marshals to "0001-01-01T00:00:00Z" and the
// profile page and the admin user list showed a registration date of the year 1.
func TestTimestampsSurviveTheRowConversion(t *testing.T) {
	before := time.Now().Add(-2 * time.Second)

	t.Run("user", func(t *testing.T) {
		users := NewUserStore(testQueries)
		created, err := users.CreateUser(t.Context(), newTestUser())
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		got, err := users.GetUserByID(t.Context(), int(created.ID))
		if err != nil {
			t.Fatalf("GetUserByID: %v", err)
		}
		if got.CreatedAt.Before(before) {
			t.Fatalf("user created_at came back as %s, which is before the row was inserted (%s)",
				got.CreatedAt.UTC().Format(time.RFC3339), before.UTC().Format(time.RFC3339))
		}
		if err := users.DeleteUser(t.Context(), int(created.ID)); err != nil {
			t.Fatalf("DeleteUser: %v", err)
		}
	})

	t.Run("payment method", func(t *testing.T) {
		store := NewPaymentMethodStore(testQueries)
		users := NewUserStore(testQueries)
		owner, err := users.CreateUser(t.Context(), newTestUser())
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		created, err := store.CreatePaymentMethod(t.Context(), &model.PaymentMethod{
			UserID:      owner.ID,
			Brand:       "Visa",
			Last4:       "4242",
			ExpiryMonth: 12,
			ExpiryYear:  2030,
		})
		if err != nil {
			t.Fatalf("CreatePaymentMethod: %v", err)
		}
		if created.CreatedAt.Before(before) {
			t.Fatalf("payment method created_at came back as %s",
				created.CreatedAt.UTC().Format(time.RFC3339))
		}
		if err := store.DeletePaymentMethod(t.Context(), int(created.ID)); err != nil {
			t.Fatalf("DeletePaymentMethod: %v", err)
		}
		if err := users.DeleteUser(t.Context(), int(owner.ID)); err != nil {
			t.Fatalf("DeleteUser: %v", err)
		}
	})
}
