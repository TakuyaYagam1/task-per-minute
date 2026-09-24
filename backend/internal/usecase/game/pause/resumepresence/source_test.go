package resumepresence

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

func TestSourceResumeAuthorityTimeBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		delta time.Duration
		valid bool
	}{
		{"before", -time.Microsecond, true},
		{"equal", 0, true},
		{"after", time.Microsecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authority := sourceResumeAuthorityFixture()
			authority.Resume.Pause.PausedAt = authority.GameDecision.StartedAt.Add(-tc.delta)
			err := validateSourceResumeAuthority(authority)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrInvalidPauseResumePresence)
			}
		})
	}
}

func TestSourceResumeAuthorityRejectsMissingOrMismatchedSource(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*PauseResumePresenceAuthority)
	}{
		{"missing", func(a *PauseResumePresenceAuthority) { a.Resume.Pause.Graph.Games[0].SourcePause = nil }},
		{"identity", func(a *PauseResumePresenceAuthority) { a.GameDecision.PauseID = uuid.New() }},
		{"clock", func(a *PauseResumePresenceAuthority) { a.GameDecision.GameClock.Remaining++ }},
		{"reason", func(a *PauseResumePresenceAuthority) {
			a.Resume.Pause.Graph.Games[0].SourcePause.Reason = model.PauseReasonPlatform
		}},
		{"parent", func(a *PauseResumePresenceAuthority) { id := uuid.New(); a.GameDecision.ParentPauseID = &id }},
		{"series", func(a *PauseResumePresenceAuthority) { a.SeriesDecision.PauseID = uuid.New() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authority := sourceResumeAuthorityFixture()
			tc.mutate(&authority)
			require.ErrorIs(t, validateSourceResumeAuthority(authority), ErrInvalidPauseResumePresence)
		})
	}
}

func sourceResumeAuthorityFixture() PauseResumePresenceAuthority {
	startedAt := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	gameID, seriesID, pauseID := uuid.New(), uuid.New(), uuid.New()
	clock := PauseResumeGameClock{
		PauseID: pauseID, GameID: gameID, OriginalDeadline: startedAt.Add(time.Minute + 466*time.Microsecond),
		FrozenAt: startedAt, Remaining: time.Minute + 466*time.Microsecond, Revision: 1,
	}
	source := &model.PauseGameSourcePause{
		PauseID: pauseID, SeriesID: seriesID, GameID: gameID, Reason: model.PauseReasonDisconnect,
		State: PauseStateActive, CurrentRevisionID: uuid.New(), Revision: 1, StartedAt: startedAt,
		Clock: model.PauseFrozenDeadline{OriginalDeadline: clock.OriginalDeadline, FrozenAt: clock.FrozenAt, Remaining: clock.Remaining, Revision: 1},
	}
	return PauseResumePresenceAuthority{
		SourceAdoption: true,
		Resume: PauseResumeAuthority{Pause: model.NormalPauseRecord{
			PausedAt: startedAt.Add(time.Second),
			Graph: PauseGraph{
				Series: []PauseSeries{{Execution: seriesdomain.Execution{Series: domain.Series{ID: seriesID, State: domain.SeriesStateActive}}, CurrentGameID: &gameID}},
				Games:  []PauseGame{{SeriesID: seriesID, Game: domain.Game{ID: gameID, State: domain.GameStatePaused}, SourcePause: source}},
			},
		}},
		GameDecision: PauseResumeDecisionAuthority{
			PauseID: pauseID, ScopeKind: PauseResumeDecisionScopeGameAttempt, CurrentRevisionID: source.CurrentRevisionID,
			State: PauseStateActive, Revision: 1, SeriesID: seriesID, GameID: gameID, StartedAt: startedAt, GameClock: &clock,
		},
	}
}
