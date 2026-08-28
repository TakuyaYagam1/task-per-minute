package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrArenaGoldenNotFound = errors.New("arena Golden repository: aggregate not found")

type ArenaGoldenPostgres struct {
	tx *TxManager
}

type ArenaGoldenScope struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
	AttemptID    uuid.UUID
}

type ArenaGoldenMembershipInput struct {
	ID              uuid.UUID
	ParticipantID   uuid.UUID
	SelectionKind   string
	ReservePosition *int16
	SelectedAt      time.Time
	ExcludedAt      *time.Time
	ExclusionReason string
}

type ArenaGoldenCreateAttemptInput struct {
	Scope             ArenaGoldenScope
	AttemptNumber     int32
	PreviousAttemptID *uuid.UUID
	Memberships       []ArenaGoldenMembershipInput
	CreatedAt         time.Time
}

type ArenaGoldenAttemptTransitionInput struct {
	Scope              ArenaGoldenScope
	ExpectedState      string
	NextState          string
	DisclosedAt        *time.Time
	ReadyAt            *time.Time
	StartedAt          *time.Time
	PausedAt           *time.Time
	CompletedAt        *time.Time
	CancelledAt        *time.Time
	CancellationReason string
	SupersededAt       *time.Time
	SupersessionReason string
}

type ArenaGoldenMemberEventInput struct {
	Scope        ArenaGoldenScope
	MembershipID uuid.UUID
	OccurredAt   time.Time
}

type ArenaGoldenPromotionInput struct {
	ID                    uuid.UUID
	Scope                 ArenaGoldenScope
	ReserveMembershipID   uuid.UUID
	ReserveParticipantID  uuid.UUID
	ReplacedMembershipID  uuid.UUID
	ReplacedParticipantID uuid.UUID
	Reason                string
	PromotedAt            time.Time
	CreatedAt             time.Time
}

type ArenaGoldenDisconnectInput struct {
	ID             uuid.UUID
	Scope          ArenaGoldenScope
	MembershipID   uuid.UUID
	ParticipantID  uuid.UUID
	SequenceNumber int32
	DisconnectedAt time.Time
	CreatedAt      time.Time
}

type ArenaGoldenDisconnectCloseInput struct {
	Scope         ArenaGoldenScope
	DisconnectID  uuid.UUID
	NextState     string
	ReconnectedAt *time.Time
	ExpiredAt     *time.Time
}

type ArenaGoldenSubmissionInput struct {
	ID                  uuid.UUID
	Scope               ArenaGoldenScope
	MembershipID        uuid.UUID
	ParticipantID       uuid.UUID
	ServerSequence      int64
	IdempotencyKey      uuid.UUID
	ProvisionalPosition int16
	ElapsedMilliseconds int64
	Status              string
	RejectionReason     string
	PayloadDigest       [32]byte
	SubmittedAt         time.Time
	ReceivedAt          time.Time
	CreatedAt           time.Time
}

type ArenaGoldenPositionInput struct {
	ID                       uuid.UUID
	Scope                    ArenaGoldenScope
	MembershipID             uuid.UUID
	ParticipantID            uuid.UUID
	ProvisionalSubmissionID  uuid.UUID
	PreviousPositionCommitID *uuid.UUID
	Position                 int16
	CommittedAt              time.Time
	CreatedAt                time.Time
}

type ArenaGoldenRecoveryInput struct {
	ID               uuid.UUID
	Scope            ArenaGoldenScope
	State            string
	RecoveryEvidence json.RawMessage
	RecordedAt       time.Time
	CreatedAt        time.Time
}

type ArenaGoldenRecord struct {
	Attempt     sqlc.ArenaGoldenAttempt
	Memberships []sqlc.ArenaGoldenMembership
	Disconnects []sqlc.ArenaGoldenReadyDisconnect
	Submissions []sqlc.ArenaGoldenProvisionalSubmission
	Positions   []sqlc.ArenaGoldenPositionCommit
	Promotions  []sqlc.ArenaGoldenReservePromotion
	Recoveries  []sqlc.ArenaGoldenRecoveryRevision
}

func NewArenaGoldenPostgres(tx *TxManager) *ArenaGoldenPostgres {
	return &ArenaGoldenPostgres{tx: tx}
}

func (r *ArenaGoldenPostgres) CreateAttempt(
	ctx context.Context,
	in ArenaGoldenCreateAttemptInput,
) (*ArenaGoldenRecord, error) {
	if r == nil || r.tx == nil || !validArenaGoldenCreateAttemptInput(in) {
		return nil, domain.ErrValidation
	}
	var record *ArenaGoldenRecord
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockArenaProjectionRoster(txCtx, sqlc.LockArenaProjectionRosterParams{
			RosterID: in.Scope.RosterID, TournamentID: in.Scope.TournamentID,
		}); err != nil {
			return arenaGoldenLookupError("CreateAttempt - lock roster", err)
		}
		attempts, err := querier.ListArenaGoldenAttempts(txCtx, sqlc.ListArenaGoldenAttemptsParams{
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		})
		if err != nil {
			return fmt.Errorf("ArenaGoldenPostgres - CreateAttempt - lineage: %w", err)
		}
		if !arenaGoldenLineageMatches(attempts, in.AttemptNumber, in.PreviousAttemptID) {
			return domain.ErrConflict
		}
		if _, err := querier.CreateArenaGoldenAttempt(txCtx, sqlc.CreateArenaGoldenAttemptParams{
			ID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			AttemptNumber: in.AttemptNumber, PreviousAttemptID: nullableUUID(in.PreviousAttemptID),
			CreatedAt: tstz(in.CreatedAt),
		}); err != nil {
			return mapArenaRepositoryWriteError("ArenaGoldenPostgres - CreateAttempt", err)
		}
		for _, member := range in.Memberships {
			if _, err := querier.CreateArenaGoldenMembership(txCtx, sqlc.CreateArenaGoldenMembershipParams{
				ID: member.ID, AttemptID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID,
				RosterID: in.Scope.RosterID, ParticipantID: member.ParticipantID,
				SelectionKind: member.SelectionKind, ReservePosition: member.ReservePosition,
				SelectedAt: tstz(member.SelectedAt), ExcludedAt: nullableTSTZ(member.ExcludedAt),
				ExclusionReason: optionalTrimmedString(member.ExclusionReason), CreatedAt: tstz(in.CreatedAt),
			}); err != nil {
				return mapArenaRepositoryWriteError("ArenaGoldenPostgres - CreateAttempt - membership", err)
			}
		}
		loaded, err := loadArenaGoldenRecord(txCtx, querier, in.Scope)
		if err != nil {
			return err
		}
		record = loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (r *ArenaGoldenPostgres) TransitionAttempt(
	ctx context.Context,
	in ArenaGoldenAttemptTransitionInput,
) (*sqlc.ArenaGoldenAttempt, bool, error) {
	if r == nil || r.tx == nil || !validArenaGoldenTransitionInput(in) {
		return nil, false, domain.ErrValidation
	}
	var attempt sqlc.ArenaGoldenAttempt
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.LockArenaGoldenAttempt(txCtx, arenaGoldenLockParams(in.Scope))
		if err != nil {
			return arenaGoldenLookupError("TransitionAttempt - lock", err)
		}
		if current.State != in.ExpectedState {
			attempt = current
			return nil
		}
		attempt, err = querier.UpdateArenaGoldenAttemptCAS(txCtx, sqlc.UpdateArenaGoldenAttemptCASParams{
			NextState: in.NextState, DisclosedAt: nullableTSTZ(in.DisclosedAt), ReadyAt: nullableTSTZ(in.ReadyAt),
			StartedAt: nullableTSTZ(in.StartedAt), PausedAt: nullableTSTZ(in.PausedAt),
			CompletedAt: nullableTSTZ(in.CompletedAt), CancelledAt: nullableTSTZ(in.CancelledAt),
			CancellationReason: optionalTrimmedString(in.CancellationReason),
			SupersededAt:       nullableTSTZ(in.SupersededAt), SupersessionReason: optionalTrimmedString(in.SupersessionReason),
			ID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			ExpectedState: in.ExpectedState,
		})
		if err != nil {
			return arenaGoldenCASWriteError("TransitionAttempt", err)
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &attempt, changed, nil
}

func (r *ArenaGoldenPostgres) MarkReady(
	ctx context.Context,
	in ArenaGoldenMemberEventInput,
) (*sqlc.ArenaGoldenMembership, error) {
	return r.updateMembership(ctx, in, "MarkReady", func(
		ctx context.Context,
		querier *sqlc.Queries,
	) (sqlc.ArenaGoldenMembership, error) {
		return querier.MarkArenaGoldenMembershipReady(ctx, sqlc.MarkArenaGoldenMembershipReadyParams{
			ReadyAt: tstz(in.OccurredAt), ID: in.MembershipID, AttemptID: in.Scope.AttemptID,
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		})
	})
}

func (r *ArenaGoldenPostgres) MarkNoShow(
	ctx context.Context,
	in ArenaGoldenMemberEventInput,
) (*sqlc.ArenaGoldenMembership, error) {
	return r.updateMembership(ctx, in, "MarkNoShow", func(
		ctx context.Context,
		querier *sqlc.Queries,
	) (sqlc.ArenaGoldenMembership, error) {
		return querier.MarkArenaGoldenMembershipNoShow(ctx, sqlc.MarkArenaGoldenMembershipNoShowParams{
			NoShowAt: tstz(in.OccurredAt), ID: in.MembershipID, AttemptID: in.Scope.AttemptID,
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		})
	})
}

func (r *ArenaGoldenPostgres) EstablishParticipation(
	ctx context.Context,
	in ArenaGoldenMemberEventInput,
) (*sqlc.ArenaGoldenMembership, error) {
	return r.updateMembership(ctx, in, "EstablishParticipation", func(
		ctx context.Context,
		querier *sqlc.Queries,
	) (sqlc.ArenaGoldenMembership, error) {
		return querier.EstablishArenaGoldenParticipation(ctx, sqlc.EstablishArenaGoldenParticipationParams{
			EstablishedAt: tstz(in.OccurredAt), ID: in.MembershipID, AttemptID: in.Scope.AttemptID,
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		})
	})
}

func (r *ArenaGoldenPostgres) updateMembership(
	ctx context.Context,
	in ArenaGoldenMemberEventInput,
	operation string,
	update func(context.Context, *sqlc.Queries) (sqlc.ArenaGoldenMembership, error),
) (*sqlc.ArenaGoldenMembership, error) {
	if r == nil || r.tx == nil || !validArenaGoldenScope(in.Scope) || in.MembershipID == uuid.Nil ||
		!validServerTime(in.OccurredAt) {
		return nil, domain.ErrValidation
	}
	var membership sqlc.ArenaGoldenMembership
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockArenaGoldenAttempt(txCtx, arenaGoldenLockParams(in.Scope)); err != nil {
			return arenaGoldenLookupError(operation+" - lock attempt", err)
		}
		row, err := update(txCtx, querier)
		if err != nil {
			return arenaGoldenCASWriteError(operation, err)
		}
		membership = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &membership, nil
}

func (r *ArenaGoldenPostgres) PromoteReserve(
	ctx context.Context,
	in ArenaGoldenPromotionInput,
) (*sqlc.ArenaGoldenReservePromotion, error) {
	if r == nil || r.tx == nil || !validArenaGoldenPromotionInput(in) {
		return nil, domain.ErrValidation
	}
	var promotion sqlc.ArenaGoldenReservePromotion
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockArenaGoldenAttempt(txCtx, arenaGoldenLockParams(in.Scope)); err != nil {
			return arenaGoldenLookupError("PromoteReserve - lock attempt", err)
		}
		row, err := querier.CreateArenaGoldenReservePromotion(txCtx, sqlc.CreateArenaGoldenReservePromotionParams{
			ID: in.ID, AttemptID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, ReserveMembershipID: in.ReserveMembershipID,
			ReserveParticipantID: in.ReserveParticipantID, ReplacedMembershipID: in.ReplacedMembershipID,
			ReplacedParticipantID: in.ReplacedParticipantID, Reason: in.Reason,
			PromotedAt: tstz(in.PromotedAt), CreatedAt: tstz(in.CreatedAt),
		})
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaGoldenPostgres - PromoteReserve", err)
		}
		promotion = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &promotion, nil
}

func (r *ArenaGoldenPostgres) OpenDisconnect(
	ctx context.Context,
	in ArenaGoldenDisconnectInput,
) (*sqlc.ArenaGoldenReadyDisconnect, error) {
	if r == nil || r.tx == nil || !validArenaGoldenDisconnectInput(in) {
		return nil, domain.ErrValidation
	}
	var disconnect sqlc.ArenaGoldenReadyDisconnect
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockArenaGoldenAttempt(txCtx, arenaGoldenLockParams(in.Scope)); err != nil {
			return arenaGoldenLookupError("OpenDisconnect - lock attempt", err)
		}
		row, err := querier.CreateArenaGoldenReadyDisconnect(txCtx, sqlc.CreateArenaGoldenReadyDisconnectParams{
			ID: in.ID, MembershipID: in.MembershipID, AttemptID: in.Scope.AttemptID,
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			ParticipantID: in.ParticipantID, SequenceNumber: in.SequenceNumber,
			DisconnectedAt: tstz(in.DisconnectedAt), CreatedAt: tstz(in.CreatedAt),
		})
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaGoldenPostgres - OpenDisconnect", err)
		}
		disconnect = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &disconnect, nil
}

func (r *ArenaGoldenPostgres) CloseDisconnect(
	ctx context.Context,
	in ArenaGoldenDisconnectCloseInput,
) (*sqlc.ArenaGoldenReadyDisconnect, error) {
	if r == nil || r.tx == nil || !validArenaGoldenDisconnectCloseInput(in) {
		return nil, domain.ErrValidation
	}
	var disconnect sqlc.ArenaGoldenReadyDisconnect
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockArenaGoldenAttempt(txCtx, arenaGoldenLockParams(in.Scope)); err != nil {
			return arenaGoldenLookupError("CloseDisconnect - lock attempt", err)
		}
		row, err := querier.CloseArenaGoldenReadyDisconnectCAS(txCtx, sqlc.CloseArenaGoldenReadyDisconnectCASParams{
			NextState: in.NextState, ReconnectedAt: nullableTSTZ(in.ReconnectedAt), ExpiredAt: nullableTSTZ(in.ExpiredAt),
			ID: in.DisconnectID, AttemptID: in.Scope.AttemptID,
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		})
		if err != nil {
			return arenaGoldenCASWriteError("CloseDisconnect", err)
		}
		disconnect = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &disconnect, nil
}

func (r *ArenaGoldenPostgres) RecordSubmission(
	ctx context.Context,
	in ArenaGoldenSubmissionInput,
) (*sqlc.ArenaGoldenProvisionalSubmission, bool, error) {
	if r == nil || r.tx == nil || !validArenaGoldenSubmissionInput(in) {
		return nil, false, domain.ErrValidation
	}
	var submission sqlc.ArenaGoldenProvisionalSubmission
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockArenaGoldenAttempt(txCtx, arenaGoldenLockParams(in.Scope)); err != nil {
			return arenaGoldenLookupError("RecordSubmission - lock attempt", err)
		}
		existing, err := querier.GetArenaGoldenSubmissionByIdempotencyKey(txCtx, in.IdempotencyKey)
		if err == nil {
			if !arenaGoldenSubmissionMatches(existing, in) {
				return domain.ErrConflict
			}
			submission = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("ArenaGoldenPostgres - RecordSubmission - idempotency: %w", err)
		}
		row, err := querier.CreateArenaGoldenProvisionalSubmission(
			txCtx,
			sqlc.CreateArenaGoldenProvisionalSubmissionParams{
				ID: in.ID, AttemptID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID,
				RosterID: in.Scope.RosterID, MembershipID: in.MembershipID, ParticipantID: in.ParticipantID,
				ServerSequence: in.ServerSequence, IdempotencyKey: in.IdempotencyKey,
				ProvisionalPosition: in.ProvisionalPosition, ElapsedMilliseconds: in.ElapsedMilliseconds,
				Status: in.Status, RejectionReason: optionalTrimmedString(in.RejectionReason),
				PayloadDigest: append([]byte(nil), in.PayloadDigest[:]...), SubmittedAt: tstz(in.SubmittedAt),
				ReceivedAt: tstz(in.ReceivedAt), CreatedAt: tstz(in.CreatedAt),
			},
		)
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaGoldenPostgres - RecordSubmission", err)
		}
		submission = row
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &submission, changed, nil
}

func (r *ArenaGoldenPostgres) CommitPosition(
	ctx context.Context,
	in ArenaGoldenPositionInput,
) (*sqlc.ArenaGoldenPositionCommit, error) {
	if r == nil || r.tx == nil || !validArenaGoldenPositionInput(in) {
		return nil, domain.ErrValidation
	}
	var position sqlc.ArenaGoldenPositionCommit
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockArenaGoldenAttempt(txCtx, arenaGoldenLockParams(in.Scope)); err != nil {
			return arenaGoldenLookupError("CommitPosition - lock attempt", err)
		}
		row, err := querier.CreateArenaGoldenPositionCommit(txCtx, sqlc.CreateArenaGoldenPositionCommitParams{
			ID: in.ID, AttemptID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, MembershipID: in.MembershipID, ParticipantID: in.ParticipantID,
			ProvisionalSubmissionID:  in.ProvisionalSubmissionID,
			PreviousPositionCommitID: nullableUUID(in.PreviousPositionCommitID),
			Position:                 in.Position, CommittedAt: tstz(in.CommittedAt), CreatedAt: tstz(in.CreatedAt),
		})
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaGoldenPostgres - CommitPosition", err)
		}
		position = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &position, nil
}

func (r *ArenaGoldenPostgres) RecordRecovery(
	ctx context.Context,
	in ArenaGoldenRecoveryInput,
) (*sqlc.ArenaGoldenRecoveryRevision, error) {
	if r == nil || r.tx == nil || !validArenaGoldenRecoveryInput(in) {
		return nil, domain.ErrValidation
	}
	var recovery sqlc.ArenaGoldenRecoveryRevision
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.LockArenaGoldenAttempt(txCtx, arenaGoldenLockParams(in.Scope)); err != nil {
			return arenaGoldenLookupError("RecordRecovery - lock attempt", err)
		}
		latest, latestErr := querier.GetLatestArenaGoldenRecoveryRevision(
			txCtx,
			sqlc.GetLatestArenaGoldenRecoveryRevisionParams{
				AttemptID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			},
		)
		revisionNumber := int64(1)
		previousRevisionID := uuid.NullUUID{}
		if latestErr == nil {
			revisionNumber = latest.RevisionNumber + 1
			previousRevisionID = nullableUUIDValue(latest.ID)
		} else if !errors.Is(latestErr, pgx.ErrNoRows) {
			return fmt.Errorf("ArenaGoldenPostgres - RecordRecovery - current: %w", latestErr)
		}
		row, err := querier.CreateArenaGoldenRecoveryRevision(txCtx, sqlc.CreateArenaGoldenRecoveryRevisionParams{
			ID: in.ID, AttemptID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, RevisionNumber: revisionNumber,
			PreviousRevisionID: previousRevisionID, State: in.State,
			RecoveryEvidence: append([]byte(nil), in.RecoveryEvidence...),
			RecordedAt:       tstz(in.RecordedAt), CreatedAt: tstz(in.CreatedAt),
		})
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaGoldenPostgres - RecordRecovery", err)
		}
		recovery = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &recovery, nil
}

func (r *ArenaGoldenPostgres) Get(
	ctx context.Context,
	scope ArenaGoldenScope,
) (*ArenaGoldenRecord, error) {
	if r == nil || r.tx == nil || !validArenaGoldenScope(scope) {
		return nil, domain.ErrValidation
	}
	return loadArenaGoldenRecord(ctx, r.tx.Querier(ctx), scope)
}

func (r *ArenaGoldenPostgres) History(
	ctx context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
) ([]sqlc.ArenaGoldenAttempt, error) {
	if r == nil || r.tx == nil || tournamentID == uuid.Nil || rosterID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	rows, err := r.tx.Querier(ctx).ListArenaGoldenAttempts(ctx, sqlc.ListArenaGoldenAttemptsParams{
		TournamentID: tournamentID, RosterID: rosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaGoldenPostgres - History: %w", err)
	}
	return rows, nil
}

func loadArenaGoldenRecord(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ArenaGoldenScope,
) (*ArenaGoldenRecord, error) {
	attempt, err := querier.GetArenaGoldenAttemptScoped(ctx, sqlc.GetArenaGoldenAttemptScopedParams{
		ID: scope.AttemptID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, arenaGoldenLookupError("Get", err)
	}
	memberships, err := querier.ListArenaGoldenMemberships(ctx, sqlc.ListArenaGoldenMembershipsParams{
		AttemptID: scope.AttemptID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaGoldenPostgres - Get - memberships: %w", err)
	}
	disconnects, err := querier.ListArenaGoldenReadyDisconnects(ctx, sqlc.ListArenaGoldenReadyDisconnectsParams{
		AttemptID: scope.AttemptID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaGoldenPostgres - Get - disconnects: %w", err)
	}
	submissions, err := querier.ListArenaGoldenProvisionalSubmissions(
		ctx,
		sqlc.ListArenaGoldenProvisionalSubmissionsParams{
			AttemptID: scope.AttemptID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ArenaGoldenPostgres - Get - submissions: %w", err)
	}
	positions, err := querier.ListArenaGoldenPositionCommits(ctx, sqlc.ListArenaGoldenPositionCommitsParams{
		AttemptID: scope.AttemptID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaGoldenPostgres - Get - positions: %w", err)
	}
	promotions, err := querier.ListArenaGoldenReservePromotions(ctx, sqlc.ListArenaGoldenReservePromotionsParams{
		AttemptID: scope.AttemptID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaGoldenPostgres - Get - promotions: %w", err)
	}
	recoveries, err := querier.ListArenaGoldenRecoveryRevisions(ctx, sqlc.ListArenaGoldenRecoveryRevisionsParams{
		AttemptID: scope.AttemptID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaGoldenPostgres - Get - recoveries: %w", err)
	}
	return &ArenaGoldenRecord{
		Attempt: attempt, Memberships: memberships, Disconnects: disconnects,
		Submissions: submissions, Positions: positions, Promotions: promotions, Recoveries: recoveries,
	}, nil
}

func validArenaGoldenCreateAttemptInput(in ArenaGoldenCreateAttemptInput) bool {
	if !validArenaGoldenScope(in.Scope) || in.AttemptNumber < 1 ||
		!validServerTime(in.CreatedAt) || len(in.Memberships) < 2 {
		return false
	}
	if (in.AttemptNumber == 1) != (in.PreviousAttemptID == nil) ||
		(in.PreviousAttemptID != nil && (*in.PreviousAttemptID == uuid.Nil || *in.PreviousAttemptID == in.Scope.AttemptID)) {
		return false
	}
	return validArenaGoldenMembershipSet(in.Memberships, in.CreatedAt)
}

func validArenaGoldenMembershipSet(memberships []ArenaGoldenMembershipInput, createdAt time.Time) bool {
	participantIDs := make(map[uuid.UUID]struct{}, len(memberships))
	membershipIDs := make(map[uuid.UUID]struct{}, len(memberships))
	reservePositions := make(map[int16]struct{})
	activeCount := 0
	for _, member := range memberships {
		if member.ID == uuid.Nil || member.ParticipantID == uuid.Nil || !validServerTime(member.SelectedAt) ||
			member.SelectedAt.After(createdAt) {
			return false
		}
		if _, exists := participantIDs[member.ParticipantID]; exists {
			return false
		}
		if _, exists := membershipIDs[member.ID]; exists {
			return false
		}
		participantIDs[member.ParticipantID] = struct{}{}
		membershipIDs[member.ID] = struct{}{}
		if !validArenaGoldenMembership(member, reservePositions) {
			return false
		}
		if member.SelectionKind != "excluded" {
			activeCount++
		}
	}
	return activeCount >= 2
}

func validArenaGoldenMembership(
	member ArenaGoldenMembershipInput,
	reservePositions map[int16]struct{},
) bool {
	switch member.SelectionKind {
	case "direct":
		return validArenaGoldenDirectMember(member)
	case "reserve":
		return validArenaGoldenReserveMember(member, reservePositions)
	case "excluded":
		return validArenaGoldenExcludedMember(member)
	default:
		return false
	}
}

func validArenaGoldenDirectMember(member ArenaGoldenMembershipInput) bool {
	return member.ReservePosition == nil && member.ExcludedAt == nil && member.ExclusionReason == ""
}

func validArenaGoldenReserveMember(
	member ArenaGoldenMembershipInput,
	reservePositions map[int16]struct{},
) bool {
	if member.ReservePosition == nil || *member.ReservePosition < 1 || *member.ReservePosition > 16 ||
		member.ExcludedAt != nil || member.ExclusionReason != "" {
		return false
	}
	if _, exists := reservePositions[*member.ReservePosition]; exists {
		return false
	}
	reservePositions[*member.ReservePosition] = struct{}{}
	return true
}

func validArenaGoldenExcludedMember(member ArenaGoldenMembershipInput) bool {
	return member.ReservePosition == nil && member.ExcludedAt != nil && validServerTime(*member.ExcludedAt) &&
		!member.ExcludedAt.Before(member.SelectedAt) && validTrimmedText(member.ExclusionReason)
}

func validArenaGoldenTransitionInput(in ArenaGoldenAttemptTransitionInput) bool {
	if !validArenaGoldenScope(in.Scope) || !validArenaGoldenAttemptState(in.ExpectedState) ||
		!validArenaGoldenAttemptState(in.NextState) || in.ExpectedState == in.NextState {
		return false
	}
	times := []*time.Time{
		in.DisclosedAt, in.ReadyAt, in.StartedAt, in.PausedAt, in.CompletedAt, in.CancelledAt, in.SupersededAt,
	}
	for _, value := range times {
		if value != nil && !validServerTime(*value) {
			return false
		}
	}
	if in.CancellationReason != "" && strings.TrimSpace(in.CancellationReason) != in.CancellationReason {
		return false
	}
	return in.SupersessionReason == "" || strings.TrimSpace(in.SupersessionReason) == in.SupersessionReason
}

func validArenaGoldenAttemptState(state string) bool {
	switch state {
	case "prepared", "ready", "active", "technical_pause", "completed", "cancelled", "superseded":
		return true
	default:
		return false
	}
}

func validArenaGoldenPromotionInput(in ArenaGoldenPromotionInput) bool {
	return validArenaGoldenScope(in.Scope) && in.ID != uuid.Nil && in.ReserveMembershipID != uuid.Nil &&
		in.ReserveParticipantID != uuid.Nil && in.ReplacedMembershipID != uuid.Nil &&
		in.ReplacedParticipantID != uuid.Nil && in.ReserveParticipantID != in.ReplacedParticipantID &&
		strings.TrimSpace(in.Reason) == in.Reason && in.Reason != "" &&
		validServerTime(in.PromotedAt) && validServerTime(in.CreatedAt) && !in.PromotedAt.After(in.CreatedAt)
}

func validArenaGoldenDisconnectInput(in ArenaGoldenDisconnectInput) bool {
	return validArenaGoldenScope(in.Scope) && in.ID != uuid.Nil && in.MembershipID != uuid.Nil &&
		in.ParticipantID != uuid.Nil && in.SequenceNumber >= 1 &&
		validServerTime(in.DisconnectedAt) && validServerTime(in.CreatedAt) && !in.DisconnectedAt.After(in.CreatedAt)
}

func validArenaGoldenDisconnectCloseInput(in ArenaGoldenDisconnectCloseInput) bool {
	if !validArenaGoldenScope(in.Scope) || in.DisconnectID == uuid.Nil {
		return false
	}
	switch in.NextState {
	case "reconnected":
		return in.ReconnectedAt != nil && validServerTime(*in.ReconnectedAt) && in.ExpiredAt == nil
	case "expired":
		return in.ReconnectedAt == nil && in.ExpiredAt != nil && validServerTime(*in.ExpiredAt)
	default:
		return false
	}
}

func validArenaGoldenSubmissionInput(in ArenaGoldenSubmissionInput) bool {
	if !validArenaGoldenSubmissionIdentity(in) || !validArenaGoldenSubmissionTimes(in) {
		return false
	}
	if in.Status == "accepted" {
		return in.RejectionReason == ""
	}
	return in.Status == "rejected" && strings.TrimSpace(in.RejectionReason) == in.RejectionReason &&
		in.RejectionReason != ""
}

func validArenaGoldenSubmissionIdentity(in ArenaGoldenSubmissionInput) bool {
	return validArenaGoldenScope(in.Scope) && in.ID != uuid.Nil && in.MembershipID != uuid.Nil &&
		in.ParticipantID != uuid.Nil && in.ServerSequence >= 1 && in.IdempotencyKey != uuid.Nil &&
		in.ProvisionalPosition >= 1 && in.ProvisionalPosition <= 16 && in.ElapsedMilliseconds >= 0 &&
		!zeroDigest(in.PayloadDigest[:])
}

func validArenaGoldenSubmissionTimes(in ArenaGoldenSubmissionInput) bool {
	return validServerTime(in.SubmittedAt) && validServerTime(in.ReceivedAt) && validServerTime(in.CreatedAt) &&
		!in.SubmittedAt.After(in.ReceivedAt) && !in.ReceivedAt.After(in.CreatedAt)
}

func validArenaGoldenPositionInput(in ArenaGoldenPositionInput) bool {
	return validArenaGoldenScope(in.Scope) && in.ID != uuid.Nil && in.MembershipID != uuid.Nil &&
		in.ParticipantID != uuid.Nil && in.ProvisionalSubmissionID != uuid.Nil && in.Position >= 1 && in.Position <= 16 &&
		(in.PreviousPositionCommitID == nil || *in.PreviousPositionCommitID != uuid.Nil) &&
		validServerTime(in.CommittedAt) && validServerTime(in.CreatedAt) && !in.CommittedAt.After(in.CreatedAt)
}

func validArenaGoldenRecoveryInput(in ArenaGoldenRecoveryInput) bool {
	if !validArenaGoldenScope(in.Scope) || in.ID == uuid.Nil || !validServerTime(in.RecordedAt) ||
		!validServerTime(in.CreatedAt) || in.RecordedAt.After(in.CreatedAt) || !json.Valid(in.RecoveryEvidence) {
		return false
	}
	var evidence map[string]json.RawMessage
	if err := json.Unmarshal(in.RecoveryEvidence, &evidence); err != nil || len(evidence) == 0 {
		return false
	}
	switch in.State {
	case "stable", "technical_pause", "recovering", "resumed":
		return true
	default:
		return false
	}
}

func validArenaGoldenScope(scope ArenaGoldenScope) bool {
	return scope.TournamentID != uuid.Nil && scope.RosterID != uuid.Nil && scope.AttemptID != uuid.Nil
}

func arenaGoldenLineageMatches(
	attempts []sqlc.ArenaGoldenAttempt,
	attemptNumber int32,
	previousAttemptID *uuid.UUID,
) bool {
	if len(attempts)+1 != int(attemptNumber) {
		return false
	}
	if len(attempts) == 0 {
		return previousAttemptID == nil
	}
	return previousAttemptID != nil && attempts[len(attempts)-1].ID == *previousAttemptID
}

func arenaGoldenSubmissionMatches(row sqlc.ArenaGoldenProvisionalSubmission, in ArenaGoldenSubmissionInput) bool {
	return row.ID == in.ID && row.AttemptID == in.Scope.AttemptID && row.TournamentID == in.Scope.TournamentID &&
		row.RosterID == in.Scope.RosterID && row.MembershipID == in.MembershipID &&
		row.ParticipantID == in.ParticipantID && row.ServerSequence == in.ServerSequence &&
		row.ProvisionalPosition == in.ProvisionalPosition && row.ElapsedMilliseconds == in.ElapsedMilliseconds &&
		row.Status == in.Status && stringValue(row.RejectionReason) == in.RejectionReason &&
		bytes.Equal(row.PayloadDigest, in.PayloadDigest[:]) && row.SubmittedAt.Time.Equal(in.SubmittedAt) &&
		row.ReceivedAt.Time.Equal(in.ReceivedAt) && row.CreatedAt.Time.Equal(in.CreatedAt)
}

func arenaGoldenLockParams(scope ArenaGoldenScope) sqlc.LockArenaGoldenAttemptParams {
	return sqlc.LockArenaGoldenAttemptParams{
		ID: scope.AttemptID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	}
}

func arenaGoldenLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArenaGoldenNotFound
	}
	return fmt.Errorf("ArenaGoldenPostgres - %s: %w", operation, err)
}

func arenaGoldenCASWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapArenaRepositoryWriteError("ArenaGoldenPostgres - "+operation, err)
}
