package arena_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenFailureReplay(t *testing.T) {
	t.Parallel()

	failedAt := time.Date(2026, 9, 1, 19, 0, 0, 0, time.UTC)
	authority := task050GoldenFailureAuthority(t, failedAt, false)
	repository := newTask050FailureRepository(authority)
	command := task050GoldenFailureReplayCommand(authority, 21000)

	record, changed, err := arena.NewGoldenFailureReplayUseCase(
		repository,
		fixedArenaClock{now: failedAt},
	).Replay(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, arena.GoldenFailureRouteReplay, record.Route)
	require.Equal(t, domain.ArenaGoldenAttemptStateVoid, record.Attempt.State)
	require.Equal(t, domain.ArenaWaveStateCompleted, record.OldWave.State)
	require.Equal(t, command.ClosedWaveRevisionID, record.OldWave.RevisionID)
	require.Equal(t, authority.Submissions.Expectation(), record.DiscardedSubmissions)
	require.Equal(t, authority.Submissions.ProvisionalOrder(), record.DiscardedOrder)
	require.Equal(t, authority.Positions, record.PriorPositions)
	require.Equal(t, authority.Positions, record.Positions)
	require.NotNil(t, record.Replacement)
	require.Nil(t, record.TechnicalPause)
	require.Equal(t, authority.Classification.NextEdge.Position, record.Replacement.Assignment.EdgePosition)
	require.Equal(t, authority.Classification.NextEdge.ID, record.Replacement.Assignment.EdgeID)
	require.Equal(t, authority.Classification.ParticipantIDs, record.Replacement.Attempt.ParticipantIDs)
	require.Equal(t, domain.ArenaGoldenAttemptStatePlanned, record.Replacement.Attempt.State)
	require.Equal(t, domain.ArenaWaveStateReadyWindowOpen, record.Replacement.Wave.State)
	require.Nil(t, record.Replacement.Wave.StartedAt)
	require.Empty(t, record.Replacement.Window.ReadyParticipantIDs)
	require.Equal(t, authority.Classification.ParticipantIDs, record.Replacement.Window.PresentParticipantIDs)
	require.Equal(t, failedAt, record.FailedAt)

	replayed, replayChanged, replayErr := arena.NewGoldenFailureReplayUseCase(
		repository,
		fixedArenaClock{now: failedAt.Add(time.Hour)},
	).Replay(t.Context(), command)
	require.NoError(t, replayErr)
	require.False(t, replayChanged)
	require.Equal(t, record.PayloadDigest, replayed.PayloadDigest)
	require.Equal(t, 1, repository.writeCount())

	reused := command
	reused.NextAttemptID = task049ID(21990)
	result, reusedChanged, reusedErr := arena.NewGoldenFailureReplayUseCase(
		repository,
		fixedArenaClock{now: failedAt},
	).Replay(t.Context(), reused)
	require.Nil(t, result)
	require.False(t, reusedChanged)
	require.ErrorIs(t, reusedErr, arena.ErrGoldenFailureCommandReuse)

	record.Positions.Positions = append(record.Positions.Positions, arena.GoldenCommittedPosition{
		ParticipantID: task049ID(21991),
	})
	stored := repository.currentSnapshot()
	require.Equal(t, authority.Positions, stored.Positions)

	concurrentAuthority := task050GoldenFailureAuthority(t, failedAt, false)
	concurrentRepository := newTask050FailureRepository(concurrentAuthority)
	concurrentRepository.beforeCommit = task049TwoPartyBarrier(t)
	concurrentCommand := task050GoldenFailureReplayCommand(concurrentAuthority, 26000)
	type concurrentFailureResult struct {
		record  *arena.GoldenFailureRecord
		changed bool
		err     error
	}
	results := make(chan concurrentFailureResult, 2)
	for range 2 {
		go func() {
			record, changed, err := arena.NewGoldenFailureReplayUseCase(
				concurrentRepository, fixedArenaClock{now: failedAt},
			).Replay(t.Context(), concurrentCommand)
			results <- concurrentFailureResult{record: record, changed: changed, err: err}
		}()
	}
	changedCount := 0
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		require.NotNil(t, result.record)
		if result.changed {
			changedCount++
		}
	}
	require.Equal(t, 1, changedCount)
}

func TestGoldenFailureReplayValidation(t *testing.T) {
	t.Parallel()

	failedAt := time.Date(2026, 9, 1, 19, 30, 0, 0, time.UTC)
	base := task050GoldenFailureAuthority(t, failedAt, false)

	activeCases := []struct {
		name   string
		mutate func(*arena.GoldenFailureActiveExecution)
	}{
		{name: "Wave revision", mutate: func(active *arena.GoldenFailureActiveExecution) {
			active.Expectation.WaveRevisionID = domain.ArenaWaveRevisionID(task049ID(29600))
		}},
		{name: "ready window", mutate: func(active *arena.GoldenFailureActiveExecution) {
			active.Expectation.Window.WindowID = task049ID(29601)
		}},
		{name: "ready set digest", mutate: func(active *arena.GoldenFailureActiveExecution) {
			active.Expectation.Window.ReadinessDigest = sha256.Sum256([]byte("foreign readiness"))
		}},
		{name: "presence set digest", mutate: func(active *arena.GoldenFailureActiveExecution) {
			active.Expectation.Window.PresenceDigest = sha256.Sum256([]byte("foreign presence"))
		}},
		{name: "assignment revision", mutate: func(active *arena.GoldenFailureActiveExecution) {
			active.Expectation.AssignmentRevisionID = task049ID(29602)
		}},
		{name: "assignment revision number", mutate: func(active *arena.GoldenFailureActiveExecution) {
			active.Expectation.AssignmentRevision++
		}},
		{name: "membership digest", mutate: func(active *arena.GoldenFailureActiveExecution) {
			active.Expectation.MembershipDigest = sha256.Sum256([]byte("foreign membership"))
		}},
		{name: "group tournament", mutate: func(active *arena.GoldenFailureActiveExecution) {
			active.Group.TournamentID = task049ID(29603)
		}},
		{name: "Wave tournament", mutate: func(active *arena.GoldenFailureActiveExecution) {
			active.Wave.TournamentID = task049ID(29604)
		}},
	}
	for _, test := range activeCases {
		t.Run("active_"+test.name, func(t *testing.T) {
			active := base.Active.Snapshot()
			test.mutate(&active)
			require.Error(t, active.Validate())
		})
	}

	authorityCases := []struct {
		name   string
		mutate func(*arena.GoldenFailureAuthority)
	}{
		{name: "full exact plan", mutate: func(authority *arena.GoldenFailureAuthority) {
			authority.Plan.CreatedAt = authority.Plan.CreatedAt.Add(time.Nanosecond)
			task050SealExactPlanProof(t, &authority.Plan)
			require.NoError(t, authority.Plan.Validate())
		}},
		{name: "position range", mutate: func(authority *arena.GoldenFailureAuthority) {
			authority.Positions.PositionTo++
			task049SealPositionLedger(t, &authority.Positions)
			require.NoError(t, authority.Positions.Validate())
		}},
		{name: "foreign committed participant", mutate: func(authority *arena.GoldenFailureAuthority) {
			foreignID := task049ID(29610)
			authority.Positions.Positions[0].ParticipantID = foreignID
			authority.Positions.Attempts[0].Order[0].ParticipantID = foreignID
			task049SealOrdering(t, &authority.Positions.Attempts[0])
			task049SealPositionLedger(t, &authority.Positions)
			require.NoError(t, authority.Positions.Validate())
		}},
		{name: "inactive current submitter", mutate: func(authority *arena.GoldenFailureAuthority) {
			inactiveID := authority.Active.Group.Attempts[0].ParticipantIDs[0]
			require.NotContains(t, authority.Active.ParticipantIDs, inactiveID)
			authority.Submissions.Submissions[0].ParticipantID = inactiveID
			authority.Submissions.Receipts[0].ParticipantID = inactiveID
			task049SealSubmissionLedger(t, &authority.Submissions)
			require.NoError(t, authority.Submissions.Validate())
		}},
		{name: "stale submission assignment", mutate: func(authority *arena.GoldenFailureAuthority) {
			authority.Submissions.Submissions[0].AssignmentDigest = sha256.Sum256([]byte("stale assignment"))
			task049SealSubmissionLedger(t, &authority.Submissions)
			require.NoError(t, authority.Submissions.Validate())
		}},
		{name: "submission preflight", mutate: func(authority *arena.GoldenFailureAuthority) {
			authority.Submissions.Submissions = make([]arena.GoldenSubmissionRecord, domain.ArenaMaxParticipants+1)
		}},
		{name: "receipt preflight", mutate: func(authority *arena.GoldenFailureAuthority) {
			authority.Submissions.Receipts = make([]arena.GoldenSubmissionReceipt, domain.ArenaMaxParticipants*2+1)
		}},
		{name: "position preflight", mutate: func(authority *arena.GoldenFailureAuthority) {
			authority.Positions.Positions = make([]arena.GoldenCommittedPosition, domain.ArenaMaxParticipants+1)
		}},
		{name: "attempt preflight", mutate: func(authority *arena.GoldenFailureAuthority) {
			authority.Positions.Attempts = make(
				[]arena.GoldenAttemptOrderingEvidence,
				domain.ArenaAssignmentReserveCount+2,
			)
		}},
		{name: "plan nested preflight", mutate: func(authority *arena.GoldenFailureAuthority) {
			authority.Plan.Authority.Candidates = make(
				[]arena.GoldenExactTaskVersion,
				domain.ArenaMaxParticipants*(domain.ArenaAssignmentReserveCount+1)+1,
			)
		}},
	}
	for _, test := range authorityCases {
		t.Run("authority_"+test.name, func(t *testing.T) {
			authority := base.Snapshot()
			test.mutate(&authority)
			require.Error(t, authority.Validate())
		})
	}

	t.Run("dishonest unchanged return", func(t *testing.T) {
		sourceAuthority := task050GoldenFailureAuthority(t, failedAt, false)
		sourceRepository := newTask050FailureRepository(sourceAuthority)
		oldCommand := task050GoldenFailureReplayCommand(sourceAuthority, 27000)
		oldRecord, changed, err := arena.NewGoldenFailureReplayUseCase(
			sourceRepository, fixedArenaClock{now: failedAt},
		).Replay(t.Context(), oldCommand)
		require.NoError(t, err)
		require.True(t, changed)

		authority := task050GoldenFailureAuthority(t, failedAt, false)
		repository := newTask050FailureRepository(authority)
		repository.returnRecord = oldRecord
		command := task050GoldenFailureReplayCommand(authority, 27100)
		result, resultChanged, resultErr := arena.NewGoldenFailureReplayUseCase(
			repository, fixedArenaClock{now: failedAt},
		).Replay(t.Context(), command)
		require.Nil(t, result)
		require.False(t, resultChanged)
		require.ErrorIs(t, resultErr, domain.ErrInternal)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("commit error is atomic", func(t *testing.T) {
		authority := task050GoldenFailureAuthority(t, failedAt, false)
		repository := newTask050FailureRepository(authority)
		repository.commitErr = errors.New("failure store unavailable")
		before := repository.authoritySnapshot()
		result, changed, err := arena.NewGoldenFailureReplayUseCase(
			repository, fixedArenaClock{now: failedAt},
		).Replay(t.Context(), task050GoldenFailureReplayCommand(authority, 27200))
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorContains(t, err, "failure store unavailable")
		require.Equal(t, before, repository.authoritySnapshot())
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("unchanged concurrent winner may have another captured time", func(t *testing.T) {
		authority := task050GoldenFailureAuthority(t, failedAt, false)
		command := task050GoldenFailureReplayCommand(authority, 27300)
		source := newTask050FailureRepository(authority)
		winner, changed, err := arena.NewGoldenFailureReplayUseCase(
			source, fixedArenaClock{now: failedAt},
		).Replay(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)

		repository := newTask050FailureRepository(authority)
		repository.returnRecord = winner
		result, resultChanged, resultErr := arena.NewGoldenFailureReplayUseCase(
			repository, fixedArenaClock{now: failedAt.Add(time.Nanosecond)},
		).Replay(t.Context(), command)
		require.NoError(t, resultErr)
		require.False(t, resultChanged)
		require.Equal(t, winner.PayloadDigest, result.PayloadDigest)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("canonical task pool larger than selected demand", func(t *testing.T) {
		authority := task050GoldenFailureAuthority(t, failedAt, false)
		authority = task050ExpandFailurePlanCandidates(t, authority, 49)
		require.Greater(t, len(authority.Plan.Authority.Candidates), 48)
		require.NoError(t, authority.Plan.Validate())
		require.NoError(t, authority.State.ExactPlan.Validate())
		require.NoError(t, authority.Validate())
	})
}

func task050ExpandFailurePlanCandidates(
	t *testing.T,
	authority arena.GoldenFailureAuthority,
	want int,
) arena.GoldenFailureAuthority {
	t.Helper()
	planAuthority := authority.Plan.Authority.Snapshot()
	for index := len(planAuthority.Candidates); index < want; index++ {
		task := goldenTask(30000 + index)
		version := 2
		planAuthority.Pool.Versions = append(planAuthority.Pool.Versions, arena.TaskVersionRef{
			TaskID: task.ID, Version: version,
		})
		planAuthority.Candidates = append(planAuthority.Candidates, arena.GoldenExactTaskVersion{
			PoolRevisionID: planAuthority.Pool.ID, Version: version, Task: task,
			Health: arena.TaskVersionHealth{
				TaskID: task.ID, Version: version, PoolRevisionID: planAuthority.Pool.ID,
				PoolKind: domain.ArenaTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: arena.GoldenTaskArtifactDigest(task, version),
		})
	}
	canonical, err := arena.BuildGoldenExactPlanAuthority(planAuthority)
	require.NoError(t, err)
	groups := make([]arena.GoldenExactGroupCommand, len(authority.Plan.Groups))
	for groupIndex, group := range authority.Plan.Groups {
		groups[groupIndex].GroupID = group.GroupID
		groups[groupIndex].GroupRevisionID = group.GroupRevisionID
		for edgeIndex, edge := range group.Edges {
			groups[groupIndex].EdgeIDs[edgeIndex] = edge.ID
			groups[groupIndex].ReservationIDs[edgeIndex] = edge.ReservationID
			groups[groupIndex].SnapshotIDs[edgeIndex] = edge.Snapshot.SnapshotID
		}
	}
	plan, err := arena.BuildGoldenExactPlan(arena.GoldenExactPlanCommand{
		Scope: canonical.Scope, PlanID: authority.Plan.PlanID, PlanRevisionID: authority.Plan.PlanRevisionID,
		Expected: canonical.Expectation(), GroupCommands: groups, CreatedAt: authority.Plan.CreatedAt,
	}, canonical)
	require.NoError(t, err)
	require.Equal(t, authority.Plan.Groups, plan.Groups)
	state := authority.State.Snapshot()
	state.ExactPlan = plan.Snapshot()
	state.Plan = arena.GoldenPlanStateBinding{}
	state.PayloadDigest = [sha256.Size]byte{}
	builtState, err := arena.BuildGoldenState(state)
	require.NoError(t, err)
	authority.State = builtState
	authority.Plan = plan.Snapshot()
	authority.Active.Expectation.Source = builtState.Expectation()
	authority.Active.Assignment.Plan = builtState.Plan
	task049SealAttemptAssignmentEvidence(t, &authority.Active.Assignment)
	classification, err := arena.ClassifyGoldenFailure(authority.Plan, authority.Active)
	require.NoError(t, err)
	authority.Classification = classification
	return authority
}

func task050SealExactPlanProof(t *testing.T, plan *arena.GoldenExactPlan) {
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
		Scope          arena.GoldenExactPlanScope       `json:"scope"`
		PlanID         uuid.UUID                        `json:"plan_id"`
		PlanRevisionID uuid.UUID                        `json:"plan_revision_id"`
		Expected       arena.GoldenExactPlanExpectation `json:"expected"`
		Groups         []groupProof                     `json:"groups"`
		CreatedAt      time.Time                        `json:"created_at"`
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
) arena.GoldenFailureAuthority {
	t.Helper()
	startedAt := failedAt.Add(-time.Minute)
	state, execution := task049StartedFixture(t, startedAt)
	active, err := arena.NewGoldenFailureActiveExecution(execution)
	require.NoError(t, err)
	if exhausted {
		active = task050ExhaustedActiveExecution(t, state, active, startedAt)
	} else {
		active = task050ReplayActiveExecution(t, state, active, startedAt)
	}
	scope := active.Scope
	submissions, err := arena.NewGoldenSubmissionLedger(scope, task049ID(22001+active.Attempt.AttemptNo*100))
	require.NoError(t, err)
	submissions = task050CurrentSubmissionLedger(t, submissions, active, execution, startedAt)
	positions, err := arena.NewGoldenPositionLedger(
		state.Scope, state.Group.PositionFrom, state.Group.PositionTo, task049ID(22200+active.Attempt.AttemptNo*100),
	)
	require.NoError(t, err)
	positions = task050PriorPositionLedger(t, positions, active, startedAt)
	classification, err := arena.ClassifyGoldenFailure(state.ExactPlan, active)
	require.NoError(t, err)
	authority := arena.GoldenFailureAuthority{
		State: state.Snapshot(), Active: active.Snapshot(), Submissions: submissions.Snapshot(),
		Positions: positions.Snapshot(), Plan: state.ExactPlan.Snapshot(),
		SwissPoints:    task049SwissPointSentinel(22300 + active.Attempt.AttemptNo*100),
		Classification: classification.Snapshot(),
	}
	require.NoError(t, authority.Validate())
	return authority
}

func task050ReplayActiveExecution(
	t *testing.T,
	state arena.GoldenState,
	active arena.GoldenFailureActiveExecution,
	startedAt time.Time,
) arena.GoldenFailureActiveExecution {
	t.Helper()
	all := append([]uuid.UUID(nil), active.ParticipantIDs...)
	require.GreaterOrEqual(t, len(all), 3)
	unresolved := append([]uuid.UUID(nil), all[1:]...)
	firstStarted := startedAt.Add(-2 * time.Minute)
	firstFinished := startedAt.Add(-time.Minute)
	first := active.Attempt
	first.ID = task049ID(22400)
	first.AttemptNo = 1
	first.PreviousAttemptID = nil
	first.State = domain.ArenaGoldenAttemptStateCompleted
	first.ParticipantIDs = all
	first.StartedAt = &firstStarted
	first.FinishedAt = &firstFinished
	second := active.Attempt
	second.ID = task049ID(22401)
	second.AttemptNo = 2
	second.PreviousAttemptID = &first.ID
	second.State = domain.ArenaGoldenAttemptStateActive
	second.ParticipantIDs = unresolved
	second.StartedAt = &startedAt
	second.FinishedAt = nil
	active.Attempt = second
	active.Group.Attempts = []domain.ArenaGoldenAttempt{first, second}
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
	active.Assignment.ExecutionPayloadDigest = task049GobDigest(t, "second execution")
	active.Assignment.Private = append([]arena.GoldenPrivateAssignment(nil), active.Assignment.Private[1:]...)
	for index := range active.Assignment.Private {
		active.Assignment.Private[index].SnapshotID = edge.Snapshot.SnapshotID
		active.Assignment.Private[index].ContentDigest = edge.ContentDigest
	}
	task049SealAttemptAssignmentEvidence(t, &active.Assignment)
	active.Expectation.AssignmentDigest = active.Assignment.ExecutionPayloadDigest
	active.Expectation.PayloadDigest = task049GobDigest(t, "second active head")
	active.Expectation.MembershipDigest = task049GobDigest(t, unresolved)
	active.Expectation.Window.ReadinessDigest = task049GobDigest(t, unresolved)
	active.Expectation.Window.PresenceDigest = task049GobDigest(t, unresolved)
	active.Wave.Members = make([]domain.ArenaWaveMember, len(unresolved))
	for index, participantID := range unresolved {
		active.Wave.Members[index] = domain.ArenaWaveMember{ParticipantID: participantID, Ready: true}
	}
	active.StartedAt = startedAt
	active.Deadline = startedAt.Add(time.Minute)
	active.ParticipantIDs = unresolved
	require.NoError(t, active.Validate())
	return active
}

func task050CurrentSubmissionLedger(
	t *testing.T,
	ledger arena.GoldenSubmissionLedger,
	active arena.GoldenFailureActiveExecution,
	execution arena.GoldenWaveExecution,
	startedAt time.Time,
) arena.GoldenSubmissionLedger {
	t.Helper()
	participantID := active.ParticipantIDs[0]
	expected := ledger.Expectation()
	verifiedAt := startedAt.Add(time.Second)
	committedAt := startedAt.Add(2 * time.Second)
	previous := ledger.RevisionID
	ledger.PreviousRevisionID = &previous
	ledger.RevisionID = task049ID(22500 + active.Attempt.AttemptNo)
	ledger.Revision++
	ledger.NextSubmissionID = 2
	ledger.Submissions = []arena.GoldenSubmissionRecord{{
		ID: 1, Scope: active.Scope, ParticipantID: participantID,
		VerificationID:         task049ID(22510 + active.Attempt.AttemptNo),
		VerificationRevisionID: task049ID(22520 + active.Attempt.AttemptNo),
		EvidenceDigest:         sha256.Sum256([]byte("current provisional evidence")),
		VerifiedAt:             verifiedAt, CommittedAt: committedAt,
		Authority:        execution.Start.Authority.Identity,
		AssignmentDigest: active.Assignment.ExecutionPayloadDigest,
	}}
	ledger.Receipts = []arena.GoldenSubmissionReceipt{{
		CommandID: task049ID(22530 + active.Attempt.AttemptNo), Scope: active.Scope,
		ParticipantID: participantID, VerificationID: ledger.Submissions[0].VerificationID,
		CommandDigest: sha256.Sum256([]byte("current provisional command")),
		Disposition:   arena.GoldenSubmissionAccepted, SubmissionID: 1, Expected: expected,
		ResultRevisionID: ledger.RevisionID, ResultRevision: ledger.Revision, CommittedAt: committedAt,
	}}
	task049SealSubmissionLedger(t, &ledger)
	require.NoError(t, ledger.Validate())
	return ledger
}

func task050PriorPositionLedger(
	t *testing.T,
	ledger arena.GoldenPositionLedger,
	active arena.GoldenFailureActiveExecution,
	startedAt time.Time,
) arena.GoldenPositionLedger {
	t.Helper()
	priorAttempt := active.Group.Attempts[0]
	participantID := priorAttempt.ParticipantIDs[0]
	order := arena.GoldenAttemptOrderingEvidence{
		AttemptID: priorAttempt.ID, AttemptNo: priorAttempt.AttemptNo,
		SubmissionHead: arena.GoldenSubmissionLedgerExpectation{
			Scope: arena.GoldenSubmissionScope{
				State: active.Scope.State, AttemptID: priorAttempt.ID, WaveID: task049ID(22600),
				AssignmentID: task049ID(22601), SnapshotID: task049ID(22602), TaskID: task049ID(22603),
			},
			RevisionID: task049ID(22604), Revision: 2, NextSubmissionID: 2,
			PayloadDigest: sha256.Sum256([]byte("prior submission head")),
		},
		Order: []arena.GoldenPositionOrderEntry{{
			SubmissionID: 1, ParticipantID: participantID,
			CommittedAt:    startedAt.Add(-90 * time.Second),
			EvidenceDigest: sha256.Sum256([]byte("prior committed evidence")),
		}},
	}
	task049SealOrdering(t, &order)
	previous := ledger.RevisionID
	ledger.PreviousRevisionID = &previous
	ledger.RevisionID = task049ID(22605 + active.Attempt.AttemptNo)
	ledger.Revision++
	ledger.RevisionIDs = append(ledger.RevisionIDs, ledger.RevisionID)
	ledger.Attempts = []arena.GoldenAttemptOrderingEvidence{order}
	ledger.Positions = []arena.GoldenCommittedPosition{{
		Position: ledger.PositionFrom, ParticipantID: participantID, AttemptID: priorAttempt.ID,
		AttemptNo: priorAttempt.AttemptNo, SubmissionID: 1,
		EvidenceDigest: order.Order[0].EvidenceDigest, CommitID: task049ID(22610 + active.Attempt.AttemptNo),
	}}
	task049SealPositionLedger(t, &ledger)
	require.NoError(t, ledger.Validate())
	return ledger
}

func task050GoldenFailureReplayCommand(
	authority arena.GoldenFailureAuthority,
	base int,
) arena.GoldenFailureReplayCommand {
	private := make([]arena.GoldenPrivateAssignmentCommand, len(authority.Classification.ParticipantIDs))
	for index, participantID := range authority.Classification.ParticipantIDs {
		private[index] = arena.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID, AssignmentID: task049ID(base + 20 + index),
		}
	}
	return arena.GoldenFailureReplayCommand{
		Scope: authority.Active.Scope, CommandID: task049ID(base), FailureID: task049ID(base + 1),
		Expected: authority.Expectation(), ClosedWaveRevisionID: domain.ArenaWaveRevisionID(task049ID(base + 2)),
		NextAttemptID: task049ID(base + 3), NextAssignmentID: task049ID(base + 4),
		NextAssignmentRevisionID: task049ID(base + 5), NextWaveID: task049ID(base + 6),
		NextWaveRevisionID:       domain.ArenaWaveRevisionID(task049ID(base + 7)),
		NextWindowID:             task049ID(base + 8),
		NextWaveWindowRevisionID: domain.ArenaReadyWindowRevisionID(task049ID(base + 9)),
		NextWindowRevisionID:     task049ID(base + 10), NextReadinessRevisionID: task049ID(base + 11),
		NextPresenceRevisionID: task049ID(base + 12), NextMembershipID: task049ID(base + 13),
		NextMembershipRevisionID: task049ID(base + 14), PrivateAssignments: private,
	}
}

type task050FailureRepository struct {
	mu           sync.Mutex
	authority    arena.GoldenFailureAuthority
	replays      map[uuid.UUID]arena.GoldenFailureRecord
	writes       int
	beforeCommit func()
	commitErr    error
	returnRecord *arena.GoldenFailureRecord
	unrelated    []task050UnrelatedArenaState
}

type task050UnrelatedArenaState struct {
	TournamentID uuid.UUID
	GroupID      uuid.UUID
	Digest       [sha256.Size]byte
}

func newTask050FailureRepository(authority arena.GoldenFailureAuthority) *task050FailureRepository {
	return &task050FailureRepository{
		authority: authority.Snapshot(), replays: make(map[uuid.UUID]arena.GoldenFailureRecord),
		unrelated: []task050UnrelatedArenaState{
			{TournamentID: task049ID(29700), GroupID: task049ID(29701), Digest: sha256.Sum256([]byte("other tournament"))},
			{TournamentID: authority.Active.Scope.State.TournamentID, GroupID: task049ID(29702), Digest: sha256.Sum256([]byte("other group"))},
		},
	}
}

func (r *task050FailureRepository) unrelatedSnapshot() []task050UnrelatedArenaState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]task050UnrelatedArenaState(nil), r.unrelated...)
}

func (r *task050FailureRepository) FindGoldenFailure(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*arena.GoldenFailureRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, found := r.replays[commandID]
	if !found {
		return nil, nil
	}
	clone := record.Snapshot()
	return &clone, nil
}

func (r *task050FailureRepository) LoadGoldenFailureAuthority(
	_ context.Context,
	_ arena.GoldenSubmissionScope,
) (arena.GoldenFailureAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority.Snapshot(), nil
}

func (r *task050FailureRepository) CommitGoldenFailure(
	_ context.Context,
	commit arena.GoldenFailureCommit,
) (*arena.GoldenFailureRecord, bool, error) {
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

func (r *task050FailureRepository) authoritySnapshot() arena.GoldenFailureAuthority {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority.Snapshot()
}

func (r *task050FailureRepository) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *task050FailureRepository) currentSnapshot() arena.GoldenFailureRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority.Current.Snapshot()
}

var _ arena.GoldenFailureRepository = (*task050FailureRepository)(nil)
