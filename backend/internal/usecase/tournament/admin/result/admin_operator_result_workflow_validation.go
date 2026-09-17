package result

import (
	"bytes"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func validOperatorResultAuthority(
	authority OperatorResultAuthority,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
) bool {
	return authority.TournamentID == tournamentID && authority.RosterID != uuid.Nil &&
		authority.SeriesID == seriesID && authority.TournamentState.IsValid() &&
		authority.AuthorityRevision >= 1 && authority.ProjectionRevisionID != uuid.Nil &&
		authority.ProjectionRevision >= 1
}

func validOperatorResultCommandRecord(record OperatorResultCommandRecord) bool {
	return validCommandScope(record.CommandScope) && record.SeriesID != uuid.Nil && record.Action.valid() &&
		record.ExpectedAuthorityRevision >= 1 && !zeroOperatorResultDigest(record.RequestDigest) &&
		record.CommitID != uuid.Nil && record.ResultEventID != uuid.Nil &&
		domain.IsValidServerTime(record.ExecutedAt)
}

func (action OperatorResultAction) valid() bool {
	return action == OperatorResultActionNoShow || action == OperatorResultActionForfeit
}

func operatorResultCommandMatches(
	record OperatorResultCommandRecord,
	scope CommandScope,
	seriesID uuid.UUID,
	expectedAuthorityRevision int64,
	action OperatorResultAction,
	digest [32]byte,
) bool {
	return validOperatorResultCommandRecord(record) && record.CommandScope == scope &&
		record.SeriesID == seriesID && record.Action == action &&
		record.ExpectedAuthorityRevision == expectedAuthorityRevision && record.RequestDigest == digest
}

func operatorNoShowCommand(command NoShowCommand) gameusecase.NoShowCommand {
	gameRevisionIDs := make([]domain.OfficialResultRevisionID, len(command.GameResultRevisionIDs))
	for index, revisionID := range command.GameResultRevisionIDs {
		gameRevisionIDs[index] = domain.OfficialResultRevisionID(revisionID)
	}
	return gameusecase.NoShowCommand{
		Scope: domain.NormalNoShowScope{
			TournamentID: command.TournamentID,
			WaveID:       command.WaveID,
			WindowID:     command.WindowID,
			SeriesID:     command.SeriesID,
		},
		CommandID:                command.CommandID,
		ExpectedWaveRevisionID:   domain.WaveRevisionID(command.ExpectedWaveRevisionID),
		ExpectedWindowRevisionID: domain.ReadyWindowRevisionID(command.ExpectedWindowRevisionID),
		ExpectedSeriesState:      command.ExpectedSeriesState,
		GameResultRevisionIDs:    gameRevisionIDs,
		ScoreRevisionID:          domain.SeriesScoreRevisionID(command.ScoreRevisionID),
		SeriesResultRevisionID:   domain.OfficialResultRevisionID(command.SeriesResultRevisionID),
	}
}

func operatorForfeitCommand(command ForfeitCommand) gameusecase.OperatorCommand {
	var expectedGame *gameusecase.GameExpectation
	if command.ExpectedGame != nil {
		expectedGame = &gameusecase.GameExpectation{
			SlotID: command.ExpectedGame.SlotID, GameID: command.ExpectedGame.GameID,
			AttemptNo: command.ExpectedGame.AttemptNo, State: command.ExpectedGame.State,
		}
	}
	var gameRevisionID *domain.OfficialResultRevisionID
	if command.GameResultRevisionID != nil {
		value := domain.OfficialResultRevisionID(*command.GameResultRevisionID)
		gameRevisionID = &value
	}
	return gameusecase.OperatorCommand{
		Scope:     gameusecase.Scope{TournamentID: command.TournamentID, SeriesID: command.SeriesID},
		CommandID: command.CommandID, ActorOperatorID: command.Operator.ActorID,
		ForfeitingParticipantID: command.ForfeitingParticipantID,
		ExpectedGame:            expectedGame,
		Evidence: gameusecase.OperatorEvidence{
			Confirmed: command.Confirmed, Basis: gameusecase.OperatorBasis(command.Basis),
			Reason: command.Reason, RuleID: command.RuleID,
			EvidenceIDs: append([]uuid.UUID(nil), command.EvidenceIDs...),
		},
		Revisions: gameusecase.ForfeitRevisionSet{
			GameResultRevisionID:   gameRevisionID,
			ScoreRevisionID:        domain.SeriesScoreRevisionID(command.ScoreRevisionID),
			SeriesResultRevisionID: domain.OfficialResultRevisionID(command.SeriesResultRevisionID),
			AuditEventID:           command.AuditEventID, OutboxEventID: command.OutboxEventID,
			ProjectionRevisionID: command.ProjectionRevisionID,
		},
	}
}

func validOperatorNoShowResolution(
	resolution *gameusecase.NoShowResolution,
	command NoShowCommand,
	resolvedAt time.Time,
) bool {
	return resolution != nil && resolution.Validate() == nil &&
		resolution.Scope == operatorNoShowCommand(command).Scope && resolution.CommandID == command.CommandID &&
		resolution.ExpectedAuthorityRevision == command.ExpectedAuthorityRevision &&
		resolution.ResolvedAt.Equal(resolvedAt)
}

func validOperatorForfeitResolution(
	resolution *gameusecase.ForfeitResolution,
	command ForfeitCommand,
	resolvedAt time.Time,
) bool {
	return resolution != nil && resolution.Validate() == nil && resolution.Source == gameusecase.SourceOperator &&
		resolution.Scope == (gameusecase.Scope{TournamentID: command.TournamentID, SeriesID: command.SeriesID}) &&
		resolution.CommandID == command.CommandID && resolution.ActorID == command.Operator.ActorID &&
		resolution.ExpectedAuthorityRevision == command.ExpectedAuthorityRevision &&
		resolution.ResolvedAt.Equal(resolvedAt)
}

func zeroOperatorResultDigest(digest [32]byte) bool {
	return bytes.Equal(digest[:], make([]byte, len(digest)))
}
