package golden

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenReadyWindowUseCase struct {
	repository WaveRepository
	clock      WaveClock
}

func NewGoldenReadyWindowUseCase(repository WaveRepository, clock WaveClock) *GoldenReadyWindowUseCase {
	return &GoldenReadyWindowUseCase{repository: repository, clock: clock}
}

func (u *GoldenReadyWindowUseCase) Open(
	ctx context.Context,
	command OpenGoldenReadyWindowCommand,
) (*GoldenWaveExecution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	command.PrivateAssignments = append([]GoldenPrivateAssignmentCommand(nil), command.PrivateAssignments...)
	if err := validateOpenGoldenReadyWindowCommand(command); err != nil {
		return nil, false, err
	}
	if replay, found, err := findGoldenWaveReplay(ctx, u.repository, command.Scope, command.CommandID); err != nil {
		return nil, false, err
	} else if found {
		return reconcileGoldenWaveReplay(command.Scope, command.CommandID,
			GoldenWaveCommandOpened, goldenOpenCommandDigest(command), replay)
	}
	openedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(openedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenWaveCommitAttempts {
		execution, changed, retry, err := u.openAttempt(ctx, command, openedAt)
		if retry {
			continue
		}
		return execution, changed, err
	}
	return nil, false, ErrGoldenWaveCommitConflict
}

func (u *GoldenReadyWindowUseCase) openAttempt(
	ctx context.Context,
	command OpenGoldenReadyWindowCommand,
	openedAt time.Time,
) (*GoldenWaveExecution, bool, bool, error) {
	digest := goldenOpenCommandDigest(command)
	current, err := u.repository.LoadGoldenWaveExecution(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenReadyWindowUseCase - load execution: %w", err)
	}
	if replay, found, findErr := findGoldenWaveReplay(
		ctx, u.repository, command.Scope, command.CommandID,
	); findErr != nil {
		return nil, false, false, findErr
	} else if found {
		result, changed, replayErr := reconcileGoldenWaveReplay(
			command.Scope, command.CommandID, GoldenWaveCommandOpened, digest, replay,
		)
		return result, changed, false, replayErr
	}
	if current != nil {
		if current.Validate() != nil {
			return nil, false, false, domain.ErrInternal
		}
		if receipt, found := goldenExecutionReceiptByCommand(*current, command.CommandID); found {
			if receipt.Kind != GoldenWaveCommandOpened || receipt.CommandDigest != digest {
				return nil, false, false, ErrGoldenWaveCommandReuse
			}
			clone := current.Snapshot()
			return &clone, false, false, nil
		}
		return nil, false, false, ErrGoldenWaveAuthorityConflict
	}
	state, err := u.repository.LoadGoldenState(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenReadyWindowUseCase - load state: %w", err)
	}
	if state.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	if !state.Expectation().Equal(command.ExpectedState) {
		return nil, false, false, ErrGoldenWaveAuthorityConflict
	}
	execution, err := buildGoldenWaveExecution(state, command, openedAt)
	if err != nil {
		return nil, false, false, err
	}
	identities := goldenOpenCommandIdentityIDs(command)
	SortIDs(identities)
	commit := GoldenWaveExecutionCommit{
		ExpectedState: command.ExpectedState, NewIdentityIDs: identities, Next: execution,
	}
	return commitGoldenWaveExecution(ctx, u.repository, commit, command.CommandID,
		GoldenWaveCommandOpened, digest)
}

func buildGoldenWaveExecution(
	state GoldenState,
	command OpenGoldenReadyWindowCommand,
	openedAt time.Time,
) (GoldenWaveExecution, error) {
	active := state.ActiveParticipantIDs()
	SortIDs(active)
	if len(active) < 2 || state.Allocation != nil || goldenStateHasUnresolvedAttempt(state) {
		return GoldenWaveExecution{}, ErrGoldenReadyWindowIneligible
	}
	if err := validateGoldenOpenIdentityOwnership(state, command); err != nil {
		return GoldenWaveExecution{}, err
	}
	groupPlan, edge, found := goldenAttemptPlanEdge(state, state.Scope)
	if !found {
		return GoldenWaveExecution{}, ErrGoldenReadyWindowIneligible
	}
	planned := append([]uuid.UUID(nil), groupPlan.ParticipantIDs...)
	SortIDs(planned)
	if !IDsSubset(active, planned) {
		return GoldenWaveExecution{}, ErrGoldenReadyWindowIneligible
	}
	private, err := buildGoldenPrivateAssignments(command.PrivateAssignments, active, edge)
	if err != nil {
		return GoldenWaveExecution{}, err
	}
	deadline := openedAt.Add(domain.ReadyWindowDuration)
	attempt := buildGoldenAttempt(state.Group, command.AttemptID, active)
	group := CloneGroup(state.Group)
	group.Attempts = append(group.Attempts, attempt)
	if _, err := domain.NewGoldenGroup(group); err != nil {
		return GoldenWaveExecution{}, goldenWaveError("group attempt: %v", err)
	}
	groupBindingDigest, err := ExecutionGroupBindingDigest(group, group.ParticipationEstablished)
	if err != nil {
		return GoldenWaveExecution{}, goldenWaveError("encode group binding: %v", err)
	}
	wave := domain.Wave{
		ID: command.WaveID, TournamentID: state.Scope.TournamentID,
		RevisionID: command.WaveRevisionID, State: domain.WaveStatePlanned,
		Members: BuildMembers(active),
	}
	if err := wave.OpenReadyWindow(command.WindowID, command.WaveWindowRevisionID, openedAt, deadline); err != nil {
		return GoldenWaveExecution{}, goldenWaveError("open Wave: %v", err)
	}
	membership := GoldenWaveMembershipBinding{
		ID: command.MembershipID, RevisionID: command.MembershipRevisionID, Revision: 1,
		Source: state.Membership, ParticipantIDs: append([]uuid.UUID(nil), active...),
		PayloadDigest: DigestIDs(active),
	}
	assignment := GoldenAttemptAssignment{
		ID: command.AssignmentID, RevisionID: command.AssignmentRevisionID, Revision: 1,
		Scope: state.Scope, AttemptID: command.AttemptID, WaveID: command.WaveID,
		MembershipID: command.MembershipID, Plan: state.Plan, EdgeID: edge.ID,
		ReservationID: edge.ReservationID, Snapshot: CloneTaskSnapshot(edge.Snapshot),
		ContentDigest: edge.ContentDigest, Private: private,
	}
	assignment.PayloadDigest = goldenAssignmentDigest(assignment)
	window := GoldenReadyWindow{
		ID: command.WindowID, RevisionID: command.WindowRevisionID, Revision: 1,
		AttemptID: command.AttemptID, AttemptNo: attempt.AttemptNo,
		OpenedAt: openedAt, Deadline: deadline, State: GoldenReadyWindowOpen,
		ReadinessRevisionID: command.ReadinessRevisionID, ReadinessRevision: 1,
		PresenceRevisionID: command.PresenceRevisionID, PresenceRevision: 1,
		BasePresentParticipantIDs: append([]uuid.UUID(nil), active...),
		PresentParticipantIDs:     append([]uuid.UUID(nil), active...),
	}
	window.ReadinessDigest = DigestIDs(nil)
	window.PresenceDigest = DigestIDs(active)
	execution := GoldenWaveExecution{
		Scope: state.Scope, Source: state.Expectation(), RevisionID: command.ExecutionRevisionID,
		Revision: 1, Group: group, GroupBindingDigest: groupBindingDigest,
		OpeningParticipationEstablished: group.ParticipationEstablished,
		Attempt:                         attempt, Wave: wave, Membership: membership,
		Assignment: assignment, Window: window, OpenedAt: openedAt, Deadline: deadline,
	}
	receipt := GoldenWaveCommandReceipt{
		CommandID: command.CommandID, Scope: command.Scope, Kind: GoldenWaveCommandOpened,
		CommandDigest: goldenOpenCommandDigest(command), Result: execution.Expectation(), OccurredAt: openedAt,
	}
	execution.Receipts = []GoldenWaveCommandReceipt{receipt}
	if err := SealExecution(&execution); err != nil {
		return GoldenWaveExecution{}, err
	}
	if err := validateGoldenExecutionSource(state, execution); err != nil {
		return GoldenWaveExecution{}, err
	}
	if err := execution.Validate(); err != nil {
		return GoldenWaveExecution{}, err
	}
	return execution.Snapshot(), nil
}

func rebuildGoldenWaveExecution(execution *GoldenWaveExecution) error {
	if execution == nil {
		return goldenWaveError("nil execution")
	}
	receiptsDigest, err := goldenWaveReceiptsDigest(execution.Receipts)
	if err != nil {
		return goldenWaveError("encode command receipts: %v", err)
	}
	execution.ReceiptsDigest = receiptsDigest
	execution.PayloadDigest = goldenWavePayloadDigest(*execution)
	return nil
}

func SealExecution(execution *GoldenWaveExecution) error {
	if execution == nil || len(execution.Receipts) == 0 {
		return goldenWaveError("cannot seal execution without receipt")
	}
	last := len(execution.Receipts) - 1
	execution.Receipts[last].Result.PayloadDigest = [sha256.Size]byte{}
	if err := rebuildGoldenWaveExecution(execution); err != nil {
		return err
	}
	execution.Receipts[last].Result = execution.Expectation()
	return nil
}
