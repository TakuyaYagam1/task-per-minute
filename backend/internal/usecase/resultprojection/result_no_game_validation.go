package resultprojection

import (
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

//nolint:gocyclo // This is one fail-closed audit boundary for the recorded no-show aggregate.
func validateRecordedNoGameResult(recorded RecordedNoGameResult) error {
	gameCount := len(recorded.GameResults)
	if !recorded.Scope.IsValid() || recorded.CommandID == uuid.Nil ||
		!recorded.Format.IsValid() || recorded.FirstParticipantID == uuid.Nil ||
		recorded.SecondParticipantID == uuid.Nil || recorded.FirstParticipantID == recorded.SecondParticipantID ||
		!domain.IsValidServerTime(recorded.ResolvedAt) || gameCount == 0 || gameCount > 16 {
		return invalidOfficialResultProjection("invalid no-show revision bounds")
	}
	firstOrdinal := recorded.GameResults[0].Ordinal
	if firstOrdinal < 1 || firstOrdinal > math.MaxInt-gameCount-1 ||
		recorded.Score.Ordinal != firstOrdinal+gameCount ||
		recorded.Series.Ordinal != firstOrdinal+gameCount+1 {
		return invalidOfficialResultProjection("invalid no-show revision ordinals")
	}
	if len(recorded.Topology) != gameCount || len(recorded.GameSourceRevisions) != gameCount ||
		len(recorded.GameProjections) != gameCount || len(recorded.GameDependencies) != gameCount ||
		len(recorded.Score.GameResultRevisionIDs) != gameCount {
		return invalidOfficialResultProjection("no-show Game evidence is incomplete")
	}
	for index, game := range recorded.GameResults {
		binding := recorded.Topology[index]
		if game.Ordinal != firstOrdinal+index || game.ID.IsZero() || game.GameID == uuid.Nil ||
			game.State != domain.GameStateCancelled || game.Reason != domain.GameResultReasonSeriesCancelled ||
			!domain.IsValidServerTime(game.RecordedAt) || !game.RecordedAt.Equal(recorded.ResolvedAt) ||
			recorded.Score.GameResultRevisionIDs[index] != game.ID || binding.SeriesID != recorded.Scope.SeriesID ||
			binding.SlotID == uuid.Nil || binding.SlotPosition < 1 || binding.GameID != game.GameID ||
			binding.AttemptNo < 1 || binding.ResultRevisionID != game.ID || game.PreviousRevisionID != nil ||
			(index > 0 && binding.SlotPosition <= recorded.Topology[index-1].SlotPosition) {
			return invalidOfficialResultProjection("invalid no-show Game revision provenance")
		}
	}
	if recorded.Score.ID.IsZero() || recorded.Score.SeriesID != recorded.Scope.SeriesID ||
		recorded.Score.Score.Validate(recorded.Format) != nil ||
		!domain.IsValidServerTime(recorded.Score.RecordedAt) || !recorded.Score.RecordedAt.Equal(recorded.ResolvedAt) ||
		recorded.Series.ID.IsZero() ||
		recorded.Series.SeriesID != recorded.Scope.SeriesID ||
		recorded.Series.ScoreRevisionID != recorded.Score.ID ||
		!domain.IsValidServerTime(recorded.Series.RecordedAt) || !recorded.Series.RecordedAt.Equal(recorded.ResolvedAt) {
		return invalidOfficialResultProjection("invalid no-show revision timestamp")
	}
	if err := validateRecordedNoGameIdentities(recorded); err != nil {
		return err
	}
	for index, projection := range recorded.GameProjections {
		revision := projection.Revision()
		game := recorded.GameResults[index]
		if validateExactProjection(recorded.GameSourceRevisions[index], projection) != nil ||
			revision.TournamentID() != recorded.Scope.TournamentID ||
			revision.Artifact() != (domain.ArtifactRef{Kind: domain.ArtifactKindGameResult, EntityID: game.GameID}) ||
			revision.CreatedAt().After(game.RecordedAt) || recorded.GameDependencies[index] != (domain.RevisionDependency{
			SourceRevisionID: revision.ID(), DerivedRevisionID: recorded.ScoreProjection.Revision().ID(),
		}) {
			return invalidOfficialResultProjection("invalid no-show Game projection")
		}
	}
	if err := validateExactProjection(recorded.ScoreSourceRevision, recorded.ScoreProjection); err != nil {
		return invalidOfficialResultProjection("invalid recorded no-show score source: %v", err)
	}
	if err := validateNoGameProjectionNode(
		recorded.ScoreProjection, recorded.Scope.TournamentID,
		domain.ArtifactKindSeriesScore, recorded.Scope.SeriesID, recorded.ResolvedAt,
	); err != nil {
		return err
	}
	if err := validateExactProjection(recorded.ResultSourceRevision, recorded.ResultProjection); err != nil {
		return invalidOfficialResultProjection("invalid recorded no-show result source: %v", err)
	}
	if err := validateNoGameProjectionNode(
		recorded.ResultProjection, recorded.Scope.TournamentID,
		domain.ArtifactKindSeriesResult, recorded.Scope.SeriesID, recorded.ResolvedAt,
	); err != nil {
		return err
	}
	if recorded.ResultProjection.Revision().CreatedAt().Before(recorded.ScoreProjection.Revision().CreatedAt()) ||
		recorded.ResultDependency != (domain.RevisionDependency{
			SourceRevisionID:  recorded.ScoreProjection.Revision().ID(),
			DerivedRevisionID: recorded.ResultProjection.Revision().ID(),
		}) {
		return invalidOfficialResultProjection("no-show result projection predates score")
	}
	return validateNoGameOutcome(recorded)
}

func validateNoGameProjectionNode(
	projection domain.ProjectionRevision,
	tournamentID uuid.UUID,
	kind domain.ArtifactKind,
	entityID uuid.UUID,
	recordedAt time.Time,
) error {
	revision := projection.Revision()
	if projection.Validate() != nil || revision.TournamentID() != tournamentID ||
		revision.Artifact() != (domain.ArtifactRef{Kind: kind, EntityID: entityID}) ||
		revision.CreatedAt().After(recordedAt) {
		return invalidOfficialResultProjection("invalid no-show projection provenance")
	}
	return nil
}

func validateNoGameOutcome(recorded RecordedNoGameResult) error {
	switch recorded.Action {
	case domain.NormalNoShowActionReopenWave:
		winner := recorded.Score.Score.Winner(
			recorded.FirstParticipantID, recorded.SecondParticipantID, recorded.Format,
		)
		if recorded.ReadyParticipantID == nil ||
			(*recorded.ReadyParticipantID != recorded.FirstParticipantID && *recorded.ReadyParticipantID != recorded.SecondParticipantID) ||
			recorded.Series.State != domain.SeriesStateCompleted || winner == nil ||
			recorded.Series.WinnerID == nil || *winner != *recorded.Series.WinnerID ||
			*winner != *recorded.ReadyParticipantID {
			return invalidOfficialResultProjection("no-show winner does not match forced score")
		}
	case domain.NormalNoShowActionPauseWave:
		if recorded.ReadyParticipantID != nil || recorded.Series.State != domain.SeriesStateCancelled ||
			recorded.Series.WinnerID != nil || recorded.Score.Score.Winner(
			recorded.FirstParticipantID, recorded.SecondParticipantID, recorded.Format,
		) != nil {
			return invalidOfficialResultProjection("cancelled no-show has a winner")
		}
	default:
		return invalidOfficialResultProjection("unknown no-show action")
	}
	return nil
}
