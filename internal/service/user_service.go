package service

import (
	"context"

	"dvbs/internal/model"
	"dvbs/internal/store"
)

type UserService struct {
	users store.User
}

func NewUserService(users store.User) *UserService {
	return &UserService{users: users}
}

func (s *UserService) ListUsers(ctx context.Context) ([]*model.User, error) {
	return s.users.GetAllUsers(ctx)
}

func (s *UserService) GetUser(ctx context.Context, id int) (*model.User, error) {
	return s.users.GetUserByID(ctx, id)
}

// UpdateUserRequest is the whole surface a user is allowed to change about
// themselves.
//
// The original signature took a model.User straight from the request body, which
// meant every field of the table was writable over HTTP: role and balance_cents
// came along for the ride. The store dutifully preserved the password hash, and
// the service dutifully wrote down an admin role because the client had asked
// for one.
//
// A dedicated DTO fixes it structurally rather than by remembering to zero a
// field afterwards: a field that is not in this struct cannot be set, so adding
// a column to users does not silently add a new way in.
//
// The fields are pointers so that "not mentioned" is distinguishable from
// "sent as empty". With plain strings, a client that sends only first_name would
// blank last_name and email, and blanking email runs straight into the unique
// constraint.
type UpdateUserRequest struct {
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	Email     *string `json:"email"`
}

func (s *UserService) UpdateUser(ctx context.Context, id int, req *UpdateUserRequest) (*model.User, error) {
	existing, err := s.users.GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Start from the stored row and overwrite only what was actually sent.
	update := *existing
	if req.FirstName != nil {
		update.FirstName = *req.FirstName
	}
	if req.LastName != nil {
		update.LastName = *req.LastName
	}
	if req.Email != nil {
		update.Email = *req.Email
	}
	// Role, BalanceCents, PasswordHash and TokenVersion are carried over from
	// existing untouched, not taken from the request.

	return s.users.UpdateUser(ctx, id, &update)
}

func (s *UserService) DeleteUser(ctx context.Context, id int) error {
	return s.users.DeleteUser(ctx, id)
}
