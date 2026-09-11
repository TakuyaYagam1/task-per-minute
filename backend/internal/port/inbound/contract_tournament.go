package usecase

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	DefaultTournamentPageSize = 50
	MaxTournamentPageSize     = 200
)

const operatorIdentityScope = "task-per-minute:tournament-operator:"

// TournamentUseCase is the transport-neutral tournament catalog boundary.
type TournamentUseCase interface {
	ListTournaments(ctx context.Context, command TournamentListCommand) (TournamentPage, error)
	CreateTournament(ctx context.Context, command TournamentCreateCommand) (TournamentResult, error)
}

type OperatorIdentity struct{ ActorID uuid.UUID }

func OperatorActorID(subject string) (uuid.UUID, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" || len(subject) > 512 {
		return uuid.Nil, domain.ErrValidation
	}
	actorID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(operatorIdentityScope+subject))
	if actorID == uuid.Nil {
		return uuid.Nil, domain.ErrInternal
	}
	return actorID, nil
}

type TournamentListCommand struct {
	Operator OperatorIdentity
	State    domain.TournamentState
	After    *TournamentCursor
	PageSize int
}

type TournamentCursor struct {
	CreatedAt    time.Time
	TournamentID uuid.UUID
}

type TournamentPage struct {
	Items []TournamentView
	Next  *TournamentCursor
}

type TournamentCreateCommand struct {
	Operator          OperatorIdentity
	IdempotencyKey    uuid.UUID
	ExpectedRevision  int64
	Preset            domain.TournamentPreset
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
}

type TournamentResult struct {
	Tournament TournamentView
	Changed    bool
}

type TournamentView struct {
	ID                uuid.UUID
	RosterID          uuid.UUID
	Preset            domain.TournamentPreset
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
	State             domain.TournamentState
	PausedFromState   *domain.TournamentState
	Revision          int64
	RosterSize        int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
}

// TournamentRevisionConflictError carries current aggregate evidence without
// tying the inbound boundary to a protocol-specific conflict payload.
type TournamentRevisionConflictError struct {
	AggregateID      uuid.UUID
	ExpectedRevision int64
	CurrentRevision  int64
	CurrentState     domain.TournamentState
}

func (e *TournamentRevisionConflictError) Error() string {
	return "tournament revision conflict"
}

func (e *TournamentRevisionConflictError) Unwrap() error {
	return domain.ErrConflict
}
