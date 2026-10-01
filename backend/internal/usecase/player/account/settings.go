package account

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/google/uuid"
)

const (
	defaultEmailChangeTTL       = 10 * time.Minute
	emailChangeCooldown         = time.Minute
	emailChangeWindow           = time.Hour
	maxEmailChangeSends         = int32(5)
	maxEmailChangeAttempts      = int32(5)
	emailChangeCodeDigits       = 6
	maximumEmailChangeCodeValue = int64(1_000_000)
)

type SettingsConfig struct {
	SessionTTL time.Duration
}

type AccountSettingsUseCase struct {
	cfg         SettingsConfig
	tx          TransactionManager
	accounts    SettingsRepository
	passwords   PasswordHasher
	mailer      AccountSettingsMailer
	leaderboard playerusecase.LeaderboardInvalidator
	clock       Clock
}

type emailChangeVerificationFailure uint8

const (
	emailChangeVerificationInvalid emailChangeVerificationFailure = iota + 1
	emailChangeVerificationAttemptsExceeded
)

type emailChangeVerification struct {
	valid      bool
	failure    emailChangeVerificationFailure
	retryUntil time.Time
}

func (v emailChangeVerification) validationError(now time.Time) error {
	switch v.failure {
	case emailChangeVerificationAttemptsExceeded:
		return retryAfter(domain.ErrEmailChangeAttemptsExceeded, v.retryUntil, now)
	case emailChangeVerificationInvalid:
		return domain.ErrEmailChangeCodeInvalid
	default:
		return nil
	}
}

var _ inbound.AccountSettingsService = (*AccountSettingsUseCase)(nil)

func NewAccountSettingsUseCase(
	cfg SettingsConfig,
	tx TransactionManager,
	accounts SettingsRepository,
	passwords PasswordHasher,
	mailer AccountSettingsMailer,
	leaderboard playerusecase.LeaderboardInvalidator,
	clock Clock,
) (*AccountSettingsUseCase, error) {
	if tx == nil || accounts == nil || passwords == nil || clock == nil {
		return nil, domain.ErrInternal
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultSessionTTL
	}
	return &AccountSettingsUseCase{
		cfg:         cfg,
		tx:          tx,
		accounts:    accounts,
		passwords:   passwords,
		mailer:      mailer,
		leaderboard: leaderboard,
		clock:       clock,
	}, nil
}

func (u *AccountSettingsUseCase) GetAccountSettings(
	ctx context.Context,
	playerID uuid.UUID,
	sessionToken uuid.UUID,
) (*domain.AccountSettings, error) {
	now := u.clock.Now().UTC()
	var settings *domain.AccountSettings
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		record, err := u.accounts.LockAccountSettings(txCtx, playerID, sessionToken, now)
		if err != nil {
			return err
		}
		settings = accountSettingsFromRecord(record, now)
		return nil
	}); err != nil {
		return nil, err
	}
	return settings, nil
}

func (u *AccountSettingsUseCase) ChangeUsername(
	ctx context.Context,
	playerID uuid.UUID,
	sessionToken uuid.UUID,
	command inbound.ChangeUsernameCommand,
) (*domain.Player, error) {
	username, normalizedUsername, err := normalizeUsername(command.Username)
	if err != nil {
		return nil, err
	}
	var player *domain.Player
	usernameChanged := false
	now := u.clock.Now().UTC()
	err = u.tx.Do(ctx, func(txCtx context.Context) error {
		record, err := u.accounts.LockAccountSettings(txCtx, playerID, sessionToken, now)
		if err != nil {
			return err
		}
		if err := u.verifyCurrentPassword(command.CurrentPassword, record.PasswordHash); err != nil {
			return err
		}
		usernameChanged = username != record.Player.Username
		if err := u.accounts.ChangeAccountUsername(txCtx, record, username, normalizedUsername); err != nil {
			return err
		}
		updated := *record.Player
		updated.Username = username
		player = &updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	if usernameChanged && u.leaderboard != nil {
		u.leaderboard.Invalidate()
	}
	return player, nil
}

func (u *AccountSettingsUseCase) ChangePassword(
	ctx context.Context,
	playerID uuid.UUID,
	sessionToken uuid.UUID,
	command inbound.ChangePasswordCommand,
) (*domain.Player, error) {
	if err := validatePassword(command.NewPassword); err != nil {
		return nil, err
	}
	newPasswordHash, err := u.passwords.Hash(command.NewPassword)
	if err != nil {
		return nil, fmt.Errorf("hash changed player password: %w", err)
	}
	newSessionToken, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("create player session token for password change: %w", err)
	}
	now := u.clock.Now().UTC()
	var player *domain.Player
	err = u.tx.Do(ctx, func(txCtx context.Context) error {
		record, err := u.accounts.LockAccountSettings(txCtx, playerID, sessionToken, now)
		if err != nil {
			return err
		}
		if err := u.verifyCurrentPassword(command.CurrentPassword, record.PasswordHash); err != nil {
			return err
		}
		if command.CurrentPassword == command.NewPassword {
			return domain.ErrValidation
		}
		player, err = u.accounts.ChangeAccountPassword(
			txCtx,
			record,
			newPasswordHash,
			sessionToken,
			newSessionToken,
			now,
			now.Add(u.cfg.SessionTTL),
		)
		return err
	})
	if err != nil {
		return nil, err
	}
	return player, nil
}

func (u *AccountSettingsUseCase) BeginEmailChange(
	ctx context.Context,
	playerID uuid.UUID,
	sessionToken uuid.UUID,
	command inbound.BeginEmailChangeCommand,
) (*domain.AccountSettings, error) {
	email, normalizedEmail, err := normalizeEmail(command.NewEmail)
	if err != nil {
		return nil, err
	}
	code, codeHash, err := u.newEmailChangeCodeHash()
	if err != nil {
		return nil, err
	}
	now := u.clock.Now().UTC()
	var record *AccountSettingsRecord
	var deliveryEmail string
	err = u.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		record, err = u.accounts.LockAccountSettings(txCtx, playerID, sessionToken, now)
		if err != nil {
			return err
		}
		if err := u.verifyCurrentPassword(command.CurrentPassword, record.PasswordHash); err != nil {
			return err
		}
		if normalizedEmail == record.EmailNormalized {
			return domain.ErrValidation
		}
		if u.mailer == nil {
			return domain.ErrEmailChangeUnavailable
		}
		sendWindow, sendCount, attemptWindow, attemptCount, err := emailChangeSendState(record, now)
		if err != nil {
			return err
		}
		if err := u.accounts.StartEmailChange(
			txCtx,
			record,
			email,
			normalizedEmail,
			codeHash,
			now.Add(defaultEmailChangeTTL),
			now,
			sendWindow,
			sendCount,
			attemptWindow,
			attemptCount,
		); err != nil {
			return err
		}
		applyEmailChangeChallenge(record, email, normalizedEmail, codeHash, now.Add(defaultEmailChangeTTL), now, sendWindow, sendCount, attemptWindow, attemptCount)
		deliveryEmail = email
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := u.mailer.SendEmailChangeCode(ctx, deliveryEmail, code); err != nil {
		return nil, domain.WrapError(err, domain.ErrEmailChangeUnavailable)
	}
	return accountSettingsFromRecord(record, u.clock.Now().UTC()), nil
}

func (u *AccountSettingsUseCase) ResendEmailChange(
	ctx context.Context,
	playerID uuid.UUID,
	sessionToken uuid.UUID,
) (*domain.AccountSettings, error) {
	code, codeHash, err := u.newEmailChangeCodeHash()
	if err != nil {
		return nil, err
	}
	now := u.clock.Now().UTC()
	var record *AccountSettingsRecord
	err = u.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		record, err = u.accounts.LockAccountSettings(txCtx, playerID, sessionToken, now)
		if err != nil {
			return err
		}
		if record.PendingEmail == nil || record.PendingEmailNormalized == nil {
			return domain.ErrConflict
		}
		if u.mailer == nil {
			return domain.ErrEmailChangeUnavailable
		}
		sendWindow, sendCount, attemptWindow, attemptCount, err := emailChangeSendState(record, now)
		if err != nil {
			return err
		}
		if err := u.accounts.StartEmailChange(
			txCtx,
			record,
			*record.PendingEmail,
			*record.PendingEmailNormalized,
			codeHash,
			now.Add(defaultEmailChangeTTL),
			now,
			sendWindow,
			sendCount,
			attemptWindow,
			attemptCount,
		); err != nil {
			return err
		}
		applyEmailChangeChallenge(
			record,
			*record.PendingEmail,
			*record.PendingEmailNormalized,
			codeHash,
			now.Add(defaultEmailChangeTTL),
			now,
			sendWindow,
			sendCount,
			attemptWindow,
			attemptCount,
		)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if record.PendingEmail == nil {
		return nil, domain.ErrInternal
	}
	if err := u.mailer.SendEmailChangeCode(ctx, *record.PendingEmail, code); err != nil {
		return nil, domain.WrapError(err, domain.ErrEmailChangeUnavailable)
	}
	return accountSettingsFromRecord(record, u.clock.Now().UTC()), nil
}

func (u *AccountSettingsUseCase) CancelEmailChange(
	ctx context.Context,
	playerID uuid.UUID,
	sessionToken uuid.UUID,
) (*domain.AccountSettings, error) {
	now := u.clock.Now().UTC()
	var settings *domain.AccountSettings
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		record, err := u.accounts.LockAccountSettings(txCtx, playerID, sessionToken, now)
		if err != nil {
			return err
		}
		if record.PendingEmail != nil {
			if err := u.accounts.CancelEmailChange(txCtx, record); err != nil {
				return err
			}
			record.PendingEmail = nil
			record.PendingEmailNormalized = nil
			record.EmailChangeCodeHash = ""
			record.EmailChangeExpiresAt = nil
		}
		settings = accountSettingsFromRecord(record, now)
		return nil
	}); err != nil {
		return nil, err
	}
	return settings, nil
}

func (u *AccountSettingsUseCase) ConfirmEmailChange(
	ctx context.Context,
	playerID uuid.UUID,
	sessionToken uuid.UUID,
	command inbound.ConfirmEmailChangeCommand,
) (*domain.EmailChangeResult, error) {
	now := u.clock.Now().UTC()
	var record *AccountSettingsRecord
	var result *domain.EmailChangeResult
	var verificationErr error
	var previousEmail string
	err := u.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		record, err = u.accounts.LockAccountSettings(txCtx, playerID, sessionToken, now)
		if err != nil {
			return err
		}
		verification, err := u.verifyAndCountEmailChangeCode(txCtx, record, command.Code, now)
		if err != nil {
			return err
		}
		if !verification.valid {
			verificationErr = verification.validationError(now)
			return nil
		}
		result, previousEmail, err = u.commitEmailChange(txCtx, record, sessionToken, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	if verificationErr != nil {
		return nil, verificationErr
	}
	if result == nil {
		return nil, domain.ErrInternal
	}
	if u.mailer != nil {
		if err := u.mailer.SendEmailChangedNotice(ctx, previousEmail, result.Email); err == nil {
			result.PreviousEmailNotified = true
		}
	}
	return result, nil
}

func (u *AccountSettingsUseCase) verifyAndCountEmailChangeCode(
	ctx context.Context,
	record *AccountSettingsRecord,
	code string,
	now time.Time,
) (emailChangeVerification, error) {
	if record.PendingEmail == nil || record.PendingEmailNormalized == nil ||
		record.EmailChangeCodeHash == "" || record.EmailChangeExpiresAt == nil {
		return emailChangeVerification{failure: emailChangeVerificationInvalid}, nil
	}
	attemptWindow, attemptCount := activeEmailWindow(record.EmailChangeAttemptWindowStartedAt, record.EmailChangeAttemptCount, now)
	if attemptCount >= maxEmailChangeAttempts {
		return emailChangeVerification{
			failure:    emailChangeVerificationAttemptsExceeded,
			retryUntil: attemptWindow.Add(emailChangeWindow),
		}, nil
	}
	valid := false
	var err error
	if now.Before(*record.EmailChangeExpiresAt) && isEmailChangeCode(code) {
		valid, err = u.passwords.Verify(code, record.EmailChangeCodeHash)
		if err != nil {
			return emailChangeVerification{}, fmt.Errorf("verify player email change code: %w", err)
		}
	}
	if valid {
		return emailChangeVerification{valid: true}, nil
	}
	attemptCount++
	if err := u.accounts.RecordEmailChangeAttempt(ctx, record, attemptWindow, attemptCount); err != nil {
		return emailChangeVerification{}, err
	}
	record.EmailChangeAttemptWindowStartedAt = timePointer(attemptWindow)
	record.EmailChangeAttemptCount = attemptCount
	if attemptCount >= maxEmailChangeAttempts {
		return emailChangeVerification{
			failure:    emailChangeVerificationAttemptsExceeded,
			retryUntil: attemptWindow.Add(emailChangeWindow),
		}, nil
	}
	return emailChangeVerification{failure: emailChangeVerificationInvalid}, nil
}

func (u *AccountSettingsUseCase) commitEmailChange(
	ctx context.Context,
	record *AccountSettingsRecord,
	sessionToken uuid.UUID,
	now time.Time,
) (*domain.EmailChangeResult, string, error) {
	newSessionToken, err := uuid.NewRandom()
	if err != nil {
		return nil, "", fmt.Errorf("create player session token for email change: %w", err)
	}
	previousEmail := record.CurrentEmail
	newEmail := *record.PendingEmail
	player, err := u.accounts.ConfirmEmailChange(
		ctx,
		record,
		sessionToken,
		newSessionToken,
		now,
		now.Add(u.cfg.SessionTTL),
	)
	if err != nil {
		return nil, "", err
	}
	return &domain.EmailChangeResult{Player: player, Email: newEmail}, previousEmail, nil
}

func (u *AccountSettingsUseCase) verifyCurrentPassword(password, passwordHash string) error {
	if password == "" || len(password) > maxPasswordBytes || !utf8.ValidString(password) || strings.IndexByte(password, 0) >= 0 {
		return domain.ErrCurrentPasswordInvalid
	}
	valid, err := u.passwords.Verify(password, passwordHash)
	if err != nil {
		return fmt.Errorf("verify current player password: %w", err)
	}
	if !valid {
		return domain.ErrCurrentPasswordInvalid
	}
	return nil
}

func (u *AccountSettingsUseCase) newEmailChangeCodeHash() (string, string, error) {
	codeValue, err := rand.Int(rand.Reader, big.NewInt(maximumEmailChangeCodeValue))
	if err != nil {
		return "", "", fmt.Errorf("generate player email change code: %w", err)
	}
	code := fmt.Sprintf("%0*d", emailChangeCodeDigits, codeValue.Int64())
	hash, err := u.passwords.Hash(code)
	if err != nil {
		return "", "", fmt.Errorf("hash player email change code: %w", err)
	}
	return code, hash, nil
}

func emailChangeSendState(
	record *AccountSettingsRecord,
	now time.Time,
) (time.Time, int32, time.Time, int32, error) {
	sendWindow, sendCount := activeEmailWindow(record.EmailChangeSendWindowStartedAt, record.EmailChangeSendCount, now)
	if sendCount >= maxEmailChangeSends {
		return time.Time{}, 0, time.Time{}, 0, retryAfter(domain.ErrEmailChangeRateLimited, sendWindow.Add(emailChangeWindow), now)
	}
	if record.EmailChangeLastSentAt != nil {
		cooldownEnds := record.EmailChangeLastSentAt.Add(emailChangeCooldown)
		if now.Before(cooldownEnds) {
			return time.Time{}, 0, time.Time{}, 0, retryAfter(domain.ErrEmailChangeRateLimited, cooldownEnds, now)
		}
	}
	attemptWindow, attemptCount, attemptErr := emailChangeAttemptState(record, now)
	if attemptErr != nil {
		return time.Time{}, 0, time.Time{}, 0, attemptErr
	}
	if attemptCount >= maxEmailChangeAttempts {
		return time.Time{}, 0, time.Time{}, 0, retryAfter(domain.ErrEmailChangeAttemptsExceeded, attemptWindow.Add(emailChangeWindow), now)
	}
	return sendWindow, sendCount + 1, attemptWindow, attemptCount, nil
}

func emailChangeAttemptState(record *AccountSettingsRecord, now time.Time) (time.Time, int32, error) {
	window, count := activeEmailWindow(record.EmailChangeAttemptWindowStartedAt, record.EmailChangeAttemptCount, now)
	if count >= maxEmailChangeAttempts {
		return window, count, retryAfter(domain.ErrEmailChangeAttemptsExceeded, window.Add(emailChangeWindow), now)
	}
	return window, count, nil
}

func activeEmailWindow(startedAt *time.Time, count int32, now time.Time) (time.Time, int32) {
	if startedAt == nil || !now.Before(startedAt.Add(emailChangeWindow)) {
		return now, 0
	}
	return *startedAt, count
}

func retryAfter(err error, until, now time.Time) error {
	wait := until.Sub(now)
	if wait < time.Second {
		wait = time.Second
	}
	return domain.WithRetryAfter(err, wait)
}

func applyEmailChangeChallenge(
	record *AccountSettingsRecord,
	email string,
	normalizedEmail string,
	codeHash string,
	expiresAt time.Time,
	sentAt time.Time,
	sendWindow time.Time,
	sendCount int32,
	attemptWindow time.Time,
	attemptCount int32,
) {
	record.PendingEmail = stringPointer(email)
	record.PendingEmailNormalized = stringPointer(normalizedEmail)
	record.EmailChangeCodeHash = codeHash
	record.EmailChangeExpiresAt = timePointer(expiresAt)
	record.EmailChangeLastSentAt = timePointer(sentAt)
	record.EmailChangeSendWindowStartedAt = timePointer(sendWindow)
	record.EmailChangeSendCount = sendCount
	record.EmailChangeAttemptWindowStartedAt = timePointer(attemptWindow)
	record.EmailChangeAttemptCount = attemptCount
}

func accountSettingsFromRecord(record *AccountSettingsRecord, now time.Time) *domain.AccountSettings {
	if record == nil || record.Player == nil {
		return nil
	}
	settings := &domain.AccountSettings{
		Username:              record.Player.Username,
		Email:                 record.CurrentEmail,
		PendingEmail:          record.PendingEmail,
		PendingEmailExpiresAt: record.EmailChangeExpiresAt,
	}
	var availableAt *time.Time
	if record.EmailChangeLastSentAt != nil {
		cooldown := record.EmailChangeLastSentAt.Add(emailChangeCooldown)
		if now.Before(cooldown) {
			availableAt = timePointer(cooldown)
		}
	}
	if record.EmailChangeSendWindowStartedAt != nil && record.EmailChangeSendCount >= maxEmailChangeSends {
		reset := record.EmailChangeSendWindowStartedAt.Add(emailChangeWindow)
		if now.Before(reset) {
			availableAt = latestPointer(availableAt, reset)
		}
	}
	if record.EmailChangeAttemptWindowStartedAt != nil && record.EmailChangeAttemptCount >= maxEmailChangeAttempts {
		reset := record.EmailChangeAttemptWindowStartedAt.Add(emailChangeWindow)
		if now.Before(reset) {
			availableAt = latestPointer(availableAt, reset)
		}
	}
	settings.EmailResendAvailableAt = availableAt
	return settings
}

func isEmailChangeCode(code string) bool {
	if len(code) != emailChangeCodeDigits {
		return false
	}
	for i := range len(code) {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

func stringPointer(value string) *string {
	return &value
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func latestPointer(current *time.Time, candidate time.Time) *time.Time {
	if current == nil || candidate.After(*current) {
		return timePointer(candidate)
	}
	return current
}
