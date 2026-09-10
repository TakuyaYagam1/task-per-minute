package admin

import (
	"bytes"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
)

func validLifecycleAuthority(authority LifecycleAuthority, tournamentID uuid.UUID) bool {
	return authority.ProjectionRevisionID != uuid.Nil && authority.ProjectionRevision >= 1 &&
		validTournamentView(authority.Tournament, tournamentID)
}

func validateLifecycleExecutionSnapshot(
	snapshot LifecycleExecutionSnapshot,
	authority LifecycleAuthority,
) error {
	if snapshot.GraphRevision != authority.ProjectionRevision || snapshot.GraphRevision < 1 ||
		snapshot.ExpectedChildren < 0 || snapshot.ObservedChildren < 0 || snapshot.IncompleteChildren < 0 ||
		snapshot.ExpectedChildren != snapshot.ObservedChildren || snapshot.IncompleteChildren != 0 ||
		snapshot.ActiveGolden || !validJSONObject(snapshot.Document) {
		return domain.ErrConflict
	}
	return nil
}

func validateLifecycleCommandRecord(record LifecycleCommandRecord) error {
	if !validCommandScope(record.CommandScope) || !record.Action.valid() ||
		record.SourceProjectionRevisionID == uuid.Nil || record.SourceProjectionRevision < 1 ||
		record.SourceTournamentRevision < 1 || !record.SourceTournamentState.IsValid() ||
		record.Result.Revision != record.SourceTournamentRevision+1 ||
		!validTournamentView(record.Result, record.TournamentID) ||
		!record.ExecutedAt.Equal(record.Result.UpdatedAt) {
		return domain.ErrInternal
	}
	if !validLifecycleRecordEvidence(record) || !validLifecycleRecordTransition(record) {
		return domain.ErrInternal
	}
	return nil
}

func validLifecycleRecordEvidence(record LifecycleCommandRecord) bool {
	switch record.Action {
	case TournamentActionPause:
		return record.PauseID != uuid.Nil && record.SourcePauseCommandID == uuid.Nil &&
			validJSONObject(record.ExecutionSnapshot)
	case TournamentActionResume:
		return record.PauseID != uuid.Nil && record.SourcePauseCommandID != uuid.Nil &&
			validJSONObject(record.ExecutionSnapshot)
	case TournamentActionOpenRegistration, TournamentActionStartSwiss, TournamentActionStartGolden,
		TournamentActionStartPlayoffs, TournamentActionComplete, TournamentActionCancel:
		return record.PauseID == uuid.Nil && record.SourcePauseCommandID == uuid.Nil &&
			len(record.ExecutionSnapshot) == 0
	default:
		return false
	}
}

func validLifecycleRecordTransition(record LifecycleCommandRecord) bool {
	switch record.Action {
	case TournamentActionPause:
		return record.Result.State == domain.TournamentStateTechnicalPause &&
			record.Result.PausedFromState != nil && *record.Result.PausedFromState == record.SourceTournamentState
	case TournamentActionResume:
		return record.SourceTournamentState == domain.TournamentStateTechnicalPause &&
			record.Result.State != domain.TournamentStateTechnicalPause && record.Result.PausedFromState == nil
	case TournamentActionCancel:
		return record.Result.State == domain.TournamentStateCancelled && record.Result.PausedFromState == nil
	case TournamentActionOpenRegistration, TournamentActionStartSwiss, TournamentActionStartGolden,
		TournamentActionStartPlayoffs, TournamentActionComplete:
		next, ok := lifecycleActionState(record.Action)
		return ok && record.Result.State == next && record.Result.PausedFromState == nil
	default:
		return false
	}
}

func lifecycleCommandMatches(record LifecycleCommandRecord, command TournamentActionCommand) bool {
	return validateLifecycleCommandRecord(record) == nil && record.TournamentID == command.TournamentID &&
		record.CommandID == command.CommandID && record.Operator.ActorID == command.Operator.ActorID &&
		record.Action == command.Action && record.SourceProjectionRevision == command.ExpectedProjectionRevision &&
		record.Reason == command.Reason
}

func lifecycleTournamentView(
	base usecase.TournamentView,
	record *lifecycleusecase.LifecycleTournamentRecord,
) (usecase.TournamentView, error) {
	if record == nil || record.ID != base.ID {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	view := adminCloneTournamentView(base)
	view.State = record.State
	view.PausedFromState = cloneTournamentState(record.PausedFromState)
	view.Revision = record.Revision
	view.UpdatedAt = record.UpdatedAt
	view.StartedAt = cloneTime(record.StartedAt)
	view.FinishedAt = cloneTime(record.FinishedAt)
	if !validTournamentView(view, base.ID) {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	return view, nil
}

func pausedTournamentView(
	base usecase.TournamentView,
	record *tournamentpause.TournamentTechnicalPauseRecord,
) (usecase.TournamentView, error) {
	if record == nil || record.Tournament.ID != base.ID {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	view := adminCloneTournamentView(base)
	view.State = record.Tournament.State
	view.PausedFromState = cloneTournamentState(record.Tournament.PausedFromState)
	view.Revision = record.Tournament.Revision
	view.UpdatedAt = record.Tournament.UpdatedAt
	if !validTournamentView(view, base.ID) {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	return view, nil
}

func cancelledTournamentView(
	base usecase.TournamentView,
	record *tournamentcancellation.TournamentCancellationRecord,
) (usecase.TournamentView, error) {
	if record == nil || record.Tournament.ID != base.ID {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	view := adminCloneTournamentView(base)
	view.State = record.Tournament.State
	view.PausedFromState = cloneTournamentState(record.Tournament.PausedFromState)
	view.Revision = record.Tournament.Revision
	view.UpdatedAt = record.Tournament.UpdatedAt
	view.FinishedAt = cloneTime(record.Tournament.FinishedAt)
	if !validTournamentView(view, base.ID) {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	return view, nil
}

func adminCloneTournamentView(view usecase.TournamentView) usecase.TournamentView {
	cloned := view
	cloned.PausedFromState = cloneTournamentState(view.PausedFromState)
	cloned.StartedAt = cloneTime(view.StartedAt)
	cloned.FinishedAt = cloneTime(view.FinishedAt)
	return cloned
}

func cloneTournamentState(value *domain.TournamentState) *domain.TournamentState {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	return bytes.Clone(value)
}

func validJSONObject(value json.RawMessage) bool {
	if !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil && len(object) > 0
}
