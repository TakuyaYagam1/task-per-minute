package assignment

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

func TestExactNormalDigestRejectsMissingAndZeroEvidence(t *testing.T) {
	t.Parallel()

	_, err := exactNormalDigest(nil)
	require.Error(t, err)

	_, err = exactNormalDigest(make([]byte, sha256.Size))
	require.Error(t, err)
}

func TestExactNormalCandidateRefsAreCanonical(t *testing.T) {
	t.Parallel()

	first, second := uuid.New(), uuid.New()
	candidates := []assignmentusecase.ExactNormalTaskVersion{
		{Task: domain.Task{ID: second}, Version: 2},
		{Task: domain.Task{ID: first}, Version: 1},
	}
	refs := exactNormalCandidateRefs(candidates)

	require.Len(t, refs, 2)
	require.Negative(t, domain.CompareTaskVersionRefs(refs[0], refs[1]))
}

func TestExactNormalPlanMatchesAuthorityTreatsEmptyHistoryAsEqual(t *testing.T) {
	t.Parallel()

	plan, authority := exactNormalSemanticFixture(t)
	authority.History = []capacity.TaskUse{}

	require.Nil(t, plan.History)
	require.True(t, exactNormalPlanMatchesAuthority(plan, authority))
}

func TestExactNormalPlanMatchesAuthorityUsesCategoryEligibleCandidates(t *testing.T) {
	t.Parallel()

	plan, authority := exactNormalSemanticFixture(t)

	require.Len(t, authority.Candidates, 5)
	require.Len(t, plan.CandidateTaskVersions, 4)
	require.True(t, exactNormalPlanMatchesAuthority(plan, authority))
}

func TestExactNormalSwissStageUsesPersistedRoundAndProjectionEvidence(t *testing.T) {
	t.Parallel()

	commandID, tournamentID, rosterID := uuid.New(), uuid.New(), uuid.New()
	seriesID, firstParticipantID, secondParticipantID := uuid.New(), uuid.New(), uuid.New()
	categoryRevisionID, poolRevisionID, projectionRevisionID := uuid.New(), uuid.New(), uuid.New()
	graphDigest := bytesOf(0x11)
	artifactDigest := bytesOf(0x22)
	row := sqlc.LockExactNormalAssignmentSwissStageRow{
		CommandID: commandID, TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		SeriesRevision: 3, RosterRevision: 7, CategoryRevisionID: categoryRevisionID,
		CategoryRevision: 2, SourcePoolRevisionID: poolRevisionID, PoolRevision: 4,
		Category: string(domain.CategoryWeb), PublishedProjectionRevisionID: projectionRevisionID,
		PublishedProjectionRevision: 8, GraphDigest: graphDigest, ArtifactDigest: artifactDigest,
	}

	got := exactNormalStageFromSwiss(row)
	require.Equal(t, exactNormalStage{
		CommandID: commandID, TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID,
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		SeriesRevision: 3, RosterRevision: 7, CategoryRevisionID: categoryRevisionID,
		CategoryRevision: 2, SourcePoolRevisionID: poolRevisionID, PoolRevision: 4,
		Category: string(domain.CategoryWeb), PublishedProjectionRevisionID: projectionRevisionID,
		PublishedProjectionRevision: 8, GraphDigest: graphDigest, ArtifactDigest: artifactDigest,
	}, got)
}

func TestExactNormalSwissStageSQLUsesPersistedRoundAuthority(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "db", "queries", "assignment.sql"))
	require.NoError(t, err)
	text := string(query)

	for _, fragment := range []string{
		"-- name: LockExactNormalAssignmentStage :one",
		"-- name: LockExactNormalAssignmentSwissStage :one",
		"FROM series_score_revisions AS score",
		"INNER JOIN wave_series AS wave_series",
		"INNER JOIN swiss_wave_links AS wave_link",
		"round.generation_kind = 'automatic'",
		"THEN round.decision_replay_digest",
		"round.generation_kind = 'manual'",
		"round.pairing_inputs::TEXT",
		"standings.payload_digest AS artifact_digest",
		"projection.id = score.source_projection_revision_id",
		"score.operation = 'initialize'",
		"category.mode IN ('random', 'admin')",
		"FOR UPDATE OF score, wave_series, wave, wave_link, round, pairing",
		"FROM tournament_stage_playoff_semifinals AS stage",
	} {
		require.Contains(t, text, fragment)
	}
}

func exactNormalSemanticFixture(t *testing.T) (assignmentusecase.ExactNormalAssignmentPlan, assignmentusecase.ExactNormalAssignmentAuthority) {
	t.Helper()
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	scope := assignmentusecase.ExactNormalAssignmentScope{
		TournamentID: exactNormalTestID(1), RosterID: exactNormalTestID(2),
		SeriesID: exactNormalTestID(3), SlotID: exactNormalTestID(4), CategoryLockID: exactNormalTestID(5),
	}
	poolID := exactNormalTestID(6)
	participants := []uuid.UUID{exactNormalTestID(20), exactNormalTestID(21)}
	tasks := make([]domain.Task, 5)
	versions := make([]domain.TaskVersionRef, len(tasks))
	candidates := make([]assignmentusecase.ExactNormalTaskVersion, len(tasks))
	for index := range tasks {
		category := domain.CategoryWeb
		if index == len(tasks)-1 {
			category = domain.CategoryCrypto
		}
		tasks[index] = domain.Task{
			ID: exactNormalTestID(30 + index), Title: "Task", Description: "Task description",
			Category: category, Difficulty: domain.DifficultyEasy, TimeLimit: 60, Flag: "flag",
			Kind: domain.TaskKindNormal, Enabled: true, CurrentVersion: 1, CreatedAt: at,
		}
		versions[index] = domain.TaskVersionRef{TaskID: tasks[index].ID, Version: 1}
		candidates[index] = assignmentusecase.ExactNormalTaskVersion{
			PoolRevisionID: poolID, Version: 1, Task: tasks[index],
		}
	}
	authority := assignmentusecase.ExactNormalAssignmentAuthority{
		Scope: scope, Category: domain.CategoryWeb,
		Revisions: assignmentusecase.ExactNormalAssignmentSourceRevisions{
			SeriesRevision: 2, PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: exactNormalTestID(7), HistoryRevision: 1,
			RosterRevision: 1, ArtifactRevisionID: exactNormalTestID(8), ArtifactRevision: 1,
			CategoryRevisionID: scope.CategoryLockID, CategoryRevision: 1,
		},
		Pool:           domain.TaskPoolRevision{ID: poolID, Revision: 1, Kind: domain.AssignmentTaskKindNormal, Versions: versions},
		ParticipantIDs: participants,
		ParticipantReservations: []assignmentusecase.ExactNormalParticipantReservation{
			{
				ParticipantID: participants[0], PlayerID: exactNormalTestID(50),
				Reservation: domain.ParticipantReservation{PlayerID: exactNormalTestID(50), ReservationID: exactNormalTestID(40), TournamentID: scope.TournamentID, Revision: 1, AcquiredAt: at, UpdatedAt: at},
			},
			{
				ParticipantID: participants[1], PlayerID: exactNormalTestID(51),
				Reservation: domain.ParticipantReservation{PlayerID: exactNormalTestID(51), ReservationID: exactNormalTestID(41), TournamentID: scope.TournamentID, Revision: 1, AcquiredAt: at, UpdatedAt: at},
			},
		},
		History: nil, Candidates: candidates,
		GraphDigest: sha256.Sum256([]byte("semantic-graph")), ArtifactDigest: sha256.Sum256([]byte("semantic-artifact")),
	}
	command := assignmentusecase.ExactNormalAssignmentCommand{
		Scope: scope, PlanID: exactNormalTestID(100), PlanRevisionID: exactNormalTestID(101),
		BranchID: exactNormalTestID(102), DecisionEvidenceID: exactNormalTestID(103), CreatedAt: at,
	}
	for index := range command.EdgeIDs {
		command.EdgeIDs[index] = exactNormalTestID(110 + index)
		command.ReservationIDs[index] = exactNormalTestID(120 + index)
		command.SnapshotIDs[index] = exactNormalTestID(130 + index)
	}
	plan, err := assignmentusecase.BuildExactNormalAssignment(command, authority)
	require.NoError(t, err)
	require.NoError(t, plan.Validate())
	return plan, authority
}

func exactNormalTestID(value int) uuid.UUID {
	return uuid.UUID{15: byte(value)}
}

func bytesOf(value byte) []byte {
	result := make([]byte, sha256.Size)
	for index := range result {
		result[index] = value
	}
	return result
}
