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

func TestSeriesScoreProgression(t *testing.T) {
	t.Parallel()

	t.Run("completes BO1 with its only winner", func(t *testing.T) {
		t.Parallel()

		current := task038Series(domain.ArenaSeriesFormatBO1, nil)
		before := task038CloneSeriesExecution(current)
		command := task038ProgressionCommand(current, 1, nil)

		progression, changed, err := arena.ProgressSeriesScore(current, command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, before, current)
		require.Equal(t, domain.ArenaSeriesStateCompleted, progression.Series.Series.State)
		require.Equal(t, domain.ArenaSeriesScore{FirstParticipantWins: 1}, progression.Series.Series.Score)
		require.Equal(t, current.Series.FirstParticipantID, *progression.Series.Series.WinnerID)
		require.Equal(t, command.ScoreRevision.ID, *progression.Series.Series.CurrentScoreRevisionID)
		require.Equal(t, *command.TerminalResultRevisionID, *progression.Series.Series.CurrentResultRevisionID)
		require.Len(t, progression.Series.Series.Slots, 1)
		require.Equal(t, domain.ArenaGameStateCompleted, progression.Series.Series.Slots[0].Attempts[0].State)
		require.Nil(t, progression.NextWave)
		require.NoError(t, progression.Validate())
	})

	t.Run("completes BO3 at two zero without Game three", func(t *testing.T) {
		t.Parallel()

		current := task038Series(domain.ArenaSeriesFormatBO3, []int{1})
		command := task038ProgressionCommand(current, 1, nil)

		progression, changed, err := arena.ProgressSeriesScore(current, command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.ArenaSeriesStateCompleted, progression.Series.Series.State)
		require.Equal(t, domain.ArenaSeriesScore{FirstParticipantWins: 2}, progression.Series.Series.Score)
		require.Equal(t, current.Series.FirstParticipantID, *progression.Series.Series.WinnerID)
		require.Len(t, progression.Series.Series.Slots, 2)
		require.Nil(t, progression.NextWave)
		require.NoError(t, progression.Validate())
	})

	t.Run("completes BO3 at two one", func(t *testing.T) {
		t.Parallel()

		current := task038Series(domain.ArenaSeriesFormatBO3, []int{1, 2})
		command := task038ProgressionCommand(current, 1, nil)

		progression, changed, err := arena.ProgressSeriesScore(current, command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.ArenaSeriesStateCompleted, progression.Series.Series.State)
		require.Equal(t, domain.ArenaSeriesScore{FirstParticipantWins: 2, SecondParticipantWins: 1}, progression.Series.Series.Score)
		require.Len(t, progression.Series.Series.Slots, 3)
		require.Nil(t, progression.NextWave)
		require.NoError(t, progression.Validate())
	})

	t.Run("creates only the required next BO3 slot Wave", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name         string
			priorWinners []int
			winner       int
			wantScore    domain.ArenaSeriesScore
			wantPosition int
			category     domain.Category
		}{
			{
				name: "Game two after one zero", winner: 1,
				wantScore:    domain.ArenaSeriesScore{FirstParticipantWins: 1},
				wantPosition: 2, category: domain.CategoryCrypto,
			},
			{
				name: "Game three after one one", priorWinners: []int{1}, winner: 2,
				wantScore:    domain.ArenaSeriesScore{FirstParticipantWins: 1, SecondParticipantWins: 1},
				wantPosition: 3, category: domain.CategoryReverse,
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				current := task038Series(domain.ArenaSeriesFormatBO3, test.priorWinners)
				next := task038NextGameWave(test.wantPosition, test.category)
				command := task038ProgressionCommand(current, test.winner, &next)

				progression, changed, err := arena.ProgressSeriesScore(current, command)
				require.NoError(t, err)
				require.True(t, changed)
				require.Equal(t, domain.ArenaSeriesStateActive, progression.Series.Series.State)
				require.Equal(t, test.wantScore, progression.Series.Series.Score)
				require.Nil(t, progression.Series.Series.WinnerID)
				require.Nil(t, progression.Series.Series.CurrentResultRevisionID)
				require.Len(t, progression.Series.Series.Slots, test.wantPosition)

				slot := progression.Series.Series.Slots[test.wantPosition-1]
				require.Equal(t, next.SlotID, slot.ID)
				require.Equal(t, test.wantPosition, slot.Position)
				require.Equal(t, test.category, slot.Category)
				require.Equal(t, test.wantScore, slot.ScoreBefore)
				require.Len(t, slot.Attempts, 1)
				require.Equal(t, next.GameID, slot.Attempts[0].ID)
				require.Equal(t, domain.ArenaGameStatePlanned, slot.Attempts[0].State)

				require.NotNil(t, progression.NextWave)
				require.Equal(t, next.WaveID, progression.NextWave.ID)
				require.Equal(t, next.WaveRevisionID, progression.NextWave.RevisionID)
				require.Equal(t, domain.ArenaWaveStatePlanned, progression.NextWave.State)
				require.Equal(t, []domain.ArenaWaveMember{
					{ParticipantID: current.Series.FirstParticipantID},
					{ParticipantID: current.Series.SecondParticipantID},
				}, progression.NextWave.Members)
				require.NoError(t, progression.Validate())
			})
		}
	})

	t.Run("reconciles a duplicate result without another slot", func(t *testing.T) {
		t.Parallel()

		current := task038Series(domain.ArenaSeriesFormatBO3, nil)
		next := task038NextGameWave(2, domain.CategoryCrypto)
		command := task038ProgressionCommand(current, 1, &next)
		first, changed, err := arena.ProgressSeriesScore(current, command)
		require.NoError(t, err)
		require.True(t, changed)

		repeated, changed, err := arena.ProgressSeriesScore(first.Series, command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, first, repeated)
		require.Len(t, repeated.Series.Series.Slots, 2)

		altered := command
		altered.ScoreRevision.ScoreBefore = domain.ArenaSeriesScore{SecondParticipantWins: 1}
		_, changed, err = arena.ProgressSeriesScore(first.Series, altered)
		require.ErrorIs(t, err, arena.ErrInvalidSeriesScoreProgression)
		require.False(t, changed)
	})

	t.Run("rejects a draw, stale revision and extra or missing Wave", func(t *testing.T) {
		t.Parallel()

		bo3 := task038Series(domain.ArenaSeriesFormatBO3, nil)
		next := task038NextGameWave(2, domain.CategoryCrypto)
		valid := task038ProgressionCommand(bo3, 1, &next)
		tests := []struct {
			name   string
			mutate func(*arena.SeriesScoreProgressionCommand)
		}{
			{
				name: "draw score",
				mutate: func(command *arena.SeriesScoreProgressionCommand) {
					command.ScoreRevision.ScoreAfter = domain.ArenaSeriesScore{}
				},
			},
			{
				name: "stale score head",
				mutate: func(command *arena.SeriesScoreProgressionCommand) {
					stale := domain.ArenaSeriesScoreRevisionID(task038ID(900))
					command.ScoreRevision.PreviousRevisionID = &stale
				},
			},
			{
				name: "missing next Wave",
				mutate: func(command *arena.SeriesScoreProgressionCommand) {
					command.Next = nil
				},
			},
			{
				name: "terminal evidence before winner",
				mutate: func(command *arena.SeriesScoreProgressionCommand) {
					terminal := domain.ArenaOfficialResultRevisionID(task038ID(901))
					command.TerminalResultRevisionID = &terminal
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				command := valid
				command.Next = task038CloneNext(valid.Next)
				command.ScoreRevision.PreviousRevisionID = task038CloneScoreRevisionID(
					valid.ScoreRevision.PreviousRevisionID,
				)
				test.mutate(&command)
				_, changed, err := arena.ProgressSeriesScore(bo3, command)
				require.ErrorIs(t, err, arena.ErrInvalidSeriesScoreProgression)
				require.False(t, changed)
			})
		}

		bo1 := task038Series(domain.ArenaSeriesFormatBO1, nil)
		extra := task038NextGameWave(2, domain.CategoryCrypto)
		command := task038ProgressionCommand(bo1, 1, &extra)
		_, changed, err := arena.ProgressSeriesScore(bo1, command)
		require.ErrorIs(t, err, arena.ErrInvalidSeriesScoreProgression)
		require.False(t, changed)

		withPrior := task038Series(domain.ArenaSeriesFormatBO3, []int{1})
		reusedGame := task038NextGameWave(3, domain.CategoryReverse)
		reusedGame.GameID = withPrior.Series.Slots[0].Attempts[0].ID
		command = task038ProgressionCommand(withPrior, 2, &reusedGame)
		_, changed, err = arena.ProgressSeriesScore(withPrior, command)
		require.ErrorIs(t, err, arena.ErrInvalidSeriesScoreProgression)
		require.False(t, changed)
	})
}

func task038Series(format domain.ArenaSeriesFormat, priorWinners []int) arena.SeriesExecution {
	firstID := task038ID(1)
	secondID := task038ID(2)
	seriesID := task038ID(3)
	categories := []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse}
	score := domain.ArenaSeriesScore{}
	slots := make([]domain.ArenaGameSlot, 0, len(priorWinners)+1)
	for index, winner := range priorWinners {
		slotID := task038ID(20 + index*10)
		resultRevisionID := domain.ArenaOfficialResultRevisionID(task038ID(22 + index*10))
		winnerID := firstID
		if winner == 2 {
			winnerID = secondID
		}
		slots = append(slots, domain.ArenaGameSlot{
			ID: slotID, SeriesID: seriesID, Position: index + 1,
			Category: categories[index], ScoreBefore: score,
			Attempts: []domain.ArenaGame{{
				ID: task038ID(21 + index*10), SlotID: slotID, AttemptNo: 1,
				State: domain.ArenaGameStateCompleted, ResultReason: domain.ArenaGameResultReasonSolved,
				WinnerID: &winnerID, ResultRevisionID: &resultRevisionID,
			}},
		})
		if winner == 1 {
			score.FirstParticipantWins++
		} else {
			score.SecondParticipantWins++
		}
	}
	position := len(slots) + 1
	slotID := task038ID(20 + len(priorWinners)*10)
	slots = append(slots, domain.ArenaGameSlot{
		ID: slotID, SeriesID: seriesID, Position: position,
		Category: categories[position-1], ScoreBefore: score,
		Attempts: []domain.ArenaGame{{
			ID: task038ID(21 + len(priorWinners)*10), SlotID: slotID,
			AttemptNo: 1, State: domain.ArenaGameStateActive,
		}},
	})
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task038ID(4 + len(priorWinners)))
	return arena.SeriesExecution{Series: domain.ArenaSeries{
		ID: seriesID, TournamentID: task038ID(4),
		FirstParticipantID: firstID, SecondParticipantID: secondID,
		Format: format, State: domain.ArenaSeriesStateActive, Score: score,
		Slots: slots, CurrentScoreRevisionID: &scoreRevisionID,
	}}
}

func task038ProgressionCommand(
	current arena.SeriesExecution,
	winner int,
	next *arena.NextSeriesGameWave,
) arena.SeriesScoreProgressionCommand {
	series := current.Series
	slot := series.Slots[len(series.Slots)-1]
	game := slot.Attempts[len(slot.Attempts)-1]
	winnerID := series.FirstParticipantID
	if winner == 2 {
		winnerID = series.SecondParticipantID
	}
	resultRevisionID := domain.ArenaOfficialResultRevisionID(task038ID(100 + slot.Position))
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
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task038ID(110 + slot.Position))
	command := arena.SeriesScoreProgressionCommand{
		Game: game,
		ScoreRevision: arena.ArenaSettlementScoreRevision{
			ID: scoreRevisionID, SeriesID: series.ID,
			FirstParticipantID:  series.FirstParticipantID,
			SecondParticipantID: series.SecondParticipantID,
			PreviousRevisionID:  task038CloneScoreRevisionID(series.CurrentScoreRevisionID),
			Ordinal:             slot.Position + 1, Format: series.Format,
			ScoreBefore: series.Score, ScoreAfter: scoreAfter,
			GameResultRevisionIDs: []domain.ArenaOfficialResultRevisionID{resultRevisionID},
			RecordedAt:            time.Date(2026, time.August, 30, 21, slot.Position, 0, 0, time.UTC),
		},
		Next: task038CloneNext(next),
	}
	if scoreAfter.Winner(series.FirstParticipantID, series.SecondParticipantID, series.Format) != nil {
		terminal := domain.ArenaOfficialResultRevisionID(task038ID(120 + slot.Position))
		command.TerminalResultRevisionID = &terminal
	}
	return command
}

func task038NextGameWave(position int, category domain.Category) arena.NextSeriesGameWave {
	return arena.NextSeriesGameWave{
		WaveID:         task038ID(200 + position),
		WaveRevisionID: domain.ArenaWaveRevisionID(task038ID(210 + position)),
		SlotID:         task038ID(220 + position), GameID: task038ID(230 + position),
		Category: category,
	}
}

func task038CloneSeriesExecution(value arena.SeriesExecution) arena.SeriesExecution {
	clone := value
	clone.Series.Slots = make([]domain.ArenaGameSlot, len(value.Series.Slots))
	for index, slot := range value.Series.Slots {
		clone.Series.Slots[index] = slot
		clone.Series.Slots[index].Attempts = append([]domain.ArenaGame(nil), slot.Attempts...)
	}
	clone.Series.CurrentScoreRevisionID = task038CloneScoreRevisionID(value.Series.CurrentScoreRevisionID)
	return clone
}

func task038CloneNext(value *arena.NextSeriesGameWave) *arena.NextSeriesGameWave {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func task038CloneScoreRevisionID(
	value *domain.ArenaSeriesScoreRevisionID,
) *domain.ArenaSeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func task038ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("38000000-0000-0000-0000-%012d", value))
}
