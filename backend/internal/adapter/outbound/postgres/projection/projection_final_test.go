package projection

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestFinalProjectionRevisionConflictUsesLockedRevisions(t *testing.T) {
	t.Parallel()

	publication := projection.FinalPublication{
		Scope: projection.FinalScope{TournamentID: uuid.New()},
		Expected: projection.FinalHeadExpectation{
			TournamentRevision: 8,
			ProjectionRevision: 3,
		},
	}
	aggregate := sqlc.LockFinalProjectionAggregateRow{
		TournamentState:    string(domain.TournamentStatePlayoffs),
		TournamentRevision: 9,
	}

	err := finalProjectionRevisionConflict(publication, aggregate, 4, false)
	var conflict *projection.FinalRevisionConflictError
	require.ErrorAs(t, err, &conflict)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.Equal(t, int64(9), conflict.CurrentTournamentRevision)
	require.Equal(t, int64(4), conflict.CurrentProjectionRevision)

	aggregate.TournamentRevision = publication.Expected.TournamentRevision
	require.NoError(t, finalProjectionRevisionConflict(publication, aggregate, 3, false))

	aggregate.TournamentState = string(domain.TournamentStateCompleted)
	aggregate.TournamentRevision++
	require.NoError(t, finalProjectionRevisionConflict(publication, aggregate, 4, true))
}

func TestFinalChampionOutboxReadbackAcceptsJSONBFormatting(t *testing.T) {
	publication := projection.FinalPublication{IDs: projection.PublicationIDs{RevisionID: uuid.New()}}
	eventID := uuid.New()
	event := sqlc.CreateFinalChampionOutboxEventRow{ID: eventID, ProjectionRevisionID: publication.IDs.RevisionID,
		ProjectionRevision: 9007199254740993, ProjectionOrdinal: 1, Terminal: true, Audience: "all", Topic: "tournament.champion.published",
		Payload: []byte(`{"schema": "test", "revision": 9007199254740993}`)}
	payload := []byte(`{"revision":9007199254740993,"schema":"test"}`)
	require.True(t, finalChampionOutboxMatches(event, publication, event.ProjectionRevision, eventID, payload))
	for _, invalid := range []string{`{"revision":9007199254740992,"schema":"test"}`, `{"revision":9007199254740993,"schema":"test","extra":true}`, `[]`, `{`} {
		event.Payload = []byte(invalid)
		require.False(t, finalChampionOutboxMatches(event, publication, event.ProjectionRevision, eventID, payload))
	}
}

func TestFinalProjectionArtifactKindsRequireTerminalResultSet(t *testing.T) {
	t.Parallel()

	require.True(t, validFinalProjectionArtifactKinds([]byte(
		`["game_result","series_score","standings","series_result"]`,
	)))
	for _, invalid := range [][]byte{
		[]byte(`["standings","bracket","top_four","champion"]`),
		[]byte(`["game_result","series_score","standings"]`),
		[]byte(`["game_result","series_score","standings","standings"]`),
		[]byte(`not-json`),
	} {
		require.False(t, validFinalProjectionArtifactKinds(invalid))
	}
}

func TestFinalProjectionSourceConflictReturnsLockedAuthority(t *testing.T) {
	t.Parallel()
	publication := projection.FinalPublication{
		Scope:    projection.FinalScope{TournamentID: uuid.New(), GameAttemptID: uuid.New()},
		Expected: projection.FinalHeadExpectation{TournamentRevision: 8, ProjectionRevision: 3},
	}
	aggregate := sqlc.LockFinalProjectionAggregateRow{TournamentRevision: 8, TournamentState: "playoffs"}
	err := validateFinalProjectionAuthority(publication, aggregate, nil,
		sqlc.LockFinalProjectionSeriesResultHeadRow{}, sqlc.LockFinalProjectionScoreHeadRow{}, nil,
		sqlc.LockFinalProjectionResultCommitsRow{})
	var conflict *projection.FinalRevisionConflictError
	require.ErrorAs(t, err, &conflict)
	require.EqualValues(t, 8, conflict.CurrentTournamentRevision)
}

func TestValidateFinalProjectionAttemptsRequiresCompleteCurrentHeads(t *testing.T) {
	t.Parallel()

	firstParticipantID := uuid.New()
	secondParticipantID := uuid.New()
	finalAttemptID := uuid.New()
	finalResultRevisionID := uuid.New()
	publication := projection.FinalPublication{
		Scope: projection.FinalScope{GameAttemptID: finalAttemptID},
		Expected: projection.FinalHeadExpectation{
			GameAttemptRevision:  3,
			GameResultRevisionID: domain.OfficialResultRevisionID(finalResultRevisionID),
			WinnerID:             firstParticipantID,
		},
	}
	rows := []sqlc.LockFinalProjectionAttemptsRow{
		finalProjectionAttemptRow(1, 1, uuid.New(), uuid.New(), firstParticipantID, 2),
		finalProjectionTerminalAttemptRow(2, 1, uuid.New(), uuid.New(), domain.GameStateVoid, nil, 2),
		finalProjectionAttemptRow(2, 2, finalAttemptID, finalResultRevisionID, firstParticipantID, 3),
	}

	firstWins, secondWins, err := validateFinalProjectionAttempts(
		publication,
		rows,
		firstParticipantID,
		secondParticipantID,
	)
	require.NoError(t, err)
	require.Equal(t, int16(2), firstWins)
	require.Equal(t, int16(0), secondWins)

	commits := finalProjectionCommitRows(rows)
	publication.Expected.ScoreRevisionID = domain.SeriesScoreRevisionID(
		commits[len(commits)-1].SeriesScoreRevisionID,
	)
	publication.Expected.SeriesResultRevisionID = domain.OfficialResultRevisionID(
		commits[len(commits)-1].SeriesResultRevisionID.UUID,
	)
	for index := range commits {
		commits[index].ProjectionRevisionID = uuid.New()
		commits[index].ProjectionOrdinal = int16(index + 1)
	}
	require.True(t, validFinalProjectionCommitAuthority(commits, rows))
	terminal, found := finalProjectionTerminalCommit(publication, commits)
	require.True(t, found)
	require.Equal(t, finalAttemptID, terminal.AttemptID)

	t.Run("rejects a missing immutable target", func(t *testing.T) {
		t.Parallel()

		invalid := append([]sqlc.LockFinalProjectionResultCommitsRow(nil), commits...)
		invalid[0].ProjectionRevisionID = uuid.Nil
		require.False(t, validFinalProjectionCommitAuthority(invalid, rows))
	})

	t.Run("rejects an incomplete immutable target", func(t *testing.T) {
		t.Parallel()

		invalid := append([]sqlc.LockFinalProjectionResultCommitsRow(nil), commits...)
		invalid[0].ProjectionOrdinal = 0
		require.False(t, validFinalProjectionCommitAuthority(invalid, rows))

		incomplete := commits[1:]
		require.False(t, validFinalProjectionCommitAuthority(incomplete, rows))
	})

	t.Run("rejects stale final result head", func(t *testing.T) {
		t.Parallel()

		stale := append([]sqlc.LockFinalProjectionAttemptsRow(nil), rows...)
		stale[len(stale)-1].HeadRevisionID = uuid.New()
		_, _, err := validateFinalProjectionAttempts(
			publication,
			stale,
			firstParticipantID,
			secondParticipantID,
		)
		require.ErrorIs(t, err, domain.ErrConflict)
	})

	t.Run("rejects an attempt after a completed slot", func(t *testing.T) {
		t.Parallel()

		invalid := append([]sqlc.LockFinalProjectionAttemptsRow(nil), rows...)
		extra := finalProjectionTerminalAttemptRow(
			1,
			2,
			uuid.New(),
			uuid.New(),
			domain.GameStateVoid,
			nil,
			2,
		)
		invalid = append(invalid[:1], append([]sqlc.LockFinalProjectionAttemptsRow{extra}, invalid[1:]...)...)
		_, _, err := validateFinalProjectionAttempts(
			publication,
			invalid,
			firstParticipantID,
			secondParticipantID,
		)
		require.ErrorIs(t, err, domain.ErrConflict)
	})

	t.Run("rejects advancing before a slot produces a winner", func(t *testing.T) {
		t.Parallel()

		invalid := []sqlc.LockFinalProjectionAttemptsRow{
			finalProjectionTerminalAttemptRow(
				1,
				1,
				uuid.New(),
				uuid.New(),
				domain.GameStateVoid,
				nil,
				2,
			),
			finalProjectionAttemptRow(2, 1, uuid.New(), uuid.New(), firstParticipantID, 2),
			finalProjectionAttemptRow(3, 1, finalAttemptID, finalResultRevisionID, firstParticipantID, 3),
		}
		_, _, err := validateFinalProjectionAttempts(
			publication,
			invalid,
			firstParticipantID,
			secondParticipantID,
		)
		require.ErrorIs(t, err, domain.ErrConflict)
	})
}

func finalProjectionAttemptRow(
	slot int16,
	attemptNumber int32,
	attemptID uuid.UUID,
	revisionID uuid.UUID,
	winnerID uuid.UUID,
	attemptRevision int64,
) sqlc.LockFinalProjectionAttemptsRow {
	return finalProjectionTerminalAttemptRow(
		slot,
		attemptNumber,
		attemptID,
		revisionID,
		domain.GameStateCompleted,
		&winnerID,
		attemptRevision,
	)
}

func finalProjectionTerminalAttemptRow(
	slot int16,
	attemptNumber int32,
	attemptID uuid.UUID,
	revisionID uuid.UUID,
	state domain.GameState,
	winnerID *uuid.UUID,
	attemptRevision int64,
) sqlc.LockFinalProjectionAttemptsRow {
	reason := domain.GameResultReasonSolved
	if state == domain.GameStateVoid {
		reason = domain.GameResultReasonTaskFailure
	}
	return sqlc.LockFinalProjectionAttemptsRow{
		SlotNumber:              slot,
		AttemptID:               attemptID,
		AttemptNumber:           attemptNumber,
		AttemptState:            string(state),
		AttemptResultReason:     stringPointer(string(reason)),
		AttemptWinnerID:         nullableUUID(winnerID),
		AttemptResultRevisionID: nullableUUIDValue(revisionID),
		AttemptRevision:         attemptRevision,
		HeadRevisionID:          revisionID,
		HeadRevision:            1,
		ResultRevisionNumber:    1,
		ResultEventID:           uuid.New(),
		ResultState:             string(state),
		ResultReason:            string(reason),
		ResultWinnerID:          nullableUUID(winnerID),
	}
}

func stringPointer(value string) *string {
	return &value
}

func finalProjectionCommitRows(
	attempts []sqlc.LockFinalProjectionAttemptsRow,
) []sqlc.LockFinalProjectionResultCommitsRow {
	commits := make([]sqlc.LockFinalProjectionResultCommitsRow, len(attempts))
	for index, attempt := range attempts {
		commits[index] = sqlc.LockFinalProjectionResultCommitsRow{
			CommitID:              uuid.New(),
			ResultEventID:         attempt.ResultEventID,
			AttemptID:             attempt.AttemptID,
			GameResultRevisionID:  attempt.HeadRevisionID,
			SeriesScoreRevisionID: uuid.New(),
			OutboxEventID:         uuid.New(),
			ProjectionEvidenceID:  uuid.New(),
			OutboxSequence:        int64(index + 1),
		}
	}
	last := len(commits) - 1
	commits[last].SeriesResultRevisionID = nullableUUIDValue(uuid.New())
	return commits
}
