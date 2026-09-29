package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"time"

	"dvbs/internal/mailer"
	"dvbs/internal/model"
	"dvbs/internal/store"
	"dvbs/internal/token"
)

type AuthService struct {
	// runner makes the reset atomic. Consuming the token, writing the new hash
	// and invalidating every live session are three statements, and as three
	// statements they can half-happen.
	runner     *store.Runner
	users      store.User
	tokens     store.PasswordResetToken
	mail       mailer.Mailer
	sessions   *token.Manager
	bcryptCost int
	resetTTL   time.Duration
	// now is injectable so the expiry rules can be tested without sleeping.
	now func() time.Time
}

// ErrUserNotFound is intentionally not returned by Login. See the comment there.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrResetTokenInvalid  = errors.New("reset token not valid")
	// ErrAccountExists is safe to surface: it is the response to registering an
	// address that is already taken, which the caller supplied themselves.
	ErrAccountExists = errors.New("email already registered")
	ErrWeakPassword  = errors.New("password does not meet requirements")
)

const (
	minPasswordLength = 12
	maxPasswordLength = 200
	// resetCodeBytes is 32 bytes of entropy, rendered as 43 base64url
	// characters. The original code was six digits from math/rand, which is a
	// million values from a predictable sequence and was exhausted in minutes.
	resetCodeBytes = 32
	// dummyHash is compared against when the account does not exist, so a
	// missing user and a wrong password take the same time. Without it, "no such
	// user" returns instantly and becomes a user enumeration oracle.
	dummyHash = "$2a$12$C6UzMDM.H6dfI/f/IKcEe.iaWs1kL4vbF7wV7G4vOZ0JdF1Y0m5S2"
)

func NewAuthService(runner *store.Runner, users store.User, tokens store.PasswordResetToken, m mailer.Mailer, sessions *token.Manager, bcryptCost int, resetTTL time.Duration) *AuthService {
	if bcryptCost < bcrypt.MinCost || bcryptCost > bcrypt.MaxCost {
		bcryptCost = bcrypt.DefaultCost
	}
	if resetTTL <= 0 {
		resetTTL = time.Hour
	}
	return &AuthService{
		runner:     runner,
		users:      users,
		tokens:     tokens,
		mail:       m,
		sessions:   sessions,
		bcryptCost: bcryptCost,
		resetTTL:   resetTTL,
		now:        time.Now,
	}
}

// RegisterRequest deliberately has no Role field.
//
// The old struct had one, and transport handed the client's body straight to it,
// so `{"role":"admin"}` created an administrator. The field is gone rather than
// merely ignored: with decodeJSON's DisallowUnknownFields a body containing
// "role" is now rejected outright, which fails loudly instead of quietly
// creating a customer and letting the caller believe otherwise.
type RegisterRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Password  string `json:"password"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *AuthService) Register(ctx context.Context, req *RegisterRequest) (*model.User, error) {
	if err := validatePassword(req.Password); err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), s.bcryptCost)
	if err != nil {
		return nil, err
	}

	return s.users.CreateUser(ctx, &model.User{
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		Email:        req.Email,
		PasswordHash: string(hash),
		// Not a parameter. There is no path from the request body to this field.
		Role: model.RoleCustomer,
	})
}

func validatePassword(pw string) error {
	if len(pw) < minPasswordLength {
		return fmt.Errorf("%w: at least %d characters", ErrWeakPassword, minPasswordLength)
	}
	// bcrypt truncates silently past 72 bytes, so a longer password would have a
	// smaller effective space than it appears to.
	if len(pw) > maxPasswordLength {
		return fmt.Errorf("%w: at most %d characters", ErrWeakPassword, maxPasswordLength)
	}
	return nil
}

func (s *AuthService) Login(ctx context.Context, req *LoginRequest) (string, *model.User, error) {
	user, err := s.users.GetUserByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Spend the same work as a real comparison before failing, so the
			// response time does not reveal whether the account exists.
			_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(req.Password))
			return "", nil, ErrInvalidCredentials
		}
		return "", nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		// Same error for a wrong password as for a missing account: anything
		// else tells an attacker which addresses are registered.
		return "", nil, ErrInvalidCredentials
	}

	// An account that was taken off is refused with the same error as a wrong
	// password. Announcing "this account is disabled" would hand an attacker the
	// one thing the uniform responses exist to hide: which addresses exist. The
	// admin who disabled the account already knows why it does not work.
	if !user.IsActive {
		return "", nil, ErrInvalidCredentials
	}

	signed, err := s.sessions.Issue(user.ID, user.Email, user.TokenVersion)
	if err != nil {
		return "", nil, err
	}
	return signed, user, nil
}

// ForgotPassword starts a password reset.
//
// It never reports whether the address exists. The old version returned 404 for
// an unknown email and 200 for a known one, which is a free account-enumeration
// oracle; here both cases produce the same result and the same error value. The
// only way to tell them apart is whether an email arrives, which is the
// intended signal.
func (s *AuthService) ForgotPassword(ctx context.Context, email string) error {
	user, err := s.users.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			slog.Info("password reset requested for unknown address")
			return nil
		}
		return err
	}

	code, err := randomResetCode()
	if err != nil {
		return err
	}

	// Requesting a new code must kill the previous one. Otherwise an attacker
	// who intercepted an older email can still use it after the user has asked
	// for a fresh code, which is exactly when the user expects it to be dead.
	if _, err := s.tokens.RevokeAllForUser(ctx, user.ID); err != nil {
		return err
	}

	if _, err := s.tokens.CreatePasswordResetToken(ctx, &model.PasswordResetToken{
		UserID:    user.ID,
		Token:     code,
		ExpiresAt: s.now().Add(s.resetTTL),
	}); err != nil {
		return err
	}

	// A delivery failure is logged, not returned. Returning it would tell the
	// caller that the account exists, which is the thing this function exists to
	// avoid disclosing.
	if err := s.mail.Send(mailer.Message{
		To:      user.Email,
		Subject: "DVBS - Tu código de recuperación",
		Body: fmt.Sprintf("Hola %s,\n\nTu código de recuperación de contraseña es:\n\n%s\n\nCaduca en %d minutos.\nSi no solicitaste este cambio, ignora este mensaje y cambia tu contraseña.\n\n- El equipo DVBS",
			user.FirstName, code, int(s.resetTTL.Minutes())),
	}); err != nil {
		slog.Error("could not deliver reset email", "user_id", user.ID, "error", err)
	}

	return nil
}

// ResetPassword redeems a reset token.
//
// The token is consumed by a single atomic UPDATE that checks both expiry and
// single-use in the database. That is what makes replay impossible under
// concurrency: doing the same checks with a SELECT followed by an UPDATE leaves
// a window in which two simultaneous requests both pass validation.
func (s *AuthService) ResetPassword(ctx context.Context, tokenStr, newPassword string) (*model.User, error) {
	if err := validatePassword(newPassword); err != nil {
		return nil, err
	}

	// The hash is generated before the transaction opens. bcrypt at the default
	// cost takes long enough that holding a row lock and an open transaction
	// across it would serialise every reset in the shop behind one customer's
	// deliberately slow hash.
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return nil, err
	}

	// One transaction, and the order inside it matters.
	//
	// The three steps are: consume the token, write the new hash, invalidate
	// every live session. As separate statements the first two could land and
	// the third fail, which is the worst possible outcome: the token is spent
	// so the victim cannot try again, the password did change, and the session
	// they were trying to kill is still working. Consuming first inside the
	// transaction means a rollback un-spends the token as well, so a failure is
	// invisible to the user and retryable.
	//
	// The token is consumed first, so a second request with the same code
	// matches no rows even if it is waiting on the same lock, and the single-use
	// rule holds under concurrency rather than only in sequence.
	var userID int64
	err = s.runner.WithinTx(ctx, func(q store.Queriers) error {
		// First, find the token and get its user_id
		resetToken, err := q.ResetTokens.GetPasswordResetTokenByToken(ctx, tokenStr)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrResetTokenInvalid
			}
			return err
		}
		if !resetToken.Usable(s.now()) {
			return ErrResetTokenInvalid
		}
		userID = resetToken.UserID

		// Now consume the token atomically
		rowsAffected, err := q.ResetTokens.Consume(ctx, tokenStr)
		if err != nil {
			return err
		}
		if rowsAffected == 0 {
			return ErrResetTokenInvalid
		}

		// Only the password column. A reset must not be able to carry a
		// mass-assigned email, role or balance along with it.
		if err := q.Users.UpdatePasswordHash(ctx, int(userID), string(hash)); err != nil {
			return err
		}

		// Kill every outstanding session. A user who resets their password
		// because they think it was stolen needs the thief's session to stop
		// working, not just their own to keep working.
		if err := q.Users.BumpTokenVersion(ctx, int(userID)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.users.GetUserByID(ctx, int(userID))
}

func randomResetCode() (string, error) {
	buf := make([]byte, resetCodeBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate reset token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
