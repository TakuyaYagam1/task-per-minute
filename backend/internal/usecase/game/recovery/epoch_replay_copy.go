package recovery

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
)

func cloneEpochReplayAuthority(authority EpochReplayAuthority) EpochReplayAuthority {
	clone := authority
	clone.Lease = authority.Lease.Clone()
	clone.Attempt = cloneEpochFailedAttemptAuthority(authority.Attempt)
	if authority.Current != nil {
		current := cloneEpochReplayRecord(*authority.Current)
		clone.Current = &current
	}
	return clone
}

func cloneEpochFailedAttemptAuthority(
	authority attemptusecase.AttemptAuthority,
) attemptusecase.AttemptAuthority {
	clone := authority
	clone.Wave = cloneEpochWave(authority.Wave)
	clone.Series = cloneEpochSeriesExecution(authority.Series)
	clone.CurrentGameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	if authority.Current != nil {
		current := attemptusecase.CloneRecord(*authority.Current)
		clone.Current = &current
	}
	return clone
}

func cloneEpochReplayRecord(record EpochReplayRecord) EpochReplayRecord {
	clone := record
	clone.Attempt = attemptusecase.CloneRecord(record.Attempt)
	return clone
}

func cloneEpochSeriesExecution(execution seriesdomain.Execution) seriesdomain.Execution {
	clone := execution
	if execution.ResumeState != nil {
		resumeState := *execution.ResumeState
		clone.ResumeState = &resumeState
	}
	clone.Series = cloneEpochDomainSeries(execution.Series)
	return clone
}

func cloneEpochDomainSeries(series domain.Series) domain.Series {
	clone := series
	clone.WinnerID = cloneEpochUUIDPointer(series.WinnerID)
	clone.CurrentScoreRevisionID = cloneEpochScoreRevisionIDPointer(series.CurrentScoreRevisionID)
	clone.CurrentResultRevisionID = cloneEpochResultRevisionIDPointer(series.CurrentResultRevisionID)
	clone.Slots = make([]domain.GameSlot, len(series.Slots))
	for index := range series.Slots {
		clone.Slots[index] = cloneEpochGameSlot(series.Slots[index])
	}
	return clone
}

func cloneEpochGameSlot(slot domain.GameSlot) domain.GameSlot {
	clone := slot
	clone.Attempts = make([]domain.Game, len(slot.Attempts))
	for index := range slot.Attempts {
		clone.Attempts[index] = cloneEpochGame(slot.Attempts[index])
	}
	return clone
}

func cloneEpochGame(value domain.Game) domain.Game {
	clone := value
	clone.WinnerID = cloneEpochUUIDPointer(value.WinnerID)
	clone.ResultRevisionID = cloneEpochResultRevisionIDPointer(value.ResultRevisionID)
	return clone
}

func cloneEpochWave(wave domain.Wave) domain.Wave {
	clone := wave
	clone.Members = append([]domain.WaveMember(nil), wave.Members...)
	if wave.ReadyWindow != nil {
		window := *wave.ReadyWindow
		window.ConsumedAt = cloneEpochTimePointer(wave.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = cloneEpochTimePointer(wave.StartedAt)
	clone.PausedAt = cloneEpochTimePointer(wave.PausedAt)
	return clone
}

func cloneEpochTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneEpochUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneEpochScoreRevisionIDPointer(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneEpochResultRevisionIDPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
