package service

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

// Errors the role change reports distinctly, so the handler can answer 400 and
// 409 rather than one opaque failure.
var (
	ErrRoleInvalid      = errors.New("role must be admin, capturista, auditor or customer")
	ErrSelfDemotion     = errors.New("an administrator cannot remove their own admin role")
	ErrSelfDeactivation = errors.New("an administrator cannot take their own account off")
)

type SystemStats struct {
	Users          int64 `json:"users"`
	Orders         int64 `json:"orders"`
	Books          int64 `json:"books"`
	Coupons        int64 `json:"coupons"`
	PaymentMethods int64 `json:"payment_methods"`
	// TotalOrderValueCents is an integer count of cents. The field was a
	// float64 accumulating a NUMERIC column, so a shop with many orders drifted
	// on the figure it reported as revenue.
	TotalOrderValueCents int64 `json:"total_order_value_cents"`
}

type AdminService struct {
	users   store.User
	orders  store.Order
	books   store.Store
	coupons store.Coupon
	cards   store.PaymentMethod
	reviews store.Review
	// bcryptCost is the same ladder the auth service uses: creating an account
	// from the panel hashes its password, and a deliberately cheap hash here
	// would not be a cost saving, it would be the one account an admin creates
	// that an attacker can crack in a hurry.
	bcryptCost int
}

func NewAdminService(users store.User, orders store.Order, books store.Store, coupons store.Coupon, cards store.PaymentMethod, reviews store.Review, bcryptCost int) *AdminService {
	return &AdminService{
		users:      users,
		orders:     orders,
		books:      books,
		coupons:    coupons,
		cards:      cards,
		reviews:    reviews,
		bcryptCost: bcryptCost,
	}
}

// ListReviews is the moderation queue.
//
// It lives here rather than on ReviewService because nothing outside the admin
// panel may call it: the rows carry author email addresses, which is the one
// piece of a review that the public book page does not need and must not have.
func (s *AdminService) ListReviews(ctx context.Context) ([]store.ModerationRow, error) {
	return s.reviews.ListForModeration(ctx)
}

// Stats is six aggregates.
//
// The previous version called GetAllUsers, GetAllOrders, GetAllBooks,
// GetAllCoupons and GetAllPaymentMethods, took len() of each slice and summed
// one field of the order slice. Asking the dashboard for six numbers therefore
// read and materialised every row of every table in the shop, and the cost grew
// with the size of the business. None of that was visible to the caller: the
// response was the same six integers either way.
func (s *AdminService) Stats(ctx context.Context) (*SystemStats, error) {
	stats := &SystemStats{}

	var err error
	if stats.Users, err = s.users.CountUsers(ctx); err != nil {
		return nil, err
	}
	if stats.Orders, err = s.orders.CountOrders(ctx); err != nil {
		return nil, err
	}
	if stats.Books, err = s.books.CountBooks(ctx); err != nil {
		return nil, err
	}
	if stats.Coupons, err = s.coupons.CountCoupons(ctx); err != nil {
		return nil, err
	}
	if stats.PaymentMethods, err = s.cards.CountPaymentMethods(ctx); err != nil {
		return nil, err
	}
	// Only orders whose money was actually kept count as revenue, and the filter
	// lives in the query. An order that was cancelled or refunded has had its
	// money returned; counting it would overstate the shop by exactly the refunds
	// it granted, which is the one number a shop is least able to be wrong
	// about.
	if stats.TotalOrderValueCents, err = s.orders.SumOrderTotals(ctx); err != nil {
		return nil, err
	}

	return stats, nil
}

func (s *AdminService) ListUsers(ctx context.Context) ([]*model.User, error) {
	return s.users.GetAllUsers(ctx)
}

// GetUser is the users interface's "consulta": one account at a time, so the
// panel can show who is behind a row before it moves that row.
func (s *AdminService) GetUser(ctx context.Context, id int) (*model.User, error) {
	return s.users.GetUserByID(ctx, id)
}

// SetUserRole grants or revokes a role.
//
// The route is admin-only, and AdminOnly re-reads the role from the database on
// every request rather than trusting the session, so this is an administrative
// action and not a privilege a customer can reach by editing a token.
//
// Two things are refused on purpose. An administrator cannot demote themselves,
// because a shop with no administrator has no way back in through the UI, and
// the row would have to be edited by hand in the database. And the role is
// validated against the closed set of model.IsRole: the capturista and the
// auditor are operating roles with a narrower reach than the admin, never a
// back door into the management tabs.
func (s *AdminService) SetUserRole(ctx context.Context, actorID int, targetID int, role string) (*model.User, error) {
	if !model.IsRole(role) {
		return nil, ErrRoleInvalid
	}
	if actorID == targetID && role != model.RoleAdmin {
		return nil, ErrSelfDemotion
	}
	if err := s.users.SetUserRole(ctx, targetID, role); err != nil {
		return nil, err
	}
	return s.users.GetUserByID(ctx, targetID)
}

// CreateUserRequest is the whole body of creating an account from the panel.
//
// Like RegisterRequest it is a closed DTO, so nothing else in the request — a
// balance, a version, an id — is addressable. The difference from self-service
// registration is the role: an admin may open an account as capturista or
// auditor, which registration never can, because both are operating roles that
// only an administrator should hand out.
type CreateUserRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	Role      string `json:"role"`
}

// CreateUser creates an account with the requested (defaulted) role.
func (s *AdminService) CreateUser(ctx context.Context, req *CreateUserRequest) (*model.User, error) {
	if strings.TrimSpace(req.FirstName) == "" || strings.TrimSpace(req.LastName) == "" || strings.TrimSpace(req.Email) == "" {
		return nil, errors.New("first_name, last_name and email are required")
	}
	if err := validatePassword(req.Password); err != nil {
		return nil, err
	}
	role := req.Role
	if role == "" {
		role = model.RoleCustomer
	}
	if !model.IsRole(role) {
		return nil, ErrRoleInvalid
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), s.bcryptCost)
	if err != nil {
		return nil, err
	}

	return s.users.CreateUser(ctx, &model.User{
		FirstName:    strings.TrimSpace(req.FirstName),
		LastName:     strings.TrimSpace(req.LastName),
		Email:        strings.TrimSpace(req.Email),
		PasswordHash: string(hash),
		Role:         role,
	})
}

// UpdateUserProfileRequest is what an admin may change about another account:
// the display name and the address. Role changes go through SetUserRole, and no
// budget, balance or password lives here.
type UpdateUserProfileRequest struct {
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	Email     *string `json:"email"`
}

// UpdateUserProfile edits another account's profile. The pointers distinguish
// "not mentioned" from "sent as empty", so a form that only changes the first
// name does not blank the email out from under the unique index.
//
// Changing an email keeps existing sessions attached to the old address in
// their claim, so the version is bumped and the user logs in again. It is the
// same mechanism a password reset uses, applied to a change the token's claims
// would otherwise contradict.
func (s *AdminService) UpdateUserProfile(ctx context.Context, targetID int, req *UpdateUserProfileRequest) (*model.User, error) {
	existing, err := s.users.GetUserByID(ctx, targetID)
	if err != nil {
		return nil, err
	}

	update := *existing
	emailChanged := false
	if req.FirstName != nil {
		update.FirstName = strings.TrimSpace(*req.FirstName)
	}
	if req.LastName != nil {
		update.LastName = strings.TrimSpace(*req.LastName)
	}
	if req.Email != nil {
		update.Email = strings.TrimSpace(*req.Email)
		emailChanged = update.Email != existing.Email
	}

	user, err := s.users.UpdateUser(ctx, targetID, &update)
	if err != nil {
		return nil, err
	}
	if emailChanged {
		if err := s.users.BumpTokenVersion(ctx, targetID); err != nil {
			return nil, err
		}
	}
	return user, nil
}

// SetUserActive takes an account off or puts it back.
//
// Taking it off revokes its sessions: the service bumps token_version in the
// same breath, which is the mechanism the session middleware checks on every
// request. Putting it back is just the bit, since a deactivated session is
// already dead and cannot be resurrected. An admin cannot take their own
// account off, for the same reason they cannot demote themselves: it is the
// one change that can leave a shop with no way back in through the UI.
func (s *AdminService) SetUserActive(ctx context.Context, actorID int, targetID int, active bool) (*model.User, error) {
	if actorID == targetID && !active {
		return nil, ErrSelfDeactivation
	}
	if !active {
		if err := s.users.BumpTokenVersion(ctx, targetID); err != nil {
			return nil, err
		}
	}
	if err := s.users.SetUserActive(ctx, targetID, active); err != nil {
		return nil, err
	}
	return s.users.GetUserByID(ctx, targetID)
}
