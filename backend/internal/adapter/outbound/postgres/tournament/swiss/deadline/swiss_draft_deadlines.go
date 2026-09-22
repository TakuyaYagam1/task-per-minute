package deadline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

const SwissDraftDeadlineMaximumBatchSize int32 = 256

// SwissDraftDeadlinePostgres combines the Swiss due scan with the existing
// participant draft repository. Timeout commits therefore retain the same
// transaction boundary and immutable revision CAS as player actions.
type SwissDraftDeadlinePostgres struct {
	ParticipantDraftRepository

	tx         *db.TxManager
	postseason FinalDraftActivator
}

// FinalDraftActivator is optional for compatibility with isolated Swiss
// repository tests. Production wiring supplies the terminal coordinator so a
// completed BO3 final draft activates its first Wave in the same transaction
// as the final automatic action.
type FinalDraftActivator interface {
	ActivateFinalAfterDraft(ctx context.Context, command playoff.TerminalDraftCommand) (playoff.TerminalReceipt, error)
}

// ParticipantDraftRepository contains the participant-draft operations that
// the deadline adapter delegates after its Swiss scope checks.
type ParticipantDraftRepository interface {
	LoadDraft(ctx context.Context, draftID uuid.UUID) (*draftusecase.Execution, error)
	FindDraftCommand(ctx context.Context, draftID, commandID uuid.UUID) (*draftusecase.Execution, error)
	CommitDraftRevisions(
		ctx context.Context,
		expected draftusecase.RevisionExpectation,
		revisions []draftusecase.Execution,
	) (*draftusecase.Execution, bool, error)
}

func NewSwissDraftDeadlinePostgres(
	tx *db.TxManager,
	drafts ParticipantDraftRepository,
) *SwissDraftDeadlinePostgres {
	return NewSwissDraftDeadlinePostgresWithActivator(tx, drafts, nil)
}

func NewSwissDraftDeadlinePostgresWithActivator(
	tx *db.TxManager,
	drafts ParticipantDraftRepository,
	postseason FinalDraftActivator,
) *SwissDraftDeadlinePostgres {
	return &SwissDraftDeadlinePostgres{ParticipantDraftRepository: drafts, tx: tx, postseason: postseason}
}

// CommitDraftRevisions rechecks the durable Swiss scope after the due scan and
// before the participant repository appends a timeout revision. The shared
// tournament prefix serializes this check with pause/cancel and materialized
// result writers; the embedded repository then keeps its own revision CAS and
// completion graph in the same transaction.
func (repository *SwissDraftDeadlinePostgres) CommitDraftRevisions(
	ctx context.Context,
	expected draftusecase.RevisionExpectation,
	revisions []draftusecase.Execution,
) (committed *draftusecase.Execution, changed bool, resultErr error) {
	if !validSwissDraftDeadlineCommit(ctx, repository, expected, revisions) {
		return nil, false, domain.ErrValidation
	}
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		committed, changed, resultErr = repository.commitDraftRevisionsTx(txCtx, expected, revisions)
		return resultErr
	})
	if err != nil {
		return nil, false, err
	}
	return committed, changed, nil
}

func (repository *SwissDraftDeadlinePostgres) commitDraftRevisionsTx(
	txCtx context.Context,
	expected draftusecase.RevisionExpectation,
	revisions []draftusecase.Execution,
) (*draftusecase.Execution, bool, error) {
	q := repository.tx.Querier(txCtx)
	draftID := revisions[0].ID
	identity, err := resolveSwissDraftDeadlineIdentity(txCtx, q, draftID)
	if err != nil {
		return nil, false, err
	}
	if err := lockTournamentResultScope(txCtx, q, identity.TournamentID, identity.RosterID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, domain.ErrConflict
		}
		return nil, false, fmt.Errorf("swiss draft deadline postgres - lock tournament scope: %w", err)
	}
	if err := lockDraftDeadlineScope(txCtx, q, identity, draftID, expected, revisions[0]); err != nil {
		return nil, false, err
	}
	committed, changed, err := repository.ParticipantDraftRepository.CommitDraftRevisions(txCtx, expected, revisions)
	if err != nil {
		return nil, false, err
	}
	if err := repository.activateFinalAfterDraft(txCtx, identity.TournamentID, committed); err != nil {
		return nil, false, err
	}
	return committed, changed, nil
}

func resolveSwissDraftDeadlineIdentity(
	ctx context.Context,
	q *sqlc.Queries,
	draftID uuid.UUID,
) (sqlc.GetSwissDraftDeadlineIdentityRow, error) {
	identity, err := q.GetSwissDraftDeadlineIdentity(ctx, draftID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.GetSwissDraftDeadlineIdentityRow{}, domain.ErrConflict
	}
	if err != nil {
		return sqlc.GetSwissDraftDeadlineIdentityRow{}, fmt.Errorf("swiss draft deadline postgres - resolve identity: %w", err)
	}
	if identity.TournamentID == uuid.Nil || identity.RosterID == uuid.Nil {
		return sqlc.GetSwissDraftDeadlineIdentityRow{}, domain.ErrConflict
	}
	return identity, nil
}

func lockDraftDeadlineScope(
	ctx context.Context,
	q *sqlc.Queries,
	identity sqlc.GetSwissDraftDeadlineIdentityRow,
	draftID uuid.UUID,
	expected draftusecase.RevisionExpectation,
	revision draftusecase.Execution,
) error {
	switch revision.Format {
	case domain.SeriesFormatBO1:
		scope, err := q.LockSwissDraftDeadlineCommit(ctx, sqlc.LockSwissDraftDeadlineCommitParams{
			DraftID: draftID, TournamentID: identity.TournamentID, RosterID: identity.RosterID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("swiss draft deadline postgres - lock Swiss draft scope: %w", err)
		}
		if !swissDraftDeadlineCommitScopeMatches(scope, draftID, expected, revision) {
			return domain.ErrConflict
		}
	case domain.SeriesFormatBO3:
		scope, err := q.LockFinalDraftDeadlineCommit(ctx, sqlc.LockFinalDraftDeadlineCommitParams{
			DraftID: draftID, TournamentID: identity.TournamentID, RosterID: identity.RosterID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("swiss draft deadline postgres - lock final draft scope: %w", err)
		}
		if !finalDraftDeadlineCommitScopeMatches(scope, draftID, expected, revision) {
			return domain.ErrConflict
		}
	default:
		return domain.ErrValidation
	}
	return nil
}

func (repository *SwissDraftDeadlinePostgres) activateFinalAfterDraft(
	ctx context.Context,
	tournamentID uuid.UUID,
	committed *draftusecase.Execution,
) error {
	if committed == nil || committed.Format != domain.SeriesFormatBO3 ||
		committed.State != draftusecase.ExecutionStateCompleted {
		return nil
	}
	if repository.postseason == nil {
		return domain.ErrInternal
	}
	if _, err := repository.postseason.ActivateFinalAfterDraft(ctx, playoff.TerminalDraftCommand{
		TournamentID: tournamentID,
		SeriesID:     committed.SeriesID,
		DraftID:      committed.ID,
		CommandID:    committed.CommandID,
	}); err != nil {
		return fmt.Errorf("swiss draft deadline postgres - activate final after draft: %w", err)
	}
	return nil
}

func (repository *SwissDraftDeadlinePostgres) ListDueSwissDrafts(
	ctx context.Context,
	observedAt time.Time,
	limit int32,
) ([]draftusecase.SwissDraftDeadline, error) {
	if ctx == nil || repository == nil || repository.tx == nil || repository.ParticipantDraftRepository == nil ||
		!domain.IsValidServerTime(observedAt.Round(0).UTC()) ||
		limit < 1 || limit > SwissDraftDeadlineMaximumBatchSize {
		return nil, domain.ErrValidation
	}
	rows, err := repository.tx.Querier(ctx).ListDueSwissDraftDeadlines(ctx, sqlc.ListDueSwissDraftDeadlinesParams{
		ObservedAt: tstz(observedAt.Round(0).UTC()), BatchSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("swiss draft deadline postgres - list due swiss drafts: %w", err)
	}
	deadlines := make([]draftusecase.SwissDraftDeadline, len(rows))
	for index, row := range rows {
		deadline := draftusecase.SwissDraftDeadline{
			DraftID: row.DraftID, RevisionID: row.DraftRevisionID,
			Revision: row.DraftRevision, ServiceEpoch: row.ServiceEpoch,
			Turn: int(row.TurnNumber), Deadline: row.AbsoluteDeadline.Time.Round(0).UTC(),
		}
		if err := deadline.Validate(); err != nil {
			return nil, fmt.Errorf("swiss draft deadline postgres - map deadline %s: %w", row.DraftID, err)
		}
		deadlines[index] = deadline
	}
	return deadlines, nil
}

var _ draftusecase.DeadlineRepository = (*SwissDraftDeadlinePostgres)(nil)

func validSwissDraftDeadlineCommit(
	ctx context.Context,
	repository *SwissDraftDeadlinePostgres,
	expected draftusecase.RevisionExpectation,
	revisions []draftusecase.Execution,
) bool {
	valid := ctx != nil && repository != nil && repository.tx != nil &&
		repository.ParticipantDraftRepository != nil && expected.RevisionID != uuid.Nil &&
		expected.Revision >= 1 && expected.ServiceEpoch != uuid.Nil && len(revisions) > 0 &&
		revisions[0].ID != uuid.Nil &&
		(revisions[0].Format == domain.SeriesFormatBO1 || revisions[0].Format == domain.SeriesFormatBO3)
	return valid && (revisions[0].Format != domain.SeriesFormatBO3 || repository.postseason != nil)
}

func swissDraftDeadlineCommitScopeMatches(
	scope sqlc.LockSwissDraftDeadlineCommitRow,
	draftID uuid.UUID,
	expected draftusecase.RevisionExpectation,
	revision draftusecase.Execution,
) bool {
	return scope.TournamentState == string(domain.TournamentStateSwiss) &&
		scope.SeriesState == string(domain.SeriesStatePlanned) &&
		scope.SeriesFormat == string(domain.SeriesFormatBO1) &&
		scope.WaveID != uuid.Nil && scope.WaveState == string(domain.WaveStatePlanned) &&
		scope.DraftFormat == string(domain.SeriesFormatBO1) &&
		scope.RevisionState == string(draftusecase.ExecutionStateActive) &&
		scope.DraftID == draftID && scope.SeriesID == revision.SeriesID &&
		scope.RevisionID == expected.RevisionID && scope.Revision == expected.Revision &&
		scope.ServiceEpoch == expected.ServiceEpoch && revision.ID == draftID
}

func finalDraftDeadlineCommitScopeMatches(
	scope sqlc.LockFinalDraftDeadlineCommitRow,
	draftID uuid.UUID,
	expected draftusecase.RevisionExpectation,
	revision draftusecase.Execution,
) bool {
	return scope.TournamentState == string(domain.TournamentStatePlayoffs) &&
		scope.SeriesState == string(domain.SeriesStatePlanned) &&
		scope.SeriesFormat == string(domain.SeriesFormatBO3) &&
		scope.DraftFormat == string(domain.SeriesFormatBO3) && scope.FinalDraftID == draftID &&
		scope.RevisionState == string(draftusecase.ExecutionStateActive) &&
		scope.DraftID == draftID && scope.SeriesID == revision.SeriesID &&
		scope.RevisionID == expected.RevisionID && scope.Revision == expected.Revision &&
		scope.ServiceEpoch == expected.ServiceEpoch && revision.ID == draftID
}
