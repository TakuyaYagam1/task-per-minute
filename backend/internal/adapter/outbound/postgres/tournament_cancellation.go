package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
)

const maxCancellationReasonLength = 512

type TournamentCancellationPostgres struct {
	tx *TxManager
}

func NewTournamentCancellationPostgres(tx *TxManager) *TournamentCancellationPostgres {
	return &TournamentCancellationPostgres{tx: tx}
}

func (r *TournamentCancellationPostgres) GetTournament(
	ctx context.Context,
	id uuid.UUID,
) (*tournamentcancellation.CancellationTournamentRecord, error) {
	if !validCancellationRepository(ctx, r) || id == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetTournament(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tournamentcancellation.ErrTournamentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentCancellationPostgres - GetTournament: %w", err)
	}
	record, err := cancellationTournamentRecord(row)
	if err != nil {
		return nil, fmt.Errorf("TournamentCancellationPostgres - GetTournament - map: %w", err)
	}
	return record, nil
}

func (r *TournamentCancellationPostgres) GetTournamentCancellation(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*tournamentcancellation.TournamentCancellationRecord, error) {
	if !validCancellationRepository(ctx, r) || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FindTournamentCancellation(
		ctx,
		sqlc.FindTournamentCancellationParams{TournamentID: tournamentID, CommandID: commandID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tournamentcancellation.ErrTournamentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentCancellationPostgres - GetTournamentCancellation: %w", err)
	}
	record, err := foundCancellationRecord(row)
	if err != nil {
		return nil, fmt.Errorf("TournamentCancellationPostgres - GetTournamentCancellation - map: %w", err)
	}
	return record, nil
}

func (r *TournamentCancellationPostgres) CancelTournament(
	ctx context.Context,
	in tournamentcancellation.TournamentCancellationInput,
) (*tournamentcancellation.TournamentCancellationRecord, bool, error) {
	if !validCancellationRepository(ctx, r) || !validCancellationInput(in) {
		return nil, false, domain.ErrValidation
	}

	var record *tournamentcancellation.TournamentCancellationRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		record, changed, err = r.cancelTournament(txCtx, in)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if record == nil {
		return nil, false, domain.ErrInternal
	}
	return record, changed, nil
}

func (r *TournamentCancellationPostgres) cancelTournament(
	ctx context.Context,
	in tournamentcancellation.TournamentCancellationInput,
) (*tournamentcancellation.TournamentCancellationRecord, bool, error) {
	querier := r.tx.Querier(ctx)
	authority, err := querier.LockTournamentCancellationAuthority(ctx, in.TournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, tournamentcancellation.ErrTournamentNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("TournamentCancellationPostgres - lock authority: %w", err)
	}

	existing, err := findCancellation(ctx, querier, in)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	if !cancellationAuthorityMatches(authority, in) {
		return nil, false, domain.ErrConflict
	}

	updated, err := querier.CancelTournamentForCancellationCAS(
		ctx,
		sqlc.CancelTournamentForCancellationCASParams{
			CancelledAt:      tstz(in.CancelledAt),
			TournamentID:     in.TournamentID,
			ExpectedRevision: in.ExpectedRevision,
			ExpectedState:    string(in.ExpectedState),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, domain.ErrConflict
	}
	if err != nil {
		return nil, false, cancellationWriteError("cancel tournament", err)
	}
	if _, err = querier.ReleaseTournamentReservations(ctx, in.TournamentID); err != nil {
		return nil, false, cancellationWriteError("release reservations", err)
	}

	evidence, err := createCancellationEvidence(ctx, querier, authority, in, newCancellationEvidence(in.CommandID))
	if err != nil {
		return nil, false, err
	}
	ledger, err := querier.CreateTournamentCancellation(
		ctx, cancellationLedgerParams(authority.RosterID, in, evidence),
	)
	if err != nil {
		return nil, false, cancellationWriteError("create cancellation ledger", err)
	}
	record, err := createdCancellationRecordFromCreate(ledger, updated)
	if err != nil {
		return nil, false, fmt.Errorf("TournamentCancellationPostgres - map commit: %w", err)
	}
	return record, true, nil
}

func createdCancellationRecordFromCreate(
	row sqlc.CreateTournamentCancellationRow,
	tournamentRow sqlc.Tournament,
) (*tournamentcancellation.TournamentCancellationRecord, error) {
	tournament, err := cancellationTournamentRecord(tournamentRow)
	if err != nil {
		return nil, err
	}
	return mapCancellationRecord(
		row.CommandID,
		row.TournamentID,
		row.RosterID,
		row.SourceRevision,
		row.ResultingRevision,
		row.SourceState,
		row.ActorID,
		row.Reason,
		row.AuditEventID,
		row.OutboxEventID,
		row.CancelledAt,
		row.CreatedAt,
		tournament,
	)
}

func findCancellation(
	ctx context.Context,
	querier *sqlc.Queries,
	in tournamentcancellation.TournamentCancellationInput,
) (*tournamentcancellation.TournamentCancellationRecord, error) {
	row, err := querier.FindTournamentCancellation(
		ctx,
		sqlc.FindTournamentCancellationParams{TournamentID: in.TournamentID, CommandID: in.CommandID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentCancellationPostgres - find cancellation: %w", err)
	}
	if row.TournamentID != in.TournamentID || row.CommandID != in.CommandID ||
		row.SourceRevision != in.ExpectedRevision || row.SourceState != string(in.ExpectedState) ||
		row.ActorID != in.ActorID || row.Reason != in.Reason {
		return nil, domain.ErrConflict
	}
	record, err := foundCancellationRecord(row)
	if err != nil {
		return nil, fmt.Errorf("TournamentCancellationPostgres - map cancellation: %w", err)
	}
	return record, nil
}

func createCancellationEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	authority sqlc.LockTournamentCancellationAuthorityRow,
	in tournamentcancellation.TournamentCancellationInput,
	evidence cancellationEvidence,
) (cancellationEvidence, error) {
	if err := querier.LockTournamentCancellationOutboxIdempotency(ctx, evidence.idempotencyKey.String()); err != nil {
		return cancellationEvidence{}, cancellationWriteError("lock cancellation outbox idempotency", err)
	}
	outbox, err := querier.CreateTournamentCancellationOutboxEvent(
		ctx,
		sqlc.CreateTournamentCancellationOutboxEventParams{
			ID: evidence.outboxID, TournamentID: in.TournamentID, RosterID: authority.RosterID,
			ProjectionRevisionID: authority.ProjectionRevisionID, ProjectionRevision: authority.ProjectionRevision,
			IdempotencyKey: evidence.idempotencyKey, CancellationCommandID: in.CommandID,
			CancelledAt: tstz(in.CancelledAt),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return cancellationEvidence{}, domain.ErrConflict
	}
	if err != nil {
		return cancellationEvidence{}, cancellationWriteError("create cancellation outbox", err)
	}
	if outbox.ID != evidence.outboxID || outbox.Sequence < 1 || outbox.ProjectionOrdinal < 1 ||
		outbox.ProjectionRevisionID != authority.ProjectionRevisionID ||
		outbox.ProjectionRevision != authority.ProjectionRevision {
		return cancellationEvidence{}, domain.ErrInternal
	}
	evidence.projectionRevisionID = outbox.ProjectionRevisionID
	evidence.projectionRevision = outbox.ProjectionRevision
	evidence.projectionOrdinal = outbox.ProjectionOrdinal

	auditID, err := querier.CreateTournamentCancellationAuditEvent(
		ctx,
		sqlc.CreateTournamentCancellationAuditEventParams{
			ID: evidence.auditID, TournamentID: in.TournamentID, RosterID: authority.RosterID,
			ActorID: nullableUUIDValue(in.ActorID), Reason: in.Reason,
			SourceRevision: in.ExpectedRevision, ResultingRevision: in.ExpectedRevision + 1,
			CancelledAt: tstz(in.CancelledAt),
		},
	)
	if err != nil {
		return cancellationEvidence{}, cancellationWriteError("create cancellation audit", err)
	}
	if auditID != evidence.auditID {
		return cancellationEvidence{}, domain.ErrInternal
	}
	return evidence, nil
}

func cancellationLedgerParams(
	rosterID uuid.UUID,
	in tournamentcancellation.TournamentCancellationInput,
	evidence cancellationEvidence,
) sqlc.CreateTournamentCancellationParams {
	return sqlc.CreateTournamentCancellationParams{
		CommandID: in.CommandID, TournamentID: in.TournamentID, RosterID: rosterID,
		SourceRevision: in.ExpectedRevision, ResultingRevision: in.ExpectedRevision + 1,
		SourceState: string(in.ExpectedState), ActorID: in.ActorID, Reason: in.Reason,
		AuditEventID: evidence.auditID, OutboxEventID: evidence.outboxID,
		CancelledAt: tstz(in.CancelledAt), ProjectionRevisionID: evidence.projectionRevisionID,
		ProjectionRevision: evidence.projectionRevision, ProjectionOrdinal: evidence.projectionOrdinal,
	}
}

func validCancellationRepository(ctx context.Context, r *TournamentCancellationPostgres) bool {
	return ctx != nil && r != nil && r.tx != nil
}

func validCancellationInput(in tournamentcancellation.TournamentCancellationInput) bool {
	reason := strings.TrimSpace(in.Reason)
	return in.TournamentID != uuid.Nil && in.CommandID != uuid.Nil && in.ActorID != uuid.Nil &&
		in.ExpectedRevision >= 1 && in.ExpectedState.IsValid() && !in.ExpectedState.IsTerminal() &&
		reason != "" && reason == in.Reason && len(reason) <= maxCancellationReasonLength &&
		domain.IsValidServerTime(in.CancelledAt)
}

func cancellationAuthorityMatches(
	authority sqlc.LockTournamentCancellationAuthorityRow,
	in tournamentcancellation.TournamentCancellationInput,
) bool {
	if authority.ID != in.TournamentID || authority.RosterID == uuid.Nil ||
		authority.ProjectionRevisionID == uuid.Nil || authority.ProjectionRevision < 1 ||
		authority.Revision != in.ExpectedRevision || authority.State != string(in.ExpectedState) ||
		!authority.UpdatedAt.Valid {
		return false
	}
	state := domain.TournamentState(authority.State)
	pausedFromState := cancellationPausedFromState(authority.PausedFromState)
	if err := (domain.Tournament{State: state, PausedFromState: pausedFromState}).Validate(); err != nil {
		return false
	}
	return !state.IsTerminal() && !authority.FinishedAt.Valid
}

func cancellationWriteError(operation string, err error) error {
	return mapRepositoryWriteError("TournamentCancellationPostgres - "+operation, err)
}

type cancellationEvidence struct {
	auditID              uuid.UUID
	outboxID             uuid.UUID
	idempotencyKey       uuid.UUID
	projectionRevisionID uuid.UUID
	projectionRevision   int64
	projectionOrdinal    int16
}

func newCancellationEvidence(commandID uuid.UUID) cancellationEvidence {
	return cancellationEvidence{
		auditID:        cancellationEvidenceID(commandID, "audit"),
		outboxID:       cancellationEvidenceID(commandID, "outbox"),
		idempotencyKey: cancellationEvidenceID(commandID, "idempotency"),
	}
}

func cancellationEvidenceID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("tournament-cancellation:"+role))
}

var _ tournamentcancellation.TournamentCancellationRepository = (*TournamentCancellationPostgres)(nil)
