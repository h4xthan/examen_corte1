package store

import (
	"context"
	"database/sql"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type User interface {
	GetAllUsers(ctx context.Context) ([]*model.User, error)
	GetUserByID(ctx context.Context, id int) (*model.User, error)
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	CreateUser(ctx context.Context, user *model.User) (*model.User, error)
	UpdateUser(ctx context.Context, id int, user *model.User) (*model.User, error)
	DebitUserBalance(ctx context.Context, amount int64, userID int) error
	DeleteUser(ctx context.Context, id int) error
	UpdatePasswordHash(ctx context.Context, id int, hash string) error
	BumpTokenVersion(ctx context.Context, id int) error
	GetSessionState(ctx context.Context, id int) (*model.SessionInfo, error)
	ResolveSession(ctx context.Context, userID int64) (model.SessionInfo, error)
	GetUserBalanceForUpdate(ctx context.Context, id int) (int64, error)
	DebitUserBalanceIfSufficient(ctx context.Context, userID int, amount int64) (int64, error)
	CreditUserBalance(ctx context.Context, userID int, amount int64) error
	CountUsers(ctx context.Context) (int64, error)
	SetUserRole(ctx context.Context, id int, role string) error
	SetUserActive(ctx context.Context, id int, active bool) error
	Ping(ctx context.Context) error
}

type UserStore struct {
	q db.Querier
}

func NewUserStore(q db.Querier) User {
	return &UserStore{q: q}
}

func userFromDB(r db.User) *model.User {
	return &model.User{
		ID:           int64(r.ID),
		FirstName:    r.FirstName,
		LastName:     r.LastName,
		Email:        r.Email,
		PasswordHash: r.PasswordHash,
		Role:         r.Role,
		BalanceCents: r.BalanceCents,
		TokenVersion: int(r.TokenVersion),
		IsActive:     r.IsActive,
		// Dropping this one did not fail anything: the zero time marshals to
		// "0001-01-01T00:00:00Z" and both the profile page and the admin list
		// just showed a nonsensical date. The column was there the whole time.
		CreatedAt: r.CreatedAt,
	}
}

func (s *UserStore) GetAllUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := s.q.GetAllUsers(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]*model.User, 0, len(rows))
	for _, r := range rows {
		users = append(users, userFromDB(r))
	}
	return users, nil
}

func (s *UserStore) GetUserByID(ctx context.Context, id int) (*model.User, error) {
	r, err := s.q.GetUserByID(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return userFromDB(r), nil
}

func (s *UserStore) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	r, err := s.q.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return userFromDB(r), nil
}

func (s *UserStore) CreateUser(ctx context.Context, user *model.User) (*model.User, error) {
	res, err := s.q.CreateUser(ctx, db.CreateUserParams{
		FirstName:    user.FirstName,
		LastName:     user.LastName,
		Email:        user.Email,
		PasswordHash: user.PasswordHash,
		Role:         user.Role,
	})
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, int(id))
}

func (s *UserStore) UpdateUser(ctx context.Context, id int, user *model.User) (*model.User, error) {
	if err := s.q.UpdateUser(ctx, db.UpdateUserParams{
		ID:           int64(id),
		FirstName:    user.FirstName,
		LastName:     user.LastName,
		Email:        user.Email,
		PasswordHash: user.PasswordHash,
		Role:         user.Role,
		BalanceCents: user.BalanceCents,
	}); err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}

func (s *UserStore) DebitUserBalance(ctx context.Context, amount int64, userID int) error {
	return s.q.DebitUserBalance(ctx, db.DebitUserBalanceParams{
		BalanceCents: amount,
		ID:           int64(userID),
	})
}

// DeleteUser removes the account, or reports that there was nothing to remove.
// See CouponStore.DeleteCoupon for why the row count is the whole point.
func (s *UserStore) DeleteUser(ctx context.Context, id int) error {
	n, err := s.q.DeleteUser(ctx, int64(id))
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *UserStore) UpdatePasswordHash(ctx context.Context, id int, hash string) error {
	return s.q.UpdatePasswordHash(ctx, db.UpdatePasswordHashParams{
		ID:           int64(id),
		PasswordHash: hash,
	})
}

func (s *UserStore) BumpTokenVersion(ctx context.Context, id int) error {
	return s.q.BumpTokenVersion(ctx, int64(id))
}

func (s *UserStore) GetSessionState(ctx context.Context, id int) (*model.SessionInfo, error) {
	r, err := s.q.GetSessionState(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return &model.SessionInfo{
		UserID:       r.ID,
		Email:        r.Email,
		Role:         r.Role,
		TokenVersion: int(r.TokenVersion),
		IsActive:     r.IsActive,
	}, nil
}

// ResolveSession reads the account state that an authenticated request runs
// under. UserID is copied from the row, and the omission of it was not a
// cosmetic slip: middleware.Auth puts this struct into the request context, and
// middleware.UserIDFrom rejects a zero id, so leaving it unset made every
// handler that asks "who is calling" answer 401 to a request that had just been
// authenticated. The admin gate kept working because it reads the role, which
// was set. So the failure looked selective: the panel loaded, and checkout,
// reviews, orders, addresses and cards all refused a valid session.
func (s *UserStore) ResolveSession(ctx context.Context, userID int64) (model.SessionInfo, error) {
	r, err := s.q.GetSessionState(ctx, userID)
	if err != nil {
		return model.SessionInfo{}, err
	}
	return model.SessionInfo{
		UserID:       r.ID,
		Email:        r.Email,
		Role:         r.Role,
		TokenVersion: int(r.TokenVersion),
		IsActive:     r.IsActive,
	}, nil
}

func (s *UserStore) Ping(ctx context.Context) error {
	_, err := s.q.CountUsers(ctx)
	return err
}

func (s *UserStore) GetUserBalanceForUpdate(ctx context.Context, id int) (int64, error) {
	return s.q.GetUserBalanceForUpdate(ctx, int64(id))
}

func (s *UserStore) DebitUserBalanceIfSufficient(ctx context.Context, userID int, amount int64) (int64, error) {
	// The query takes the amount twice: once for the SET and once as the floor
	// the WHERE clause checks. The migration to MySQL gave the two a param
	// apiece, and leaving the second unset zeroed it — `balance_cents >= 0`
	// matches every row, so the affordability guard silently disappeared and a
	// checkout could drive the balance negative.
	return s.q.DebitUserBalanceIfSufficient(ctx, db.DebitUserBalanceIfSufficientParams{
		ID:             int64(userID),
		BalanceCents:   amount,
		BalanceCents_2: amount,
	})
}

func (s *UserStore) CreditUserBalance(ctx context.Context, userID int, amount int64) error {
	return s.q.CreditUserBalance(ctx, db.CreditUserBalanceParams{
		ID:           int64(userID),
		BalanceCents: amount,
	})
}

func (s *UserStore) CountUsers(ctx context.Context) (int64, error) {
	return s.q.CountUsers(ctx)
}

func (s *UserStore) SetUserRole(ctx context.Context, id int, role string) error {
	return s.q.SetUserRole(ctx, db.SetUserRoleParams{
		ID:   int64(id),
		Role: role,
	})
}

// SetUserActive sets or clears the account's enabled bit. It says nothing about
// sessions: the service decides to bump token_version when taking somebody off.
// Like DeleteUser, it reports a row that was not there, because a "baja" of a
// missing account is the same lie as the delete of one.
func (s *UserStore) SetUserActive(ctx context.Context, id int, active bool) error {
	n, err := s.q.SetUserActive(ctx, db.SetUserActiveParams{
		ID:       int64(id),
		IsActive: active,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
