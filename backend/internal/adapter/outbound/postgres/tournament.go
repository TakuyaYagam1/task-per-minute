package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	attendanceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster"
)

const tournamentActiveConstraint = "tournaments_single_active_idx"

var (
	ErrTournamentNotFound = errors.New("tournament repository: tournament not found")
	ErrRosterNotFound     = errors.New("tournament repository: roster not found")
)

type TournamentPostgres struct {
	tx *TxManager
}

type TournamentCatalogPostgres struct {
	tournaments *TournamentPostgres
}

type TournamentAttendancePostgres struct {
	tournaments *TournamentPostgres
}

type TournamentLifecyclePostgres struct {
	tournaments *TournamentPostgres
}

type TournamentRosterPostgres struct {
	tournaments *TournamentPostgres
}

var (
	_ tournamentpause.PauseTransactionManager        = (*TxManager)(nil)
	_ catalogusecase.TournamentRepository            = (*TournamentCatalogPostgres)(nil)
	_ attendanceusecase.AttendanceRepository         = (*TournamentAttendancePostgres)(nil)
	_ lifecycleusecase.TournamentLifecycleRepository = (*TournamentLifecyclePostgres)(nil)
	_ rosterusecase.RosterLockRepository             = (*TournamentRosterPostgres)(nil)
)

type TournamentRecord struct {
	ID              uuid.UUID
	Preset          string
	State           domain.TournamentState
	PausedFromState *domain.TournamentState
	Revision        int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

type RosterRecord struct {
	ID                 uuid.UUID
	TournamentID       uuid.UUID
	Revision           int64
	LockedAt           *time.Time
	ExecutionStartedAt *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ParticipantRecord struct {
	ID           uuid.UUID
	RosterID     uuid.UUID
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Seed         int
	Attendance   domain.AttendanceState
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ReservationRecord struct {
	PlayerID      uuid.UUID
	ReservationID uuid.UUID
	TournamentID  uuid.UUID
	Revision      int64
	AcquiredAt    time.Time
	UpdatedAt     time.Time
}

type TournamentTransitionInput struct {
	ID               uuid.UUID
	ExpectedRevision int64
	ExpectedState    domain.TournamentState
	NextState        domain.TournamentState
	PausedFromState  *domain.TournamentState
	UpdatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
}

type ParticipantInput struct {
	ID         uuid.UUID
	RosterID   uuid.UUID
	PlayerID   uuid.UUID
	Seed       int32
	Attendance domain.AttendanceState
	CreatedAt  time.Time
}

func NewTournamentPostgres(tx *TxManager) *TournamentPostgres {
	return &TournamentPostgres{tx: tx}
}

func NewTournamentCatalogPostgres(tournaments *TournamentPostgres) *TournamentCatalogPostgres {
	return &TournamentCatalogPostgres{tournaments: tournaments}
}

func NewTournamentAttendancePostgres(tournaments *TournamentPostgres) *TournamentAttendancePostgres {
	return &TournamentAttendancePostgres{tournaments: tournaments}
}

func NewTournamentLifecyclePostgres(tournaments *TournamentPostgres) *TournamentLifecyclePostgres {
	return &TournamentLifecyclePostgres{tournaments: tournaments}
}

func NewTournamentRosterPostgres(tournaments *TournamentPostgres) *TournamentRosterPostgres {
	return &TournamentRosterPostgres{tournaments: tournaments}
}

func (r *TournamentPostgres) Create(
	ctx context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (*TournamentRecord, *RosterRecord, error) {
	if r == nil || r.tx == nil || tournamentID == uuid.Nil || rosterID == uuid.Nil || !validServerTime(createdAt) {
		return nil, nil, domain.ErrValidation
	}

	var tournament sqlc.Tournament
	var roster sqlc.Roster
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		binding, bindErr := loadTournamentV1ContentBinding(txCtx, querier, tournamentID, rosterID)
		if bindErr != nil {
			return bindErr
		}
		if bindErr = revalidateTournamentV1ContentPools(txCtx, querier, binding); bindErr != nil {
			return bindErr
		}

		var createErr error
		tournament, createErr = querier.CreateTournament(txCtx, sqlc.CreateTournamentParams{
			ID:        tournamentID,
			CreatedAt: tstz(createdAt),
		})
		if createErr != nil {
			return fmt.Errorf("TournamentPostgres - Create - Querier.CreateTournament: %w", createErr)
		}
		roster, createErr = querier.CreateTournamentRoster(txCtx, sqlc.CreateTournamentRosterParams{
			ID:           rosterID,
			TournamentID: tournamentID,
			CreatedAt:    tstz(createdAt),
		})
		if createErr != nil {
			return fmt.Errorf("TournamentPostgres - Create - Querier.CreateTournamentRoster: %w", createErr)
		}
		if createErr = persistTournamentV1ContentBinding(txCtx, querier, binding, createdAt); createErr != nil {
			return createErr
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return tournamentRecord(tournament), rosterRecord(roster), nil
}

func (r *TournamentPostgres) Get(ctx context.Context, id uuid.UUID) (*TournamentRecord, error) {
	row, err := r.tx.Querier(ctx).GetTournament(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTournamentNotFound
		}
		return nil, fmt.Errorf("TournamentPostgres - Get - Querier.GetTournament: %w", err)
	}
	return tournamentRecord(row), nil
}

func (r *TournamentPostgres) Active(ctx context.Context) (*TournamentRecord, error) {
	row, err := r.tx.Querier(ctx).GetActiveTournament(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("TournamentPostgres - Active - Querier.GetActiveTournament: %w", err)
	}
	return tournamentRecord(row), nil
}

func (r *TournamentPostgres) List(ctx context.Context) ([]TournamentRecord, error) {
	rows, err := r.tx.Querier(ctx).ListTournaments(ctx)
	if err != nil {
		return nil, fmt.Errorf("TournamentPostgres - List - Querier.ListTournaments: %w", err)
	}
	out := make([]TournamentRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, *tournamentRecord(row))
	}
	return out, nil
}

func (r *TournamentPostgres) Transition(
	ctx context.Context,
	in TournamentTransitionInput,
) (*TournamentRecord, bool, error) {
	if err := validateTournamentTransitionInput(in); err != nil {
		return nil, false, err
	}

	var updated sqlc.Tournament
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		updated, err = r.tx.Querier(txCtx).UpdateTournamentCAS(txCtx, sqlc.UpdateTournamentCASParams{
			NextState:        string(in.NextState),
			PausedFromState:  nullableState(in.PausedFromState),
			UpdatedAt:        tstz(in.UpdatedAt),
			StartedAt:        nullableTSTZ(in.StartedAt),
			FinishedAt:       nullableTSTZ(in.FinishedAt),
			ID:               in.ID,
			ExpectedRevision: in.ExpectedRevision,
			ExpectedState:    string(in.ExpectedState),
		})
		if err != nil {
			return err
		}
		if !in.NextState.IsTerminal() {
			return nil
		}
		if _, err = r.tx.Querier(txCtx).ReleaseTournamentReservations(txCtx, in.ID); err != nil {
			return fmt.Errorf("TournamentPostgres - Transition - Querier.ReleaseTournamentReservations: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if isUniqueViolation(err, tournamentActiveConstraint) {
			return nil, false, domain.WrapError(err, domain.ErrConflict)
		}
		return nil, false, fmt.Errorf("TournamentPostgres - Transition - Querier.UpdateTournamentCAS: %w", err)
	}
	return tournamentRecord(updated), true, nil
}

type tournamentV1ContentBinding struct {
	tournamentID         uuid.UUID
	configurationID      uuid.UUID
	publicationID        uuid.UUID
	normalPoolRevisionID uuid.UUID
	goldenPoolRevisionID uuid.UUID
	bo1CategoryPoolID    uuid.UUID
	bo3CategoryPoolID    uuid.UUID
}

func loadTournamentV1ContentBinding(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
) (tournamentV1ContentBinding, error) {
	publication, err := querier.LockCurrentTaskPoolPublication(ctx)
	if err != nil {
		return tournamentV1ContentBinding{}, fmt.Errorf(
			"TournamentPostgres - Create - lock current task pools: %w", err,
		)
	}
	if len(publication) != 2 {
		return tournamentV1ContentBinding{}, fmt.Errorf(
			"TournamentPostgres - Create - current task pools: %w", domain.ErrInvalidContentConfiguration,
		)
	}

	binding := tournamentV1ContentBinding{
		tournamentID:      tournamentID,
		configurationID:   tournamentV1ContentID(tournamentID, rosterID, "configuration"),
		bo1CategoryPoolID: tournamentV1ContentID(tournamentID, rosterID, "category-pool:bo1"),
		bo3CategoryPoolID: tournamentV1ContentID(tournamentID, rosterID, "category-pool:bo3"),
	}
	for _, pool := range publication {
		if pool.PublicationID == uuid.Nil || pool.PoolRevisionID == uuid.Nil ||
			pool.PublicationID != publication[0].PublicationID ||
			pool.PublicationRevision != publication[0].PublicationRevision ||
			pool.PublicationRevision < 1 || pool.PoolRevision != pool.PublicationRevision ||
			!pool.PublishedAt.Valid {
			return tournamentV1ContentBinding{}, fmt.Errorf(
				"TournamentPostgres - Create - invalid current task pools: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		binding.publicationID = pool.PublicationID
		switch domain.AssignmentTaskKind(pool.Kind) {
		case domain.AssignmentTaskKindNormal:
			if binding.normalPoolRevisionID != uuid.Nil {
				return tournamentV1ContentBinding{}, fmt.Errorf(
					"TournamentPostgres - Create - duplicate normal task pool: %w", domain.ErrInvalidContentConfiguration,
				)
			}
			binding.normalPoolRevisionID = pool.PoolRevisionID
		case domain.AssignmentTaskKindGolden:
			if binding.goldenPoolRevisionID != uuid.Nil {
				return tournamentV1ContentBinding{}, fmt.Errorf(
					"TournamentPostgres - Create - duplicate golden task pool: %w", domain.ErrInvalidContentConfiguration,
				)
			}
			binding.goldenPoolRevisionID = pool.PoolRevisionID
		default:
			return tournamentV1ContentBinding{}, fmt.Errorf(
				"TournamentPostgres - Create - unknown task pool: %w", domain.ErrInvalidContentConfiguration,
			)
		}
	}
	if binding.normalPoolRevisionID == uuid.Nil || binding.goldenPoolRevisionID == uuid.Nil ||
		binding.normalPoolRevisionID == binding.goldenPoolRevisionID {
		return tournamentV1ContentBinding{}, fmt.Errorf(
			"TournamentPostgres - Create - incomplete task pools: %w", domain.ErrInvalidContentConfiguration,
		)
	}
	return binding, nil
}

func revalidateTournamentV1ContentPools(
	ctx context.Context,
	querier *sqlc.Queries,
	binding tournamentV1ContentBinding,
) error {
	rows, err := querier.ListTaskPoolVersionHealth(
		ctx,
		[]uuid.UUID{binding.normalPoolRevisionID, binding.goldenPoolRevisionID},
	)
	if err != nil {
		return fmt.Errorf("TournamentPostgres - Create - task pool health: %w", err)
	}
	poolCounts := map[uuid.UUID]int{
		binding.normalPoolRevisionID: 0,
		binding.goldenPoolRevisionID: 0,
	}
	seenVersions := make(map[[2]uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		if row.TaskID == uuid.Nil || row.TaskVersion < 1 || row.PoolRevisionID == uuid.Nil ||
			!row.TaskExists || !row.TaskEnabled || !row.TaskHealthy || !row.TaskMutationLocked {
			return fmt.Errorf(
				"TournamentPostgres - Create - unhealthy task pool version: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		if _, exists := poolCounts[row.PoolRevisionID]; !exists {
			return fmt.Errorf(
				"TournamentPostgres - Create - foreign task pool version: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		wantKind := domain.AssignmentTaskKindNormal
		if row.PoolRevisionID == binding.goldenPoolRevisionID {
			wantKind = domain.AssignmentTaskKindGolden
		}
		if domain.AssignmentTaskKind(row.PoolKind) != wantKind {
			return fmt.Errorf(
				"TournamentPostgres - Create - mismatched task pool kind: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		key := [2]uuid.UUID{row.TaskID, row.PoolRevisionID}
		if _, duplicate := seenVersions[key]; duplicate {
			return fmt.Errorf(
				"TournamentPostgres - Create - duplicate task pool version: %w", domain.ErrInvalidContentConfiguration,
			)
		}
		seenVersions[key] = struct{}{}
		poolCounts[row.PoolRevisionID]++
	}
	if poolCounts[binding.normalPoolRevisionID] == 0 || poolCounts[binding.goldenPoolRevisionID] == 0 {
		return fmt.Errorf(
			"TournamentPostgres - Create - empty task pool: %w", domain.ErrInvalidContentConfiguration,
		)
	}
	return nil
}

func persistTournamentV1ContentBinding(
	ctx context.Context,
	querier *sqlc.Queries,
	binding tournamentV1ContentBinding,
	createdAt time.Time,
) error {
	if _, err := querier.CreateTournamentContentConfiguration(
		ctx,
		sqlc.CreateTournamentContentConfigurationParams{
			ID:                   binding.configurationID,
			TournamentID:         binding.tournamentID,
			Revision:             1,
			PoolPublicationID:    binding.publicationID,
			NormalPoolRevisionID: binding.normalPoolRevisionID,
			GoldenPoolRevisionID: binding.goldenPoolRevisionID,
			CreatedAt:            tstz(createdAt),
		},
	); err != nil {
		return fmt.Errorf("TournamentPostgres - Create - content configuration: %w", err)
	}
	for _, pool := range []struct {
		id         uuid.UUID
		format     domain.SeriesFormat
		categories []domain.Category
	}{
		{
			id: binding.bo1CategoryPoolID, format: domain.SeriesFormatBO1,
			categories: []domain.Category{domain.CategoryCrypto, domain.CategoryReverse, domain.CategoryWeb},
		},
		{
			id: binding.bo3CategoryPoolID, format: domain.SeriesFormatBO3,
			categories: []domain.Category{
				domain.CategoryCrypto,
				domain.CategoryForensics,
				domain.CategoryPwn,
				domain.CategoryReverse,
				domain.CategoryWeb,
			},
		},
	} {
		if _, err := querier.CreateTournamentCategoryPoolRevision(
			ctx,
			sqlc.CreateTournamentCategoryPoolRevisionParams{
				ID:              pool.id,
				ConfigurationID: binding.configurationID,
				Format:          string(pool.format),
				Revision:        1,
				CreatedAt:       tstz(createdAt),
			},
		); err != nil {
			return fmt.Errorf("TournamentPostgres - Create - category pool: %w", err)
		}
		for _, category := range pool.categories {
			if err := querier.CreateTournamentCategoryPoolMembership(
				ctx,
				sqlc.CreateTournamentCategoryPoolMembershipParams{
					CategoryPoolRevisionID: pool.id,
					Category:               string(category),
					CreatedAt:              tstz(createdAt),
				},
			); err != nil {
				return fmt.Errorf("TournamentPostgres - Create - category membership: %w", err)
			}
		}
	}
	for _, stageDefault := range []struct {
		stage          domain.TournamentStage
		format         domain.SeriesFormat
		mode           domain.CategoryMode
		categoryPoolID uuid.UUID
		taskPoolKind   domain.AssignmentTaskKind
	}{
		{
			stage: domain.TournamentStageSwiss, format: domain.SeriesFormatBO1,
			mode: domain.CategoryModeRandom, categoryPoolID: binding.bo1CategoryPoolID,
			taskPoolKind: domain.AssignmentTaskKindNormal,
		},
		{
			stage: domain.TournamentStageGolden, format: domain.SeriesFormatBO1,
			mode: domain.CategoryModeRandom, categoryPoolID: binding.bo1CategoryPoolID,
			taskPoolKind: domain.AssignmentTaskKindGolden,
		},
		{
			stage: domain.TournamentStageSemifinal, format: domain.SeriesFormatBO1,
			mode: domain.CategoryModeDraft, categoryPoolID: binding.bo1CategoryPoolID,
			taskPoolKind: domain.AssignmentTaskKindNormal,
		},
		{
			stage: domain.TournamentStageFinal, format: domain.SeriesFormatBO3,
			mode: domain.CategoryModeDraft, categoryPoolID: binding.bo3CategoryPoolID,
			taskPoolKind: domain.AssignmentTaskKindNormal,
		},
	} {
		if err := querier.CreateTournamentContentStageDefault(
			ctx,
			sqlc.CreateTournamentContentStageDefaultParams{
				ConfigurationID:        binding.configurationID,
				Stage:                  string(stageDefault.stage),
				Format:                 string(stageDefault.format),
				CategoryMode:           string(stageDefault.mode),
				CategoryPoolRevisionID: stageDefault.categoryPoolID,
				TaskPoolKind:           string(stageDefault.taskPoolKind),
				CreatedAt:              tstz(createdAt),
			},
		); err != nil {
			return fmt.Errorf("TournamentPostgres - Create - content stage default: %w", err)
		}
	}
	if _, err := querier.PublishTournamentContentConfiguration(
		ctx,
		sqlc.PublishTournamentContentConfigurationParams{ID: binding.configurationID, PublishedAt: tstz(createdAt)},
	); err != nil {
		return fmt.Errorf("TournamentPostgres - Create - publish content configuration: %w", err)
	}
	return nil
}

func tournamentV1ContentID(tournamentID, rosterID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(tournamentID, []byte("tournament_v1:content:"+rosterID.String()+":"+role))
}
