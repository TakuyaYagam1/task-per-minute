package forfeit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

const forfeitAttempts = 2

var (
	ErrInvalidForfeit              = errors.New("invalid game forfeit")
	ErrForfeitUnavailable          = errors.New("game forfeit is unavailable")
	ErrSurrenderDisconnected       = errors.New("surrendering participant is disconnected")
	ErrOperatorForfeitUnauthorized = errors.New("operator is not authorized to record a forfeit")
	ErrForfeitAuthorityConflict    = errors.New("game forfeit authority conflict")
	ErrForfeitCommandReuse         = errors.New("game forfeit command was reused")
)

type Source string

const (
	SourceSurrender Source = "surrender"
	SourceOperator  Source = "operator"
)

type OperatorBasis string

const OperatorBasisRuleViolation OperatorBasis = "rule_violation"

type Scope struct {
	TournamentID uuid.UUID
	SeriesID     uuid.UUID
}

type GameExpectation struct {
	SlotID    uuid.UUID
	GameID    uuid.UUID
	AttemptNo int
	State     domain.GameState
}

type ForfeitRevisionSet struct {
	GameResultRevisionID   *domain.OfficialResultRevisionID
	ScoreRevisionID        domain.SeriesScoreRevisionID
	SeriesResultRevisionID domain.OfficialResultRevisionID
	AuditEventID           uuid.UUID
	OutboxEventID          uuid.UUID
	ProjectionRevisionID   uuid.UUID
}

type OperatorEvidence struct {
	Confirmed   bool
	Basis       OperatorBasis
	Reason      string
	RuleID      string
	EvidenceIDs []uuid.UUID
}

type SurrenderCommand struct {
	Scope                   Scope
	CommandID               uuid.UUID
	ActorParticipantID      uuid.UUID
	ForfeitingParticipantID uuid.UUID
	ExpectedGame            GameExpectation
	Revisions               ForfeitRevisionSet
}

type OperatorCommand struct {
	Scope                   Scope
	CommandID               uuid.UUID
	ActorOperatorID         uuid.UUID
	ForfeitingParticipantID uuid.UUID
	ExpectedGame            *GameExpectation
	Evidence                OperatorEvidence
	Revisions               ForfeitRevisionSet
}

type ForfeitAuthority struct {
	Scope                        Scope
	Revision                     int64
	Series                       seriesdomain.Execution
	ConnectedParticipantIDs      []uuid.UUID
	AuthorizedOperatorIDs        []uuid.UUID
	CurrentOrdinal               int
	CurrentSeriesResultOrdinal   int
	CurrentProjectionRevision    int64
	CurrentGameResultRevisionIDs []domain.OfficialResultRevisionID
	Current                      *ForfeitResolution
}

type ForfeitResolution struct {
	Source                    Source
	Reason                    domain.GameResultReason
	Scope                     Scope
	CommandID                 uuid.UUID
	ActorID                   uuid.UUID
	ForfeitingParticipantID   uuid.UUID
	ExpectedGame              *GameExpectation
	ExpectedAuthorityRevision int64
	Series                    seriesdomain.Execution
	Game                      *domain.Game
	GameRevision              *GameRevision
	ScoreRevision             seriesdomain.ScoreRevision
	SeriesRevision            SeriesRevision
	OperatorEvidence          *OperatorEvidence
	Evidence                  seriesdomain.SettlementEvidence
	ResolvedAt                time.Time
}

type ForfeitUseCase struct {
	repository ForfeitRepository
	clock      ForfeitClock
}

type ForfeitClock interface {
	Now() time.Time
}

type ForfeitRepository interface {
	LoadForfeitAuthority(ctx context.Context, scope Scope) (ForfeitAuthority, error)
	CommitForfeitResolution(
		ctx context.Context,
		resolution ForfeitResolution,
	) (*ForfeitResolution, bool, error)
}

type forfeitRequest struct {
	source                  Source
	scope                   Scope
	commandID               uuid.UUID
	actorID                 uuid.UUID
	forfeitingParticipantID uuid.UUID
	expectedGame            *GameExpectation
	operatorEvidence        *OperatorEvidence
	revisions               ForfeitRevisionSet
}

func ForfeitNewUseCase(repository ForfeitRepository, clock ForfeitClock) *ForfeitUseCase {
	return &ForfeitUseCase{repository: repository, clock: clock}
}

func (u *ForfeitUseCase) Surrender(
	ctx context.Context,
	command SurrenderCommand,
) (*ForfeitResolution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if command.ActorParticipantID != command.ForfeitingParticipantID ||
		command.ActorParticipantID == uuid.Nil {
		return nil, false, domain.ErrAssignmentParticipant
	}
	request := forfeitRequest{
		source: SourceSurrender, scope: command.Scope, commandID: command.CommandID,
		actorID: command.ActorParticipantID, forfeitingParticipantID: command.ForfeitingParticipantID,
		expectedGame: &command.ExpectedGame, revisions: command.Revisions,
	}
	if err := validateForfeitRequest(request); err != nil {
		return nil, false, err
	}
	return u.resolve(ctx, request)
}

func (u *ForfeitUseCase) OperatorForfeit(
	ctx context.Context,
	command OperatorCommand,
) (*ForfeitResolution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	evidence := cloneOperatorForfeitEvidence(command.Evidence)
	request := forfeitRequest{
		source: SourceOperator, scope: command.Scope, commandID: command.CommandID,
		actorID: command.ActorOperatorID, forfeitingParticipantID: command.ForfeitingParticipantID,
		expectedGame:     cloneForfeitGameExpectation(command.ExpectedGame),
		operatorEvidence: &evidence, revisions: command.Revisions,
	}
	if err := validateForfeitRequest(request); err != nil {
		return nil, false, err
	}
	return u.resolve(ctx, request)
}

func (u *ForfeitUseCase) resolve(
	ctx context.Context,
	request forfeitRequest,
) (*ForfeitResolution, bool, error) {
	resolvedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(resolvedAt) {
		return nil, false, domain.ErrValidation
	}
	for range forfeitAttempts {
		resolution, changed, retry, err := u.resolveAttempt(ctx, request, resolvedAt)
		if retry {
			continue
		}
		return resolution, changed, err
	}
	return nil, false, ErrForfeitAuthorityConflict
}

func (u *ForfeitUseCase) resolveAttempt(
	ctx context.Context,
	request forfeitRequest,
	resolvedAt time.Time,
) (*ForfeitResolution, bool, bool, error) {
	authority, err := u.repository.LoadForfeitAuthority(ctx, request.scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("ForfeitUseCase - load authority: %w", err)
	}
	if err := validateForfeitAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != request.scope {
		return nil, false, false, forfeitError("authority scope does not match command")
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileForfeitResolution(*authority.Current, request)
		return current, false, false, reconcileErr
	}
	if err := validateForfeitAuthorityForRequest(authority, request); err != nil {
		return nil, false, false, err
	}
	resolution, err := buildForfeitResolution(authority, request, resolvedAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitForfeitResolution(ctx, resolution)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("ForfeitUseCase - commit resolution: %w", err)
	}
	if !validCommittedForfeitResolution(committed, resolution, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneForfeitResolution(*committed)
	return &result, changed, false, nil
}
