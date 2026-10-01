package account

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	accountusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/account"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ accountusecase.SettingsRepository = (*AccountPostgres)(nil)

func (r *AccountPostgres) LockAccountSettings(
	ctx context.Context,
	playerID uuid.UUID,
	sessionToken uuid.UUID,
	now time.Time,
) (*accountusecase.AccountSettingsRecord, error) {
	q := r.tx.Querier(ctx)
	playerRow, err := q.LockPlayerAccountSettingsSession(ctx, sqlc.LockPlayerAccountSettingsSessionParams{
		ID:           playerID,
		SessionToken: uuid.NullUUID{UUID: sessionToken, Valid: true},
		Now:          tstz(now),
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("lock player account settings session: %w", err)
		}
		deleted, tombstoneErr := q.IsDeletedPlayerAccountSession(ctx, sqlc.IsDeletedPlayerAccountSessionParams{
			SessionTokenHash: sessionTokenHash(sessionToken),
			ExpiresAt:        tstz(now),
		})
		if tombstoneErr != nil {
			return nil, fmt.Errorf("check deleted player account session: %w", tombstoneErr)
		}
		if deleted {
			return nil, domain.ErrAccountDeleted
		}
		return nil, domain.ErrInvalidSession
	}
	accountRow, err := q.GetPlayerAccountSettingsForUpdate(ctx, uuid.NullUUID{UUID: playerID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvalidSession
		}
		return nil, fmt.Errorf("lock player account settings record: %w", err)
	}
	codeHash := ""
	if accountRow.EmailChangeCodeHash != nil {
		codeHash = *accountRow.EmailChangeCodeHash
	}
	return &accountusecase.AccountSettingsRecord{
		AccountID:                         accountRow.ID,
		Player:                            playerToDomain(playerRow),
		UsernameNormalized:                accountRow.UsernameNormalized,
		CurrentEmail:                      accountRow.Email,
		EmailNormalized:                   accountRow.EmailNormalized,
		PasswordHash:                      accountRow.PasswordHash,
		PendingEmail:                      accountRow.PendingEmail,
		PendingEmailNormalized:            accountRow.PendingEmailNormalized,
		EmailChangeCodeHash:               codeHash,
		EmailChangeExpiresAt:              optionalTimestamp(accountRow.EmailChangeExpiresAt),
		EmailChangeLastSentAt:             optionalTimestamp(accountRow.EmailChangeLastSentAt),
		EmailChangeSendWindowStartedAt:    optionalTimestamp(accountRow.EmailChangeSendWindowStartedAt),
		EmailChangeSendCount:              accountRow.EmailChangeSendCount,
		EmailChangeAttemptWindowStartedAt: optionalTimestamp(accountRow.EmailChangeAttemptWindowStartedAt),
		EmailChangeAttemptCount:           accountRow.EmailChangeAttemptCount,
	}, nil
}

func (r *AccountPostgres) ChangeAccountUsername(
	ctx context.Context,
	record *accountusecase.AccountSettingsRecord,
	username string,
	normalizedUsername string,
) error {
	if record == nil || record.Player == nil {
		return domain.ErrInvalidSession
	}
	q := r.tx.Querier(ctx)
	if err := validateAccountUsernameChange(ctx, q, record, normalizedUsername); err != nil {
		return err
	}
	if normalizedUsername == record.UsernameNormalized && username == record.Player.Username {
		return nil
	}
	if normalizedUsername != record.UsernameNormalized {
		if err := moveAccountUsernameReservation(ctx, q, record, normalizedUsername); err != nil {
			return err
		}
	}
	return persistAccountUsername(ctx, q, record, username, normalizedUsername)
}

func validateAccountUsernameChange(
	ctx context.Context,
	q *sqlc.Queries,
	record *accountusecase.AccountSettingsRecord,
	normalizedUsername string,
) error {
	if err := lockPlayerAccountIdentityKeys(ctx, q, record.UsernameNormalized, normalizedUsername); err != nil {
		return fmt.Errorf("lock player username change: %w", err)
	}
	if err := ensureAccountUsernameAvailable(ctx, q, record.AccountID, normalizedUsername); err != nil {
		return err
	}
	if err := ensureLegacyUsernameAvailable(ctx, q, record.Player.ID, normalizedUsername); err != nil {
		return err
	}
	return ensureUsernameReservationAvailable(ctx, q, record, normalizedUsername)
}

func ensureAccountUsernameAvailable(
	ctx context.Context,
	q *sqlc.Queries,
	accountID uuid.UUID,
	normalizedUsername string,
) error {
	existingAccountID, err := q.GetPlayerAccountByUsername(ctx, normalizedUsername)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check player account username: %w", err)
	}
	if existingAccountID != accountID {
		return domain.ErrUsernameTaken
	}
	return nil
}

func ensureLegacyUsernameAvailable(
	ctx context.Context,
	q *sqlc.Queries,
	playerID uuid.UUID,
	normalizedUsername string,
) error {
	legacyExists, err := q.PlayerUsernameExistsExcept(ctx, sqlc.PlayerUsernameExistsExceptParams{
		Username: normalizedUsername,
		ID:       playerID,
	})
	if err != nil {
		return fmt.Errorf("check legacy player username: %w", err)
	}
	if legacyExists {
		return domain.ErrUsernameTaken
	}
	return nil
}

func ensureUsernameReservationAvailable(
	ctx context.Context,
	q *sqlc.Queries,
	record *accountusecase.AccountSettingsRecord,
	normalizedUsername string,
) error {
	reservation, err := q.GetPlayerUsernameReservation(ctx, normalizedUsername)
	if errors.Is(err, pgx.ErrNoRows) {
		if normalizedUsername == record.UsernameNormalized {
			return domain.ErrInternal
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("check player username reservation: %w", err)
	}
	if !reservation.AccountID.Valid || reservation.AccountID.UUID != record.AccountID || reservation.LegacyCount != 0 {
		return domain.ErrUsernameTaken
	}
	return nil
}

func moveAccountUsernameReservation(
	ctx context.Context,
	q *sqlc.Queries,
	record *accountusecase.AccountSettingsRecord,
	normalizedUsername string,
) error {
	rows, err := q.UpdatePlayerAccountUsernameReservation(ctx, sqlc.UpdatePlayerAccountUsernameReservationParams{
		AccountID:                 uuid.NullUUID{UUID: record.AccountID, Valid: true},
		NewNormalizedUsername:     normalizedUsername,
		CurrentNormalizedUsername: record.UsernameNormalized,
	})
	if err != nil {
		if isUniqueViolation(err, "player_username_reservations_pkey") {
			return domain.WrapError(err, domain.ErrUsernameTaken)
		}
		return fmt.Errorf("update player account username reservation: %w", err)
	}
	if rows != 1 {
		return domain.ErrUsernameTaken
	}
	return nil
}

func persistAccountUsername(
	ctx context.Context,
	q *sqlc.Queries,
	record *accountusecase.AccountSettingsRecord,
	username string,
	normalizedUsername string,
) error {
	rows, err := q.UpdatePlayerAccountUsername(ctx, sqlc.UpdatePlayerAccountUsernameParams{
		ID:                 record.AccountID,
		Username:           username,
		UsernameNormalized: normalizedUsername,
	})
	if err != nil {
		if isUniqueViolation(err, playerAccountUsernameUniqueConstraint) {
			return domain.WrapError(err, domain.ErrUsernameTaken)
		}
		return fmt.Errorf("update player account username: %w", err)
	}
	if rows != 1 {
		return domain.ErrInvalidSession
	}
	_, err = q.UpdateAccountBackedPlayerUsername(ctx, sqlc.UpdateAccountBackedPlayerUsernameParams{
		ID:       record.Player.ID,
		Username: username,
	})
	if err == nil {
		return nil
	}
	if isUniqueViolation(err, "players_username_key") {
		return domain.WrapError(err, domain.ErrUsernameTaken)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrInvalidSession
	}
	return fmt.Errorf("update player username: %w", err)
}

func (r *AccountPostgres) ChangeAccountPassword(
	ctx context.Context,
	record *accountusecase.AccountSettingsRecord,
	passwordHash string,
	sessionToken uuid.UUID,
	newSessionToken uuid.UUID,
	now time.Time,
	expiresAt time.Time,
) (*domain.Player, error) {
	if record == nil || record.Player == nil {
		return nil, domain.ErrInvalidSession
	}
	q := r.tx.Querier(ctx)
	rows, err := q.UpdatePlayerAccountPasswordHash(ctx, sqlc.UpdatePlayerAccountPasswordHashParams{
		ID:           record.AccountID,
		PasswordHash: passwordHash,
	})
	if err != nil {
		return nil, fmt.Errorf("update player account password: %w", err)
	}
	if rows != 1 {
		return nil, domain.ErrInvalidSession
	}
	player, err := q.UpdateAccountPlayerSessionForSettings(ctx, sqlc.UpdateAccountPlayerSessionForSettingsParams{
		ID:                  record.Player.ID,
		NewSessionToken:     uuid.NullUUID{UUID: newSessionToken, Valid: true},
		NewSessionExpiresAt: tstz(expiresAt),
		CurrentSessionToken: uuid.NullUUID{UUID: sessionToken, Valid: true},
		Now:                 tstz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvalidSession
		}
		return nil, fmt.Errorf("rotate player session after password change: %w", err)
	}
	return playerToDomain(player), nil
}

func (r *AccountPostgres) StartEmailChange(
	ctx context.Context,
	record *accountusecase.AccountSettingsRecord,
	email string,
	normalizedEmail string,
	codeHash string,
	expiresAt time.Time,
	sentAt time.Time,
	sendWindowStartedAt time.Time,
	sendCount int32,
	attemptWindowStartedAt time.Time,
	attemptCount int32,
) error {
	if record == nil || record.Player == nil {
		return domain.ErrInvalidSession
	}
	q := r.tx.Querier(ctx)
	if err := lockPlayerAccountIdentityKeys(ctx, q, normalizedEmail); err != nil {
		return fmt.Errorf("lock player email change identity: %w", err)
	}
	if existingID, err := q.PlayerAccountEmailExists(ctx, normalizedEmail); err == nil && existingID != record.AccountID {
		return domain.ErrEmailTaken
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check player email change address: %w", err)
	}
	rows, err := q.StartPlayerEmailChange(ctx, sqlc.StartPlayerEmailChangeParams{
		ID:                                record.AccountID,
		PendingEmail:                      &email,
		PendingEmailNormalized:            &normalizedEmail,
		EmailChangeCodeHash:               &codeHash,
		EmailChangeExpiresAt:              tstz(expiresAt),
		EmailChangeLastSentAt:             tstz(sentAt),
		EmailChangeSendWindowStartedAt:    tstz(sendWindowStartedAt),
		EmailChangeSendCount:              sendCount,
		EmailChangeAttemptWindowStartedAt: tstz(attemptWindowStartedAt),
		EmailChangeAttemptCount:           attemptCount,
	})
	if err != nil {
		return fmt.Errorf("store player email change challenge: %w", err)
	}
	if rows != 1 {
		return domain.ErrInvalidSession
	}
	return nil
}

func (r *AccountPostgres) RecordEmailChangeAttempt(
	ctx context.Context,
	record *accountusecase.AccountSettingsRecord,
	windowStartedAt time.Time,
	attemptCount int32,
) error {
	if record == nil {
		return domain.ErrInvalidSession
	}
	rows, err := r.tx.Querier(ctx).RecordPlayerEmailChangeAttempt(ctx, sqlc.RecordPlayerEmailChangeAttemptParams{
		ID:                                record.AccountID,
		EmailChangeAttemptWindowStartedAt: tstz(windowStartedAt),
		EmailChangeAttemptCount:           attemptCount,
	})
	if err != nil {
		return fmt.Errorf("record player email confirmation attempt: %w", err)
	}
	if rows != 1 {
		return domain.ErrInvalidSession
	}
	return nil
}

func (r *AccountPostgres) CancelEmailChange(ctx context.Context, record *accountusecase.AccountSettingsRecord) error {
	if record == nil {
		return domain.ErrInvalidSession
	}
	rows, err := r.tx.Querier(ctx).CancelPlayerEmailChange(ctx, record.AccountID)
	if err != nil {
		return fmt.Errorf("cancel player email change: %w", err)
	}
	if rows != 1 {
		return domain.ErrInvalidSession
	}
	return nil
}

func (r *AccountPostgres) ConfirmEmailChange(
	ctx context.Context,
	record *accountusecase.AccountSettingsRecord,
	sessionToken uuid.UUID,
	newSessionToken uuid.UUID,
	now time.Time,
	expiresAt time.Time,
) (*domain.Player, error) {
	if record == nil || record.Player == nil || record.PendingEmail == nil || record.PendingEmailNormalized == nil {
		return nil, domain.ErrEmailChangeCodeInvalid
	}
	q := r.tx.Querier(ctx)
	if err := lockPlayerAccountIdentityKeys(ctx, q, *record.PendingEmailNormalized); err != nil {
		return nil, fmt.Errorf("lock player email confirmation address: %w", err)
	}
	if existingID, err := q.PlayerAccountEmailExists(ctx, *record.PendingEmailNormalized); err == nil && existingID != record.AccountID {
		return nil, domain.ErrEmailTaken
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check player email confirmation address: %w", err)
	}
	rows, err := q.CompletePlayerEmailChange(ctx, sqlc.CompletePlayerEmailChangeParams{
		ID:                     record.AccountID,
		PlayerID:               uuid.NullUUID{UUID: record.Player.ID, Valid: true},
		PendingEmailNormalized: record.PendingEmailNormalized,
		ExpectedCodeHash:       &record.EmailChangeCodeHash,
	})
	if err != nil {
		if isUniqueViolation(err, playerAccountEmailUniqueConstraint) {
			return nil, domain.WrapError(err, domain.ErrEmailTaken)
		}
		return nil, fmt.Errorf("complete player email change: %w", err)
	}
	if rows != 1 {
		return nil, domain.ErrEmailChangeCodeInvalid
	}
	player, err := q.UpdateAccountPlayerSessionForSettings(ctx, sqlc.UpdateAccountPlayerSessionForSettingsParams{
		ID:                  record.Player.ID,
		NewSessionToken:     uuid.NullUUID{UUID: newSessionToken, Valid: true},
		NewSessionExpiresAt: tstz(expiresAt),
		CurrentSessionToken: uuid.NullUUID{UUID: sessionToken, Valid: true},
		Now:                 tstz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvalidSession
		}
		return nil, fmt.Errorf("rotate player session after email change: %w", err)
	}
	return playerToDomain(player), nil
}

func sessionTokenHash(token uuid.UUID) []byte {
	hash := sha256.Sum256(token[:])
	return hash[:]
}

func optionalTimestamp(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
