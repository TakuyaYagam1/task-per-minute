//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestGoldenLastUnresolvedCompletesAfterDeadline(t *testing.T) {
	setup := prepareTwoGroupGoldenThroughREST(t)
	fixture, adminToken, tournamentID := setup.rest, setup.adminToken, setup.tournamentID
	runtime := tournamentFlowRuntimeForFixture(t, fixture)
	runtime.clock.FreezeAt(runtime.clock.Now())

	activeGroups := make([]api.GoldenOperatorGroup, 0, len(setup.ready.Groups))
	sourceGroups := make(map[uuid.UUID]api.GoldenOperatorGroup, len(setup.ready.Groups))
	for _, group := range setup.ready.Groups {
		require.Len(t, group.Members, 2, "fixture must leave exactly one unresolved after one solve")
		started := startGoldenGroupWithCurrentRevision(t, fixture, adminToken, tournamentID, group.GroupId)
		active := goldenOperatorGroupByID(t, started, group.GroupId)
		require.Equal(t, api.GoldenRuntimeState("active"), active.State)
		require.NotNil(t, active.Deadline)
		activeGroups = append(activeGroups, active)
		sourceGroups[active.GroupRevisionId] = active
	}

	positions := make(map[uuid.UUID]int64)
	solvers := make(map[uuid.UUID]bool)
	earliestDeadline, latestDeadline := *activeGroups[0].Deadline, *activeGroups[0].Deadline
	for _, group := range activeGroups {
		if group.Deadline.Before(earliestDeadline) {
			earliestDeadline = *group.Deadline
		}
		if group.Deadline.After(latestDeadline) {
			latestDeadline = *group.Deadline
		}
		solver := group.Members[0].ParticipantId
		participant := goldenParticipantThroughREST(t, fixture, tournamentID, setup.players[solver])
		require.NotNil(t, participant.Task)
		flag, found := setup.catalog.flags[participant.Task.TaskId]
		require.True(t, found, "fixture must contain the assigned Golden task")
		submitted := submitGoldenParticipantThroughREST(t, fixture, tournamentID, setup.players[solver], participant, flag)
		require.True(t, submitted.Submitted)
		require.NotNil(t, submitted.Position)
		positions[solver] = int64(*submitted.Position)
		positions[group.Members[1].ParticipantId] = int64(*submitted.Position) + 1
		solvers[solver] = true
	}

	before := readGoldenLastUnresolvedEvidence(t, tournamentID)
	require.Equal(t, 2, before.attempts)
	require.Equal(t, 4, before.members)
	require.Equal(t, 2, before.submissions)
	require.Equal(t, 2, before.acceptedSubmissions)
	require.Zero(t, before.noShows)
	require.Zero(t, before.finalizedAssignments)
	realPositions := readGoldenRealPositionCommits(t, tournamentID)
	require.Len(t, realPositions, len(activeGroups))
	runtime.clock.FreezeAt(earliestDeadline.Add(-time.Second))
	require.NoError(t, runtime.golden.Recover(t.Context(), tournamentID))
	require.Equal(t, before, readGoldenLastUnresolvedEvidence(t, tournamentID), "recovery before deadline must not mutate durable state")
	stillActive := goldenOperatorThroughREST(t, fixture, adminToken, tournamentID)
	for _, group := range activeGroups {
		current := goldenOperatorGroupByID(t, stillActive, group.GroupId)
		require.Equal(t, api.GoldenRuntimeState("active"), current.State)
		require.True(t, group.AttemptId == current.AttemptId)
		for _, member := range current.Members {
			require.Equal(t, solvers[member.ParticipantId], member.Submitted)
			if !solvers[member.ParticipantId] {
				require.Nil(t, member.Position, "last unresolved must have no position before deadline")
			}
		}
	}
	t.Logf("golden-last-unresolved phase=before_deadline groups=2 attempts=%d submissions=%d no_shows=%d unchanged=true",
		before.attempts, before.submissions, before.noShows)

	recoveryAt := latestDeadline.Add(time.Microsecond)
	runtime.clock.FreezeAt(recoveryAt)
	require.NoError(t, runtime.golden.Recover(t.Context(), tournamentID))
	after := readGoldenLastUnresolvedEvidence(t, tournamentID)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/golden"
	req, resp := doTournamentFlowJSON(t, fixture, http.MethodGet, path, "", adminSession(adminToken), uuid.New(), "")
	require.Equal(t, http.StatusOK, resp.Code)
	completed := decodeJSON[api.GoldenOperatorResponse](t, resp)
	t.Logf("golden-last-unresolved phase=after_deadline attempts=%d previous_attempts=%d submissions=%d no_shows=%d http_status=%d",
		after.attempts, before.attempts, after.submissions, after.noShows, resp.Code)
	for ordinal, group := range completed.Groups {
		t.Logf("golden-last-unresolved phase=after_deadline ordinal=%d state=%s members=%d revision=%d",
			ordinal+1, group.State, len(group.Members), group.RuntimeRevision)
	}
	require.Equal(t, before.attempts, after.attempts, "terminal fallback must not create a singleton reserve attempt")
	require.Equal(t, before.members, after.members)
	require.Equal(t, before.submissions, after.submissions, "terminal fallback must not invent a submission")
	require.Equal(t, before.acceptedSubmissions, after.acceptedSubmissions)
	require.Zero(t, after.noShows, "a participant who did not solve is not a no-show")
	require.Equal(t, len(activeGroups), after.finalizedAssignments)
	require.Greater(t, after.runtimeRevision, before.runtimeRevision)
	fixture.validateResponse(t, req, resp)
	require.Len(t, completed.Groups, len(activeGroups))
	for _, group := range activeGroups {
		current := goldenOperatorGroupByID(t, completed, group.GroupId)
		require.True(t, group.AttemptId == current.AttemptId)
		require.Equal(t, api.GoldenRuntimeState("completed"), current.State)
		require.Len(t, current.Members, 2)
		for _, member := range current.Members {
			require.Equal(t, solvers[member.ParticipantId], member.Submitted)
			require.NotNil(t, member.Position)
			require.EqualValues(t, positions[member.ParticipantId], *member.Position)
			participant := goldenParticipantThroughREST(t, fixture, tournamentID, setup.players[member.ParticipantId])
			require.Equal(t, api.GoldenRuntimeState("completed"), participant.State)
			require.Equal(t, solvers[member.ParticipantId], participant.Submitted)
			require.NotNil(t, participant.Position)
			require.EqualValues(t, positions[member.ParticipantId], *participant.Position)
		}
	}
	require.Equal(t, realPositions, readGoldenRealPositionCommits(t, tournamentID), "real solve provenance must remain unchanged")
	terminalPositions := readGoldenTerminalPositions(t, tournamentID)
	require.Len(t, terminalPositions, len(activeGroups), "one durable terminal placement per group")
	seenGroups := make(map[uuid.UUID]bool, len(activeGroups))
	for _, terminal := range terminalPositions {
		group, found := sourceGroups[terminal.groupRevisionID]
		require.True(t, found, "evidence must reference an original group revision")
		require.False(t, seenGroups[terminal.groupRevisionID])
		seenGroups[terminal.groupRevisionID] = true
		require.Equal(t, group.AttemptId, terminal.attemptID)
		require.Equal(t, group.Members[1].ParticipantId, terminal.participantID)
		require.False(t, solvers[terminal.participantID])
		require.Equal(t, positions[terminal.participantID], terminal.position)
		require.True(t, terminal.deadline.Equal(*group.Deadline), "evidence must retain the original deadline")
		require.True(t, terminal.recordedAt.Equal(recoveryAt))
		require.True(t, terminal.createdAt.Equal(terminal.recordedAt))
		require.False(t, terminal.recordedAt.Before(terminal.deadline))
		require.GreaterOrEqual(t, terminal.runtimeRevision, before.runtimeRevision)
		require.Less(t, terminal.runtimeRevision, after.runtimeRevision)
		require.True(t, terminal.exactProvenance, "evidence, membership, assignment and commit must share exact authority")
		require.True(t, terminal.exactTimestamps, "commit and finalization must use the evidence timestamp")
		require.True(t, terminal.validDigest)
		require.Zero(t, terminal.submissions, "terminal participant must have no manufactured submission")
	}

	// Repeating the same elapsed boundary must preserve its durable result.
	require.NoError(t, runtime.golden.Recover(t.Context(), tournamentID))
	require.Equal(t, after, readGoldenLastUnresolvedEvidence(t, tournamentID))
	require.Equal(t, realPositions, readGoldenRealPositionCommits(t, tournamentID))
	require.Equal(t, terminalPositions, readGoldenTerminalPositions(t, tournamentID), "replay must preserve evidence identity and timestamps")
	replayed := goldenOperatorThroughREST(t, fixture, adminToken, tournamentID)
	require.Equal(t, completed.Groups, replayed.Groups)
	t.Logf("golden-last-unresolved phase=replay groups=2 attempts=%d submissions=%d no_shows=%d unchanged=true",
		after.attempts, after.submissions, after.noShows)

	projectionRevision := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID).
		NextCursor.ProjectionRevision
	playoffs := applyTournamentActionThroughREST(t, fixture, adminToken, tournamentID,
		projectionRevision, "start_playoffs", uuid.New())
	require.Equal(t, api.TournamentState("playoffs"), playoffs.State)
	t.Log("golden-last-unresolved phase=progression state=playoffs")
}

type goldenLastUnresolvedEvidence struct {
	runtimeRevision      int64
	attempts             int
	members              int
	submissions          int
	acceptedSubmissions  int
	noShows              int
	positionCommits      int
	finalizedAssignments int
	recoveryRevisions    int
	commands             int
	ledgerRevisions      int
}

func readGoldenLastUnresolvedEvidence(t *testing.T, tournamentID uuid.UUID) goldenLastUnresolvedEvidence {
	t.Helper()
	var evidence goldenLastUnresolvedEvidence
	require.NoError(t, sharedPool.QueryRow(t.Context(), `
		SELECT
			(SELECT revision FROM golden_runtime_heads WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM golden_attempts WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM golden_memberships WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM golden_provisional_submissions WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM golden_provisional_submissions WHERE tournament_id = $1 AND status = 'accepted'),
			(SELECT COUNT(*) FROM golden_memberships WHERE tournament_id = $1 AND no_show_at IS NOT NULL),
			(SELECT COUNT(*) FROM golden_position_commits WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM golden_runtime_assignments WHERE tournament_id = $1 AND finalized_at IS NOT NULL),
			(SELECT COUNT(*) FROM golden_recovery_revisions WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM golden_runtime_commands WHERE tournament_id = $1),
			(SELECT COUNT(*) FROM golden_position_ledger_revisions WHERE tournament_id = $1)`,
		tournamentID,
	).Scan(&evidence.runtimeRevision, &evidence.attempts, &evidence.members,
		&evidence.submissions, &evidence.acceptedSubmissions, &evidence.noShows,
		&evidence.positionCommits, &evidence.finalizedAssignments,
		&evidence.recoveryRevisions, &evidence.commands, &evidence.ledgerRevisions))
	return evidence
}

type goldenRealPositionCommit struct {
	id            uuid.UUID
	attemptID     uuid.UUID
	memberID      uuid.UUID
	participantID uuid.UUID
	submissionID  uuid.UUID
	position      int64
	committedAt   time.Time
	createdAt     time.Time
}

func readGoldenRealPositionCommits(t *testing.T, tournamentID uuid.UUID) []goldenRealPositionCommit {
	t.Helper()
	rows, err := sharedPool.Query(t.Context(), `
		SELECT id, attempt_id, membership_id, participant_id, provisional_submission_id,
			position, committed_at, created_at
		FROM golden_position_commits
		WHERE tournament_id = $1 AND terminal_evidence_id IS NULL
		ORDER BY id`, tournamentID)
	require.NoError(t, err)
	defer rows.Close()
	var positions []goldenRealPositionCommit
	for rows.Next() {
		var position goldenRealPositionCommit
		require.NoError(t, rows.Scan(&position.id, &position.attemptID, &position.memberID,
			&position.participantID, &position.submissionID, &position.position,
			&position.committedAt, &position.createdAt))
		positions = append(positions, position)
	}
	require.NoError(t, rows.Err())
	return positions
}

type goldenTerminalPosition struct {
	id              uuid.UUID
	groupRevisionID uuid.UUID
	attemptID       uuid.UUID
	memberID        uuid.UUID
	participantID   uuid.UUID
	runtimeRevision int64
	deadline        time.Time
	position        int64
	recordedAt      time.Time
	createdAt       time.Time
	commitID        uuid.UUID
	exactProvenance bool
	exactTimestamps bool
	validDigest     bool
	submissions     int
}

func readGoldenTerminalPositions(t *testing.T, tournamentID uuid.UUID) []goldenTerminalPosition {
	t.Helper()
	rows, err := sharedPool.Query(t.Context(), `
		SELECT evidence.id, evidence.group_revision_id, evidence.attempt_id, evidence.membership_id,
			evidence.participant_id, evidence.runtime_revision, evidence.deadline, evidence.position,
			evidence.recorded_at, evidence.created_at, committed.id,
			(committed.provisional_submission_id IS NULL AND committed.previous_position_commit_id IS NULL
				AND committed.attempt_id = evidence.attempt_id AND committed.membership_id = evidence.membership_id
				AND committed.participant_id = evidence.participant_id AND committed.position = evidence.position
				AND committed.tournament_id = evidence.tournament_id AND committed.roster_id = evidence.roster_id
				AND membership.attempt_id = evidence.attempt_id AND membership.participant_id = evidence.participant_id
				AND membership.tournament_id = evidence.tournament_id AND membership.roster_id = evidence.roster_id
				AND membership.participation_established_at IS NOT NULL
				AND membership.no_show_at IS NULL AND membership.excluded_at IS NULL
				AND assignment.group_revision_id = evidence.group_revision_id
				AND assignment.tournament_id = evidence.tournament_id AND assignment.roster_id = evidence.roster_id
				AND assignment.deadline = evidence.deadline AND assignment.settlement_revision_id IS NOT NULL
				AND assignment.settlement_revision_id <> '00000000-0000-0000-0000-000000000000'::uuid),
			(committed.committed_at = evidence.recorded_at AND committed.created_at = evidence.created_at
				AND assignment.finalized_at = evidence.recorded_at),
			(octet_length(evidence.payload_digest) = 32 AND evidence.payload_digest <> decode(repeat('00', 32), 'hex')),
			(SELECT COUNT(*) FROM golden_provisional_submissions AS submission
				WHERE submission.tournament_id = evidence.tournament_id AND submission.participant_id = evidence.participant_id)
		FROM golden_terminal_position_evidence AS evidence
		JOIN golden_position_commits AS committed ON committed.terminal_evidence_id = evidence.id
		JOIN golden_memberships AS membership ON membership.id = evidence.membership_id
		JOIN golden_runtime_assignments AS assignment ON assignment.attempt_id = evidence.attempt_id
		WHERE evidence.tournament_id = $1
		ORDER BY evidence.id`, tournamentID)
	require.NoError(t, err)
	defer rows.Close()
	var positions []goldenTerminalPosition
	for rows.Next() {
		var position goldenTerminalPosition
		require.NoError(t, rows.Scan(&position.id, &position.groupRevisionID, &position.attemptID, &position.memberID,
			&position.participantID, &position.runtimeRevision, &position.deadline, &position.position,
			&position.recordedAt, &position.createdAt, &position.commitID, &position.exactProvenance,
			&position.exactTimestamps, &position.validDigest, &position.submissions))
		positions = append(positions, position)
	}
	require.NoError(t, rows.Err())
	var count int
	require.NoError(t, sharedPool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM golden_terminal_position_evidence WHERE tournament_id = $1`, tournamentID,
	).Scan(&count))
	require.Len(t, positions, count, "every terminal evidence record must have exact committed provenance")
	return positions
}

// prepareGoldenThreeMemberSwiss gives the reserve test a three-way tie by
// playing all real Swiss rounds: one undefeated player and a cycle of wins
// among the other three. No standings or Golden group rows are rewritten.
func prepareGoldenThreeMemberSwiss(t *testing.T) tournamentAdminSwissProofFixture {
	t.Helper()
	ctx := t.Context()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })
	prepareCreateToChampionContent(ctx, t)
	fixture := createTournamentAdminSwissProofFixture(ctx, t)
	return finishGoldenThreeMemberSwiss(ctx, t, fixture)
}

func finishGoldenThreeMemberSwiss(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
) tournamentAdminSwissProofFixture {
	t.Helper()
	require.Len(t, fixture.participants, 4)
	undefeated := fixture.participants[0]
	beats := map[uuid.UUID]uuid.UUID{
		fixture.participants[1]: fixture.participants[2],
		fixture.participants[2]: fixture.participants[3],
		fixture.participants[3]: fixture.participants[1],
	}
	wins := make(map[uuid.UUID]int)
	for round := 1; round <= 3; round++ {
		if round > 1 {
			fixture = nextSwissReceiptWave(ctx, t, fixture, round, false, false)
		}
		_, changed, err := fixture.start.Start(ctx, fixture.startCommand(ctx, t))
		require.NoError(t, err)
		require.True(t, changed)
		for index, binding := range fixture.binding {
			winner := binding.FirstParticipantID
			if binding.SecondParticipantID == undefeated ||
				(binding.FirstParticipantID != undefeated && beats[binding.SecondParticipantID] == binding.FirstParticipantID) {
				winner = binding.SecondParticipantID
			}
			wins[winner]++
			settleCorrectionSwissReceiptSeries(ctx, t, fixture, index, winner)
		}
		closeSwissReceiptWave(ctx, t, fixture)
	}
	require.Equal(t, 3, wins[undefeated])
	for _, participantID := range fixture.participants[1:] {
		require.Equal(t, 1, wins[participantID])
	}
	return fixture
}
