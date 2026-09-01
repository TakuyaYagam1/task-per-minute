package arena_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestFinalChampion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		winners   []int
		wantScore domain.ArenaSeriesScore
		wantGames int
	}{
		{name: "two zero", winners: []int{1, 1}, wantScore: domain.ArenaSeriesScore{FirstParticipantWins: 2}, wantGames: 2},
		{name: "two one", winners: []int{1, 2, 1}, wantScore: domain.ArenaSeriesScore{FirstParticipantWins: 2, SecondParticipantWins: 1}, wantGames: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			final := task052Final(t)
			var terminal arena.FinalProgressionCommand
			for index, winner := range test.winners {
				terminal = task052FinalProgression(final, winner, index == len(test.winners)-1)
				next, changed, err := arena.ProgressFinal(final, terminal)
				require.NoError(t, err)
				require.True(t, changed)
				if index < len(test.winners)-1 {
					replayed, replayChanged, replayErr := arena.ProgressFinal(next, terminal)
					require.NoError(t, replayErr)
					require.False(t, replayChanged)
					require.Equal(t, next.Execution().Series, replayed.Execution().Series)
				}
				final = next
			}

			series := final.Execution().Series
			require.Equal(t, domain.ArenaSeriesStateCompleted, series.State)
			require.Equal(t, test.wantScore, series.Score)
			require.Len(t, series.Slots, test.wantGames)
			require.Equal(t, domain.ArenaTournamentStateCompleted, final.Tournament().State)
			require.Len(t, final.ChampionRevisions(), 1)
			require.Equal(t, domain.ArenaArtifactKindChampion, final.ChampionRevisions()[0].Revision().Artifact().Kind)
			require.Equal(t, []domain.ArenaRevisionDependency{{
				SourceRevisionID:  task051RevisionID(5202),
				DerivedRevisionID: final.ChampionRevisions()[0].Revision().ID(),
			}}, final.ChampionDependencies())
			require.Equal(t, *series.WinnerID, final.ChampionID())
			require.NoError(t, final.Validate())

			repeated, changed, err := arena.ProgressFinal(final, terminal)
			require.NoError(t, err)
			require.False(t, changed)
			require.Len(t, repeated.ChampionRevisions(), 1)
			require.Equal(t, domain.ArenaTournamentStateCompleted, repeated.Tournament().State)
		})
	}
}

func task052Final(t *testing.T) arena.Final {
	t.Helper()

	bracket := task052SemifinalBracket(t)
	matches := bracket.Semifinals()
	advanced, changed, err := arena.AdvanceSemifinalResults(
		arena.SemifinalAdvancement{},
		bracket,
		[]domain.ArenaSeries{
			task052CompletedSemifinal(matches[0].Series, true, 1),
			task052CompletedSemifinal(matches[1].Series, false, 2),
		},
	)
	require.NoError(t, err)
	require.True(t, changed)

	seriesID := task052ID(300)
	participants := advanced.FinalParticipants()
	createdAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	revision := task029CategoryRevision(t, arena.ArenaStageFinal, true, task052ID(301), createdAt)
	revision.TournamentID = bracket.Projection().Revision().TournamentID()
	revision.SeriesID = seriesID
	draft, err := arena.NewBO3FinalDraft(revision, arena.DraftStartCommand{
		DraftID: task052ID(302), FirstParticipantID: participants[0], SecondParticipantID: participants[1],
		FirstDeadline: createdAt.Add(time.Minute),
	})
	require.NoError(t, err)
	actions := []arena.DraftActionCommand{
		{ExpectedTurn: 1, ActorID: participants[0], Action: domain.ArenaDraftActionBan, Category: domain.CategoryCrypto, OccurredAt: createdAt.Add(10 * time.Second), NextDeadline: createdAt.Add(2 * time.Minute)},
		{ExpectedTurn: 2, ActorID: participants[1], Action: domain.ArenaDraftActionBan, Category: domain.CategoryForensics, OccurredAt: createdAt.Add(70 * time.Second), NextDeadline: createdAt.Add(3 * time.Minute)},
		{ExpectedTurn: 3, ActorID: participants[0], Action: domain.ArenaDraftActionPick, Category: domain.CategoryPwn, OccurredAt: createdAt.Add(130 * time.Second), NextDeadline: createdAt.Add(4 * time.Minute)},
		{ExpectedTurn: 4, ActorID: participants[1], Action: domain.ArenaDraftActionPick, Category: domain.CategoryReverse, OccurredAt: createdAt.Add(190 * time.Second)},
	}
	for _, action := range actions {
		draft, err = arena.ApplyBO3FinalDraftAction(draft, action)
		require.NoError(t, err)
	}

	final, err := arena.NewFinal(arena.FinalCommand{
		SeriesID: seriesID, Advancement: advanced, Draft: draft,
		InitialScoreRevisionID: domain.ArenaSeriesScoreRevisionID(task052ID(303)),
		FirstSlotID:            task052ID(304), FirstGameID: task052ID(305),
	})
	require.NoError(t, err)
	return final
}

func task052FinalProgression(final arena.Final, winner int, terminal bool) arena.FinalProgressionCommand {
	execution := final.Execution()
	series := execution.Series
	slot := series.Slots[len(series.Slots)-1]
	game := slot.Attempts[len(slot.Attempts)-1]
	winnerID := series.FirstParticipantID
	if winner == 2 {
		winnerID = series.SecondParticipantID
	}
	resultRevisionID := domain.ArenaOfficialResultRevisionID(task052ID(400 + slot.Position))
	game.State = domain.ArenaGameStateCompleted
	game.ResultReason = domain.ArenaGameResultReasonSolved
	game.WinnerID = &winnerID
	game.ResultRevisionID = &resultRevisionID
	scoreAfter := series.Score
	if winner == 1 {
		scoreAfter.FirstParticipantWins++
	} else {
		scoreAfter.SecondParticipantWins++
	}
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task052ID(410 + slot.Position))
	command := arena.FinalProgressionCommand{Progression: arena.SeriesScoreProgressionCommand{
		Game: game,
		ScoreRevision: arena.ArenaSettlementScoreRevision{
			ID: scoreRevisionID, SeriesID: series.ID,
			FirstParticipantID: series.FirstParticipantID, SecondParticipantID: series.SecondParticipantID,
			PreviousRevisionID: series.CurrentScoreRevisionID,
			Ordinal:            slot.Position + 1, Format: series.Format,
			ScoreBefore: series.Score, ScoreAfter: scoreAfter,
			GameResultRevisionIDs: []domain.ArenaOfficialResultRevisionID{resultRevisionID},
			RecordedAt:            time.Date(2026, time.August, 31, 13, slot.Position, 0, 0, time.UTC),
		},
	}}
	if terminal {
		seriesResultRevisionID := domain.ArenaOfficialResultRevisionID(task052ID(420 + slot.Position))
		command.Progression.TerminalResultRevisionID = &seriesResultRevisionID
		command.ChampionRevisionID = domain.ArenaDerivedRevisionID(task052ID(430))
		command.RecordedAt = time.Date(2026, time.August, 31, 14, 0, 0, 0, time.UTC)
		return command
	}
	nextPosition := slot.Position + 1
	command.Progression.Next = &arena.NextSeriesGameWave{
		WaveID:         task052ID(500 + nextPosition),
		WaveRevisionID: domain.ArenaWaveRevisionID(task052ID(510 + nextPosition)),
		SlotID:         task052ID(520 + nextPosition), GameID: task052ID(530 + nextPosition),
		Category: final.GameCategories()[nextPosition-1],
	}
	return command
}

func task052ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("52000000-0000-0000-0000-%012d", number))
}
