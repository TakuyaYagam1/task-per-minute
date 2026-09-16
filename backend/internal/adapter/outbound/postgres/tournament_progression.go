package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	progressionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/progression"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	projectionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

// TournamentProgressionPostgres preserves the root package facade while the
// durable progression implementation lives in its bounded child package.
type TournamentProgressionPostgres struct {
	tx    *TxManager
	inner *progressionrepo.TournamentProgressionPostgres
}

var (
	_ tournamentprogression.Repository                  = (*TournamentProgressionPostgres)(nil)
	_ tournamentprogression.SwissTerminalEvidenceReader = (*TournamentProgressionPostgres)(nil)
	_ tournamentprogression.Transitioner                = (*TournamentProgressionPostgres)(nil)
	_ tournamentprogression.PlayoffProjectionPublisher  = (*TournamentProgressionPostgres)(nil)
)

func NewTournamentProgressionPostgres(tx *TxManager) *TournamentProgressionPostgres {
	return &TournamentProgressionPostgres{tx: tx, inner: progressionrepo.NewTournamentProgressionPostgres(tx)}
}

func (r *TournamentProgressionPostgres) TransitionStage(
	ctx context.Context,
	command tournamentprogression.TransitionCommand,
) (usecase.TournamentView, bool, error) {
	if r == nil || r.inner == nil {
		return usecase.TournamentView{}, false, domain.ErrValidation
	}
	return r.inner.TransitionStage(ctx, command)
}

func (r *TournamentProgressionPostgres) FindStageProgression(
	ctx context.Context,
	tournamentID, commandID uuid.UUID,
) (*tournamentprogression.Receipt, error) {
	if r == nil || r.inner == nil {
		return nil, domain.ErrValidation
	}
	return r.inner.FindStageProgression(ctx, tournamentID, commandID)
}

func (r *TournamentProgressionPostgres) LoadSwissEvidence(
	ctx context.Context,
	authority tournamentprogression.Authority,
) (tournamentprogression.SwissEvidence, error) {
	if r == nil || r.inner == nil {
		return tournamentprogression.SwissEvidence{}, domain.ErrValidation
	}
	return r.inner.LoadSwissEvidence(ctx, authority)
}

func (r *TournamentProgressionPostgres) LoadGoldenEvidence(
	ctx context.Context,
	authority tournamentprogression.Authority,
) (tournamentprogression.GoldenEvidence, error) {
	if r == nil || r.inner == nil {
		return tournamentprogression.GoldenEvidence{}, domain.ErrValidation
	}
	return r.inner.LoadGoldenEvidence(ctx, authority)
}

func (r *TournamentProgressionPostgres) PersistStageProgression(
	ctx context.Context,
	plan tournamentprogression.Plan,
	publication *tournamentprogression.PlayoffPublication,
) (tournamentprogression.PersistenceReceipt, error) {
	if r == nil || r.inner == nil {
		return tournamentprogression.PersistenceReceipt{}, domain.ErrValidation
	}
	return r.inner.PersistStageProgression(ctx, plan, publication)
}

func (r *TournamentProgressionPostgres) PublishPlayoffStage(
	ctx context.Context,
	plan tournamentprogression.Plan,
) (tournamentprogression.PlayoffPublication, error) {
	if r == nil || r.inner == nil {
		return tournamentprogression.PlayoffPublication{}, domain.ErrValidation
	}
	return r.inner.PublishPlayoffStage(ctx, plan)
}

func (r *TournamentProgressionPostgres) LoadLockedSwissTerminalEvidence(
	ctx context.Context,
	command tournamentprogression.Command,
	authority tournamentprogression.Authority,
) (playoff.ProgressionSwissInput, error) {
	if r == nil || r.inner == nil {
		return playoff.ProgressionSwissInput{}, domain.ErrValidation
	}
	return r.inner.LoadLockedSwissTerminalEvidence(ctx, command, authority)
}

// persistFinalSwissReceipt keeps the private root bridge used by result
// publication while the implementation remains owned by the child package.
func (r *TournamentProgressionPostgres) persistFinalSwissReceipt(
	ctx context.Context,
	scope ProjectionScope,
	projectionID uuid.UUID,
	now time.Time,
) error {
	if r == nil || r.inner == nil {
		return domain.ErrValidation
	}
	return r.inner.PersistFinalSwissReceipt(ctx, scope, projectionID, now)
}

func progressionUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	return progressionrepo.ProgressionUUIDPointer(value)
}

func progressionRoundRevisionIDs(
	rounds []sqlc.LockTournamentProgressionSwissRoundsRow,
	proofs []sqlc.SwissRoundLockProof,
) map[uuid.UUID]uuid.UUID {
	return progressionrepo.ProgressionRoundRevisionIDs(rounds, proofs)
}

func progressionCanonicalSwissInput(
	tournamentID uuid.UUID,
	roundRevisionIDs map[uuid.UUID]uuid.UUID,
	participants []sqlc.LockTournamentProgressionParticipantsRow,
	ledger []sqlc.LockTournamentProgressionSwissLedgerRow,
) (projectionusecase.CanonicalMaterializationInput, error) {
	return progressionrepo.ProgressionCanonicalSwissInput(tournamentID, roundRevisionIDs, participants, ledger)
}

func progressionReceiptRoundProofs(
	authority tournamentprogression.Authority,
	roots []sqlc.SwissRoundLockProof,
	members []sqlc.SwissRoundLockProofMember,
	series []sqlc.SwissRoundLockProofSeries,
) (map[uuid.UUID]swissusecase.RoundLockProof, error) {
	return progressionrepo.ProgressionReceiptRoundProofs(authority, roots, members, series)
}
