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

type AccountSettingsMailer interface {
	SendEmailChangeCode(ctx context.Context, recipient, code string) error
	SendEmailChangedNotice(ctx context.Context, previousEmail, newEmail string) error
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
	Username           string
	UsernameNormalized string
	Email              string
	EmailNormalized    string
	PasswordHash       string
	PlayerID           *uuid.UUID
	EmailVerifiedAt    *time.Time
}

type PendingVerification struct {
	Email string
}

type AccountSettingsRecord struct {
	AccountID                         uuid.UUID
	Player                            *domain.Player
	UsernameNormalized                string
	CurrentEmail                      string
	EmailNormalized                   string
	PasswordHash                      string
	PendingEmail                      *string
	PendingEmailNormalized            *string
	EmailChangeCodeHash               string
	EmailChangeExpiresAt              *time.Time
	EmailChangeLastSentAt             *time.Time
	EmailChangeSendWindowStartedAt    *time.Time
	EmailChangeSendCount              int32
	EmailChangeAttemptWindowStartedAt *time.Time
	EmailChangeAttemptCount           int32
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
		normalizedLogin string,
		expectedPasswordHash string,
		token uuid.UUID,
		expiresAt time.Time,
	) (*domain.Player, error)
}

type SettingsRepository interface {
	LockAccountSettings(
		ctx context.Context,
		playerID uuid.UUID,
		sessionToken uuid.UUID,
		now time.Time,
	) (*AccountSettingsRecord, error)
	ChangeAccountUsername(
		ctx context.Context,
		record *AccountSettingsRecord,
		username string,
		normalizedUsername string,
	) error
	ChangeAccountPassword(
		ctx context.Context,
		record *AccountSettingsRecord,
		passwordHash string,
		sessionToken uuid.UUID,
		newSessionToken uuid.UUID,
		now time.Time,
		expiresAt time.Time,
	) (*domain.Player, error)
	StartEmailChange(
		ctx context.Context,
		record *AccountSettingsRecord,
		email string,
		normalizedEmail string,
		codeHash string,
		expiresAt time.Time,
		sentAt time.Time,
		sendWindowStartedAt time.Time,
		sendCount int32,
		attemptWindowStartedAt time.Time,
		attemptCount int32,
	) error
	RecordEmailChangeAttempt(
		ctx context.Context,
		record *AccountSettingsRecord,
		windowStartedAt time.Time,
		attemptCount int32,
	) error
	CancelEmailChange(ctx context.Context, record *AccountSettingsRecord) error
	ConfirmEmailChange(
		ctx context.Context,
		record *AccountSettingsRecord,
		sessionToken uuid.UUID,
		newSessionToken uuid.UUID,
		now time.Time,
		expiresAt time.Time,
	) (*domain.Player, error)
}
