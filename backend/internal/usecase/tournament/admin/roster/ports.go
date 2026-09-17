package roster

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

type OperatorIdentity = operationusecase.OperatorIdentity
type RevisionConflictError = operationusecase.RevisionConflictError
type CommandScope = operationusecase.CommandScope

type RosterTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type RosterAuthority struct {
	Roster               RosterView
	TournamentPreset     domain.TournamentPreset
	PlannedRosterSize    int
	ContentRevision      int64
	TournamentState      domain.TournamentState
	TournamentRevision   int64
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
}

type RosterOperationAction string

const (
	RosterOperationReplace   RosterOperationAction = "replace"
	RosterOperationPreflight RosterOperationAction = "preflight"
	RosterOperationLock      RosterOperationAction = "lock"
	RosterOperationUnlock    RosterOperationAction = "unlock"
)

type RosterOperationRecord struct {
	CommandScope

	RosterID                    uuid.UUID
	Action                      RosterOperationAction
	PreflightRevisionID         uuid.UUID
	SourceProjectionRevisionID  uuid.UUID
	SourceProjectionRevision    int64
	SourceTournamentRevision    int64
	SourceTournamentState       domain.TournamentState
	ResultingTournamentRevision int64
	ResultingTournamentState    domain.TournamentState
	SourceRosterRevision        int64
	ResultingRosterRevision     int64
	RequestDigest               [32]byte
	CheckedInPlayerIDs          []uuid.UUID
	ResultDocument              json.RawMessage
	ExecutedAt                  time.Time
}

type RosterWorkflowRepository interface {
	GetRoster(ctx context.Context, tournamentID uuid.UUID) (RosterView, error)
	LockRosterAuthority(ctx context.Context, tournamentID uuid.UUID) (RosterAuthority, error)
	FindRosterOperation(
		ctx context.Context,
		tournamentID uuid.UUID,
		commandID uuid.UUID,
	) (*RosterOperationRecord, error)
	ReadRosterTime(ctx context.Context) (time.Time, error)
	ReplaceRosterParticipants(
		ctx context.Context,
		authority RosterAuthority,
		participants []RosterParticipantInput,
		updatedAt time.Time,
	) (RosterView, error)
	LoadPreflightInput(
		ctx context.Context,
		authority RosterAuthority,
		evaluatedAt time.Time,
	) (tournamentpreflight.ReportInput, error)
	LockRosterWithPreflight(
		ctx context.Context,
		authority RosterAuthority,
		checkedInPlayerIDs []uuid.UUID,
		lockedAt time.Time,
	) (RosterView, error)
	UnlockRoster(
		ctx context.Context,
		authority RosterAuthority,
		updatedAt time.Time,
	) (RosterView, error)
	SaveRosterOperation(ctx context.Context, record RosterOperationRecord) error
}

// PreflightRuntimeHealthSource supplies a payload-free runtime sample. It is
// called before RosterWorkflow acquires tournament, roster, and projection
// locks, so implementations may use only bounded external probes.
type PreflightRuntimeHealthSource interface {
	RuntimeHealth(ctx context.Context) tournamentpreflight.RuntimeHealth
}

type PreflightRuntimeHealthSourceFunc func(ctx context.Context) tournamentpreflight.RuntimeHealth

func (fn PreflightRuntimeHealthSourceFunc) RuntimeHealth(ctx context.Context) tournamentpreflight.RuntimeHealth {
	if fn == nil {
		return tournamentpreflight.RuntimeHealth{}
	}
	return fn(ctx)
}

type RosterWorkflowDependencies struct {
	Transactions  RosterTransactionManager
	Repository    RosterWorkflowRepository
	RuntimeHealth PreflightRuntimeHealthSource
}
