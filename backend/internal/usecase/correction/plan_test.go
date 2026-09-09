package correction_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

func TestAtomicCorrectionRebuild(t *testing.T) {
	t.Parallel()

	t.Run("builds one detached deterministic atomic successor plan", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		beforeCommand := task056CloneCommand(command)
		beforeAuthority := task056CloneAuthority(authority)

		plan, err := correctionusecase.BuildPlan(command, authority)
		require.NoError(t, err)
		require.NoError(t, plan.Validate())
		require.NotEmpty(t, plan.Bytes())
		audit := plan.Audit()
		require.Equal(t, command.Reason, audit.Reason)
		require.Equal(t, command.Explanation, audit.Explanation)
		require.Equal(t, command.Fields, audit.Fields)
		command.Explanation = "mutated after planning"
		command.Fields[0] = correctionusecase.FieldWinner
		audit.Fields[0] = correctionusecase.FieldWinner
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
		require.Equal(t, resultusecase.SeriesScoreRevisionOperationReplaceResult, scoreRevision.Operation())

		seriesRevision := plan.SeriesResultRevision().Revision()
		require.Equal(t, command.NextSeriesResultRevisionID, seriesRevision.ID())
		require.Equal(t, authority.SeriesResult.ID, *seriesRevision.PreviousRevisionID())
		require.Equal(t, domain.SeriesResultReasonOperatorCorrection, seriesRevision.Outcome().SeriesReason)

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
		require.Equal(t, correctionusecase.ReadinessClosed, readiness.Next.State)
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
		require.Equal(t, correctionusecase.RejectionStale, correctionusecase.Code(release.ValidateCurrent(used)))
		disclosed := authority.Reservations[0]
		disclosed.Disclosed = true
		require.Equal(t, correctionusecase.RejectionStale, correctionusecase.Code(release.ValidateCurrent(disclosed)))
		malformedUsedRelease := release
		malformedUsedRelease.ExpectedUsed = true
		used = authority.Reservations[0]
		used.Used = true
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(
			malformedUsedRelease.ValidateCurrent(used),
		))
		malformedDisclosedRelease := release
		malformedDisclosedRelease.ExpectedDisclosed = true
		disclosed = authority.Reservations[0]
		disclosed.Disclosed = true
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(
			malformedDisclosedRelease.ValidateCurrent(disclosed),
		))

		cutoffCondition := plan.CutoffCondition()
		require.Equal(t, command.TournamentID, cutoffCondition.TournamentID())
		require.Equal(t, authority.TournamentState, cutoffCondition.ExpectedTournamentState())
		require.Equal(t, authority.TournamentRevision, cutoffCondition.ExpectedTournamentRevision())
		require.NoError(t, cutoffCondition.ValidateCurrent(
			authority.TournamentState, authority.TournamentRevision, authority.CutoffEvents,
		))
		require.Equal(t, correctionusecase.RejectionStale, correctionusecase.Code(
			cutoffCondition.ValidateCurrent(
				authority.TournamentState, authority.TournamentRevision+2, authority.CutoffEvents,
			),
		))
		affected := cutoffCondition.AffectedRevisions()
		require.Len(t, affected, 8)
		require.Equal(t, command.Expected.TargetProjection.ID(), cutoffCondition.TargetRevision().ID())
		blocked := correctionusecase.CutoffEvent{
			ID: correctionDAGTestID(40_350), Kind: correctionusecase.CutoffTaskDelivered,
			TournamentID: command.TournamentID, SourceRevisionID: affected[len(affected)-1].ID(),
			OccurredAt: affected[len(affected)-1].CreatedAt(),
		}
		require.ErrorIs(t, cutoffCondition.ValidateCurrent(
			authority.TournamentState, authority.TournamentRevision+2,
			[]correctionusecase.CutoffEvent{blocked},
		), correctionusecase.ErrCutoff)
		require.Equal(t, correctionusecase.RejectionCutoff, correctionusecase.Code(
			cutoffCondition.ValidateCurrent(
				authority.TournamentState, authority.TournamentRevision+2,
				[]correctionusecase.CutoffEvent{blocked},
			),
		))
		var unrelatedRevision domain.DerivedRevision
		for _, projection := range authority.DAG.Snapshot().Projections {
			artifact := projection.Revision().Artifact()
			if artifact.Kind == domain.ArtifactKindGameResult && artifact.EntityID != command.GameID {
				unrelatedRevision = projection.Revision()
				break
			}
		}
		require.False(t, unrelatedRevision.ID().IsZero())
		unrelatedEvent := correctionusecase.CutoffEvent{
			ID: correctionDAGTestID(40_351), Kind: correctionusecase.CutoffTaskDelivered,
			TournamentID: command.TournamentID, SourceRevisionID: unrelatedRevision.ID(),
			OccurredAt: unrelatedRevision.CreatedAt(),
		}
		require.Equal(t, correctionusecase.RejectionStale, correctionusecase.Code(
			cutoffCondition.ValidateCurrent(
				authority.TournamentState, authority.TournamentRevision+2,
				[]correctionusecase.CutoffEvent{unrelatedEvent},
			),
		))
		require.ErrorIs(t, cutoffCondition.ValidateCurrent(
			authority.TournamentState, authority.TournamentRevision,
			[]correctionusecase.CutoffEvent{blocked},
		), correctionusecase.ErrCutoff)
		require.Equal(t, correctionusecase.RejectionCutoff, correctionusecase.Code(
			cutoffCondition.ValidateCurrent(
				authority.TournamentState, authority.TournamentRevision,
				[]correctionusecase.CutoffEvent{blocked},
			),
		))
		require.Equal(t, correctionusecase.RejectionCutoff, correctionusecase.Code(
			cutoffCondition.ValidateCurrent(
				domain.TournamentStateCompleted, authority.TournamentRevision+2, nil,
			),
		))
		affected[0] = domain.DerivedRevision{}
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
		var unrelatedBefore domain.ProjectionRevision
		for _, projection := range authority.DAG.Snapshot().Projections {
			artifact := projection.Revision().Artifact()
			if artifact.Kind == domain.ArtifactKindGameResult && artifact.EntityID != command.GameID {
				unrelatedBefore = projection
				break
			}
		}
		require.False(t, unrelatedBefore.Revision().ID().IsZero())
		var unrelatedAfter domain.ProjectionRevision
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

		projections[0] = domain.ProjectionRevision{}
		decisions[0].Payload[0] ^= 0xff
		projectedSeries.Slots[0].Attempts[0].ResultReason = domain.GameResultReasonNoShow
		returnedReadiness := plan.Readiness()
		returnedReadiness.Expected.ParticipantIDs[0] = uuid.Nil
		returnedReleases := plan.Releases()
		returnedReleases[0] = correctionusecase.ReservationRelease{}
		returnedSupersessions := plan.Supersessions()
		if returnedSupersessions[0].PreviousDecisionID != nil {
			*returnedSupersessions[0].PreviousDecisionID = uuid.Nil
		}
		returnedSnapshot := plan.DAGSnapshot()
		returnedSnapshot.Projections[0] = domain.ProjectionRevision{}
		returnedBytes := plan.Bytes()
		returnedBytes[0] ^= 0xff
		returnedRebuild := plan.RebuildBytes()
		returnedRebuild[0] ^= 0xff
		require.NoError(t, plan.Validate())
		require.NotEqual(t, domain.ProjectionRevision{}, plan.ProjectionRevisions()[0])
		require.NotEqual(t, uuid.Nil, plan.Readiness().Expected.ParticipantIDs[0])
		require.NotEqual(t, correctionusecase.ReservationRelease{}, plan.Releases()[0])
		require.NotEqual(t, domain.ProjectionRevision{}, plan.DAGSnapshot().Projections[0])

		permutedCommand := task056CloneCommand(command)
		permutedAuthority := task056CloneAuthority(authority)
		slices.Reverse(permutedCommand.ProjectionIntents)
		slices.Reverse(permutedCommand.UnlockIntents)
		slices.Reverse(permutedAuthority.Reservations)
		slices.Reverse(permutedAuthority.Decisions)
		slices.Reverse(permutedAuthority.Readiness.ParticipantIDs)
		permuted, err := correctionusecase.BuildPlan(permutedCommand, permutedAuthority)
		require.NoError(t, err)
		require.Equal(t, plan.Bytes(), permuted.Bytes())
		require.Equal(t, plan.RebuildBytes(), permuted.RebuildBytes())
	})
}

func TestAtomicCorrectionPlanDocumentHasLedgerSchema(t *testing.T) {
	t.Parallel()

	command, authority := task056CorrectionFixture(t)
	plan, err := correctionusecase.BuildPlan(command, authority)
	require.NoError(t, err)

	var document struct {
		Schema string `json:"schema"`
	}
	require.NoError(t, json.Unmarshal(plan.Bytes(), &document))
	require.Equal(t, "result-correction-plan-v1", document.Schema)
}
