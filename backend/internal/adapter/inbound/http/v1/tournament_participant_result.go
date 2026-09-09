package v1

import (
	"math"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func participantReadinessResponse(event usecase.ReadinessEvent) (api.ReadinessEvent, error) {
	eventType := api.ReadinessEventType(event.Type)
	if event.CommandID == uuid.Nil || event.WaveID == uuid.Nil ||
		event.WindowID == uuid.Nil || event.ParticipantID == uuid.Nil ||
		!eventType.Valid() || !domain.IsValidServerTime(event.OccurredAt) {
		return api.ReadinessEvent{}, domain.ErrInternal
	}
	return api.ReadinessEvent{
		CommandId:     event.CommandID,
		WaveId:        event.WaveID,
		WindowId:      event.WindowID,
		ParticipantId: event.ParticipantID,
		Type:          eventType,
		OccurredAt:    event.OccurredAt,
	}, nil
}

func participantSubmissionResponse(
	result usecase.SubmissionResult,
) (api.ParticipantSubmissionResponse, error) {
	if result.ProjectionRevision < 1 || gamedomain.ValidateSubmission(result.Submission) != nil {
		return api.ParticipantSubmissionResponse{}, domain.ErrInternal
	}
	record := result.Submission
	return api.ParticipantSubmissionResponse{
		ProjectionRevision: result.ProjectionRevision,
		Submission: api.SubmissionRecord{
			Scope: api.SubmissionScope{
				WaveId:       record.Scope.WaveID,
				TournamentId: record.Scope.Game.TournamentID,
				SeriesId:     record.Scope.Game.SeriesID,
				SlotId:       record.Scope.Game.SlotID,
				GameId:       record.Scope.Game.GameID,
				AssignmentId: record.Scope.AssignmentID,
			},
			CommandId:     record.CommandID,
			ParticipantId: record.ParticipantID,
			Sequence:      record.Sequence,
			CommittedAt:   record.CommittedAt,
			Correct:       record.Correct,
			SnapshotId:    record.SnapshotID,
			TaskId:        record.TaskID,
			ContentDigest: participantDigest(record.ContentDigest),
		},
	}, nil
}

func participantPostSeriesResponse(
	result usecase.PostSeriesResult,
) (api.ParticipantPostSeriesResponse, error) {
	accepted := api.ParticipantPostSeriesResponseAcceptedAction(result.AcceptedAction)
	if result.SeriesID == uuid.Nil || result.ProjectionRevision < 1 || !accepted.Valid() {
		return api.ParticipantPostSeriesResponse{}, domain.ErrInternal
	}
	return api.ParticipantPostSeriesResponse{
		SeriesId:           result.SeriesID,
		ProjectionRevision: result.ProjectionRevision,
		AcceptedAction:     accepted,
	}, nil
}

func participantOfficialResultResponse(
	revision usecase.OfficialResultView,
) (api.OfficialResultRevision, error) {
	if revision.Ordinal < 1 || revision.Ordinal > math.MaxInt32 || revision.ID.IsZero() ||
		revision.CommandID == uuid.Nil || revision.TournamentID == uuid.Nil || revision.SeriesID == uuid.Nil ||
		revision.Actor.Validate() != nil || revision.WinnerID == nil || *revision.WinnerID == uuid.Nil ||
		revision.ScoreRevisionID.IsZero() || revision.SourceProjectionRevisionID == uuid.Nil ||
		!domain.IsValidServerTime(revision.RecordedAt) {
		return api.OfficialResultRevision{}, domain.ErrInternal
	}
	actorKind := api.ResultActorKind(revision.Actor.Kind)
	seriesState := api.SeriesState(revision.SeriesState)
	seriesReason := api.SeriesResultReason(revision.SeriesReason)
	if !actorKind.Valid() || !seriesState.Valid() || !seriesReason.Valid() {
		return api.OfficialResultRevision{}, domain.ErrInternal
	}

	result := api.OfficialResultRevision{
		Id:                         revision.ID.UUID(),
		PreviousRevisionId:         officialResultRevisionID(revision.PreviousRevisionID),
		Ordinal:                    int32(revision.Ordinal), //nolint:gosec // bounded above.
		CommandId:                  revision.CommandID,
		SubjectKind:                api.OfficialResultSubjectKindSeries,
		TournamentId:               revision.TournamentID,
		SeriesId:                   revision.SeriesID,
		ActorKind:                  actorKind,
		ActorId:                    participantUUIDPointer(revision.Actor.PrincipalID),
		WinnerId:                   participantUUIDPointer(revision.WinnerID),
		ScoreRevisionId:            seriesScoreRevisionID(&revision.ScoreRevisionID),
		SourceProjectionRevisionId: revision.SourceProjectionRevisionID,
		RecordedAt:                 revision.RecordedAt,
		SeriesState:                &seriesState,
		SeriesReason:               &seriesReason,
	}
	return result, nil
}

func participantUUIDValuePointer(value uuid.UUID) *uuid.UUID {
	cloned := value
	return &cloned
}
