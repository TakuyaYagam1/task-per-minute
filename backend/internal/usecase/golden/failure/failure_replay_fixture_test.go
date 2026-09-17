package golden_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenattempt "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/attempt"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/failure"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/failure/mocks"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"
	goldenwave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func task050ExpandFailurePlanCandidates(
	t *testing.T,
	authority goldenusecase.GoldenFailureAuthority,
	want int,
) goldenusecase.GoldenFailureAuthority {
	t.Helper()
	planAuthority := authority.Plan.Authority.Snapshot()
	for index := len(planAuthority.Candidates); index < want; index++ {
		task := failureGoldenPlanTask(30000 + index)
		version := 2
		planAuthority.Pool.Versions = append(planAuthority.Pool.Versions, domain.TaskVersionRef{
			TaskID: task.ID, Version: version,
		})
		planAuthority.Candidates = append(planAuthority.Candidates, goldenplan.TaskVersion{
			PoolRevisionID: planAuthority.Pool.ID, Version: version, Task: task,
			Health: domain.TaskVersionHealth{
				TaskID: task.ID, Version: version, PoolRevisionID: planAuthority.Pool.ID,
				PoolKind: domain.AssignmentTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: goldenplan.TaskArtifactDigest(task, version),
		})
	}
	canonical, err := goldenplan.BuildAuthority(planAuthority)
	require.NoError(t, err)
	groups := make([]goldenplan.GroupCommand, len(authority.Plan.Groups))
	for groupIndex, group := range authority.Plan.Groups {
		groups[groupIndex].GroupID = group.GroupID
		groups[groupIndex].GroupRevisionID = group.GroupRevisionID
		for edgeIndex, edge := range group.Edges {
			groups[groupIndex].EdgeIDs[edgeIndex] = edge.ID
			groups[groupIndex].ReservationIDs[edgeIndex] = edge.ReservationID
			groups[groupIndex].SnapshotIDs[edgeIndex] = edge.Snapshot.SnapshotID
		}
	}
	plan, err := goldenplan.BuildExactPlan(goldenplan.Command{
		Scope: canonical.Scope, PlanID: authority.Plan.PlanID, PlanRevisionID: authority.Plan.PlanRevisionID,
		Expected: canonical.Expectation(), GroupCommands: groups, CreatedAt: authority.Plan.CreatedAt,
	}, canonical)
	require.NoError(t, err)
	require.Equal(t, authority.Plan.Groups, plan.Groups)
	state := authority.State.Snapshot()
	state.ExactPlan = plan.Snapshot()
	state.Plan = goldenstate.GoldenPlanStateBinding{}
	state.PayloadDigest = [sha256.Size]byte{}
	builtState, err := goldenstate.BuildGoldenState(state)
	require.NoError(t, err)
	authority.State = builtState
	authority.Plan = plan.Snapshot()
	authority.Active.Expectation.Source = builtState.Expectation()
	authority.Active.Assignment.Plan = builtState.Plan
	failureTask049SealAttemptAssignmentEvidence(t, &authority.Active.Assignment)
	classification, err := goldenusecase.ClassifyGoldenFailure(authority.Plan, authority.Active)
	require.NoError(t, err)
	authority.Classification = classification
	return authority
}

func task050SealExactPlanProof(t *testing.T, plan *goldenplan.ExactPlan) {
	t.Helper()
	type edgeProof struct {
		ID            uuid.UUID         `json:"id"`
		ReservationID uuid.UUID         `json:"reservation_id"`
		Position      int               `json:"position"`
		SnapshotID    uuid.UUID         `json:"snapshot_id"`
		TaskID        uuid.UUID         `json:"task_id"`
		Version       int               `json:"version"`
		ContentDigest [sha256.Size]byte `json:"content_digest"`
	}
	type groupProof struct {
		GroupID                    uuid.UUID   `json:"group_id"`
		GroupRevisionID            uuid.UUID   `json:"group_revision_id"`
		SourceProjectionRevisionID uuid.UUID   `json:"source_projection_revision_id"`
		PositionFrom               int         `json:"position_from"`
		PositionTo                 int         `json:"position_to"`
		ParticipantIDs             []uuid.UUID `json:"participant_ids"`
		Edges                      []edgeProof `json:"edges"`
	}
	document := struct {
		Scope          goldenplan.Scope       `json:"scope"`
		PlanID         uuid.UUID              `json:"plan_id"`
		PlanRevisionID uuid.UUID              `json:"plan_revision_id"`
		Expected       goldenplan.Expectation `json:"expected"`
		Groups         []groupProof           `json:"groups"`
		CreatedAt      time.Time              `json:"created_at"`
	}{
		Scope: plan.Scope, PlanID: plan.PlanID, PlanRevisionID: plan.PlanRevisionID,
		Expected: plan.Expected, CreatedAt: plan.CreatedAt,
		Groups: make([]groupProof, len(plan.Groups)),
	}
	for groupIndex, group := range plan.Groups {
		document.Groups[groupIndex] = groupProof{
			GroupID: group.GroupID, GroupRevisionID: group.GroupRevisionID.UUID(),
			SourceProjectionRevisionID: group.SourceProjectionRevisionID.UUID(),
			PositionFrom:               group.PositionFrom, PositionTo: group.PositionTo,
			ParticipantIDs: append([]uuid.UUID(nil), group.ParticipantIDs...),
			Edges:          make([]edgeProof, len(group.Edges)),
		}
		for edgeIndex, edge := range group.Edges {
			document.Groups[groupIndex].Edges[edgeIndex] = edgeProof{
				ID: edge.ID, ReservationID: edge.ReservationID, Position: edge.Position,
				SnapshotID: edge.Snapshot.SnapshotID, TaskID: edge.Snapshot.TaskID,
				Version: edge.Snapshot.Version, ContentDigest: edge.ContentDigest,
			}
		}
	}
	payload, err := json.Marshal(document)
	require.NoError(t, err)
	digest := sha256.Sum256(payload)
	plan.ProofHash = hex.EncodeToString(digest[:])
}

func task050GoldenFailureAuthority(
	t *testing.T,
	failedAt time.Time,
	exhausted bool,
) goldenusecase.GoldenFailureAuthority {
	t.Helper()
	startedAt := failedAt.Add(-time.Minute)
	state, execution := failureTask049StartedFixture(t, startedAt)
	active, err := goldenusecase.NewGoldenFailureActiveExecution(execution)
	require.NoError(t, err)
	if exhausted {
		active = task050ExhaustedActiveExecution(t, state, active, startedAt)
	} else {
		active = task050ReplayActiveExecution(t, state, active, startedAt)
	}
	scope := active.Scope
	submissions, err := goldensubmission.NewGoldenSubmissionLedger(scope, failureTask049ID(22001+active.Attempt.AttemptNo*100))
	require.NoError(t, err)
	submissions = task050CurrentSubmissionLedger(t, submissions, active, execution, startedAt)
	positions, err := goldenattempt.NewGoldenPositionLedger(
		state.Scope, state.Group.PositionFrom, state.Group.PositionTo, failureTask049ID(22200+active.Attempt.AttemptNo*100),
	)
	require.NoError(t, err)
	positions = task050PriorPositionLedger(t, positions, active, startedAt)
	classification, err := goldenusecase.ClassifyGoldenFailure(state.ExactPlan, active)
	require.NoError(t, err)
	authority := goldenusecase.GoldenFailureAuthority{
		State: state.Snapshot(), Active: active.Snapshot(), Submissions: submissions.Snapshot(),
		Positions: positions.Snapshot(), Plan: state.ExactPlan.Snapshot(),
		SwissPoints:    failureTask049SwissPointSentinel(22300 + active.Attempt.AttemptNo*100),
		Classification: classification.Snapshot(),
	}
	require.NoError(t, authority.Validate())
	return authority
}

func task050ReplayActiveExecution(
	t *testing.T,
	state goldenstate.GoldenState,
	active goldenusecase.GoldenFailureActiveExecution,
	startedAt time.Time,
) goldenusecase.GoldenFailureActiveExecution {
	t.Helper()
	all := append([]uuid.UUID(nil), active.ParticipantIDs...)
	require.GreaterOrEqual(t, len(all), 3)
	unresolved := append([]uuid.UUID(nil), all[1:]...)
	firstStarted := startedAt.Add(-2 * time.Minute)
	firstFinished := startedAt.Add(-time.Minute)
	first := active.Attempt
	first.ID = failureTask049ID(22400)
	first.AttemptNo = 1
	first.PreviousAttemptID = nil
	first.State = domain.GoldenAttemptStateCompleted
	first.ParticipantIDs = all
	first.StartedAt = &firstStarted
	first.FinishedAt = &firstFinished
	second := active.Attempt
	second.ID = failureTask049ID(22401)
	second.AttemptNo = 2
	second.PreviousAttemptID = &first.ID
	second.State = domain.GoldenAttemptStateActive
	second.ParticipantIDs = unresolved
	second.StartedAt = &startedAt
	second.FinishedAt = nil
	active.Attempt = second
	active.Group.Attempts = []domain.GoldenAttempt{first, second}
	active.Scope.AttemptID = second.ID
	active.Expectation.AttemptID = second.ID
	edge := state.ExactPlan.Groups[1].Edges[1]
	active.Scope.SnapshotID = edge.Snapshot.SnapshotID
	active.Scope.TaskID = edge.Snapshot.TaskID
	active.Assignment.AttemptID = second.ID
	active.Assignment.EdgeID = edge.ID
	active.Assignment.ReservationID = edge.ReservationID
	active.Assignment.SnapshotID = edge.Snapshot.SnapshotID
	active.Assignment.TaskID = edge.Snapshot.TaskID
	active.Assignment.ContentDigest = edge.ContentDigest
	active.Assignment.ExecutionPayloadDigest = failureTask049GobDigest(t, "second execution")
	active.Assignment.Private = append([]goldenexecution.GoldenPrivateAssignment(nil), active.Assignment.Private[1:]...)
	for index := range active.Assignment.Private {
		active.Assignment.Private[index].SnapshotID = edge.Snapshot.SnapshotID
		active.Assignment.Private[index].ContentDigest = edge.ContentDigest
	}
	failureTask049SealAttemptAssignmentEvidence(t, &active.Assignment)
	active.Expectation.AssignmentDigest = active.Assignment.ExecutionPayloadDigest
	active.Expectation.PayloadDigest = failureTask049GobDigest(t, "second active head")
	active.Expectation.MembershipDigest = failureTask049GobDigest(t, unresolved)
	active.Expectation.Window.ReadinessDigest = failureTask049GobDigest(t, unresolved)
	active.Expectation.Window.PresenceDigest = failureTask049GobDigest(t, unresolved)
	active.Wave.Members = make([]domain.WaveMember, len(unresolved))
	for index, participantID := range unresolved {
		active.Wave.Members[index] = domain.WaveMember{ParticipantID: participantID, Ready: true}
	}
	active.StartedAt = startedAt
	active.Deadline = startedAt.Add(time.Minute)
	active.ParticipantIDs = unresolved
	require.NoError(t, active.Validate())
	return active
}

func task050CurrentSubmissionLedger(
	t *testing.T,
	ledger goldensubmission.GoldenSubmissionLedger,
	active goldenusecase.GoldenFailureActiveExecution,
	execution goldenexecution.GoldenWaveExecution,
	startedAt time.Time,
) goldensubmission.GoldenSubmissionLedger {
	t.Helper()
	participantID := active.ParticipantIDs[0]
	expected := ledger.Expectation()
	verifiedAt := startedAt.Add(time.Second)
	committedAt := startedAt.Add(2 * time.Second)
	previous := ledger.RevisionID
	ledger.PreviousRevisionID = &previous
	ledger.RevisionID = failureTask049ID(22500 + active.Attempt.AttemptNo)
	ledger.Revision++
	ledger.NextSubmissionID = 2
	ledger.Submissions = []goldensubmission.GoldenSubmissionRecord{{
		ID: 1, Scope: active.Scope, ParticipantID: participantID,
		VerificationID:         failureTask049ID(22510 + active.Attempt.AttemptNo),
		VerificationRevisionID: failureTask049ID(22520 + active.Attempt.AttemptNo),
		EvidenceDigest:         sha256.Sum256([]byte("current provisional evidence")),
		VerifiedAt:             verifiedAt, CommittedAt: committedAt,
		Authority:        execution.Start.Authority.Identity,
		AssignmentDigest: active.Assignment.ExecutionPayloadDigest,
	}}
	ledger.Receipts = []goldensubmission.GoldenSubmissionReceipt{{
		CommandID: failureTask049ID(22530 + active.Attempt.AttemptNo), Scope: active.Scope,
		ParticipantID: participantID, VerificationID: ledger.Submissions[0].VerificationID,
		CommandDigest: sha256.Sum256([]byte("current provisional command")),
		Disposition:   goldensubmission.GoldenSubmissionAccepted, SubmissionID: 1, Expected: expected,
		ResultRevisionID: ledger.RevisionID, ResultRevision: ledger.Revision, CommittedAt: committedAt,
	}}
	failureTask049SealSubmissionLedger(t, &ledger)
	require.NoError(t, ledger.Validate())
	return ledger
}

func task050PriorPositionLedger(
	t *testing.T,
	ledger goldenattempt.GoldenPositionLedger,
	active goldenusecase.GoldenFailureActiveExecution,
	startedAt time.Time,
) goldenattempt.GoldenPositionLedger {
	t.Helper()
	priorAttempt := active.Group.Attempts[0]
	participantID := priorAttempt.ParticipantIDs[0]
	order := goldenattempt.GoldenAttemptOrderingEvidence{
		AttemptID: priorAttempt.ID, AttemptNo: priorAttempt.AttemptNo,
		SubmissionHead: goldensubmission.GoldenSubmissionLedgerExpectation{
			Scope: goldensubmission.GoldenSubmissionScope{
				State: active.Scope.State, AttemptID: priorAttempt.ID, WaveID: failureTask049ID(22600),
				AssignmentID: failureTask049ID(22601), SnapshotID: failureTask049ID(22602), TaskID: failureTask049ID(22603),
			},
			RevisionID: failureTask049ID(22604), Revision: 2, NextSubmissionID: 2,
			PayloadDigest: sha256.Sum256([]byte("prior submission head")),
		},
		Order: []goldenattempt.GoldenPositionOrderEntry{{
			SubmissionID: 1, ParticipantID: participantID,
			CommittedAt:    startedAt.Add(-90 * time.Second),
			EvidenceDigest: sha256.Sum256([]byte("prior committed evidence")),
		}},
	}
	failureTask049SealOrdering(t, &order)
	previous := ledger.RevisionID
	ledger.PreviousRevisionID = &previous
	ledger.RevisionID = failureTask049ID(22605 + active.Attempt.AttemptNo)
	ledger.Revision++
	ledger.RevisionIDs = append(ledger.RevisionIDs, ledger.RevisionID)
	ledger.Attempts = []goldenattempt.GoldenAttemptOrderingEvidence{order}
	ledger.Positions = []goldenattempt.GoldenCommittedPosition{{
		Position: ledger.PositionFrom, ParticipantID: participantID, AttemptID: priorAttempt.ID,
		AttemptNo: priorAttempt.AttemptNo, SubmissionID: 1,
		EvidenceDigest: order.Order[0].EvidenceDigest, CommitID: failureTask049ID(22610 + active.Attempt.AttemptNo),
	}}
	failureTask049SealPositionLedger(t, &ledger)
	require.NoError(t, ledger.Validate())
	return ledger
}

func task050GoldenFailureReplayCommand(
	authority goldenusecase.GoldenFailureAuthority,
	base int,
) goldenusecase.GoldenFailureReplayCommand {
	private := make([]goldenwave.GoldenPrivateAssignmentCommand, len(authority.Classification.ParticipantIDs))
	for index, participantID := range authority.Classification.ParticipantIDs {
		private[index] = goldenwave.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID, AssignmentID: failureTask049ID(base + 20 + index),
		}
	}
	return goldenusecase.GoldenFailureReplayCommand{
		Scope: authority.Active.Scope, CommandID: failureTask049ID(base), FailureID: failureTask049ID(base + 1),
		Expected: authority.Expectation(), ClosedWaveRevisionID: domain.WaveRevisionID(failureTask049ID(base + 2)),
		NextAttemptID: failureTask049ID(base + 3), NextAssignmentID: failureTask049ID(base + 4),
		NextAssignmentRevisionID: failureTask049ID(base + 5), NextWaveID: failureTask049ID(base + 6),
		NextWaveRevisionID:       domain.WaveRevisionID(failureTask049ID(base + 7)),
		NextWindowID:             failureTask049ID(base + 8),
		NextWaveWindowRevisionID: domain.ReadyWindowRevisionID(failureTask049ID(base + 9)),
		NextWindowRevisionID:     failureTask049ID(base + 10), NextReadinessRevisionID: failureTask049ID(base + 11),
		NextPresenceRevisionID: failureTask049ID(base + 12), NextMembershipID: failureTask049ID(base + 13),
		NextMembershipRevisionID: failureTask049ID(base + 14), PrivateAssignments: private,
	}
}

type task050FailureRepositoryHarness struct {
	*goldenmocks.MockFailureRepository

	mu           sync.Mutex
	authority    goldenusecase.GoldenFailureAuthority
	replays      map[uuid.UUID]goldenusecase.GoldenFailureRecord
	writes       int
	beforeCommit func()
	commitErr    error
	returnRecord *goldenusecase.GoldenFailureRecord
	unrelated    []task050UnrelatedState
}

type task050UnrelatedState struct {
	TournamentID uuid.UUID
	GroupID      uuid.UUID
	Digest       [sha256.Size]byte
}

func newTask050FailureRepositoryHarness(
	t *testing.T,
	authority goldenusecase.GoldenFailureAuthority,
) *task050FailureRepositoryHarness {
	t.Helper()

	harness := &task050FailureRepositoryHarness{
		authority: authority.Snapshot(), replays: make(map[uuid.UUID]goldenusecase.GoldenFailureRecord),
		unrelated: []task050UnrelatedState{
			{TournamentID: failureTask049ID(29700), GroupID: failureTask049ID(29701), Digest: sha256.Sum256([]byte("other tournament"))},
			{TournamentID: authority.Active.Scope.State.TournamentID, GroupID: failureTask049ID(29702), Digest: sha256.Sum256([]byte("other group"))},
		},
	}
	repository := goldenmocks.NewMockFailureRepository(t)
	repository.EXPECT().
		FindGoldenFailure(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.findFailureReplay).
		Maybe()
	repository.EXPECT().
		LoadGoldenFailureAuthority(mock.Anything, mock.Anything).
		RunAndReturn(harness.loadFailureSnapshot).
		Maybe()
	repository.EXPECT().
		CommitGoldenFailure(mock.Anything, mock.Anything).
		RunAndReturn(harness.commitFailureState).
		Maybe()
	harness.MockFailureRepository = repository
	return harness
}

func (r *task050FailureRepositoryHarness) unrelatedSnapshot() []task050UnrelatedState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]task050UnrelatedState(nil), r.unrelated...)
}

func (r *task050FailureRepositoryHarness) findFailureReplay(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*goldenusecase.GoldenFailureRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, found := r.replays[commandID]
	if !found {
		return nil, nil
	}
	clone := record.Snapshot()
	return &clone, nil
}

func (r *task050FailureRepositoryHarness) loadFailureSnapshot(
	_ context.Context,
	_ goldensubmission.GoldenSubmissionScope,
) (goldenusecase.GoldenFailureAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority.Snapshot(), nil
}

func (r *task050FailureRepositoryHarness) commitFailureState(
	_ context.Context,
	commit goldenusecase.GoldenFailureCommit,
) (*goldenusecase.GoldenFailureRecord, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commitErr != nil {
		return nil, false, r.commitErr
	}
	if !r.authority.Expectation().Equal(commit.Expected) || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	if _, duplicate := r.replays[commit.Record.CommandID]; duplicate {
		return nil, false, errors.New("duplicate failure command")
	}
	if r.returnRecord != nil {
		clone := r.returnRecord.Snapshot()
		return &clone, false, nil
	}
	r.writes++
	record := commit.Record.Snapshot()
	r.authority.Current = &record
	r.replays[record.CommandID] = record.Snapshot()
	clone := record.Snapshot()
	return &clone, true, nil
}

func (r *task050FailureRepositoryHarness) authoritySnapshot() goldenusecase.GoldenFailureAuthority {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority.Snapshot()
}

func (r *task050FailureRepositoryHarness) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *task050FailureRepositoryHarness) currentSnapshot() goldenusecase.GoldenFailureRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority.Current.Snapshot()
}

func failureNewGoldenClock(t *testing.T, now time.Time) *goldenmocks.MockFailureClock {
	t.Helper()
	clock := goldenmocks.NewMockFailureClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}
