package snapshot

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gamepause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

func TestTournamentAdminSnapshotPauseIndexSelectsNormalRoot(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	waveID := uuid.New()
	startedAt := time.Date(2026, time.September, 6, 11, 0, 0, 0, time.UTC)
	header := tournamentAdminSnapshotHeaderState{tournament: usecase.TournamentView{
		ID: tournamentID, RosterID: rosterID, State: domain.TournamentStateTechnicalPause,
	}}
	roster := tournamentadmin.RosterView{ID: rosterID, TournamentID: tournamentID}
	waves := []tournamentadminexecution.WaveView{{Wave: domain.Wave{ID: waveID, TournamentID: tournamentID}}}
	row := tournamentAdminSnapshotPauseTestRow(tournamentID, rosterID, waveID, startedAt)

	index, err := tournamentAdminSnapshotPauses(
		[]sqlc.Pause{row},
		header,
		roster,
		waves,
		tournamentAdminSnapshotSeriesGraph{},
	)
	require.NoError(t, err)
	root, err := index.normalRoot(domain.TournamentStateTechnicalPause)
	require.NoError(t, err)
	require.NotNil(t, root)
	require.Equal(t, row.ID, root.ID)

	_, err = index.normalRoot(domain.TournamentStateSwiss)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentAdminSnapshotFrozenDeadline(t *testing.T) {
	t.Parallel()

	frozenAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	deadline := frozenAt.Add(30 * time.Second)
	value, ok := tournamentAdminSnapshotFrozenDeadline(
		gamepause.PauseDeadlineGame,
		uuid.New(),
		deadline,
		frozenAt,
		2,
	)
	require.True(t, ok)
	require.Equal(t, 30*time.Second, value.Remaining)
	require.Equal(t, deadline, value.OriginalDeadline)

	_, ok = tournamentAdminSnapshotFrozenDeadline(
		gamepause.PauseDeadlineGame,
		uuid.New(),
		frozenAt,
		frozenAt,
		1,
	)
	require.False(t, ok)
}

func TestTournamentAdminSnapshotPauseGameAdoptsIndependentSourcePause(t *testing.T) {
	t.Parallel()

	tournamentID, rosterID, waveID := uuid.New(), uuid.New(), uuid.New()
	seriesID, gameID := uuid.New(), uuid.New()
	sourcePauseID := uuid.New()
	sourceStartedAt := time.Date(2026, time.September, 6, 12, 0, 0, 123_000_000, time.UTC)
	rootStartedAt := sourceStartedAt.Add(3 * time.Second)
	root := tournamentAdminSnapshotPauseTestRow(tournamentID, rosterID, waveID, rootStartedAt)
	source := sqlc.Pause{
		ID: sourcePauseID, TournamentID: tournamentID, RosterID: rosterID,
		ScopeKind: "game_attempt", ScopeID: gameID,
		SeriesID:      uuid.NullUUID{UUID: seriesID, Valid: true},
		GameAttemptID: uuid.NullUUID{UUID: gameID, Valid: true},
		Reason:        string(gamepause.PauseReasonDisconnect), PausedFromState: string(domain.GameStateActive),
		State: string(gamepause.PauseStateActive), CurrentRevisionID: uuid.New(), Revision: 1,
		StartedAt: tournamentAdminSnapshotTestTime(sourceStartedAt),
		CreatedAt: tournamentAdminSnapshotTestTime(sourceStartedAt),
		UpdatedAt: tournamentAdminSnapshotTestTime(sourceStartedAt),
	}
	game := domain.Game{ID: gameID, SlotID: uuid.New(), AttemptNo: 1, State: domain.GameStatePaused}
	seriesGraph := tournamentAdminSnapshotSeriesGraph{
		gameRevisionByID: map[uuid.UUID]int64{gameID: 1},
		gameSeriesByID:   map[uuid.UUID]uuid.UUID{gameID: seriesID},
	}
	pauseIndex := tournamentAdminSnapshotPauseIndex{
		byID: map[uuid.UUID]sqlc.Pause{root.ID: root, source.ID: source},
		activeByScope: map[tournamentAdminSnapshotPauseScope]sqlc.Pause{
			{kind: "game_attempt", id: gameID}: source,
		},
	}

	value, err := tournamentAdminSnapshotPauseGame(
		game,
		seriesID,
		seriesdomain.Execution{Series: domain.Series{ID: seriesID, State: domain.SeriesStateActive}},
		root,
		seriesGraph,
		pauseIndex,
	)
	require.NoError(t, err)
	require.Nil(t, value.ResumeState, "adoption must not invent a Series resume overlay")
	require.Equal(t, game, value.Game)
	require.Equal(t, int64(1), value.Revision)
}

func TestTournamentAdminSnapshotPauseGameRejectsMalformedAdoptedSourcePause(t *testing.T) {
	t.Parallel()

	tournamentID, rosterID, waveID := uuid.New(), uuid.New(), uuid.New()
	seriesID, gameID := uuid.New(), uuid.New()
	sourceStartedAt := time.Date(2026, time.September, 6, 12, 0, 0, 123_000_000, time.UTC)
	root := tournamentAdminSnapshotPauseTestRow(tournamentID, rosterID, waveID, sourceStartedAt.Add(time.Second))
	base := sqlc.Pause{
		ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID,
		ScopeKind: "game_attempt", ScopeID: gameID,
		SeriesID:      uuid.NullUUID{UUID: seriesID, Valid: true},
		GameAttemptID: uuid.NullUUID{UUID: gameID, Valid: true},
		Reason:        string(gamepause.PauseReasonDisconnect), PausedFromState: string(domain.GameStateActive),
		State: string(gamepause.PauseStateActive), CurrentRevisionID: uuid.New(), Revision: 1,
		StartedAt: tournamentAdminSnapshotTestTime(sourceStartedAt),
		CreatedAt: tournamentAdminSnapshotTestTime(sourceStartedAt),
		UpdatedAt: tournamentAdminSnapshotTestTime(sourceStartedAt),
	}
	game := domain.Game{ID: gameID, SlotID: uuid.New(), AttemptNo: 1, State: domain.GameStatePaused}
	seriesGraph := tournamentAdminSnapshotSeriesGraph{
		gameRevisionByID: map[uuid.UUID]int64{gameID: 1},
		gameSeriesByID:   map[uuid.UUID]uuid.UUID{gameID: seriesID},
	}

	tests := []struct {
		name   string
		mutate func(*sqlc.Pause)
	}{
		{name: "wrong reason", mutate: func(row *sqlc.Pause) { row.Reason = string(gamepause.PauseReasonOperator) }},
		{name: "wrong Series", mutate: func(row *sqlc.Pause) { row.SeriesID.UUID = uuid.New() }},
		{name: "reparented source", mutate: func(row *sqlc.Pause) {
			row.ParentPauseID = uuid.NullUUID{UUID: root.ID, Valid: true}
			row.Depth = 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			row := base
			test.mutate(&row)
			pauseIndex := tournamentAdminSnapshotPauseIndex{
				byID: map[uuid.UUID]sqlc.Pause{root.ID: root, row.ID: row},
				activeByScope: map[tournamentAdminSnapshotPauseScope]sqlc.Pause{
					{kind: "game_attempt", id: gameID}: row,
				},
			}
			_, err := tournamentAdminSnapshotPauseGame(
				game,
				seriesID,
				seriesdomain.Execution{Series: domain.Series{ID: seriesID, State: domain.SeriesStateActive}},
				root,
				seriesGraph,
				pauseIndex,
			)
			require.ErrorIs(t, err, domain.ErrInternal)
		})
	}
}

func TestTournamentAdminSnapshotPauseGameKeepsNormalOverlay(t *testing.T) {
	t.Parallel()

	tournamentID, rosterID, waveID := uuid.New(), uuid.New(), uuid.New()
	seriesID, gameID := uuid.New(), uuid.New()
	startedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	root := tournamentAdminSnapshotPauseTestRow(tournamentID, rosterID, waveID, startedAt)
	seriesPause := sqlc.Pause{
		ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID,
		ScopeKind: "series", ScopeID: seriesID,
		SeriesID: uuid.NullUUID{UUID: seriesID, Valid: true},
		Reason:   string(gamepause.PauseReasonOperator), PausedFromState: string(domain.SeriesStateActive),
		State: string(gamepause.PauseStateActive), CurrentRevisionID: uuid.New(), Revision: 1,
		StartedAt: tournamentAdminSnapshotTestTime(startedAt),
		CreatedAt: tournamentAdminSnapshotTestTime(startedAt),
		UpdatedAt: tournamentAdminSnapshotTestTime(startedAt),
	}
	gamePause := sqlc.Pause{
		ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID,
		ScopeKind: "game_attempt", ScopeID: gameID,
		SeriesID:      uuid.NullUUID{UUID: seriesID, Valid: true},
		GameAttemptID: uuid.NullUUID{UUID: gameID, Valid: true},
		ParentPauseID: uuid.NullUUID{UUID: seriesPause.ID, Valid: true}, Depth: 1,
		Reason: string(gamepause.PauseReasonOperator), PausedFromState: string(domain.GameStateActive),
		State: string(gamepause.PauseStateActive), CurrentRevisionID: uuid.New(), Revision: 1,
		StartedAt: tournamentAdminSnapshotTestTime(startedAt),
		CreatedAt: tournamentAdminSnapshotTestTime(startedAt),
		UpdatedAt: tournamentAdminSnapshotTestTime(startedAt),
	}
	game := domain.Game{ID: gameID, SlotID: uuid.New(), AttemptNo: 1, State: domain.GameStatePaused}
	seriesGraph := tournamentAdminSnapshotSeriesGraph{gameRevisionByID: map[uuid.UUID]int64{gameID: 1}}
	index := tournamentAdminSnapshotPauseIndex{
		byID: map[uuid.UUID]sqlc.Pause{
			root.ID: root, seriesPause.ID: seriesPause, gamePause.ID: gamePause,
		},
		activeByScope: map[tournamentAdminSnapshotPauseScope]sqlc.Pause{
			{kind: "series", id: seriesID}:     seriesPause,
			{kind: "game_attempt", id: gameID}: gamePause,
		},
	}
	resumeState := domain.SeriesStateActive

	value, err := tournamentAdminSnapshotPauseGame(
		game,
		seriesID,
		seriesdomain.Execution{
			Series:      domain.Series{ID: seriesID, State: domain.SeriesStateTechnicalPause},
			ResumeState: &resumeState,
		},
		root,
		seriesGraph,
		index,
	)
	require.NoError(t, err)
	require.NotNil(t, value.ResumeState)
	require.Equal(t, domain.GameStateActive, *value.ResumeState)
	require.Nil(t, value.SourcePause)
}

func TestTournamentAdminSnapshotSourcePauseEvidenceRejectsMalformedEvidence(t *testing.T) {
	t.Parallel()

	tournamentID, rosterID, waveID := uuid.New(), uuid.New(), uuid.New()
	seriesID, gameID, pauseID := uuid.New(), uuid.New(), uuid.New()
	firstParticipantID, secondParticipantID := uuid.New(), uuid.New()
	startedAt := time.Date(2026, time.September, 6, 12, 0, 0, 123_456_000, time.UTC)
	remaining := 30*time.Second + 466*time.Microsecond
	root := tournamentAdminSnapshotPauseTestRow(tournamentID, rosterID, waveID, startedAt.Add(time.Second))
	row := sqlc.Pause{
		ID: pauseID, TournamentID: tournamentID, RosterID: rosterID,
		ScopeKind: "game_attempt", ScopeID: gameID,
		SeriesID:      uuid.NullUUID{UUID: seriesID, Valid: true},
		GameAttemptID: uuid.NullUUID{UUID: gameID, Valid: true},
		Reason:        string(gamepause.PauseReasonDisconnect), PausedFromState: string(domain.GameStateActive),
		State: string(gamepause.PauseStateActive), CurrentRevisionID: uuid.New(), Revision: 1,
		StartedAt: tournamentAdminSnapshotTestTime(startedAt),
		CreatedAt: tournamentAdminSnapshotTestTime(startedAt),
		UpdatedAt: tournamentAdminSnapshotTestTime(startedAt),
	}
	game := gamepause.PauseGame{
		SeriesID: seriesID,
		Game:     domain.Game{ID: gameID, SlotID: uuid.New(), AttemptNo: 1, State: domain.GameStatePaused},
		Revision: 1,
	}
	currentGameID := gameID
	series := gamepause.PauseSeries{
		Execution: seriesdomain.Execution{Series: domain.Series{
			ID: seriesID, State: domain.SeriesStateActive,
			FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		}},
		CurrentGameID: &currentGameID,
	}
	source := gamepause.PauseGameSourcePause{
		PauseID: pauseID, ScopeKind: "game_attempt", ScopeID: gameID,
		SeriesID: seriesID, GameID: gameID, Reason: gamepause.PauseReasonDisconnect,
		State: gamepause.PauseStateActive, CurrentRevisionID: row.CurrentRevisionID, Revision: row.Revision,
		StartedAt: startedAt,
		Clock: gamepause.PauseFrozenDeadline{
			Kind: gamepause.PauseDeadlineGame, OwnerID: gameID,
			OriginalDeadline: startedAt.Add(remaining), FrozenAt: startedAt,
			Remaining: remaining, Revision: 1,
		},
		Presence: []gamepause.PausePresenceSnapshot{
			{ParticipantID: firstParticipantID, State: pausedomain.PresenceStateDisconnected, PresenceEpoch: 2, Revision: 3, CapturedAt: startedAt},
			{ParticipantID: secondParticipantID, State: pausedomain.PresenceStateConnected, PresenceEpoch: 1, Revision: 1, CapturedAt: startedAt},
		},
	}
	clock := sqlc.PauseClock{
		PauseID: pauseID, GameAttemptID: gameID,
		OriginalDeadline:  tournamentAdminSnapshotTestTime(startedAt.Add(remaining)),
		FrozenAt:          tournamentAdminSnapshotTestTime(startedAt),
		FrozenRemainingMs: remaining.Milliseconds(),
		Revision:          1,
		CreatedAt:         tournamentAdminSnapshotTestTime(startedAt),
		UpdatedAt:         tournamentAdminSnapshotTestTime(startedAt),
	}

	tests := []struct {
		name         string
		mutateSource func(*gamepause.PauseGameSourcePause)
		mutateClock  func(*sqlc.PauseClock)
	}{
		{name: "unbound source Game", mutateSource: func(value *gamepause.PauseGameSourcePause) { value.GameID = uuid.New() }},
		{name: "source clock moved", mutateSource: func(value *gamepause.PauseGameSourcePause) {
			value.Clock.FrozenAt = value.Clock.FrozenAt.Add(-time.Nanosecond)
		}},
		{name: "missing source presence", mutateSource: func(value *gamepause.PauseGameSourcePause) { value.Presence = value.Presence[:1] }},
		{name: "changed durable clock", mutateClock: func(value *sqlc.PauseClock) { value.FrozenAt.Time = value.FrozenAt.Time.Add(time.Nanosecond) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sourceValue, clockValue := source, clock
			if test.mutateSource != nil {
				test.mutateSource(&sourceValue)
			}
			if test.mutateClock != nil {
				test.mutateClock(&clockValue)
			}
			require.False(t, tournamentAdminSnapshotSourcePauseEvidenceMatches(
				sourceValue, row, root, game, series, []sqlc.PauseClock{clockValue},
			))
		})
	}
}

func TestTournamentAdminSnapshotReconnectRootSuffix(t *testing.T) {
	t.Parallel()

	require.True(t, tournamentAdminSnapshotReconnectRootSuffixValid(
		tournamentAdminSnapshotReconnectRootRange{count: 1, first: 2, last: 2},
		2,
	))
	require.True(t, tournamentAdminSnapshotReconnectRootSuffixValid(
		tournamentAdminSnapshotReconnectRootRange{},
		0,
	))
	require.False(t, tournamentAdminSnapshotReconnectRootSuffixValid(
		tournamentAdminSnapshotReconnectRootRange{count: 1, first: 2, last: 2},
		3,
	))
	require.False(t, tournamentAdminSnapshotReconnectRootSuffixValid(
		tournamentAdminSnapshotReconnectRootRange{count: 2, first: 1, last: 3},
		3,
	))
}

func tournamentAdminSnapshotPauseTestRow(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	waveID uuid.UUID,
	startedAt time.Time,
) sqlc.Pause {
	return sqlc.Pause{
		ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID,
		ScopeKind: "wave", ScopeID: waveID, WaveID: uuid.NullUUID{UUID: waveID, Valid: true},
		Depth: 0, Reason: string(gamepause.PauseReasonOperator),
		PausedFromState: string(domain.WaveStateActive), State: string(gamepause.PauseStateActive),
		CurrentRevisionID: uuid.New(), Revision: 1,
		StartedAt: tournamentAdminSnapshotTestTime(startedAt),
		CreatedAt: tournamentAdminSnapshotTestTime(startedAt),
		UpdatedAt: tournamentAdminSnapshotTestTime(startedAt),
	}
}
