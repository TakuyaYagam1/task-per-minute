package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	accountusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/account"
)

const (
	playerAccountUsernameUniqueConstraint = "player_accounts_username_normalized_key"
	playerAccountEmailUniqueConstraint    = "player_accounts_email_normalized_key"
)

type AccountPostgres struct {
	tx *db.TxManager
}

var _ accountusecase.Repository = (*AccountPostgres)(nil)

func NewAccountPostgres(tx *db.TxManager) *AccountPostgres {
	return &AccountPostgres{tx: tx}
}

func (r *AccountPostgres) CreatePendingAccount(ctx context.Context, pending accountusecase.PendingAccount) error {
	q := r.tx.Querier(ctx)
	if err := q.LockPlayerUsername(ctx, pending.UsernameNormalized); err != nil {
		return fmt.Errorf("lock player username: %w", err)
	}
	if _, err := q.GetPlayerAccountByEmail(ctx, pending.EmailNormalized); err == nil {
		return domain.ErrEmailTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("find duplicate player account email: %w", err)
	}
	if exists, err := q.PlayerUsernameExists(ctx, pending.UsernameNormalized); err != nil {
		return fmt.Errorf("check legacy player username: %w", err)
	} else if exists {
		return domain.ErrUsernameTaken
	}
	if _, err := q.GetPlayerUsernameReservation(ctx, pending.UsernameNormalized); err == nil {
		return domain.ErrUsernameTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check player username reservation: %w", err)
	}

	accountID, err := q.CreatePendingPlayerAccount(ctx, sqlc.CreatePendingPlayerAccountParams{
		Username:              pending.Username,
		UsernameNormalized:    pending.UsernameNormalized,
		Email:                 pending.Email,
		EmailNormalized:       pending.EmailNormalized,
		PasswordHash:          pending.PasswordHash,
		VerificationTokenHash: pending.VerificationTokenHash,
		VerificationExpiresAt: tstz(pending.VerificationExpiresAt),
		VerificationSentAt:    tstz(pending.VerificationSentAt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// UseCase.Register converts duplicate email to the generic acknowledgement.
			return domain.ErrEmailTaken
		}
		if isUniqueViolation(err, playerAccountUsernameUniqueConstraint) {
			return domain.WrapError(err, domain.ErrUsernameTaken)
		}
		if isUniqueViolation(err, playerAccountEmailUniqueConstraint) {
			return domain.ErrEmailTaken
		}
		return fmt.Errorf("create pending player account: %w", err)
	}
	reserved, err := q.ReservePlayerAccountUsername(ctx, sqlc.ReservePlayerAccountUsernameParams{
		NormalizedUsername: pending.UsernameNormalized,
		AccountID:          uuid.NullUUID{UUID: accountID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("reserve player account username: %w", err)
	}
	if reserved != 1 {
		return domain.ErrUsernameTaken
	}
	return nil
}

func (r *AccountPostgres) FindLoginCredentials(ctx context.Context, login string) (*accountusecase.LoginCredentials, error) {
	row, err := r.tx.Querier(ctx).FindPlayerLoginCredentials(ctx, login)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("find player login credentials: %w", err)
	}
	var playerID *uuid.UUID
	if row.PlayerID.Valid {
		id := row.PlayerID.UUID
		playerID = &id
	}
	var verifiedAt *time.Time
	if row.EmailVerifiedAt.Valid {
		verified := row.EmailVerifiedAt.Time
		verifiedAt = &verified
	}
	return &accountusecase.LoginCredentials{
		Username:        row.Username,
		PasswordHash:    row.PasswordHash,
		PlayerID:        playerID,
		EmailVerifiedAt: verifiedAt,
	}, nil
}

func (r *AccountPostgres) ReplacePendingVerification(
	ctx context.Context,
	email string,
	eligibleBefore time.Time,
	tokenHash []byte,
	expiresAt time.Time,
	sentAt time.Time,
) (*accountusecase.PendingVerification, error) {
	email, err := r.tx.Querier(ctx).ReplacePendingPlayerVerification(ctx, sqlc.ReplacePendingPlayerVerificationParams{
		TokenHash:       tokenHash,
		ExpiresAt:       tstz(expiresAt),
		SentAt:          tstz(sentAt),
		EmailNormalized: email,
		EligibleBefore:  tstz(eligibleBefore),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("replace player verification token: %w", err)
	}
	return &accountusecase.PendingVerification{Email: email}, nil
}

func (r *AccountPostgres) VerifyPendingAccount(ctx context.Context, tokenHash []byte, now time.Time) (*domain.Player, error) {
	q := r.tx.Querier(ctx)
	account, err := q.GetPendingPlayerAccountByToken(ctx, sqlc.GetPendingPlayerAccountByTokenParams{
		VerificationTokenHash: tokenHash,
		VerificationExpiresAt: tstz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrVerificationTokenInvalid
		}
		return nil, fmt.Errorf("load pending player account: %w", err)
	}
	if err := q.LockPlayerUsername(ctx, account.UsernameNormalized); err != nil {
		return nil, fmt.Errorf("lock player account username: %w", err)
	}
	reservation, err := q.GetPlayerUsernameReservation(ctx, account.UsernameNormalized)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrUsernameTaken
		}
		return nil, fmt.Errorf("load player account username reservation: %w", err)
	}
	if !reservation.AccountID.Valid || reservation.AccountID.UUID != account.ID || reservation.LegacyCount != 0 {
		return nil, domain.ErrUsernameTaken
	}
	if exists, err := q.PlayerUsernameExists(ctx, account.UsernameNormalized); err != nil {
		return nil, fmt.Errorf("check player username before verification: %w", err)
	} else if exists {
		return nil, domain.ErrUsernameTaken
	}
	row, err := q.CreateVerifiedPlayer(ctx, account.Username)
	if err != nil {
		if isUniqueViolation(err, "players_username_key") {
			return nil, domain.ErrUsernameTaken
		}
		return nil, fmt.Errorf("create verified player: %w", err)
	}
	completed, err := q.CompletePlayerAccountVerification(ctx, sqlc.CompletePlayerAccountVerificationParams{
		ID:              account.ID,
		PlayerID:        uuid.NullUUID{UUID: row.ID, Valid: true},
		EmailVerifiedAt: tstz(now),
	})
	if err != nil {
		return nil, fmt.Errorf("complete player account verification: %w", err)
	}
	if completed != 1 {
		return nil, domain.ErrVerificationTokenInvalid
	}
	return playerToDomain(row), nil
}

func (r *AccountPostgres) UpdateAccountPlayerSession(
	ctx context.Context,
	playerID uuid.UUID,
	token uuid.UUID,
	expiresAt time.Time,
) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).UpdateAccountPlayerSession(ctx, sqlc.UpdateAccountPlayerSessionParams{
		ID:               playerID,
		SessionToken:     uuid.NullUUID{UUID: token, Valid: true},
		SessionExpiresAt: tstz(expiresAt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("rotate verified player session: %w", err)
	}
	return playerToDomain(row), nil
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func playerToDomain(row sqlc.Player) *domain.Player {
	var sessionToken *uuid.UUID
	if row.SessionToken.Valid {
		token := row.SessionToken.UUID
		sessionToken = &token
	}
	var sessionExpiresAt *time.Time
	if row.SessionExpiresAt.Valid {
		value := row.SessionExpiresAt.Time
		sessionExpiresAt = &value
	}
	return &domain.Player{
		ID:               row.ID,
		Username:         row.Username,
		SessionToken:     sessionToken,
		CreatedAt:        row.CreatedAt.Time,
		SessionExpiresAt: sessionExpiresAt,
	}
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
