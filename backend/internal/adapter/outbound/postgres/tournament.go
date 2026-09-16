package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	attendanceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster"
)

const (
	tournamentActiveConstraint   = "tournaments_single_active_idx"
	tournamentPublicIDConstraint = "tournaments_public_id_unique"
)

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
	ID                uuid.UUID
	Preset            string
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
	State             domain.TournamentState
	PausedFromState   *domain.TournamentState
	Revision          int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
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

type TournamentCreateInput struct {
	ID                uuid.UUID
	RosterID          uuid.UUID
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
	CreatedAt         time.Time
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
	in TournamentCreateInput,
) (*TournamentRecord, *RosterRecord, error) {
	repository := r.catalogRepository()
	tournament, roster, err := repository.Create(ctx, catalogrepo.TournamentCreateInput{
		ID: in.ID, RosterID: in.RosterID, Name: in.Name, PublicID: in.PublicID,
		PlannedRosterSize: in.PlannedRosterSize, ContentRevision: in.ContentRevision, CreatedAt: in.CreatedAt,
	})
	if err != nil {
		return nil, nil, err
	}
	return tournamentRecord(tournament), rosterRecord(roster), nil
}

func (r *TournamentPostgres) Get(ctx context.Context, id uuid.UUID) (*TournamentRecord, error) {
	row, err := r.catalogRepository().Get(ctx, id)
	if err != nil {
		if errors.Is(err, catalogrepo.ErrTournamentNotFound) {
			return nil, ErrTournamentNotFound
		}
		return nil, err
	}
	return tournamentRecord(row), nil
}

func (r *TournamentPostgres) Active(ctx context.Context) (*TournamentRecord, error) {
	row, err := r.catalogRepository().Active(ctx)
	if err != nil {
		return nil, err
	}
	if row.ID == uuid.Nil {
		return nil, nil
	}
	return tournamentRecord(row), nil
}

func (r *TournamentPostgres) List(ctx context.Context) ([]TournamentRecord, error) {
	rows, err := r.catalogRepository().List(ctx)
	if err != nil {
		return nil, err
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
	updated, changed, err := r.catalogRepository().Transition(ctx, catalogrepo.TournamentTransitionInput{
		ID: in.ID, ExpectedRevision: in.ExpectedRevision, ExpectedState: in.ExpectedState,
		NextState: in.NextState, PausedFromState: in.PausedFromState, UpdatedAt: in.UpdatedAt,
		StartedAt: in.StartedAt, FinishedAt: in.FinishedAt,
	})
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return nil, false, nil
	}
	return tournamentRecord(updated), true, nil
}

func (r *TournamentPostgres) catalogRepository() *catalogrepo.TournamentCatalogPostgres {
	if r == nil {
		return nil
	}
	return catalogrepo.NewTournamentCatalogPostgres(r.tx)
}
