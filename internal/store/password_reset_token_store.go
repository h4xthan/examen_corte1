package store

import (
	"context"
	"time"

	"dvbs/internal/model"
	"dvbs/internal/store/db"
)

type PasswordResetToken interface {
	GetAllPasswordResetTokens(ctx context.Context) ([]*model.PasswordResetToken, error)
	GetPasswordResetTokenByID(ctx context.Context, id int) (*model.PasswordResetToken, error)
	GetPasswordResetTokenByToken(ctx context.Context, token string) (*model.PasswordResetToken, error)
	CreatePasswordResetToken(ctx context.Context, token *model.PasswordResetToken) (*model.PasswordResetToken, error)
	UpdatePasswordResetToken(ctx context.Context, id int, token *model.PasswordResetToken) (*model.PasswordResetToken, error)
	DeletePasswordResetToken(ctx context.Context, id int) error
	RevokeAllForUser(ctx context.Context, userID int64) (int64, error)
	Consume(ctx context.Context, token string) (int64, error)
}

type PasswordResetTokenStore struct {
	q db.Querier
}

func NewPasswordResetTokenStore(q db.Querier) PasswordResetToken {
	return &PasswordResetTokenStore{q: q}
}

func passwordResetTokenFromDB(r db.PasswordResetToken) *model.PasswordResetToken {
	var usedAt *time.Time
	if r.UsedAt.Valid {
		usedAt = &r.UsedAt.Time
	}
	return &model.PasswordResetToken{
		ID:        int64(r.ID),
		UserID:    r.UserID,
		Token:     r.Token,
		ExpiresAt: r.ExpiresAt,
		UsedAt:    usedAt,
		CreatedAt: r.CreatedAt,
	}
}

func (s *PasswordResetTokenStore) GetAllPasswordResetTokens(ctx context.Context) ([]*model.PasswordResetToken, error) {
	rows, err := s.q.GetAllPasswordResetTokens(ctx)
	if err != nil {
		return nil, err
	}
	tokens := make([]*model.PasswordResetToken, 0, len(rows))
	for _, r := range rows {
		tokens = append(tokens, passwordResetTokenFromDB(r))
	}
	return tokens, nil
}

func (s *PasswordResetTokenStore) GetPasswordResetTokenByID(ctx context.Context, id int) (*model.PasswordResetToken, error) {
	r, err := s.q.GetPasswordResetTokenByID(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return passwordResetTokenFromDB(r), nil
}

func (s *PasswordResetTokenStore) GetPasswordResetTokenByToken(ctx context.Context, token string) (*model.PasswordResetToken, error) {
	r, err := s.q.GetPasswordResetTokenByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	return passwordResetTokenFromDB(r), nil
}

func (s *PasswordResetTokenStore) CreatePasswordResetToken(ctx context.Context, token *model.PasswordResetToken) (*model.PasswordResetToken, error) {
	res, err := s.q.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		UserID:    token.UserID,
		Token:     token.Token,
		ExpiresAt: token.ExpiresAt,
	})
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetPasswordResetTokenByID(ctx, int(id))
}

func (s *PasswordResetTokenStore) UpdatePasswordResetToken(ctx context.Context, id int, token *model.PasswordResetToken) (*model.PasswordResetToken, error) {
	if err := s.q.UpdatePasswordResetToken(ctx, db.UpdatePasswordResetTokenParams{
		ID:        int64(id),
		UserID:    token.UserID,
		Token:     token.Token,
		ExpiresAt: token.ExpiresAt,
	}); err != nil {
		return nil, err
	}
	return s.GetPasswordResetTokenByID(ctx, id)
}

func (s *PasswordResetTokenStore) DeletePasswordResetToken(ctx context.Context, id int) error {
	return s.q.DeletePasswordResetToken(ctx, int64(id))
}

func (s *PasswordResetTokenStore) RevokeAllForUser(ctx context.Context, userID int64) (int64, error) {
	return s.q.RevokePasswordResetTokensForUser(ctx, userID)
}

func (s *PasswordResetTokenStore) Consume(ctx context.Context, token string) (int64, error) {
	return s.q.ConsumePasswordResetToken(ctx, token)
}
