package connection

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gamepause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	gamereconnect "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	connection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/connection"
)

func TestParticipantConnectionAuthorityFromRowsResolvesExactScopeAndPlayer(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	participantID := uuid.New()
	playerID := uuid.New()
	waveID := uuid.New()
	authority := authoritydomain.Identity{
		TournamentID: tournamentID,
		HolderID:     uuid.New(),
		LeaseID:      uuid.New(),
		Epoch:        4,
		ProcessKind:  authoritydomain.ProcessAuthority,
	}
	serverAuthority := connection.ParticipantConnectionAuthority{
		TournamentID: tournamentID,
		Authority:    authority,
	}
	root := sqlc.LockParticipantConnectionIdentityRow{
		TournamentID:    tournamentID,
		RosterID:        rosterID,
		ParticipantID:   participantID,
		PlayerID:        playerID,
		TournamentState: string(domain.TournamentStateSwiss),
	}
	wave := sqlc.LockParticipantConnectionWaveRow{
		WaveID:         waveID,
		TournamentID:   tournamentID,
		RosterID:       rosterID,
		ParticipantID:  participantID,
		WaveRevisionID: uuid.New(),
		WaveRevision:   7,
		WaveState:      string(domain.WaveStateActive),
	}

	resolved, err := participantConnectionAuthorityFromRows(serverAuthority, root, wave)
	require.NoError(t, err)
	require.Equal(t, tournamentID, resolved.TournamentID)
	require.Equal(t, rosterID, resolved.RosterID)
	require.Equal(t, participantID, resolved.ParticipantID)
	require.Equal(t, playerID, resolved.PlayerID)
	require.Equal(t, waveID, resolved.Scope.WaveID)
	require.Equal(t, rosterID, resolved.Scope.RosterID)
	require.Equal(t, tournamentID, resolved.Scope.TournamentID)
	require.Equal(t, authority, resolved.Scope.Authority)
	require.NotEqual(t, uuid.Nil, resolved.Scope.WaveID)
}

func TestParticipantConnectionOperatorPauseBindingUsesCurrentAttempt(t *testing.T) {
	t.Parallel()

	state := participantConnectionState{
		Root: sqlc.LockParticipantConnectionIdentityRow{
			TournamentID:  uuid.New(),
			RosterID:      uuid.New(),
			ParticipantID: uuid.New(),
		},
		Wave: sqlc.LockParticipantConnectionWaveRow{
			WaveID: uuid.New(),
		},
	}
	row := sqlc.LockParticipantConnectionOperatorPauseRow{
		PauseID:          uuid.New(),
		TournamentID:     state.Root.TournamentID,
		RosterID:         state.Root.RosterID,
		WaveID:           state.Wave.WaveID,
		PauseRevision:    3,
		AssignmentID:     uuid.New(),
		GameAttemptID:    uuid.New(),
		SeriesID:         uuid.New(),
		PresenceID:       uuid.New(),
		PresenceSeriesID: uuid.Nil,
		ParticipantID:    state.Root.ParticipantID,
		PresenceState:    string(pausedomain.PresenceStateConnected),
		PresenceEpoch:    5,
		PresenceRevision: 9,
	}
	row.PresenceSeriesID = row.SeriesID

	binding, err := participantConnectionOperatorPauseBinding(row, state)
	require.NoError(t, err)
	require.Equal(t, participantConnectionBinding{
		AssignmentID:  row.AssignmentID,
		SeriesID:      row.SeriesID,
		GameAttemptID: row.GameAttemptID,
	}, binding)

	row.GameAttemptID = uuid.New()
	_, err = participantConnectionOperatorPauseBinding(row, state)
	require.NoError(t, err)
	// The new attempt is carried by the row, rather than being inferred from
	// the wave-level pause or from a stale socket lease.
	require.NotEqual(t, binding.GameAttemptID, row.GameAttemptID)
}

func TestParticipantConnectionCloseBindingFenceRejectsStaleOldAttempt(t *testing.T) {
	t.Parallel()

	oldBinding := participantConnectionBinding{AssignmentID: uuid.New(), SeriesID: uuid.New(), GameAttemptID: uuid.New()}
	currentBinding := participantConnectionBinding{AssignmentID: uuid.New(), SeriesID: uuid.New(), GameAttemptID: uuid.New()}
	row := participantConnectionLeaseRow(oldBinding)

	for _, action := range []connection.ActionKind{
		connection.ActionGameDisconnect,
		connection.ActionGameReconnect,
		connection.ActionPausedPresence,
	} {
		matches, err := participantConnectionActionBindingMatchesLease(row, action, currentBinding)
		require.NoError(t, err)
		require.False(t, matches, "stale lease must not match %s", action)
	}

	matches, err := participantConnectionActionBindingMatchesLease(row, connection.ActionGameDisconnect, oldBinding)
	require.NoError(t, err)
	require.True(t, matches)
}

func TestParticipantConnectionCurrentFenceUsesCloseTimeActionBinding(t *testing.T) {
	t.Parallel()

	currentAction := connection.ResolvedAction{
		Kind: connection.ActionGameDisconnect,
		Disconnect: &gamereconnect.DisconnectCommand{
			Scope:         pausedomain.GraphScope{TournamentID: uuid.New(), RosterID: uuid.New(), WaveID: uuid.New()},
			ParticipantID: uuid.New(),
		},
	}
	// The immutable lease binding remains historical evidence.  A still-current
	// realtime subscriber fence authorizes the action resolved from the graph at
	// close time, even when that graph has advanced since the socket opened.
	rebound := participantConnectionActionForSubscriberFence(true, currentAction)
	require.Equal(t, currentAction, rebound)

	// A replaced generation may close its own lease but cannot carry an action
	// into the current game.
	superseded := participantConnectionActionForSubscriberFence(false, currentAction)
	require.Equal(t, connection.ActionNone, superseded.Kind)
	require.Nil(t, superseded.Disconnect)
}

func TestParticipantConnectionActiveGameConnectKeepsBindingForLastClose(t *testing.T) {
	t.Parallel()

	state := participantConnectionState{
		Authority: connection.ParticipantConnectionAuthority{
			Scope: pausedomain.GraphScope{TournamentID: uuid.New(), RosterID: uuid.New(), WaveID: uuid.New()},
		},
		Root: sqlc.LockParticipantConnectionIdentityRow{ParticipantID: uuid.New()},
	}
	row := sqlc.LockParticipantConnectionActiveGameRow{
		AssignmentID:  uuid.New(),
		GameAttemptID: uuid.New(),
		SeriesID:      uuid.New(),
		SeriesState:   string(domain.SeriesStateActive),
		GameState:     string(domain.GameStateActive),
		TaskKind:      "normal",
		PresenceID:    uuid.New(),
		PresenceState: string(pausedomain.PresenceStateConnected),
		PresenceEpoch: 1, PresenceRevision: 2,
	}

	openAction, binding, err := participantConnectionActiveGameAction(row, state, connectionOperationConnect)
	require.NoError(t, err)
	require.Equal(t, connection.ActionNone, openAction.Kind)
	require.NotEqual(t, uuid.Nil, binding.AssignmentID)

	leaseRow := participantConnectionLeaseRow(binding)
	closeAction, closeBinding, err := participantConnectionActiveGameAction(row, state, connectionOperationDisconnect)
	require.NoError(t, err)
	require.Equal(t, connection.ActionGameDisconnect, closeAction.Kind)
	require.Equal(t, binding, closeBinding)
	matches, err := participantConnectionActionBindingMatchesLease(leaseRow, closeAction.Kind, closeBinding)
	require.NoError(t, err)
	require.True(t, matches, "the last tab must carry the same current-game fence")
}

func TestParticipantConnectionRecoveryLeavesActiveGameToExecutionRecovery(t *testing.T) {
	t.Parallel()

	state := participantConnectionState{
		Authority: connection.ParticipantConnectionAuthority{
			Scope: pausedomain.GraphScope{TournamentID: uuid.New(), RosterID: uuid.New(), WaveID: uuid.New()},
		},
		Root: sqlc.LockParticipantConnectionIdentityRow{ParticipantID: uuid.New()},
	}
	row := sqlc.LockParticipantConnectionActiveGameRow{
		AssignmentID: uuid.New(), GameAttemptID: uuid.New(), SeriesID: uuid.New(),
		SeriesState: string(domain.SeriesStateActive), GameState: string(domain.GameStateActive),
		TaskKind: "normal", PresenceID: uuid.New(), PresenceState: string(pausedomain.PresenceStateConnected),
		PresenceEpoch: 1, PresenceRevision: 2,
	}

	action, binding, err := participantConnectionActiveGameAction(row, state, connectionOperationRecovery)
	require.NoError(t, err)
	require.Equal(t, connection.ActionNone, action.Kind)
	require.NotEqual(t, uuid.Nil, binding.GameAttemptID)
}

func TestParticipantConnectionRecoveryMapsAuthorityStampAndRejectsPartialStamp(t *testing.T) {
	t.Parallel()

	row := participantConnectionLeaseRow(participantConnectionBinding{})
	authority := authoritydomain.Identity{
		TournamentID: row.TournamentID,
		HolderID:     uuid.New(), LeaseID: uuid.New(), Epoch: 3,
		ProcessKind: authoritydomain.ProcessAuthority,
	}
	row.AuthorityHolderID = nullableConnectionUUID(authority.HolderID)
	row.AuthorityLeaseID = nullableConnectionUUID(authority.LeaseID)
	row.AuthorityEpoch = authorityEpochPointer(authority.Epoch)

	candidate, err := mapParticipantConnectionRecoveryCandidate(row)
	require.NoError(t, err)
	require.Equal(t, authority, candidate.Authority)
	require.Equal(t, row.Revision, candidate.Revision)

	row.AuthorityLeaseID = uuid.NullUUID{}
	_, err = mapParticipantConnectionRecoveryCandidate(row)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestParticipantConnectionSecondTabRetainsSameCurrentGameBinding(t *testing.T) {
	t.Parallel()

	state := participantConnectionState{
		Authority: connection.ParticipantConnectionAuthority{
			Scope: pausedomain.GraphScope{TournamentID: uuid.New(), RosterID: uuid.New(), WaveID: uuid.New()},
		},
		Root: sqlc.LockParticipantConnectionIdentityRow{ParticipantID: uuid.New()},
	}
	row := sqlc.LockParticipantConnectionActiveGameRow{
		AssignmentID: uuid.New(), GameAttemptID: uuid.New(), SeriesID: uuid.New(),
		SeriesState: string(domain.SeriesStateActive), GameState: string(domain.GameStateActive),
		TaskKind: "normal", PresenceID: uuid.New(), PresenceState: string(pausedomain.PresenceStateConnected),
		PresenceEpoch: 2, PresenceRevision: 3,
	}

	_, firstBinding, err := participantConnectionActiveGameAction(row, state, connectionOperationConnect)
	require.NoError(t, err)
	_, secondBinding, err := participantConnectionActiveGameAction(row, state, connectionOperationConnect)
	require.NoError(t, err)
	require.Equal(t, firstBinding, secondBinding)
	require.True(t, participantConnectionActionBindingMatchesLeaseOrFail(t, participantConnectionLeaseRow(firstBinding), secondBinding))
}

func TestParticipantConnectionOperatorPauseSameStateKeepsBindingForClose(t *testing.T) {
	t.Parallel()

	state := participantConnectionState{
		Authority: connection.ParticipantConnectionAuthority{
			Scope: pausedomain.GraphScope{TournamentID: uuid.New(), RosterID: uuid.New(), WaveID: uuid.New()},
		},
		Root: sqlc.LockParticipantConnectionIdentityRow{
			TournamentID: uuid.New(), RosterID: uuid.New(), ParticipantID: uuid.New(),
		},
		Wave: sqlc.LockParticipantConnectionWaveRow{WaveID: uuid.New()},
	}
	row := sqlc.LockParticipantConnectionOperatorPauseRow{
		PauseID: uuid.New(), TournamentID: state.Root.TournamentID, RosterID: state.Root.RosterID,
		WaveID: state.Wave.WaveID, PauseRevision: 2, AssignmentID: uuid.New(),
		GameAttemptID: uuid.New(), SeriesID: uuid.New(), PresenceID: uuid.New(),
		ParticipantID: state.Root.ParticipantID, PresenceState: string(pausedomain.PresenceStateConnected),
		PresenceEpoch: 1, PresenceRevision: 2,
	}
	row.PresenceSeriesID = row.SeriesID

	action, binding, err := participantConnectionOperatorPauseAction(row, state, connectionOperationConnect)
	require.NoError(t, err)
	require.Equal(t, connection.ActionNone, action.Kind)
	require.NotEqual(t, uuid.Nil, binding.AssignmentID)

	leaseRow := participantConnectionLeaseRow(binding)
	row.PauseDocument = participantConnectionPauseDocument(t, row, 7)
	closeAction, closeBinding, err := participantConnectionOperatorPauseAction(row, state, connectionOperationDisconnect)
	require.NoError(t, err)
	require.Equal(t, connection.ActionPausedPresence, closeAction.Kind)
	require.Equal(t, int64(7), closeAction.PausedPresence.ExpectedGraphRevision)
	matches, err := participantConnectionActionBindingMatchesLease(leaseRow, closeAction.Kind, closeBinding)
	require.NoError(t, err)
	require.True(t, matches)
}

func TestParticipantConnectionOperatorPauseRejectsInvalidReceiptRevision(t *testing.T) {
	t.Parallel()
	state := participantConnectionState{
		Root: sqlc.LockParticipantConnectionIdentityRow{TournamentID: uuid.New(), RosterID: uuid.New(), ParticipantID: uuid.New()},
		Wave: sqlc.LockParticipantConnectionWaveRow{WaveID: uuid.New()},
	}
	row := sqlc.LockParticipantConnectionOperatorPauseRow{
		PauseID: uuid.New(), TournamentID: state.Root.TournamentID, RosterID: state.Root.RosterID, WaveID: state.Wave.WaveID,
		PauseRevision: 1, AssignmentID: uuid.New(), GameAttemptID: uuid.New(), SeriesID: uuid.New(), PresenceID: uuid.New(),
		ParticipantID: state.Root.ParticipantID, PresenceState: "disconnected", PresenceEpoch: 2, PresenceRevision: 3,
	}
	row.PresenceSeriesID = row.SeriesID
	for _, revision := range []any{nil, 0, -1, "2", 1.5, json.Number("9223372036854775808")} {
		row.PauseDocument = participantConnectionPauseDocument(t, row, revision)
		_, _, err := participantConnectionOperatorPauseAction(row, state, connectionOperationConnect)
		require.ErrorIs(t, err, domain.ErrInternal, "invalid revision %v must not become a presence command", revision)
	}
	row.PauseDocument = nil
	_, _, err := participantConnectionOperatorPauseAction(row, state, connectionOperationConnect)
	require.ErrorIs(t, err, domain.ErrInternal, "missing receipt must fail closed")
	for _, field := range []string{"pause", "tournament", "roster", "wave"} {
		wrong := row
		switch field {
		case "pause":
			wrong.PauseID = uuid.New()
		case "tournament":
			wrong.TournamentID = uuid.New()
		case "roster":
			wrong.RosterID = uuid.New()
		case "wave":
			wrong.WaveID = uuid.New()
		}
		row.PauseDocument = participantConnectionPauseDocument(t, wrong, 7)
		_, _, err := participantConnectionOperatorPauseAction(row, state, connectionOperationConnect)
		require.ErrorIs(t, err, domain.ErrInternal, "mismatched %s receipt", field)
	}
}

func participantConnectionPauseDocument(t *testing.T, row sqlc.LockParticipantConnectionOperatorPauseRow, revision any) []byte {
	t.Helper()
	scope := pausedomain.GraphScope{TournamentID: row.TournamentID, RosterID: row.RosterID, WaveID: row.WaveID}
	document, err := json.Marshal(map[string]any{
		"version": 1, "view": map[string]any{},
		"normal_pause": map[string]any{
			"PauseID": row.PauseID, "Scope": scope, "ScopeKind": "wave", "ScopeID": row.WaveID,
			"Reason": gamepause.PauseReasonOperator, "State": gamepause.PauseStateActive, "Revision": row.PauseRevision,
			"Graph": map[string]any{"Scope": scope, "ActivePauseID": row.PauseID, "Revision": revision},
		},
	})
	require.NoError(t, err)
	return document
}

func TestParticipantConnectionLeaseBindingAllowsPreStartAndRejectsPartial(t *testing.T) {
	t.Parallel()

	row := participantConnectionLeaseRow(participantConnectionBinding{})
	binding, err := participantConnectionLeaseBinding(row)
	require.NoError(t, err)
	require.Equal(t, participantConnectionBinding{}, binding)

	row.AssignmentID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
	_, err = participantConnectionLeaseBinding(row)
	require.ErrorIs(t, err, domain.ErrInternal)

	bound := participantConnectionLeaseRow(participantConnectionBinding{
		AssignmentID: uuid.New(), SeriesID: uuid.New(), GameAttemptID: uuid.New(),
	})
	matches, err := participantConnectionActionBindingMatchesLease(bound, connection.ActionClearReadiness, participantConnectionBinding{})
	require.NoError(t, err)
	require.False(t, matches, "a prior game lease must not clear a pre-start readiness row")
}

func TestParticipantConnectionPostgresRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	var repository *ParticipantConnectionPostgres
	_, err := repository.ResolveParticipantConnection(context.Background(), uuid.New(), uuid.New())
	require.ErrorIs(t, err, domain.ErrValidation)

	_, err = NewParticipantConnectionPostgres(nil, nil).ResolveParticipantConnection(context.Background(), uuid.New(), uuid.New())
	require.ErrorIs(t, err, domain.ErrValidation)
}

func participantConnectionLeaseRow(binding participantConnectionBinding) sqlc.ParticipantConnectionLease {
	at := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	return sqlc.ParticipantConnectionLease{
		ID:                   uuid.New(),
		TournamentID:         uuid.New(),
		RosterID:             uuid.New(),
		ParticipantID:        uuid.New(),
		PlayerID:             uuid.New(),
		ConnectionID:         uuid.New(),
		ConnectionGeneration: 2,
		AssignmentID:         nullableConnectionUUID(binding.AssignmentID),
		SeriesID:             nullableConnectionUUID(binding.SeriesID),
		GameAttemptID:        nullableConnectionUUID(binding.GameAttemptID),
		State:                "active",
		Revision:             1,
		ConnectedAt:          pgtype.Timestamptz{Time: at, Valid: true},
		UpdatedAt:            pgtype.Timestamptz{Time: at, Valid: true},
	}
}

func participantConnectionActionBindingMatchesLeaseOrFail(t *testing.T, row sqlc.ParticipantConnectionLease, binding participantConnectionBinding) bool {
	t.Helper()
	matches, err := participantConnectionActionBindingMatchesLease(row, connection.ActionGameDisconnect, binding)
	require.NoError(t, err)
	return matches
}
