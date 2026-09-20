package reconnect

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gamereconnect "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

func TestTournamentReconnectReceiptRoundTripPreservesDomainRecord(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 18, 30, 0, 0, time.UTC)
	scope := tournamentReconnectTestScope()
	participantID := uuid.New()
	intervalID := uuid.New()
	record := gamereconnect.ReconnectRecord{
		Kind: gamereconnect.MutationDisconnect,
		DisconnectCommand: &gamereconnect.DisconnectCommand{
			Scope: scope, CommandID: uuid.New(), ParticipantID: participantID,
			IntervalID: intervalID, Deadline: now.Add(time.Minute),
		},
		ExpectedAuthorityRevision: 4,
		ReconnectAuthority: gamereconnect.ReconnectAuthority{
			Scope: scope, Revision: 5,
			Reconnect: []pausedomain.PauseReconnectInterval{{ID: intervalID}},
		},
		RecordedAt: now,
	}

	document, expected, err := encodeTournamentReconnectReceipt(record)
	require.NoError(t, err)
	decoded, actual, err := decodeTournamentReconnectReceipt(document)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.Equal(t, record, *decoded)
	require.Equal(t, intervalID, *actual.IntervalID)
}

func TestTournamentReconnectReceiptAllowsTerminalWithoutIntervalColumn(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 18, 31, 0, 0, time.UTC)
	scope := tournamentReconnectTestScope()
	participantID := uuid.New()
	commandID := uuid.New()
	record := gamereconnect.ReconnectRecord{
		Kind: gamereconnect.MutationDisconnect,
		DisconnectCommand: &gamereconnect.DisconnectCommand{
			Scope: scope, CommandID: commandID, ParticipantID: participantID,
			IntervalID: uuid.New(), Deadline: now.Add(time.Minute),
		},
		ExpectedAuthorityRevision: 1,
		ReconnectAuthority: gamereconnect.ReconnectAuthority{
			Scope: scope, Revision: 2,
			Current: &gamereconnect.TerminalOutcome{},
		},
		ScoreRevision: &seriesdomain.ScoreRevision{},
		Evidence:      &seriesdomain.SettlementEvidence{},
		RecordedAt:    now,
	}

	// A terminal record is allowed to omit the command interval from the
	// queryable receipt column when the command exhausted the final cycle.
	record.ReconnectAuthority.Reconnect = nil
	document, meta, err := encodeTournamentReconnectReceipt(record)
	require.NoError(t, err)
	require.Nil(t, meta.IntervalID)
	decoded, decodedMeta, err := decodeTournamentReconnectReceipt(document)
	require.NoError(t, err)
	require.Equal(t, record, *decoded)
	require.Equal(t, meta, decodedMeta)
}

func TestTournamentReconnectReceiptComparisonIncludesFullRecord(t *testing.T) {
	t.Parallel()

	record := gamereconnect.ReconnectRecord{Kind: gamereconnect.MutationDisconnect}
	require.True(t, reconnectRecordsEqual(record, record))

	changed := record
	changed.Kind = gamereconnect.MutationTimeout
	require.False(t, reconnectRecordsEqual(record, changed))
}

func TestReconnectSettlementInputIncludesStandingsForTerminalSeries(t *testing.T) {
	t.Parallel()
	require.ElementsMatch(t, []domain.ArtifactKind{
		domain.ArtifactKindGameResult,
		domain.ArtifactKindSeriesScore,
	}, reconnectSettlementArtifactKinds(gamereconnect.ReconnectRecord{}))

	now := time.Date(2026, time.September, 14, 10, 0, 0, 0, time.UTC)
	scope := tournamentReconnectTestScope()
	commandID := uuid.New()
	gameID, seriesID := uuid.New(), uuid.New()
	firstParticipantID, secondParticipantID := uuid.New(), uuid.New()
	gameResultID := domain.OfficialResultRevisionID(uuid.New())
	scoreRevisionID := domain.SeriesScoreRevisionID(uuid.New())
	seriesResultID := domain.OfficialResultRevisionID(uuid.New())

	current := gamereconnect.ReconnectAuthority{
		Scope: scope, Revision: 1, GameRevision: 1, SeriesRevision: 1,
		Game:   domain.Game{ID: gameID, State: domain.GameStateActive},
		Series: domain.Series{ID: seriesID, State: domain.SeriesStateActive},
	}
	record := gamereconnect.ReconnectRecord{
		Kind: gamereconnect.MutationTimeout,
		TimeoutCommand: &gamereconnect.TimeoutCommand{
			Scope: scope, CommandID: commandID, ParticipantID: firstParticipantID,
			IntervalID: uuid.New(),
		},
		ExpectedAuthorityRevision: 1,
		ReconnectAuthority: gamereconnect.ReconnectAuthority{
			Scope: scope, Revision: 2,
			Game: domain.Game{ID: gameID, State: domain.GameStateCompleted, WinnerID: &secondParticipantID},
			Series: domain.Series{
				ID: seriesID, State: domain.SeriesStateCompleted,
				FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
				WinnerID: &secondParticipantID,
			},
			Current: &gamereconnect.TerminalOutcome{},
		},
		GameResultRevision: &gamereconnect.GameRevision{ID: gameResultID, GameID: gameID},
		ScoreRevision: &seriesdomain.ScoreRevision{
			ID: scoreRevisionID, SeriesID: seriesID,
			GameResultRevisionIDs: []domain.OfficialResultRevisionID{gameResultID},
		},
		SeriesResultRevision: &gamereconnect.SeriesRevision{
			ID: seriesResultID, SeriesID: seriesID, State: domain.SeriesStateCompleted,
			WinnerID: &secondParticipantID, ScoreRevisionID: scoreRevisionID,
		},
		Evidence: &seriesdomain.SettlementEvidence{
			AuditEventID: uuid.New(), OutboxEventID: uuid.New(), ProjectionRevisionID: uuid.New(),
		},
		RecordedAt: now,
	}

	input, err := reconnectSettlementInput(current, record)
	require.NoError(t, err)
	require.ElementsMatch(t, []domain.ArtifactKind{
		domain.ArtifactKindGameResult,
		domain.ArtifactKindSeriesScore,
		domain.ArtifactKindStandings,
		domain.ArtifactKindSeriesResult,
	}, input.ProjectionArtifactKinds)
}

func TestReconnectSyntheticPauseIDIsStablePerAuthorityRevision(t *testing.T) {
	t.Parallel()

	gameID := uuid.New()
	first := reconnectSyntheticPauseID(gameID, 7)
	second := reconnectSyntheticPauseID(gameID, 7)
	require.Equal(t, first, second)
	require.NotEqual(t, first, reconnectSyntheticPauseID(gameID, 8))
	require.NotEqual(t, first, reconnectSyntheticPauseID(uuid.New(), 7))
}

func TestRehydrateTournamentReconnectActiveStateRestoresResumedClockAndBudget(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 18, 34, 0, 0, time.UTC)
	scope := tournamentReconnectTestScope()
	oldPauseID, nextPauseID, gameID, seriesID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	frozenAt := now.Add(-10 * time.Second)
	remaining := 40*time.Second + 466*time.Microsecond
	originalDeadline := frozenAt.Add(remaining)
	resumedDeadline := now.Add(remaining)
	durableResumedDeadline := now.Add(time.Duration(remaining.Milliseconds()) * time.Millisecond)
	source := gamereconnect.ReconnectAuthority{
		Scope: scope, PauseID: oldPauseID,
		Game:   domain.Game{ID: gameID, State: domain.GameStateActive},
		Series: domain.Series{ID: seriesID, FirstParticipantID: firstID, SecondParticipantID: secondID},
		GameClock: pausedomain.PauseResumeGameClock{
			PauseID: oldPauseID, GameID: gameID, OriginalDeadline: originalDeadline,
			FrozenAt: frozenAt, Remaining: remaining, ResumedAt: &now,
			ResumedDeadline: &resumedDeadline, Revision: 3,
		},
		Counters: []pausedomain.PauseReconnectCounter{
			{PauseID: oldPauseID, RosterID: scope.RosterID, ParticipantID: firstID, Limit: domain.ReconnectCycleLimit, Used: 1, Revision: 2},
			{PauseID: oldPauseID, RosterID: scope.RosterID, ParticipantID: secondID, Limit: domain.ReconnectCycleLimit, Used: 0, Revision: 1},
		},
	}
	row := sqlc.PauseClock{
		PauseID: oldPauseID, GameAttemptID: gameID,
		OriginalDeadline: tstz(originalDeadline), FrozenAt: tstz(frozenAt), FrozenRemainingMs: remaining.Milliseconds(),
		ResumedAt: tstz(now), ResumedDeadline: tstz(durableResumedDeadline), Revision: 3,
	}

	clock, counters, err := rehydrateTournamentReconnectActiveState(source, row, nextPauseID)
	require.NoError(t, err)
	require.Equal(t, nextPauseID, clock.PauseID)
	require.Equal(t, gameID, clock.GameID)
	require.Equal(t, originalDeadline, clock.OriginalDeadline)
	require.Equal(t, frozenAt, clock.FrozenAt)
	require.Equal(t, remaining, clock.Remaining)
	require.NotNil(t, clock.ResumedAt)
	require.Equal(t, now, *clock.ResumedAt)
	require.NotNil(t, clock.ResumedDeadline)
	require.Equal(t, resumedDeadline, *clock.ResumedDeadline)
	require.Equal(t, int64(3), clock.Revision)
	require.Len(t, counters, 2)
	for _, counter := range counters {
		require.Equal(t, nextPauseID, counter.PauseID)
		require.Equal(t, scope.RosterID, counter.RosterID)
	}
	firstCounter, firstOK := reconnectCounterByParticipant(counters, firstID)
	require.True(t, firstOK)
	require.Equal(t, 1, firstCounter.Used)
	require.Equal(t, int64(2), firstCounter.Revision)
	secondCounter, secondOK := reconnectCounterByParticipant(counters, secondID)
	require.True(t, secondOK)
	require.Equal(t, 0, secondCounter.Used)
	require.Equal(t, int64(1), secondCounter.Revision)
}

func TestRehydrateTournamentReconnectActiveStateRejectsClockMismatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 18, 35, 0, 0, time.UTC)
	scope := tournamentReconnectTestScope()
	oldPauseID, nextPauseID, gameID, seriesID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	frozenAt := now.Add(-10 * time.Second)
	remaining := 40 * time.Second
	originalDeadline := frozenAt.Add(remaining)
	resumedDeadline := now.Add(remaining)
	source := gamereconnect.ReconnectAuthority{
		Scope: scope, PauseID: oldPauseID,
		Game:   domain.Game{ID: gameID, State: domain.GameStateActive},
		Series: domain.Series{ID: seriesID, FirstParticipantID: firstID, SecondParticipantID: secondID},
		GameClock: pausedomain.PauseResumeGameClock{
			PauseID: oldPauseID, GameID: gameID, OriginalDeadline: originalDeadline,
			FrozenAt: frozenAt, Remaining: remaining, ResumedAt: &now,
			ResumedDeadline: &resumedDeadline, Revision: 3,
		},
		Counters: []pausedomain.PauseReconnectCounter{
			{PauseID: oldPauseID, RosterID: scope.RosterID, ParticipantID: firstID, Limit: domain.ReconnectCycleLimit, Revision: 1},
			{PauseID: oldPauseID, RosterID: scope.RosterID, ParticipantID: secondID, Limit: domain.ReconnectCycleLimit, Revision: 1},
		},
	}
	row := sqlc.PauseClock{
		PauseID: oldPauseID, GameAttemptID: gameID,
		OriginalDeadline: tstz(originalDeadline), FrozenAt: tstz(frozenAt), FrozenRemainingMs: remaining.Milliseconds(),
		ResumedAt: tstz(now), ResumedDeadline: tstz(resumedDeadline.Add(time.Second)), Revision: 3,
	}

	_, _, err := rehydrateTournamentReconnectActiveState(source, row, nextPauseID)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestReconnectFrozenDurationRejectsPrecisionMismatch(t *testing.T) {
	t.Parallel()

	frozenAt := time.Date(2026, time.September, 13, 18, 36, 0, 123456000, time.UTC)
	originalDeadline := frozenAt.Add(40*time.Second + time.Microsecond*466)

	remaining, err := reconnectFrozenDuration(originalDeadline, frozenAt, 40_000)
	require.NoError(t, err)
	require.Equal(t, 40*time.Second+466*time.Microsecond, remaining)

	_, err = reconnectFrozenDuration(frozenAt.Add(40*time.Second+time.Millisecond), frozenAt, 40_000)
	require.ErrorIs(t, err, pausedomain.ErrInvalidGameClock)

	_, err = reconnectFrozenDuration(frozenAt.Add(40*time.Second), frozenAt, 40_001)
	require.ErrorIs(t, err, pausedomain.ErrInvalidGameClock)

	durableDeadline, err := reconnectCanonicalResumedDeadline(
		frozenAt, frozenAt.Add(40*time.Second+466*time.Microsecond), 40_000,
	)
	require.NoError(t, err)
	require.Equal(t, frozenAt.Add(40*time.Second), durableDeadline)
}

func TestRecoveryGameClockRestoresSubMillisecondFrozenDuration(t *testing.T) {
	t.Parallel()

	pauseID, gameID := uuid.New(), uuid.New()
	frozenAt := time.Date(2026, time.September, 13, 18, 37, 0, 123456000, time.UTC)
	remaining := 40*time.Second + 466*time.Microsecond
	row := sqlc.PauseClock{
		PauseID: pauseID, GameAttemptID: gameID,
		OriginalDeadline: tstz(frozenAt.Add(remaining)), FrozenAt: tstz(frozenAt),
		FrozenRemainingMs: remaining.Milliseconds(), Revision: 3,
	}

	clock, err := recoveryGameClock(row)
	require.NoError(t, err)
	require.Equal(t, pauseID, clock.PauseID)
	require.Equal(t, gameID, clock.GameID)
	require.Equal(t, remaining, clock.Remaining)
}

func TestReconnectCounterPredecessorTimeLeavesPostgreSQLTimestampCASGap(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.September, 13, 18, 32, 0, 123456789, time.UTC)
	predecessor, ok := reconnectCounterPredecessorTime(at)
	require.True(t, ok)
	require.True(t, predecessor.Before(at))
	// PostgreSQL timestamptz has microsecond precision.  The predecessor must
	// therefore differ after the driver truncates both values to that scale.
	require.Equal(t, at.UnixMicro()-1, predecessor.UnixMicro())

	_, ok = reconnectCounterPredecessorTime(time.Time{})
	require.False(t, ok)
}

func TestReconnectPausePresenceSnapshotsAllowUnchangedParticipantBeforeCapture(t *testing.T) {
	t.Parallel()

	pauseID, rosterID, seriesID := uuid.New(), uuid.New(), uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	capturedAt := time.Date(2026, time.September, 13, 18, 33, 0, 0, time.UTC)
	first := pausedomain.PausePresence{
		ParticipantID: firstID, State: pausedomain.PresenceStateConnected,
		PresenceEpoch: 1, Revision: 1, UpdatedAt: capturedAt.Add(-time.Second),
	}
	second := pausedomain.PausePresence{
		ParticipantID: secondID, State: pausedomain.PresenceStateDisconnected,
		PresenceEpoch: 2, Revision: 2, UpdatedAt: capturedAt,
	}
	rows := []sqlc.PausePresenceSnapshot{
		{
			PauseID: pauseID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: firstID,
			PresenceState: string(pausedomain.PresenceStateConnected), PresenceEpoch: 1, PresenceRevision: 1,
			CapturedAt: tstz(capturedAt), CreatedAt: tstz(capturedAt),
		},
		{
			PauseID: pauseID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: secondID,
			PresenceState: string(pausedomain.PresenceStateConnected), PresenceEpoch: 1, PresenceRevision: 1,
			CapturedAt: tstz(capturedAt), CreatedAt: tstz(capturedAt),
		},
	}

	firstState, secondState, err := reconnectPausePresenceSnapshotStates(
		rows, pauseID, rosterID, seriesID, first, second,
	)
	require.NoError(t, err)
	require.Equal(t, string(pausedomain.PresenceStateConnected), firstState)
	require.Equal(t, string(pausedomain.PresenceStateConnected), secondState)
}

func TestReconnectResumeWritesIntervalBeforePresence(t *testing.T) {
	t.Parallel()

	current := gamereconnect.ReconnectAuthority{}
	next := gamereconnect.ReconnectAuthority{}
	current.Game.State = domain.GameStatePaused
	next.Game.State = domain.GameStateActive
	require.True(t, reconnectResumeRequiresIntervalFirst(current, next))

	current.Game.State = domain.GameStateActive
	next.Game.State = domain.GameStatePaused
	require.False(t, reconnectResumeRequiresIntervalFirst(current, next))

	current.Game.State = domain.GameStatePaused
	next.Game.State = domain.GameStateCompleted
	require.False(t, reconnectResumeRequiresIntervalFirst(current, next))
}

func TestSelectTournamentReconnectGameScopesExecutableGameToParticipant(t *testing.T) {
	t.Parallel()

	participantID := uuid.New()
	otherParticipantID := uuid.New()
	wanted := sqlc.LockWaveStartGamesRow{
		SeriesID: uuid.New(), FirstParticipantID: participantID, SecondParticipantID: uuid.New(),
		SeriesState: string(domain.SeriesStateActive), GameID: uuid.New(), GameState: string(domain.GameStateActive),
	}
	other := sqlc.LockWaveStartGamesRow{
		SeriesID: uuid.New(), FirstParticipantID: otherParticipantID, SecondParticipantID: uuid.New(),
		SeriesState: string(domain.SeriesStateActive), GameID: uuid.New(), GameState: string(domain.GameStateActive),
	}

	selected, err := selectTournamentReconnectGame([]sqlc.LockWaveStartGamesRow{other, wanted}, nil, participantID)
	require.NoError(t, err)
	require.Equal(t, wanted, selected)
}

func TestSelectTournamentReconnectGameRejectsMultipleGamesForParticipant(t *testing.T) {
	t.Parallel()

	participantID := uuid.New()
	rows := []sqlc.LockWaveStartGamesRow{
		{SeriesID: uuid.New(), FirstParticipantID: participantID, SecondParticipantID: uuid.New(),
			SeriesState: string(domain.SeriesStateActive), GameID: uuid.New(), GameState: string(domain.GameStateActive)},
		{SeriesID: uuid.New(), FirstParticipantID: uuid.New(), SecondParticipantID: participantID,
			SeriesState: string(domain.SeriesStateActive), GameID: uuid.New(), GameState: string(domain.GameStatePaused)},
	}

	_, err := selectTournamentReconnectGame(rows, nil, participantID)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func tournamentReconnectTestScope() pausedomain.GraphScope {
	tournamentID := uuid.New()
	return pausedomain.GraphScope{
		TournamentID: tournamentID,
		RosterID:     uuid.New(),
		WaveID:       uuid.New(),
		Authority: authority.Identity{
			TournamentID: tournamentID,
			HolderID:     uuid.New(), LeaseID: uuid.New(), Epoch: 1,
			ProcessKind: authority.ProcessAuthority,
		},
	}
}
