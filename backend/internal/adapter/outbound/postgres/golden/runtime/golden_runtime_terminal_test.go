package runtime

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestGoldenTerminalRequiresActiveElapsedDeadline(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, time.September, 24, 12, 0, 0, 123000, time.UTC)
	deadline := started.Add(goldenRuntimeDuration)
	for _, test := range []struct {
		name  string
		state string
		now   time.Time
		valid bool
	}{
		{name: "before deadline", state: "active", now: deadline.Add(-time.Microsecond)},
		{name: "at deadline", state: "active", now: deadline, valid: true},
		{name: "after deadline", state: "active", now: deadline.Add(time.Second), valid: true},
		{name: "technical pause", state: "technical_pause", now: deadline},
		{name: "completed", state: "completed", now: deadline},
	} {
		t.Run(test.name, func(t *testing.T) {
			assignment := sqlc.GetGoldenRuntimeAssignmentRow{
				State: test.state, StartedAt: tstz(started), Deadline: tstz(deadline),
			}
			require.Equal(t, test.valid, goldenRuntimeTerminalDeadlineReached(assignment, 7, test.now))
			require.False(t, goldenRuntimeTerminalDeadlineReached(assignment, 0, test.now))
			assignment.Deadline = pgtype.Timestamptz{}
			require.False(t, goldenRuntimeTerminalDeadlineReached(assignment, 7, test.now))
		})
	}
}

func TestGoldenTerminalRequiresRealCurrentSubmission(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	winner := sqlc.ListGoldenRuntimeAttemptMembersRow{
		MembershipID: uuid.New(), ParticipantID: uuid.New(), ReadyAt: tstz(now), ParticipationEstablishedAt: tstz(now),
		CommittedAt:          tstz(now),
		SubmissionID:         uuid.NullUUID{UUID: uuid.New(), Valid: true},
		PositionCommitID:     uuid.NullUUID{UUID: uuid.New(), Valid: true},
		SubmissionRevisionID: uuid.NullUUID{UUID: uuid.New(), Valid: true},
	}
	remaining := sqlc.ListGoldenRuntimeAttemptMembersRow{
		MembershipID: uuid.New(), ParticipantID: uuid.New(), ReadyAt: tstz(now), ParticipationEstablishedAt: tstz(now),
	}
	deadline := now.Add(goldenRuntimeDuration)
	member, err := goldenRuntimeTerminalMember([]sqlc.ListGoldenRuntimeAttemptMembersRow{winner, remaining}, remaining.ParticipantID, now, deadline)
	require.NoError(t, err)
	require.Equal(t, remaining.MembershipID, member.MembershipID)
	for _, mutate := range []func(*sqlc.ListGoldenRuntimeAttemptMembersRow){
		func(row *sqlc.ListGoldenRuntimeAttemptMembersRow) { row.NoShowAt = tstz(now) },
		func(row *sqlc.ListGoldenRuntimeAttemptMembersRow) { row.SubmissionID = uuid.NullUUID{} },
		func(row *sqlc.ListGoldenRuntimeAttemptMembersRow) { row.SubmissionRevisionID = uuid.NullUUID{} },
		func(row *sqlc.ListGoldenRuntimeAttemptMembersRow) { row.CommittedAt = tstz(deadline) },
	} {
		invalid := winner
		mutate(&invalid)
		_, err = goldenRuntimeTerminalMember([]sqlc.ListGoldenRuntimeAttemptMembersRow{invalid, remaining}, remaining.ParticipantID, now, deadline)
		require.ErrorIs(t, err, domain.ErrConflict)
	}
}

func TestGoldenTerminalWatermarkRetainsLastRealSubmission(t *testing.T) {
	t.Parallel()
	realRevision, syntheticRevision := int64(1), int64(2)
	realID := uuid.New()
	evidence := goldenRuntimeTerminalAttemptEvidence{Commits: []sqlc.ListGoldenRuntimeGroupEvidenceRow{
		{SubmissionID: uuid.NullUUID{UUID: uuid.New(), Valid: true}, SubmissionRevisionID: uuid.NullUUID{UUID: realID, Valid: true}, SubmissionRevision: &realRevision},
		{SubmissionID: uuid.NullUUID{UUID: uuid.New(), Valid: true}, SubmissionRevisionID: uuid.NullUUID{UUID: uuid.New(), Valid: true}, SubmissionRevision: &syntheticRevision, NoShowAt: tstz(time.Now().UTC())},
	}}
	require.NoError(t, goldenRuntimeTerminalWatermark(&evidence))
	require.Equal(t, realID, evidence.SubmissionRevisionID)
	require.Equal(t, realRevision, evidence.SubmissionRevision)
	evidence.Commits = evidence.Commits[1:]
	require.ErrorIs(t, goldenRuntimeTerminalWatermark(&evidence), domain.ErrConflict)
}

func TestGoldenTerminalCommitRejectsChangedEvidence(t *testing.T) {
	t.Parallel()
	deadline := time.Date(2026, time.September, 24, 12, 3, 0, 123000, time.UTC)
	payload := goldenRuntimeTerminalPayload{
		Reason: "sole_unresolved", ID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(),
		GroupRevisionID: uuid.New(), AttemptID: uuid.New(), MembershipID: uuid.New(), ParticipantID: uuid.New(),
		RuntimeRevision: 7, Deadline: deadline, Position: 4, RecordedAt: deadline,
	}
	digest := goldenRuntimeDigest(payload)
	row := sqlc.GetGoldenTerminalPositionEvidenceRow{
		ID: payload.ID, TournamentID: payload.TournamentID, RosterID: payload.RosterID,
		GroupRevisionID: payload.GroupRevisionID, AttemptID: payload.AttemptID,
		MembershipID: payload.MembershipID, ParticipantID: payload.ParticipantID,
		RuntimeRevision: payload.RuntimeRevision, Deadline: tstz(deadline), Position: payload.Position,
		PayloadDigest: digest[:], RecordedAt: tstz(deadline), CreatedAt: tstz(deadline),
		PositionCommitID: uuid.NullUUID{UUID: uuid.New(), Valid: true},
	}
	commit, err := goldenRuntimeTerminalCommitFromRow(row)
	require.NoError(t, err)
	require.Equal(t, payload.ID, commit.EvidenceID)
	require.Equal(t, int16(4), commit.Position)
	t.Run("database timestamp location", func(t *testing.T) {
		stored := row
		location := time.FixedZone("database", 3*60*60)
		stored.Deadline.Time = stored.Deadline.Time.In(location)
		stored.RecordedAt.Time = stored.RecordedAt.Time.In(location)
		stored.CreatedAt.Time = stored.CreatedAt.Time.In(location)
		loaded, err := goldenRuntimeTerminalCommitFromRow(stored)
		require.NoError(t, err)
		require.Equal(t, commit, loaded)
	})
	t.Run("pgx binary timestamp", func(t *testing.T) {
		codec := pgtype.NewMap()
		encoded, err := codec.Encode(pgtype.TimestamptzOID, pgtype.BinaryFormatCode, row.Deadline, nil)
		require.NoError(t, err)
		var decoded pgtype.Timestamptz
		require.NoError(t, codec.Scan(pgtype.TimestamptzOID, pgtype.BinaryFormatCode, encoded, &decoded))
		require.True(t, decoded.Time.Equal(deadline))
		t.Logf("pgx timestamptz decoded location=%s canonical_utc=%t", decoded.Time.Location(), decoded.Time.Location() == time.UTC)
		stored := row
		stored.Deadline, stored.RecordedAt, stored.CreatedAt = decoded, decoded, decoded
		loaded, err := goldenRuntimeTerminalCommitFromRow(stored)
		require.NoError(t, err)
		require.Equal(t, commit, loaded)
	})
	t.Run("zero timestamps remain invalid", func(t *testing.T) {
		stored := row
		stored.Deadline = pgtype.Timestamptz{Valid: true}
		require.False(t, goldenRuntimeTerminalRowTimesValid(stored))
		stored = row
		stored.RecordedAt = pgtype.Timestamptz{Valid: true}
		require.False(t, goldenRuntimeTerminalRowTimesValid(stored))
	})
	for _, test := range []struct {
		name   string
		mutate func(*sqlc.GetGoldenTerminalPositionEvidenceRow)
	}{
		{name: "membership", mutate: func(row *sqlc.GetGoldenTerminalPositionEvidenceRow) { row.MembershipID = uuid.New() }},
		{name: "head revision", mutate: func(row *sqlc.GetGoldenTerminalPositionEvidenceRow) { row.RuntimeRevision++ }},
		{name: "deadline", mutate: func(row *sqlc.GetGoldenTerminalPositionEvidenceRow) {
			row.Deadline = tstz(deadline.Add(-time.Microsecond))
		}},
		{name: "missing commit", mutate: func(row *sqlc.GetGoldenTerminalPositionEvidenceRow) { row.PositionCommitID = uuid.NullUUID{} }},
		{name: "short digest", mutate: func(row *sqlc.GetGoldenTerminalPositionEvidenceRow) { row.PayloadDigest = digest[:4] }},
		{name: "early evidence", mutate: func(row *sqlc.GetGoldenTerminalPositionEvidenceRow) {
			row.RecordedAt = tstz(deadline.Add(-time.Microsecond))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := row
			test.mutate(&changed)
			_, err := goldenRuntimeTerminalCommitFromRow(changed)
			require.ErrorIs(t, err, domain.ErrConflict)
		})
	}
}
