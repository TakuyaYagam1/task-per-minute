package game

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateNormalNoShowCommand(command NoShowCommand) error {
	if !validNormalNoShowScope(command.Scope) || command.CommandID == uuid.Nil ||
		command.ExpectedWaveRevisionID.IsZero() || command.ExpectedWindowRevisionID.IsZero() ||
		!command.ExpectedSeriesState.IsValid() || command.ScoreRevisionID.IsZero() ||
		command.SeriesResultRevisionID.IsZero() {
		return normalNoShowError("invalid command identity or revision")
	}
	seen := make(map[domain.OfficialResultRevisionID]struct{}, len(command.GameResultRevisionIDs)+1)
	seen[command.SeriesResultRevisionID] = struct{}{}
	for _, revisionID := range command.GameResultRevisionIDs {
		if revisionID.IsZero() {
			return normalNoShowError("missing Game result revision")
		}
		if _, duplicate := seen[revisionID]; duplicate {
			return normalNoShowError("duplicate result revision identity")
		}
		seen[revisionID] = struct{}{}
	}
	return nil
}

func validateNormalNoShowAuthority(authority NoShowAuthority) error {
	if !validNormalNoShowScope(authority.Scope) || authority.Revision < 1 || authority.CurrentOrdinal < 0 {
		return normalNoShowError("invalid authority identity")
	}
	if authority.Current != nil {
		return validateCurrentNormalNoShow(authority)
	}
	if err := validateNormalNoShowWaveAuthority(authority); err != nil {
		return err
	}
	return validateNormalNoShowSeriesAuthority(authority)
}

func validateCurrentNormalNoShow(authority NoShowAuthority) error {
	if err := authority.Current.Validate(); err != nil || authority.Current.Scope != authority.Scope {
		return normalNoShowError("invalid current resolution")
	}
	return nil
}

func validateNormalNoShowWaveAuthority(authority NoShowAuthority) error {
	if err := authority.Wave.Validate(); err != nil || authority.Wave.ID != authority.Scope.WaveID ||
		authority.Wave.TournamentID != authority.Scope.TournamentID ||
		authority.Wave.ReadyWindow == nil || authority.Wave.ReadyWindow.ID != authority.Scope.WindowID ||
		authority.Wave.ReadyWindow.State != domain.ReadyWindowStateOpen ||
		(authority.Wave.State != domain.WaveStateReadyWindowOpen &&
			authority.Wave.State != domain.WaveStateReady) {
		return normalNoShowError("invalid open Wave authority")
	}
	return nil
}

func validateNormalNoShowSeriesAuthority(authority NoShowAuthority) error {
	if err := authority.Series.Validate(); err != nil ||
		authority.Series.Series.ID != authority.Scope.SeriesID ||
		authority.Series.Series.TournamentID != authority.Scope.TournamentID ||
		!normalNoShowSeriesState(authority.Series.Series.State) {
		return normalNoShowError("invalid Series authority")
	}
	return nil
}

func validateNormalNoShowOutcome(resolution NoShowResolution) error {
	switch resolution.Action {
	case domain.NormalNoShowActionReopenWave:
		if resolution.Series.Series.State != domain.SeriesStateCompleted ||
			resolution.Series.Series.WinnerID == nil {
			return normalNoShowError("reopen outcome requires one winner")
		}
	case domain.NormalNoShowActionPauseWave:
		if resolution.Series.Series.State != domain.SeriesStateCancelled ||
			resolution.Series.Series.WinnerID != nil {
			return normalNoShowError("pause outcome cannot invent a winner")
		}
	default:
		return normalNoShowError("unknown resolution action")
	}
	return nil
}

func validateNormalNoShowRevisions(resolution NoShowResolution) error {
	expectedOrdinal := resolution.ScoreRevision.Ordinal
	if len(resolution.GameRevisions) != 0 {
		expectedOrdinal = resolution.GameRevisions[0].Ordinal
	}
	if err := validateNormalNoShowGameRevisions(resolution, expectedOrdinal); err != nil {
		return err
	}
	scoreOrdinal := expectedOrdinal + len(resolution.GameRevisions)
	if err := validateNormalNoShowScoreRevision(resolution, scoreOrdinal); err != nil {
		return err
	}
	return validateNormalNoShowSeriesRevision(resolution, scoreOrdinal+1)
}

func validateNormalNoShowGameRevisions(
	resolution NoShowResolution,
	expectedOrdinal int,
) error {
	for index, revision := range resolution.GameRevisions {
		if revision.Ordinal != expectedOrdinal+index || revision.ID.IsZero() || revision.GameID == uuid.Nil ||
			revision.State != domain.GameStateCancelled ||
			revision.Reason != domain.GameResultReasonSeriesCancelled ||
			!revision.RecordedAt.Equal(resolution.ResolvedAt) {
			return normalNoShowError("invalid ordered Game revision")
		}
	}
	return nil
}

func validateNormalNoShowScoreRevision(
	resolution NoShowResolution,
	expectedOrdinal int,
) error {
	if resolution.ScoreRevision.Ordinal != expectedOrdinal || resolution.ScoreRevision.ID.IsZero() ||
		resolution.ScoreRevision.SeriesID != resolution.Scope.SeriesID ||
		resolution.ScoreRevision.Score != resolution.Series.Series.Score ||
		!resolution.ScoreRevision.RecordedAt.Equal(resolution.ResolvedAt) ||
		len(resolution.ScoreRevision.GameResultRevisionIDs) != len(resolution.GameRevisions) {
		return normalNoShowError("invalid ordered score revision")
	}
	for index, revisionID := range resolution.ScoreRevision.GameResultRevisionIDs {
		if revisionID != resolution.GameRevisions[index].ID {
			return normalNoShowError("score revision does not reference ordered Games")
		}
	}
	return nil
}

func validateNormalNoShowSeriesRevision(
	resolution NoShowResolution,
	expectedOrdinal int,
) error {
	if resolution.SeriesRevision.Ordinal != expectedOrdinal || resolution.SeriesRevision.ID.IsZero() ||
		resolution.SeriesRevision.SeriesID != resolution.Scope.SeriesID ||
		resolution.SeriesRevision.State != resolution.Series.Series.State ||
		resolution.SeriesRevision.ScoreRevisionID != resolution.ScoreRevision.ID ||
		!resolution.SeriesRevision.RecordedAt.Equal(resolution.ResolvedAt) ||
		!noShowUuidPointersEqual(resolution.SeriesRevision.WinnerID, resolution.Series.Series.WinnerID) {
		return normalNoShowError("invalid ordered Series revision")
	}
	if resolution.Series.Series.CurrentScoreRevisionID == nil ||
		*resolution.Series.Series.CurrentScoreRevisionID != resolution.ScoreRevision.ID ||
		resolution.Series.Series.CurrentResultRevisionID == nil ||
		*resolution.Series.Series.CurrentResultRevisionID != resolution.SeriesRevision.ID {
		return normalNoShowError("terminal Series does not reference current revisions")
	}
	return nil
}
