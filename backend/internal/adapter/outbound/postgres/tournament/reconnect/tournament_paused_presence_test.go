package reconnect

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func TestTournamentPausedPresenceSQLContractLocksExactPauseAndSelectedPresence(t *testing.T) {
	t.Parallel()

	query := string(readTournamentPausedPresenceQuery(t))
	for _, fragment := range []string{
		"-- name: LockTournamentPausedPresenceRoot :many",
		"pause.scope_kind = 'wave'",
		"pause.scope_id = sqlc.arg(wave_id)",
		"pause.parent_pause_id IS NULL",
		"pause.reason = 'operator'",
		"pause.state = 'active'",
		"ORDER BY pause.started_at DESC, pause.id DESC, command.command_id DESC",
		"FOR UPDATE OF pause, command",
		"-- name: LockTournamentPausedPresenceParticipant :many",
		"assignment.state = 'active'",
		"snapshot.kind = 'normal'",
		"receipt.assignment_id = assignment.id",
		"series.state = 'technical_pause'",
		"attempt.state = 'paused'",
		"FOR UPDATE OF pause, membership, series, attempt, assignment, snapshot, receipt, presence",
		"-- name: UpdateTournamentPausedPresenceCAS :one",
		"UPDATE presence_states AS presence",
		"pause.revision = sqlc.arg(expected_pause_revision)",
		"presence.presence_epoch = sqlc.arg(expected_presence_epoch)",
		"presence.revision = sqlc.arg(expected_presence_revision)",
		"presence.state <> sqlc.arg(next_state)",
		"RETURNING presence.id",
	} {
		require.Contains(t, query, fragment)
	}
	// Ambiguous roots and participant bindings must be surfaced to the adapter;
	// neither query may silently choose an arbitrary row.
	require.NotContains(t, query, "LIMIT")

	updateStart := strings.Index(query, "UPDATE presence_states AS presence")
	require.GreaterOrEqual(t, updateStart, 0)
	require.NotContains(t, query[updateStart:], "UPDATE pauses")
}

func TestTournamentPausedPresenceFindReliesOnOuterLeaseFence(t *testing.T) {
	t.Parallel()

	record, err := NewTournamentPausedPresencePostgres(&db.TxManager{}).FindPausedPresenceCommand(
		context.Background(), uuid.New(), uuid.New(),
	)
	require.NoError(t, err)
	require.Nil(t, record)
}

func TestTournamentPausedPresenceRootValidationRequiresExactOperatorWave(t *testing.T) {
	t.Parallel()

	scope := pausedPresenceTestScope()
	row := pausedPresenceTestRootRow(scope)
	require.NoError(t, validatePausedPresenceRoot(row, scope))

	row.Reason = string(gameusecase.PauseReasonDisconnect)
	require.ErrorIs(t, validatePausedPresenceRoot(row, scope), gameusecase.ErrPausedPresenceSuppression)

	row = pausedPresenceTestRootRow(scope)
	row.ScopeID = uuid.New()
	require.ErrorIs(t, validatePausedPresenceRoot(row, scope), gameusecase.ErrPausedPresenceSuppression)
}

func TestTournamentPausedPresenceSelectedParticipantRejectsAmbiguity(t *testing.T) {
	t.Parallel()

	participantID := uuid.New()
	_, err := pausedPresenceByParticipant([]pausedomain.PausePresence{
		{ParticipantID: participantID},
		{ParticipantID: participantID},
	}, participantID)
	require.ErrorIs(t, err, gameusecase.ErrPausedPresenceConflict)

	_, err = pausedPresenceByParticipant(nil, participantID)
	require.ErrorIs(t, err, gameusecase.ErrPausedPresenceSuppression)
}

func TestTournamentPausedPresenceCommitRejectsGraphAndBindingDrift(t *testing.T) {
	t.Parallel()

	scope := pausedPresenceTestScope()
	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	pauseID := uuid.New()
	participantID := uuid.New()
	presenceID := uuid.New()
	presence := pausedomain.PausePresence{
		ID: presenceID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		SeriesID: uuid.New(), ParticipantID: participantID,
		State: pausedomain.PresenceStateConnected, PresenceEpoch: 2, Revision: 3,
		ConnectedAt: now, UpdatedAt: now,
	}
	pause := gameusecase.NormalPauseRecord{
		Scope: scope, ScopeKind: pausedomain.ScopeWave, ScopeID: scope.WaveID,
		PauseID: pauseID, Reason: gameusecase.PauseReasonOperator,
		State: gameusecase.PauseStateActive, Revision: 2,
		Graph: gameusecase.PauseGraph{
			Scope: scope, Revision: 4, ActivePauseID: pauseID,
			PausedAt: &now, DeadlinesSuppressed: true,
		},
	}
	expected := gameusecase.PausedPresenceExpectation{
		PauseID: pauseID, GraphRevision: 4, PauseRevision: 2,
		Authority: scope.Authority,
		Presence: gameusecase.PausePresenceRevision{
			ID: presence.ID, TournamentID: presence.TournamentID, RosterID: presence.RosterID,
			SeriesID: presence.SeriesID, ParticipantID: presence.ParticipantID,
			PresenceEpoch: presence.PresenceEpoch, Revision: presence.Revision,
		},
	}
	record := gameusecase.PausedPresenceRecord{
		Command: gameusecase.PausedPresenceCommand{
			Scope: scope, PauseID: pauseID, CommandID: uuid.New(), ParticipantID: participantID,
		},
		Authority: gameusecase.PausedPresenceAuthority{
			Pause: pause, Presence: presence,
		},
	}

	require.NoError(t, matchPausedPresenceCommitAuthority(expected, record, record.Authority))
	current := record.Authority
	current.Pause.Graph.Revision++
	require.ErrorIs(t, matchPausedPresenceCommitAuthority(expected, record, current), domain.ErrConflict)

	current = record.Authority
	current.Reconnect = []pausedomain.PauseReconnectInterval{{ID: uuid.New()}}
	require.ErrorIs(t, matchPausedPresenceCommitAuthority(expected, record, current), domain.ErrConflict)
}

func TestTournamentPausedPresenceLoadedGraphRequiresRootRevision(t *testing.T) {
	t.Parallel()

	scope := pausedPresenceTestScope()
	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	pauseID := uuid.New()
	authority := gameusecase.PauseResumeAuthority{
		Pause: gameusecase.NormalPauseRecord{
			Scope: scope, ScopeKind: pausedomain.ScopeWave, ScopeID: scope.WaveID,
			PauseID: pauseID, Reason: gameusecase.PauseReasonOperator,
			State: gameusecase.PauseStateActive, Revision: 3, PausedAt: now,
			Graph: gameusecase.PauseGraph{
				Scope: scope, ActivePauseID: pauseID, PausedAt: &now,
				DeadlinesSuppressed: true,
			},
		},
	}
	require.NoError(t, validateLoadedPausedPresenceGraph(authority, pauseID, 3, scope))
	require.ErrorIs(t, validateLoadedPausedPresenceGraph(authority, pauseID, 2, scope), gameusecase.ErrPausedPresenceSuppression)
}

func TestTournamentPausedPresenceCommitGuardsRevisionOverflow(t *testing.T) {
	t.Parallel()

	expected := gameusecase.PausedPresenceExpectation{
		Presence: gameusecase.PausePresenceRevision{
			PresenceEpoch: math.MaxInt64,
		},
	}
	require.ErrorIs(t, validatePausedPresenceCommit(expected, gameusecase.PausedPresenceRecord{}), gameusecase.ErrPausedPresenceOverflow)
}

func TestTournamentPausedPresenceTimeComparisonPreservesNullableDisconnect(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	left := pausedomain.PausePresence{
		ID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New(),
		ParticipantID: uuid.New(), State: pausedomain.PresenceStateConnected,
		PresenceEpoch: 1, Revision: 1, ConnectedAt: now, UpdatedAt: now,
	}
	right := left
	require.True(t, samePausedPresence(left, right))

	disconnectedAt := now.Add(time.Minute)
	right.DisconnectedAt = &disconnectedAt
	require.False(t, samePausedPresence(left, right))
	left.DisconnectedAt = &disconnectedAt
	require.True(t, samePausedPresence(left, right))
}

func pausedPresenceTestScope() pausedomain.GraphScope {
	tournamentID := uuid.New()
	return pausedomain.GraphScope{
		TournamentID: tournamentID,
		RosterID:     uuid.New(),
		WaveID:       uuid.New(),
		Authority: authoritydomain.Identity{
			TournamentID: tournamentID,
			HolderID:     uuid.New(),
			LeaseID:      uuid.New(),
			Epoch:        1,
			ProcessKind:  authoritydomain.ProcessAuthority,
		},
	}
}

func pausedPresenceTestRootRow(scope pausedomain.GraphScope) sqlc.LockTournamentPausedPresenceRootRow {
	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	return sqlc.LockTournamentPausedPresenceRootRow{
		PauseID:           scope.WaveID,
		TournamentID:      scope.TournamentID,
		RosterID:          scope.RosterID,
		ScopeKind:         string(pausedomain.ScopeWave),
		ScopeID:           scope.WaveID,
		WaveID:            uuid.NullUUID{UUID: scope.WaveID, Valid: true},
		Reason:            string(gameusecase.PauseReasonOperator),
		State:             string(gameusecase.PauseStateActive),
		CurrentRevisionID: uuid.New(),
		PauseRevision:     1,
		StartedAt:         pgtype.Timestamptz{Time: now, Valid: true},
		CommandID:         uuid.New(),
		ResultDocument:    []byte(`{"version":1}`),
	}
}

func readTournamentPausedPresenceQuery(t *testing.T) []byte {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_paused_presence.sql"))
	require.NoError(t, err)
	return contents
}
