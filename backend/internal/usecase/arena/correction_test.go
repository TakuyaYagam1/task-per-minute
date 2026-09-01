package arena_test

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestAtomicCorrectionRebuild(t *testing.T) {
	t.Parallel()

	t.Run("builds one detached deterministic atomic successor plan", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		beforeCommand := task056CloneCommand(command)
		beforeAuthority := task056CloneAuthority(authority)

		plan, err := arena.PlanAtomicCorrection(command, authority)
		require.NoError(t, err)
		require.NoError(t, plan.Validate())
		require.NotEmpty(t, plan.Bytes())
		audit := plan.Audit()
		require.Equal(t, command.Reason, audit.Reason)
		require.Equal(t, command.Explanation, audit.Explanation)
		require.Equal(t, command.Fields, audit.Fields)
		command.Explanation = "mutated after planning"
		command.Fields[0] = arena.CorrectionFieldWinner
		audit.Fields[0] = arena.CorrectionFieldWinner
		require.Equal(t, beforeCommand.Explanation, plan.Audit().Explanation)
		require.Equal(t, beforeCommand.Fields, plan.Audit().Fields)
		command = beforeCommand

		gameRevision := plan.GameResultRevision().Revision()
		require.Equal(t, command.NextResultRevisionID, gameRevision.ID())
		require.Equal(t, authority.GameResult.ID, *gameRevision.PreviousRevisionID())
		require.Equal(t, authority.GameResult.Ordinal+1, gameRevision.Ordinal())

		scoreRevision := plan.ScoreRevision().Revision()
		require.Equal(t, command.NextScoreRevisionID, scoreRevision.ID())
		require.Equal(t, authority.Score.ID, *scoreRevision.PreviousRevisionID())
		require.Equal(t, arena.SeriesScoreRevisionOperationReplaceResult, scoreRevision.Operation())

		seriesRevision := plan.SeriesResultRevision().Revision()
		require.Equal(t, command.NextSeriesResultRevisionID, seriesRevision.ID())
		require.Equal(t, authority.SeriesResult.ID, *seriesRevision.PreviousRevisionID())
		require.Equal(t, arena.ArenaSeriesResultReasonOperatorCorrection, seriesRevision.Outcome().SeriesReason)

		projectedSeries := plan.Series()
		require.Equal(t, command.NextScoreRevisionID, *projectedSeries.CurrentScoreRevisionID)
		require.Equal(t, command.NextSeriesResultRevisionID, *projectedSeries.CurrentResultRevisionID)
		game, found := task056FindGame(projectedSeries, command.GameID)
		require.True(t, found)
		require.Equal(t, command.NextResultRevisionID, *game.ResultRevisionID)
		require.Equal(t, command.Patch.Reason, game.ResultReason)
		solve := plan.SolveTransition()
		require.Equal(t, solve.Expected, authority.CurrentSolve)
		require.Equal(t, command.Patch.SolveMetadata, solve.Next)
		if solve.Expected.SolvedAt != nil {
			*solve.Expected.SolvedAt = solve.Expected.SolvedAt.Add(time.Hour)
		}
		require.Equal(t, plan.SolveTransition().Expected, authority.CurrentSolve)

		projections := plan.ProjectionRevisions()
		require.Len(t, projections, 8)
		for index, projection := range projections {
			expected := command.ProjectionIntents[index].ExpectedRevision
			revision := projection.Revision()
			require.Equal(t, expected.ID(), *revision.PreviousRevisionID())
			require.Equal(t, expected.RevisionNo()+1, revision.RevisionNo())
			require.Equal(t, expected.Artifact(), revision.Artifact())
		}
		require.Len(t, plan.Supersessions(), 7)

		readiness := plan.Readiness()
		require.Equal(t, authority.Readiness.RevisionID, readiness.Expected.RevisionID)
		require.Equal(t, command.NextReadinessRevisionID, readiness.Next.RevisionID)
		require.Equal(t, arena.CorrectionReadinessClosed, readiness.Next.State)
		require.Equal(t, authority.Readiness.Revision+1, readiness.Next.Revision)
		require.Len(t, plan.Releases(), 1)
		release := plan.Releases()[0]
		require.Equal(t, authority.Reservations[0].ID, release.ReservationID)
		require.False(t, release.ExpectedUsed)
		require.False(t, release.ExpectedDisclosed)
		require.True(t, release.NextReleased)
		require.NoError(t, release.ValidateCurrent(authority.Reservations[0]))
		used := authority.Reservations[0]
		used.Used = true
		require.Equal(t, arena.CorrectionRejectionStale, arena.CorrectionCode(release.ValidateCurrent(used)))
		disclosed := authority.Reservations[0]
		disclosed.Disclosed = true
		require.Equal(t, arena.CorrectionRejectionStale, arena.CorrectionCode(release.ValidateCurrent(disclosed)))
		malformedUsedRelease := release
		malformedUsedRelease.ExpectedUsed = true
		used = authority.Reservations[0]
		used.Used = true
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(
			malformedUsedRelease.ValidateCurrent(used),
		))
		malformedDisclosedRelease := release
		malformedDisclosedRelease.ExpectedDisclosed = true
		disclosed = authority.Reservations[0]
		disclosed.Disclosed = true
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(
			malformedDisclosedRelease.ValidateCurrent(disclosed),
		))

		cutoffCondition := plan.CutoffCondition()
		require.Equal(t, command.TournamentID, cutoffCondition.TournamentID())
		require.Equal(t, authority.TournamentState, cutoffCondition.ExpectedTournamentState())
		require.Equal(t, authority.TournamentRevision, cutoffCondition.ExpectedTournamentRevision())
		require.NoError(t, cutoffCondition.ValidateCurrent(
			authority.TournamentState, authority.TournamentRevision, authority.CutoffEvents,
		))
		require.Equal(t, arena.CorrectionRejectionStale, arena.CorrectionCode(
			cutoffCondition.ValidateCurrent(
				authority.TournamentState, authority.TournamentRevision+2, authority.CutoffEvents,
			),
		))
		affected := cutoffCondition.AffectedRevisions()
		require.Len(t, affected, 8)
		require.Equal(t, command.Expected.TargetProjection.ID(), cutoffCondition.TargetRevision().ID())
		blocked := arena.CorrectionCutoffEvent{
			ID: task055ID(40_350), Kind: arena.CorrectionCutoffTaskDelivered,
			TournamentID: command.TournamentID, SourceRevisionID: affected[len(affected)-1].ID(),
			OccurredAt: affected[len(affected)-1].CreatedAt(),
		}
		require.ErrorIs(t, cutoffCondition.ValidateCurrent(
			authority.TournamentState, authority.TournamentRevision+2,
			[]arena.CorrectionCutoffEvent{blocked},
		), arena.ErrCorrectionCutoff)
		require.Equal(t, arena.CorrectionRejectionCutoff, arena.CorrectionCode(
			cutoffCondition.ValidateCurrent(
				authority.TournamentState, authority.TournamentRevision+2,
				[]arena.CorrectionCutoffEvent{blocked},
			),
		))
		var unrelatedRevision domain.ArenaDerivedRevision
		for _, projection := range authority.DAG.Snapshot().Projections {
			artifact := projection.Revision().Artifact()
			if artifact.Kind == domain.ArenaArtifactKindGameResult && artifact.EntityID != command.GameID {
				unrelatedRevision = projection.Revision()
				break
			}
		}
		require.False(t, unrelatedRevision.ID().IsZero())
		unrelatedEvent := arena.CorrectionCutoffEvent{
			ID: task055ID(40_351), Kind: arena.CorrectionCutoffTaskDelivered,
			TournamentID: command.TournamentID, SourceRevisionID: unrelatedRevision.ID(),
			OccurredAt: unrelatedRevision.CreatedAt(),
		}
		require.Equal(t, arena.CorrectionRejectionStale, arena.CorrectionCode(
			cutoffCondition.ValidateCurrent(
				authority.TournamentState, authority.TournamentRevision+2,
				[]arena.CorrectionCutoffEvent{unrelatedEvent},
			),
		))
		require.ErrorIs(t, cutoffCondition.ValidateCurrent(
			authority.TournamentState, authority.TournamentRevision,
			[]arena.CorrectionCutoffEvent{blocked},
		), arena.ErrCorrectionCutoff)
		require.Equal(t, arena.CorrectionRejectionCutoff, arena.CorrectionCode(
			cutoffCondition.ValidateCurrent(
				authority.TournamentState, authority.TournamentRevision,
				[]arena.CorrectionCutoffEvent{blocked},
			),
		))
		require.Equal(t, arena.CorrectionRejectionCutoff, arena.CorrectionCode(
			cutoffCondition.ValidateCurrent(
				domain.ArenaTournamentStateCompleted, authority.TournamentRevision+2, nil,
			),
		))
		affected[0] = domain.ArenaDerivedRevision{}
		require.False(t, plan.CutoffCondition().AffectedRevisions()[0].ID().IsZero())

		decisions := plan.Decisions()
		require.Len(t, decisions, 8)
		for index, decision := range decisions {
			require.Equal(t, index+1, decision.Sequence)
			require.Equal(t, projections[index].Revision().ID(), decision.ProjectionRevisionID)
		}
		require.NotEmpty(t, plan.RebuildBytes())

		snapshot := plan.DAGSnapshot()
		require.Len(t, snapshot.Projections, len(authority.DAG.Snapshot().Projections)+len(projections))
		for _, projection := range projections {
			current := task056CurrentRevision(snapshot, projection.Revision().Artifact())
			require.Equal(t, projection.Revision().ID(), current.ID())
		}
		var unrelatedBefore domain.ArenaProjectionRevision
		for _, projection := range authority.DAG.Snapshot().Projections {
			artifact := projection.Revision().Artifact()
			if artifact.Kind == domain.ArenaArtifactKindGameResult && artifact.EntityID != command.GameID {
				unrelatedBefore = projection
				break
			}
		}
		require.False(t, unrelatedBefore.Revision().ID().IsZero())
		var unrelatedAfter domain.ArenaProjectionRevision
		for _, projection := range snapshot.Projections {
			if projection.Revision().ID() == unrelatedBefore.Revision().ID() {
				unrelatedAfter = projection
				break
			}
		}
		require.Equal(t, unrelatedBefore.Revision(), unrelatedAfter.Revision())
		require.Equal(t, unrelatedBefore.Payload(), unrelatedAfter.Payload())

		require.Equal(t, beforeCommand, command)
		require.Equal(t, beforeAuthority.Series, authority.Series)
		require.Equal(t, beforeAuthority.DAG.Snapshot(), authority.DAG.Snapshot())
		require.Equal(t, beforeAuthority.Reservations, authority.Reservations)
		require.Equal(t, beforeAuthority.Decisions, authority.Decisions)

		projections[0] = domain.ArenaProjectionRevision{}
		decisions[0].Payload[0] ^= 0xff
		projectedSeries.Slots[0].Attempts[0].ResultReason = domain.ArenaGameResultReasonNoShow
		returnedReadiness := plan.Readiness()
		returnedReadiness.Expected.ParticipantIDs[0] = uuid.Nil
		returnedReleases := plan.Releases()
		returnedReleases[0] = arena.CorrectionReservationRelease{}
		returnedSupersessions := plan.Supersessions()
		if returnedSupersessions[0].PreviousDecisionID != nil {
			*returnedSupersessions[0].PreviousDecisionID = uuid.Nil
		}
		returnedSnapshot := plan.DAGSnapshot()
		returnedSnapshot.Projections[0] = domain.ArenaProjectionRevision{}
		returnedBytes := plan.Bytes()
		returnedBytes[0] ^= 0xff
		returnedRebuild := plan.RebuildBytes()
		returnedRebuild[0] ^= 0xff
		require.NoError(t, plan.Validate())
		require.NotEqual(t, domain.ArenaProjectionRevision{}, plan.ProjectionRevisions()[0])
		require.NotEqual(t, uuid.Nil, plan.Readiness().Expected.ParticipantIDs[0])
		require.NotEqual(t, arena.CorrectionReservationRelease{}, plan.Releases()[0])
		require.NotEqual(t, domain.ArenaProjectionRevision{}, plan.DAGSnapshot().Projections[0])

		permutedCommand := task056CloneCommand(command)
		permutedAuthority := task056CloneAuthority(authority)
		slices.Reverse(permutedCommand.ProjectionIntents)
		slices.Reverse(permutedCommand.UnlockIntents)
		slices.Reverse(permutedAuthority.Reservations)
		slices.Reverse(permutedAuthority.Decisions)
		slices.Reverse(permutedAuthority.Readiness.ParticipantIDs)
		permuted, err := arena.PlanAtomicCorrection(permutedCommand, permutedAuthority)
		require.NoError(t, err)
		require.Equal(t, plan.Bytes(), permuted.Bytes())
		require.Equal(t, plan.RebuildBytes(), permuted.RebuildBytes())
	})

	t.Run("enforces the combined old and successor payload cap", func(t *testing.T) {
		command, authority := task056CorrectionFixture(t)
		oldPayloadBytes := 0
		for _, projection := range authority.DAG.Snapshot().Projections {
			oldPayloadBytes += len(projection.Payload())
		}
		remaining := (512 << 10) - oldPayloadBytes
		require.Positive(t, remaining)
		for index, intent := range command.ProjectionIntents {
			size := remaining / len(command.ProjectionIntents)
			if index < remaining%len(command.ProjectionIntents) {
				size++
			}
			require.LessOrEqual(t, size, arena.MaxRecordedProjectionDecisionBytes)
			command.ProjectionIntents[index] = arena.NewCorrectionProjectionIntent(
				intent.ExpectedRevision, intent.NextRevisionID, intent.DecisionID, make([]byte, size),
			)
		}
		exact, err := arena.PlanAtomicCorrection(command, authority)
		require.NoError(t, err)
		require.NoError(t, exact.Validate())

		over := task056CloneCommand(command)
		last := len(over.ProjectionIntents) - 1
		intent := over.ProjectionIntents[last]
		payload := append([]byte(nil), intent.Payload...)
		payload = append(payload, 0x01)
		over.ProjectionIntents[last] = arena.NewCorrectionProjectionIntent(
			intent.ExpectedRevision, intent.NextRevisionID, intent.DecisionID, payload,
		)
		rejected, err := arena.PlanAtomicCorrection(over, authority)
		require.ErrorIs(t, err, arena.ErrInvalidCorrection)
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))
		require.Equal(t, arena.AtomicCorrectionPlan{}, rejected)
	})

	t.Run("rejects an affected decision set that is not a causal suffix", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		var sibling domain.ArenaDerivedRevision
		for _, projection := range authority.DAG.Snapshot().Projections {
			artifact := projection.Revision().Artifact()
			if artifact.Kind == domain.ArenaArtifactKindGameResult && artifact.EntityID != command.GameID {
				sibling = projection.Revision()
				break
			}
		}
		require.False(t, sibling.ID().IsZero())
		for index := range authority.Decisions {
			authority.Decisions[index].Sequence += 2
		}
		targetPayload := []byte(`{"target":true}`)
		siblingPayload := []byte(`{"sibling":true}`)
		authority.Decisions = append([]arena.RecordedProjectionDecision{
			{
				ID: task055ID(76_000), Sequence: 1,
				ProjectionRevisionID: command.Expected.TargetProjection.ID(),
				RecordedAt:           command.Expected.TargetProjection.CreatedAt(),
				Payload:              targetPayload, PayloadDigest: sha256.Sum256(targetPayload),
			},
			{
				ID: task055ID(76_001), Sequence: 2, ProjectionRevisionID: sibling.ID(),
				RecordedAt: sibling.CreatedAt(), Payload: siblingPayload,
				PayloadDigest: sha256.Sum256(siblingPayload),
			},
		}, authority.Decisions...)
		var err error
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: authority.DAG, Decisions: authority.Decisions,
		})
		require.NoError(t, err)
		command.Expected, err = arena.NewCorrectionExpectation(authority, command.Expected.TargetProjection.ID())
		require.NoError(t, err)
		plan, err := arena.PlanAtomicCorrection(command, authority)
		require.ErrorIs(t, err, arena.ErrInvalidCorrection)
		require.Equal(t, arena.CorrectionRejectionIncomplete, arena.CorrectionCode(err))
		require.Equal(t, arena.AtomicCorrectionPlan{}, plan)
	})

	t.Run("rejects reapplication after authority advanced", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		first, err := arena.PlanAtomicCorrection(command, authority)
		require.NoError(t, err)
		advanced := authority
		advanced.DAG = task056DAGFromCorrectionPlan(t, command, first)
		second, err := arena.PlanAtomicCorrection(command, advanced)
		require.ErrorIs(t, err, arena.ErrInvalidCorrection)
		require.Equal(t, arena.CorrectionRejectionStale, arena.CorrectionCode(err))
		require.Equal(t, arena.AtomicCorrectionPlan{}, second)
	})

	t.Run("returns zero plan on rejection without partial mutation", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		authority.CutoffEvents = []arena.CorrectionCutoffEvent{{
			ID: task055ID(43_000), Kind: arena.CorrectionCutoffWaveStarted,
			TournamentID:     command.TournamentID,
			SourceRevisionID: command.Expected.SeriesProjection.ID(),
			OccurredAt:       command.RequestedAt,
		}}
		beforeCommand := task056CloneCommand(command)
		beforeAuthority := task056CloneAuthority(authority)

		plan, err := arena.PlanAtomicCorrection(command, authority)
		require.ErrorIs(t, err, arena.ErrCorrectionCutoff)
		require.Equal(t, arena.CorrectionRejectionCutoff, arena.CorrectionCode(err))
		require.Equal(t, arena.AtomicCorrectionPlan{}, plan)
		require.Equal(t, beforeCommand, command)
		require.Equal(t, beforeAuthority.DAG.Snapshot(), authority.DAG.Snapshot())
		require.Equal(t, beforeAuthority.Reservations, authority.Reservations)
	})

	t.Run("rebuilds only the current head of an affected artifact lineage", func(t *testing.T) {
		t.Parallel()

		fixture := task055RevisionDAGFixture(t)
		var standings domain.ArenaProjectionRevision
		var goldenID domain.ArenaDerivedRevisionID
		for _, projection := range fixture.Graph.Projections() {
			//nolint:exhaustive // The lineage fixture lookup needs only these two artifacts.
			switch projection.Revision().Artifact().Kind {
			case domain.ArenaArtifactKindStandings:
				standings = projection
			case domain.ArenaArtifactKindGoldenGroup:
				goldenID = projection.Revision().ID()
			default:
			}
		}
		standingsHead := task055SuccessorProjection(
			t, standings, 43_010, standings.Revision().CreatedAt().Add(500*time.Millisecond),
			"standings-current",
		)
		projections := append(fixture.Graph.Projections(), standingsHead)
		dependencies := make([]domain.ArenaRevisionDependency, 0, len(fixture.Graph.Dependencies())+1)
		for _, dependency := range fixture.Graph.Dependencies() {
			if dependency.DerivedRevisionID == standings.Revision().ID() {
				dependencies = append(dependencies, dependency, domain.ArenaRevisionDependency{
					SourceRevisionID:  dependency.SourceRevisionID,
					DerivedRevisionID: standingsHead.Revision().ID(),
				})
				continue
			}
			if dependency.SourceRevisionID == standings.Revision().ID() &&
				dependency.DerivedRevisionID == goldenID {
				dependencies = append(dependencies, domain.ArenaRevisionDependency{
					SourceRevisionID: standingsHead.Revision().ID(), DerivedRevisionID: goldenID,
				})
				continue
			}
			dependencies = append(dependencies, dependency)
		}
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID:  standings.Revision().ID(),
			DerivedRevisionID: standingsHead.Revision().ID(),
		})
		graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		dag, err := arena.BuildRevisionDAG(arena.RevisionDAGInput{Graph: graph, Results: fixture.Results})
		require.NoError(t, err)

		command, authority := task056CorrectionFixture(t)
		authority.DAG = dag
		command.Expected, err = arena.NewCorrectionExpectation(authority, command.Expected.TargetProjection.ID())
		require.NoError(t, err)
		cutoff, err := arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: command.TournamentID,
			TargetRevisionID: command.Expected.TargetProjection.ID(),
			TournamentState:  authority.TournamentState,
		})
		require.NoError(t, err)
		require.Len(t, cutoff.Descendants(), 8)
		current := make(map[domain.ArenaArtifactRef]domain.ArenaDerivedRevision)
		for _, projection := range dag.Snapshot().Projections {
			revision := projection.Revision()
			if prior, exists := current[revision.Artifact()]; !exists || prior.RevisionNo() < revision.RevisionNo() {
				current[revision.Artifact()] = revision
			}
		}
		rebuild := []domain.ArenaDerivedRevision{command.Expected.TargetProjection}
		for _, descendant := range cutoff.Descendants() {
			if head := current[descendant.Artifact()]; head.ID() == descendant.ID() {
				rebuild = append(rebuild, descendant)
			}
		}
		command.ProjectionIntents = make([]arena.CorrectionProjectionIntent, len(rebuild))
		for index, revision := range rebuild {
			command.ProjectionIntents[index] = arena.NewCorrectionProjectionIntent(
				revision, domain.ArenaDerivedRevisionID(task055ID(43_100+index)),
				task055ID(43_200+index), []byte(fmt.Sprintf("lineage-%02d", index)),
			)
		}

		plan, err := arena.PlanAtomicCorrection(command, authority)
		require.NoError(t, err)
		require.Len(t, plan.ProjectionRevisions(), 8)
		require.Len(t, plan.Supersessions(), 7)
		for _, supersession := range plan.Supersessions() {
			require.False(t, supersession.PreviousRevisionID.IsZero())
			require.False(t, supersession.SuccessorRevisionID.IsZero())
			require.NotEqual(t, uuid.Nil, supersession.ReplacementDecisionID)
			if supersession.Artifact.Kind == domain.ArenaArtifactKindStandings {
				require.Equal(t, standingsHead.Revision().ID(), supersession.PreviousRevisionID)
				require.NotEqual(t, standings.Revision().ID(), supersession.PreviousRevisionID)
			}
		}
		standingsSuccessors := 0
		var standingsSuccessorID domain.ArenaDerivedRevisionID
		var seriesResultSuccessorID domain.ArenaDerivedRevisionID
		for _, projection := range plan.ProjectionRevisions() {
			revision := projection.Revision()
			if revision.Artifact().Kind == domain.ArenaArtifactKindSeriesResult {
				seriesResultSuccessorID = revision.ID()
			}
			if revision.Artifact().Kind != domain.ArenaArtifactKindStandings {
				continue
			}
			standingsSuccessors++
			standingsSuccessorID = revision.ID()
			require.Equal(t, standingsHead.Revision().ID(), *revision.PreviousRevisionID())
			require.NotEqual(t, standings.Revision().ID(), *revision.PreviousRevisionID())
		}
		require.Equal(t, 1, standingsSuccessors)
		require.False(t, standingsSuccessorID.IsZero())
		require.False(t, seriesResultSuccessorID.IsZero())
		standingsSources := make([]domain.ArenaDerivedRevisionID, 0, 2)
		for _, dependency := range plan.DAGSnapshot().Dependencies {
			if dependency.DerivedRevisionID == standingsSuccessorID {
				standingsSources = append(standingsSources, dependency.SourceRevisionID)
			}
		}
		require.ElementsMatch(t, []domain.ArenaDerivedRevisionID{
			standingsHead.Revision().ID(), seriesResultSuccessorID,
		}, standingsSources)
		require.NotContains(t, standingsSources, standings.Revision().ID())
	})

	t.Run("supports concurrent reuse without shared mutation", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		baseline, err := arena.PlanAtomicCorrection(command, authority)
		require.NoError(t, err)

		const workers = 12
		results := make(chan []byte, workers)
		errors := make(chan error, workers)
		var group sync.WaitGroup
		for range workers {
			group.Add(1)
			go func() {
				defer group.Done()
				if validateErr := baseline.Validate(); validateErr != nil {
					errors <- validateErr
					return
				}
				plan, planErr := arena.PlanAtomicCorrection(command, authority)
				if planErr != nil {
					errors <- planErr
					return
				}
				if validateErr := plan.Validate(); validateErr != nil {
					errors <- validateErr
					return
				}
				results <- plan.Bytes()
			}()
		}
		group.Wait()
		close(results)
		close(errors)
		for planErr := range errors {
			require.NoError(t, planErr)
		}
		for payload := range results {
			require.Equal(t, baseline.Bytes(), payload)
		}
	})
}

func task056FindGame(series domain.ArenaSeries, gameID [16]byte) (domain.ArenaGame, bool) {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == gameID {
				return game, true
			}
		}
	}
	return domain.ArenaGame{}, false
}

func task056CurrentRevision(
	snapshot arena.RevisionDAGSnapshot,
	artifact domain.ArenaArtifactRef,
) domain.ArenaDerivedRevision {
	var current domain.ArenaDerivedRevision
	for _, projection := range snapshot.Projections {
		revision := projection.Revision()
		if revision.Artifact() == artifact && revision.RevisionNo() > current.RevisionNo() {
			current = revision
		}
	}
	return current
}

func task056DAGFromCorrectionPlan(
	t *testing.T,
	command arena.CorrectionCommand,
	plan arena.AtomicCorrectionPlan,
) arena.RevisionDAG {
	t.Helper()
	snapshot := plan.DAGSnapshot()
	graph, err := domain.NewArenaRevisionGraph(snapshot.Projections, snapshot.Dependencies)
	require.NoError(t, err)
	fixture := task055RevisionDAGFixture(t)
	gameHead := plan.GameResultRevision().Revision().Head()
	scoreHead := plan.ScoreRevision().Revision().Head()
	seriesHead := plan.SeriesResultRevision().Revision().Head()
	var gameProjection, scoreProjection, seriesProjection domain.ArenaProjectionRevision
	for _, projection := range snapshot.Projections {
		artifact := projection.Revision().Artifact()
		switch {
		case artifact.Kind == domain.ArenaArtifactKindGameResult && artifact.EntityID == command.GameID:
			if gameProjection.Revision().RevisionNo() < projection.Revision().RevisionNo() {
				gameProjection = projection
			}
		case artifact.Kind == domain.ArenaArtifactKindSeriesScore && artifact.EntityID == command.SeriesID:
			if scoreProjection.Revision().RevisionNo() < projection.Revision().RevisionNo() {
				scoreProjection = projection
			}
		case artifact.Kind == domain.ArenaArtifactKindSeriesResult && artifact.EntityID == command.SeriesID:
			if seriesProjection.Revision().RevisionNo() < projection.Revision().RevisionNo() {
				seriesProjection = projection
			}
		}
	}
	require.False(t, gameProjection.Revision().ID().IsZero())
	require.False(t, scoreProjection.Revision().ID().IsZero())
	require.False(t, seriesProjection.Revision().ID().IsZero())
	inputs := make([]arena.OfficialResultProjectionInput, len(fixture.Results))
	for index, input := range fixture.Results {
		switch input.Result.Scope {
		case gameHead.Scope:
			input = arena.OfficialResultProjectionInput{
				Result: gameHead, ResultProjection: gameProjection,
			}
		case seriesHead.Scope:
			input = arena.OfficialResultProjectionInput{
				Result: seriesHead, ResultProjection: seriesProjection,
				Score: &scoreHead, ScoreProjection: &scoreProjection,
			}
		}
		inputs[index] = input
	}
	dag, err := arena.BuildRevisionDAG(arena.RevisionDAGInput{Graph: graph, Results: inputs})
	require.NoError(t, err)
	return dag
}
