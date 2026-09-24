package model

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func TestValidPauseGameSourcePauseRequiresDisconnectReason(t *testing.T) {
	t.Parallel()

	graph, series, game := validSourcePauseFixture()
	if !validPauseGameSourcePause(graph, series, game) {
		t.Fatal("valid disconnect source pause was rejected")
	}

	for _, reason := range []PauseReason{PauseReasonOperator, PauseReasonPlatform, PauseReasonExecutionEpoch} {
		t.Run(string(reason), func(t *testing.T) {
			candidateGraph, candidateSeries, candidateGame := validSourcePauseFixture()
			candidateGame.SourcePause.Reason = reason
			if validPauseGameSourcePause(candidateGraph, candidateSeries, candidateGame) {
				t.Fatalf("source pause reason %q was accepted", reason)
			}
		})
	}
}

func TestTimeCoversSourcePauseHistory(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	source := &PauseGameSourcePause{
		StartedAt: startedAt,
		Clock:     PauseFrozenDeadline{FrozenAt: startedAt},
	}
	games := []PauseGame{{SourcePause: source}}

	if timeCoversSourcePauseHistory(startedAt.Add(-time.Nanosecond), games) {
		t.Fatal("operator pause before source pause history was accepted")
	}
	if !timeCoversSourcePauseHistory(startedAt, games) {
		t.Fatal("operator pause at source pause time was rejected")
	}
}

func validSourcePauseFixture() (PauseGraph, PauseSeries, PauseGame) {
	startedAt := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	seriesID, gameID, rosterID := uuid.New(), uuid.New(), uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	pauseID := uuid.New()
	deadline := startedAt.Add(2 * time.Minute)
	series := PauseSeries{Execution: seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, FirstParticipantID: firstID, SecondParticipantID: secondID,
	}}}
	game := PauseGame{
		SeriesID: seriesID,
		Game:     domain.Game{ID: gameID, State: domain.GameStatePaused},
		SourcePause: &PauseGameSourcePause{
			PauseID: pauseID, ScopeKind: "game_attempt", ScopeID: gameID, SeriesID: seriesID, GameID: gameID,
			Reason: PauseReasonDisconnect, State: PauseStateActive, CurrentRevisionID: uuid.New(), Revision: 1,
			StartedAt: startedAt, DecisionNumber: 0,
			Clock: PauseFrozenDeadline{
				Kind: PauseDeadlineGame, OwnerID: gameID, OriginalDeadline: deadline, FrozenAt: startedAt,
				Remaining: deadline.Sub(startedAt), Revision: 1,
			},
			Presence: []PausePresenceSnapshot{
				{ParticipantID: firstID, State: pausedomain.PresenceStateDisconnected, PresenceEpoch: 2, Revision: 3, CapturedAt: startedAt},
				{ParticipantID: secondID, State: pausedomain.PresenceStateConnected, PresenceEpoch: 3, Revision: 4, CapturedAt: startedAt},
			},
		},
	}
	graph := PauseGraph{
		Scope:         pausedomain.GraphScope{RosterID: rosterID},
		ActivePauseID: uuid.New(),
		Counters: []pausedomain.PauseReconnectCounter{
			{PauseID: pauseID, RosterID: rosterID, ParticipantID: firstID, Limit: 3, Used: 2, Revision: 2},
			{PauseID: pauseID, RosterID: rosterID, ParticipantID: secondID, Limit: 3, Used: 0, Revision: 1},
		},
	}
	return graph, series, game
}
