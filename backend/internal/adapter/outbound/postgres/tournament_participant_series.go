package postgres

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

type participantSeriesAuthority struct {
	RosterID             uuid.UUID
	SeriesRevision       int64
	ScoreRevision        int64
	SeriesResultRevision int64
	ProjectionRevision   int64
	AttemptRevisions     map[uuid.UUID]int64
}

func participantSeriesExecution(
	rows []sqlc.GetParticipantSeriesExecutionRow,
) (seriesdomain.Execution, participantSeriesAuthority, error) {
	if len(rows) == 0 {
		return seriesdomain.Execution{}, participantSeriesAuthority{}, domain.ErrConflict
	}
	first := rows[0]
	series := participantSeriesFromHeader(first)
	metadata := participantSeriesMetadata(first, len(rows))
	for _, row := range rows {
		if err := appendParticipantSeriesAttempt(&series, &metadata, first, row); err != nil {
			return seriesdomain.Execution{}, participantSeriesAuthority{}, err
		}
	}

	execution := seriesdomain.Execution{Series: series}
	if series.State == domain.SeriesStateTechnicalPause {
		resumeState := domain.SeriesState(first.SeriesResumeState)
		execution.ResumeState = &resumeState
	}
	if err := execution.Validate(); err != nil {
		return seriesdomain.Execution{}, participantSeriesAuthority{}, fmt.Errorf(
			"participant Series authority: %w",
			err,
		)
	}
	if !validParticipantSeriesAuthority(metadata) {
		return seriesdomain.Execution{}, participantSeriesAuthority{}, domain.ErrInternal
	}
	return execution, metadata, nil
}

func participantSeriesFromHeader(first sqlc.GetParticipantSeriesExecutionRow) domain.Series {
	series := domain.Series{
		ID: first.SeriesID, TournamentID: first.TournamentID,
		FirstParticipantID: first.FirstParticipantID, SecondParticipantID: first.SecondParticipantID,
		Format: domain.SeriesFormat(first.Format), State: domain.SeriesState(first.SeriesState),
		Score: domain.SeriesScore{
			FirstParticipantWins:  int(first.FirstParticipantWins),
			SecondParticipantWins: int(first.SecondParticipantWins),
		},
	}
	if first.SeriesWinnerID.Valid {
		winnerID := first.SeriesWinnerID.UUID
		series.WinnerID = &winnerID
	}
	if first.CurrentScoreRevisionID.Valid {
		revisionID := domain.SeriesScoreRevisionID(first.CurrentScoreRevisionID.UUID)
		series.CurrentScoreRevisionID = &revisionID
	}
	if first.CurrentResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(first.CurrentResultRevisionID.UUID)
		series.CurrentResultRevisionID = &revisionID
	}
	return series
}

func participantSeriesMetadata(
	first sqlc.GetParticipantSeriesExecutionRow,
	attemptCount int,
) participantSeriesAuthority {
	return participantSeriesAuthority{
		RosterID: first.RosterID, SeriesRevision: first.SeriesRevision,
		ScoreRevision: first.ScoreRevision, SeriesResultRevision: first.SeriesResultRevision,
		ProjectionRevision: first.ProjectionRevision,
		AttemptRevisions:   make(map[uuid.UUID]int64, attemptCount),
	}
}

func appendParticipantSeriesAttempt(
	series *domain.Series,
	metadata *participantSeriesAuthority,
	first sqlc.GetParticipantSeriesExecutionRow,
	row sqlc.GetParticipantSeriesExecutionRow,
) error {
	if !sameParticipantSeriesHeader(first, row) {
		return domain.ErrInternal
	}
	attempt := participantSeriesAttempt(row)
	metadata.AttemptRevisions[attempt.ID] = row.AttemptRevision
	if len(series.Slots) == 0 || series.Slots[len(series.Slots)-1].ID != row.SlotID {
		series.Slots = append(series.Slots, participantSeriesSlot(row))
	}
	index := len(series.Slots) - 1
	series.Slots[index].Attempts = append(series.Slots[index].Attempts, attempt)
	return nil
}

func participantSeriesAttempt(row sqlc.GetParticipantSeriesExecutionRow) domain.Game {
	attempt := domain.Game{
		ID: row.AttemptID, SlotID: row.SlotID, AttemptNo: int(row.AttemptNumber),
		State: domain.GameState(row.AttemptState),
	}
	if row.AttemptResultReason != nil {
		attempt.ResultReason = domain.GameResultReason(*row.AttemptResultReason)
	}
	if row.AttemptWinnerID.Valid {
		winnerID := row.AttemptWinnerID.UUID
		attempt.WinnerID = &winnerID
	}
	if row.AttemptResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(row.AttemptResultRevisionID.UUID)
		attempt.ResultRevisionID = &revisionID
	}
	return attempt
}

func participantSeriesSlot(row sqlc.GetParticipantSeriesExecutionRow) domain.GameSlot {
	return domain.GameSlot{
		ID: row.SlotID, SeriesID: row.SeriesID, Position: int(row.SlotNumber),
		Category: domain.Category(row.Category),
		ScoreBefore: domain.SeriesScore{
			FirstParticipantWins:  int(row.FirstParticipantWinsBefore),
			SecondParticipantWins: int(row.SecondParticipantWinsBefore),
		},
	}
}

func validParticipantSeriesAuthority(metadata participantSeriesAuthority) bool {
	return metadata.RosterID != uuid.Nil && metadata.SeriesRevision >= 1 &&
		metadata.ScoreRevision >= 1 && metadata.ProjectionRevision >= 1 &&
		metadata.SeriesResultRevision >= 0
}

func sameParticipantSeriesHeader(
	first sqlc.GetParticipantSeriesExecutionRow,
	candidate sqlc.GetParticipantSeriesExecutionRow,
) bool {
	return sameParticipantSeriesIdentity(first, candidate) &&
		sameParticipantSeriesState(first, candidate) &&
		sameParticipantSeriesRevisions(first, candidate)
}

func sameParticipantSeriesIdentity(
	first sqlc.GetParticipantSeriesExecutionRow,
	candidate sqlc.GetParticipantSeriesExecutionRow,
) bool {
	return candidate.SeriesID == first.SeriesID && candidate.TournamentID == first.TournamentID &&
		candidate.RosterID == first.RosterID && candidate.FirstParticipantID == first.FirstParticipantID &&
		candidate.SecondParticipantID == first.SecondParticipantID && candidate.Format == first.Format
}

func sameParticipantSeriesState(
	first sqlc.GetParticipantSeriesExecutionRow,
	candidate sqlc.GetParticipantSeriesExecutionRow,
) bool {
	return candidate.SeriesState == first.SeriesState &&
		candidate.FirstParticipantWins == first.FirstParticipantWins &&
		candidate.SecondParticipantWins == first.SecondParticipantWins &&
		candidate.SeriesWinnerID == first.SeriesWinnerID &&
		candidate.SeriesResumeState == first.SeriesResumeState
}

func sameParticipantSeriesRevisions(
	first sqlc.GetParticipantSeriesExecutionRow,
	candidate sqlc.GetParticipantSeriesExecutionRow,
) bool {
	return candidate.CurrentScoreRevisionID == first.CurrentScoreRevisionID &&
		candidate.CurrentResultRevisionID == first.CurrentResultRevisionID &&
		candidate.SeriesRevision == first.SeriesRevision && candidate.ScoreRevision == first.ScoreRevision &&
		candidate.SeriesResultRevision == first.SeriesResultRevision &&
		candidate.ProjectionRevision == first.ProjectionRevision
}
