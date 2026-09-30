package account

import (
	"context"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

type Clock interface {
	Now() time.Time
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encodedHash string) (bool, error)
}

type VerificationMailer interface {
	SendVerification(ctx context.Context, recipient, verificationURL string) error
}

type PendingAccount struct {
	Username              string
	UsernameNormalized    string
	Email                 string
	EmailNormalized       string
	PasswordHash          string
	VerificationTokenHash []byte
	VerificationExpiresAt time.Time
	VerificationSentAt    time.Time
}

type LoginCredentials struct {
	Username        string
	PasswordHash    string
	PlayerID        *uuid.UUID
	EmailVerifiedAt *time.Time
}

type PendingVerification struct {
	Email string
}

type Repository interface {
	CreatePendingAccount(ctx context.Context, account PendingAccount) error
	FindLoginCredentials(ctx context.Context, normalizedLogin string) (*LoginCredentials, error)
	ReplacePendingVerification(
		ctx context.Context,
		normalizedEmail string,
		eligibleBefore time.Time,
		tokenHash []byte,
		expiresAt time.Time,
		sentAt time.Time,
	) (*PendingVerification, error)
	VerifyPendingAccount(ctx context.Context, tokenHash []byte, now time.Time) (*domain.Player, error)
	UpdateAccountPlayerSession(
		ctx context.Context,
		playerID uuid.UUID,
		token uuid.UUID,
		expiresAt time.Time,
	) (*domain.Player, error)
}
