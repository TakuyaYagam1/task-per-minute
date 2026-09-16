package result

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func TestOperatorResultRequestDocumentBindsExactCommand(t *testing.T) {
	t.Parallel()

	command := tournamentadmin.ForfeitCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
			TournamentID: uuid.New(), CommandID: uuid.New(),
		},
		SeriesID: uuid.New(), ForfeitingParticipantID: uuid.New(),
		Confirmed: true, Reason: "confirmed rule violation", ExpectedAuthorityRevision: 3,
		Basis: "rule_violation", RuleID: "game.rule.7", EvidenceIDs: []uuid.UUID{uuid.New()},
		ScoreRevisionID: uuid.New(), SeriesResultRevisionID: uuid.New(),
		AuditEventID: uuid.New(), OutboxEventID: uuid.New(), ProjectionRevisionID: uuid.New(),
	}

	digest := operatorResultDocumentDigest(t, tournamentadmin.OperatorResultActionForfeit, command)
	document, err := operatorResultRequestDocument(
		tournamentadmin.OperatorResultActionForfeit,
		command,
		digest,
	)
	require.NoError(t, err)
	require.NotEmpty(t, document)

	command.Reason = "different evidence"
	_, err = operatorResultRequestDocument(
		tournamentadmin.OperatorResultActionForfeit,
		command,
		digest,
	)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestOperatorReadinessMatchesMembersRejectsForeignScope(t *testing.T) {
	t.Parallel()

	waveID := uuid.New()
	rosterID := uuid.New()
	windowID := uuid.New()
	firstID := uuid.New()
	secondID := uuid.New()
	members := []sqlc.WaveMember{
		{WaveID: waveID, RosterID: rosterID, ParticipantID: firstID},
		{WaveID: waveID, RosterID: rosterID, ParticipantID: secondID},
	}
	readiness := []sqlc.WaveReadiness{
		{ReadyWindowID: uuid.NullUUID{UUID: windowID, Valid: true}, WaveID: waveID, RosterID: rosterID, ParticipantID: firstID},
		{ReadyWindowID: uuid.NullUUID{UUID: windowID, Valid: true}, WaveID: waveID, RosterID: rosterID, ParticipantID: secondID},
	}

	require.True(t, operatorReadinessMatchesMembers(members, readiness, waveID, rosterID, windowID))

	foreign := append([]sqlc.WaveReadiness(nil), readiness...)
	foreign[1].RosterID = uuid.New()
	require.False(t, operatorReadinessMatchesMembers(members, foreign, waveID, rosterID, windowID))

	duplicate := append([]sqlc.WaveMember(nil), members...)
	duplicate[1].ParticipantID = firstID
	require.False(t, operatorReadinessMatchesMembers(duplicate, readiness, waveID, rosterID, windowID))
}

func TestOperatorResultCommandRecordRejectsMalformedEvidence(t *testing.T) {
	t.Parallel()

	executedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	row := sqlc.OperatorResultCommand{
		CommandID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New(),
		ActorID: uuid.New(), Action: string(tournamentadmin.OperatorResultActionNoShow),
		ExpectedAuthorityRevision: 2, RequestDigest: make([]byte, sha256.Size),
		CommitID: uuid.New(), ResultEventID: uuid.New(),
		ExecutedAt: pgtype.Timestamptz{Time: executedAt, Valid: true},
	}
	row.RequestDigest[0] = 1

	record, err := operatorResultCommandRecord(row)
	require.NoError(t, err)
	require.Equal(t, row.ActorID, record.Operator.ActorID)
	require.Equal(t, executedAt, record.ExecutedAt)

	row.RequestDigest = row.RequestDigest[:sha256.Size-1]
	_, err = operatorResultCommandRecord(row)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestOperatorForfeitAnchorRejectsForeignSeries(t *testing.T) {
	t.Parallel()

	seriesID := uuid.New()
	rosterID := uuid.New()
	gameID := uuid.New()
	slotID := uuid.New()
	command := tournamentadmin.ForfeitCommand{ExpectedGame: &tournamentadmin.GameExpectation{
		SlotID: slotID, GameID: gameID, AttemptNo: 1, State: domain.GameStatePlanned,
	}}
	attempt := sqlc.GameAttempt{
		ID: gameID, SlotID: slotID, SeriesID: seriesID, RosterID: rosterID,
		AttemptNumber: 1, State: string(domain.GameStatePlanned),
	}
	snapshot := operatorSeriesSnapshot{
		series:   sqlc.Series{ID: seriesID, RosterID: rosterID},
		attempts: map[uuid.UUID]sqlc.GameAttempt{gameID: attempt},
	}

	anchor, err := operatorForfeitAnchor(command, snapshot)
	require.NoError(t, err)
	require.Equal(t, attempt, anchor)

	attempt.SeriesID = uuid.New()
	snapshot.attempts[gameID] = attempt
	_, err = operatorForfeitAnchor(command, snapshot)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func operatorResultDocumentDigest(
	t *testing.T,
	action tournamentadmin.OperatorResultAction,
	command any,
) [sha256.Size]byte {
	t.Helper()
	document, err := json.Marshal(struct {
		Action  tournamentadmin.OperatorResultAction `json:"action"`
		Command any                                  `json:"command"`
	}{Action: action, Command: command})
	require.NoError(t, err)
	return sha256.Sum256(document)
}
