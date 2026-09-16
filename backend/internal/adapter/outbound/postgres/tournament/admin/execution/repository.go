package execution

import (
	"context"
	"time"

	"github.com/google/uuid"

	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	pauserepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/pause"
	participantdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/draft"
	reconnectrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/reconnect"
	swissdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/swiss/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// Repository composes the execution, pause, and reconnect persistence owned
// by the tournament administration capability.
type Repository struct {
	*TournamentAdminExecutionPostgres
	*pauserepo.TournamentAdminNormalPausePostgres
	*reconnectrepo.TournamentReconnectPostgres
}

// NewRepository builds the complete production repository without depending
// on the compatibility facade in the parent postgres package.
func NewRepository(
	tx *db.TxManager,
	resultFinalizer resultrepo.ProjectionFinalizer,
) *Repository {
	waves := waverepo.NewWavePostgres(tx)
	core := NewTournamentAdminExecutionPostgresWithDependencies(
		tx,
		nil,
		executionWaveWriter{repository: waves},
		func(ctx context.Context, plan tournamentadmin.PairingPlan) error {
			return swissdraft.MaterializeSwissDraftBO1(ctx, tx, plan, createMaterializedSeriesPresence)
		},
	)
	return &Repository{
		TournamentAdminExecutionPostgres: core,
		TournamentAdminNormalPausePostgres: pauserepo.NewTournamentAdminNormalPausePostgresWithDependencies(
			tx,
			participantdraft.ParticipantDraftExecution,
		),
		TournamentReconnectPostgres: reconnectrepo.NewTournamentReconnectPostgresWithDependencies(
			tx,
			resultFinalizer,
		),
	}
}

// MaterializeSwissDraftBO1 persists the draft-mode Swiss execution graph.
func (repository *Repository) MaterializeSwissDraftBO1(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	if repository == nil || repository.TournamentAdminExecutionPostgres == nil {
		return domain.ErrValidation
	}
	return swissdraft.MaterializeSwissDraftBO1(
		ctx,
		repository.tx,
		plan,
		createMaterializedSeriesPresence,
	)
}

type executionWaveWriter struct {
	repository *waverepo.WavePostgres
}

func (writer executionWaveWriter) Create(ctx context.Context, input WaveCreateInput) error {
	series := make([]waverepo.WaveSeriesInput, len(input.Series))
	for index, item := range input.Series {
		series[index] = waverepo.WaveSeriesInput{
			ID: item.ID, FirstParticipantID: item.FirstParticipantID,
			SecondParticipantID: item.SecondParticipantID, Format: item.Format,
			InitialScoreRevisionID: item.InitialScoreRevisionID,
		}
	}
	_, err := writer.repository.Create(ctx, waverepo.WaveCreateInput{
		ID: input.ID, TournamentID: input.TournamentID, RosterID: input.RosterID,
		RevisionID: input.RevisionID, ParticipantIDs: append([]uuid.UUID(nil), input.ParticipantIDs...),
		Series: series, CommandID: input.CommandID,
		SourceProjectionRevisionID: input.SourceProjectionRevisionID,
		SourceProjectionRevision:   input.SourceProjectionRevision,
		CreatedAt:                  input.CreatedAt,
	})
	return err
}

func (writer executionWaveWriter) OpenReadyWindow(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedRevision int64,
	input ReadyWindowInput,
) (bool, error) {
	_, changed, err := writer.repository.OpenReadyWindow(
		ctx,
		tournamentID,
		waveID,
		expectedRevision,
		waverepo.ReadyWindowInput{
			ID: input.ID, RevisionID: input.RevisionID,
			OpenedAt: input.OpenedAt, Deadline: input.Deadline,
		},
	)
	return changed, err
}

func (writer executionWaveWriter) Start(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	expectedRevision int64,
	startedAt time.Time,
) (bool, error) {
	_, changed, err := writer.repository.Start(
		ctx,
		tournamentID,
		waveID,
		windowID,
		expectedRevision,
		startedAt,
	)
	return changed, err
}

var (
	_ tournamentadmin.ExecutionWorkflowRepository    = (*Repository)(nil)
	_ tournamentadmin.NormalPauseExecutionRepository = (*Repository)(nil)
	_ gamestart.StartRepository                      = (*Repository)(nil)
	_ pauseusecase.NormalPauseRepository             = (*Repository)(nil)
	_ pauseusecase.PauseResumeRepository             = (*Repository)(nil)
	_ pauseusecase.PauseResumePresenceRepository     = (*Repository)(nil)
	_ gameusecase.ReconnectRepository                = (*Repository)(nil)
)
