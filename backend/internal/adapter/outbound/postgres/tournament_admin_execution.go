package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	executiondraft "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// TournamentAdminExecutionPostgres keeps the historical root package API.
// Core execution lives in tournament/admin/execution; the root fields remain
// for the unmoved normal-pause, reconnect, and Swiss-draft workflows.
type TournamentAdminExecutionPostgres struct {
	tx    *TxManager
	swiss *SwissPostgres
	waves *WavePostgres
	inner *executionrepo.TournamentAdminExecutionPostgres
}

type tournamentAdminExecutionWaveWriter struct {
	waves *WavePostgres
}

func (w tournamentAdminExecutionWaveWriter) Create(
	ctx context.Context,
	in executionrepo.WaveCreateInput,
) error {
	series := make([]WaveSeriesInput, len(in.Series))
	for index, source := range in.Series {
		series[index] = WaveSeriesInput{
			ID: source.ID, FirstParticipantID: source.FirstParticipantID,
			SecondParticipantID: source.SecondParticipantID, Format: source.Format,
			InitialScoreRevisionID: source.InitialScoreRevisionID,
		}
	}
	_, err := w.waves.Create(ctx, WaveCreateInput{
		ID: in.ID, TournamentID: in.TournamentID, RosterID: in.RosterID,
		RevisionID: in.RevisionID, ParticipantIDs: append([]uuid.UUID(nil), in.ParticipantIDs...),
		Series: series, CommandID: in.CommandID,
		SourceProjectionRevisionID: in.SourceProjectionRevisionID,
		SourceProjectionRevision:   in.SourceProjectionRevision,
		CreatedAt:                  in.CreatedAt,
	})
	return err
}

func (w tournamentAdminExecutionWaveWriter) OpenReadyWindow(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedRevision int64,
	in executionrepo.ReadyWindowInput,
) (bool, error) {
	_, changed, err := w.waves.OpenReadyWindow(
		ctx, tournamentID, waveID, expectedRevision,
		ReadyWindowInput{ID: in.ID, RevisionID: in.RevisionID, OpenedAt: in.OpenedAt, Deadline: in.Deadline},
	)
	return changed, err
}

func (w tournamentAdminExecutionWaveWriter) Start(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	expectedRevision int64,
	startedAt time.Time,
) (bool, error) {
	_, changed, err := w.waves.Start(ctx, tournamentID, waveID, windowID, expectedRevision, startedAt)
	return changed, err
}

func NewTournamentAdminExecutionPostgres(tx *TxManager) *TournamentAdminExecutionPostgres {
	repository := &TournamentAdminExecutionPostgres{
		tx: tx, swiss: NewSwissPostgres(tx), waves: NewWavePostgres(tx),
	}
	repository.inner = executionrepo.NewTournamentAdminExecutionPostgresWithDependencies(
		tx, repository.swiss, tournamentAdminExecutionWaveWriter{waves: repository.waves},
		repository.materializeSwissDraftBO1,
	)
	return repository
}

func (r *TournamentAdminExecutionPostgres) LockPairingAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
) (tournamentadmin.PairingAuthority, error) {
	if r == nil || r.inner == nil {
		return tournamentadmin.PairingAuthority{}, domain.ErrValidation
	}
	return r.inner.LockPairingAuthority(ctx, tournamentID)
}

func (r *TournamentAdminExecutionPostgres) FindPairingCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*tournamentadmin.PairingCommandRecord, error) {
	if r == nil || r.inner == nil {
		return nil, domain.ErrValidation
	}
	return r.inner.FindPairingCommand(ctx, tournamentID, commandID)
}

func (r *TournamentAdminExecutionPostgres) ReadExecutionTime(ctx context.Context) (time.Time, error) {
	if r == nil || r.inner == nil {
		return time.Time{}, domain.ErrValidation
	}
	return r.inner.ReadExecutionTime(ctx)
}

func (r *TournamentAdminExecutionPostgres) CommitPairing(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) (tournamentadmin.SwissRoundView, error) {
	if r == nil || r.inner == nil {
		return tournamentadmin.SwissRoundView{}, domain.ErrValidation
	}
	return r.inner.CommitPairing(ctx, plan)
}

func (r *TournamentAdminExecutionPostgres) SavePairingCommand(
	ctx context.Context,
	record tournamentadmin.PairingCommandRecord,
) error {
	if r == nil || r.inner == nil {
		return domain.ErrValidation
	}
	return r.inner.SavePairingCommand(ctx, record)
}

func (r *TournamentAdminExecutionPostgres) LockWaveAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
) (tournamentadmin.WaveAuthority, error) {
	if r == nil || r.inner == nil {
		return tournamentadmin.WaveAuthority{}, domain.ErrValidation
	}
	return r.inner.LockWaveAuthority(ctx, tournamentID, waveID)
}

func (r *TournamentAdminExecutionPostgres) FindWaveCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*tournamentadmin.WaveCommandRecord, error) {
	if r == nil || r.inner == nil {
		return nil, domain.ErrValidation
	}
	return r.inner.FindWaveCommand(ctx, tournamentID, commandID)
}

func (r *TournamentAdminExecutionPostgres) CommitWave(
	ctx context.Context,
	mutation tournamentadmin.WaveMutation,
) (tournamentadmin.WaveView, error) {
	if r == nil || r.inner == nil {
		return tournamentadmin.WaveView{}, domain.ErrValidation
	}
	return r.inner.CommitWave(ctx, mutation)
}

func (r *TournamentAdminExecutionPostgres) SaveWaveCommand(
	ctx context.Context,
	record tournamentadmin.WaveCommandRecord,
) error {
	if r == nil || r.inner == nil {
		return domain.ErrValidation
	}
	return r.inner.SaveWaveCommand(ctx, record)
}

func (r *TournamentAdminExecutionPostgres) materializeSwissRandomBO1(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	if r == nil || r.inner == nil {
		return domain.ErrValidation
	}
	return r.inner.MaterializeSwissRandomBO1(ctx, plan)
}

func (r *TournamentAdminExecutionPostgres) materializeSwissAdminBO1(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	if r == nil || r.inner == nil {
		return domain.ErrValidation
	}
	return r.inner.MaterializeSwissAdminBO1(ctx, plan)
}

func swissCategoryRevisionParams(
	revision executiondraft.CategoryRevision,
	lock executiondraft.CategoryLock,
	normalPoolID uuid.UUID,
) (sqlc.CreateSwissCategoryRevisionParams, error) {
	return executionrepo.SwissCategoryRevisionParams(revision, lock, normalPoolID)
}

func validTournamentAdminExecutionRepository(
	ctx context.Context,
	repository *TournamentAdminExecutionPostgres,
) bool {
	return ctx != nil && repository != nil && repository.tx != nil && repository.swiss != nil &&
		repository.waves != nil && repository.inner != nil
}

func executionWriteError(operation string, err error) error {
	return mapRepositoryWriteError("TournamentAdminExecutionPostgres - "+operation, err)
}

var _ tournamentadmin.ExecutionWorkflowRepository = (*TournamentAdminExecutionPostgres)(nil)
