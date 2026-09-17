package forfeit

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

const (
	maxForfeitReasonBytes = 256
	maxForfeitRuleIDBytes = 64
	maxForfeitEvidenceIDs = 16
)

func (r ForfeitResolution) Validate() error {
	if err := validateForfeitResolutionIdentity(r); err != nil {
		return err
	}
	if err := validateForfeitResolutionSeries(r); err != nil {
		return err
	}
	if !forfeitParticipantsMatch(r) {
		return forfeitError("forfeit winner or loser does not match Series")
	}
	if err := validateForfeitGameEvidence(r); err != nil {
		return err
	}
	if err := validateForfeitScoreRevision(r); err != nil {
		return err
	}
	if err := validateForfeitSeriesRevision(r); err != nil {
		return err
	}
	if err := validateForfeitOperatorEvidence(r); err != nil {
		return err
	}
	return validateForfeitSettlementEvidence(r)
}

func validateForfeitResolutionIdentity(resolution ForfeitResolution) error {
	if !resolution.Source.IsValid() || !resolution.Scope.IsValid() ||
		resolution.CommandID == uuid.Nil || resolution.ActorID == uuid.Nil ||
		resolution.ForfeitingParticipantID == uuid.Nil ||
		resolution.ExpectedAuthorityRevision < 1 || !domain.IsValidServerTime(resolution.ResolvedAt) ||
		resolution.Reason != resolution.Source.ResultReason() {
		return forfeitError("invalid resolution identity or timestamp")
	}
	if resolution.ExpectedGame != nil && !resolution.ExpectedGame.IsValid() {
		return forfeitError("invalid expected Game binding")
	}
	return nil
}

func validateForfeitResolutionSeries(resolution ForfeitResolution) error {
	if err := resolution.Series.Validate(); err != nil ||
		resolution.Series.Series.ID != resolution.Scope.SeriesID ||
		resolution.Series.Series.TournamentID != resolution.Scope.TournamentID {
		return forfeitError("invalid resolved Series")
	}
	if err := seriesdomain.ValidateResolution(
		resolution.Source.SeriesRoute(),
		resolution.Series,
	); err != nil {
		return forfeitError("Series resolution: %v", err)
	}
	return nil
}

func (s Source) IsValid() bool {
	return s == SourceSurrender || s == SourceOperator
}

func (s Source) ResultReason() domain.GameResultReason {
	if s == SourceSurrender {
		return domain.GameResultReasonSurrender
	}
	if s == SourceOperator {
		return domain.GameResultReasonOperatorForfeit
	}
	return ""
}

func (s Source) SeriesRoute() seriesdomain.ResolutionRoute {
	if s == SourceSurrender {
		return seriesdomain.CompetitiveSeriesRouteSurrender
	}
	if s == SourceOperator {
		return seriesdomain.CompetitiveSeriesRouteOperatorForfeit
	}
	return ""
}

func (s Scope) IsValid() bool {
	return s.TournamentID != uuid.Nil && s.SeriesID != uuid.Nil
}

func validateForfeitRequest(request forfeitRequest) error {
	if !request.source.IsValid() || !request.scope.IsValid() || request.commandID == uuid.Nil ||
		request.actorID == uuid.Nil || request.forfeitingParticipantID == uuid.Nil {
		return forfeitError("invalid command identity")
	}
	if request.expectedGame != nil && !request.expectedGame.IsValid() {
		return forfeitError("invalid expected Game")
	}
	if request.source == SourceSurrender {
		if request.actorID != request.forfeitingParticipantID || request.expectedGame == nil ||
			request.operatorEvidence != nil {
			return forfeitError("invalid surrender actor or Game")
		}
	} else if request.operatorEvidence == nil || request.operatorEvidence.Validate() != nil {
		return forfeitError("invalid operator evidence")
	}
	return validateForfeitRevisionSet(request.commandID, request.revisions)
}

func (e GameExpectation) IsValid() bool {
	return e.SlotID != uuid.Nil && e.GameID != uuid.Nil && e.AttemptNo > 0 && e.State.IsValid()
}

func (e OperatorEvidence) Validate() error {
	if !e.Confirmed || e.Basis != OperatorBasisRuleViolation ||
		!validForfeitText(e.Reason, maxForfeitReasonBytes) ||
		!validForfeitRuleID(e.RuleID) || len(e.EvidenceIDs) == 0 ||
		len(e.EvidenceIDs) > maxForfeitEvidenceIDs {
		return forfeitError("operator evidence is incomplete")
	}
	seen := make(map[uuid.UUID]struct{}, len(e.EvidenceIDs))
	for _, evidenceID := range e.EvidenceIDs {
		if evidenceID == uuid.Nil {
			return forfeitError("operator evidence has an empty identity")
		}
		if _, duplicate := seen[evidenceID]; duplicate {
			return forfeitError("operator evidence has a duplicate identity")
		}
		seen[evidenceID] = struct{}{}
	}
	return nil
}

func validateForfeitRevisionSet(commandID uuid.UUID, revisions ForfeitRevisionSet) error {
	identities := []uuid.UUID{
		commandID,
		revisions.ScoreRevisionID.UUID(),
		revisions.SeriesResultRevisionID.UUID(),
		revisions.AuditEventID,
		revisions.OutboxEventID,
		revisions.ProjectionRevisionID,
	}
	if revisions.GameResultRevisionID != nil {
		identities = append(identities, revisions.GameResultRevisionID.UUID())
	}
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if identity == uuid.Nil {
			return forfeitError("missing revision or evidence identity")
		}
		if _, duplicate := seen[identity]; duplicate {
			return forfeitError("duplicate revision or evidence identity")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func validateForfeitAuthority(authority ForfeitAuthority) error {
	if !validForfeitAuthorityHeader(authority) {
		return forfeitError("invalid authority identity or Series")
	}
	if err := validateForfeitAuthorityActors(authority); err != nil {
		return err
	}
	if err := seriesdomain.ValidateGameResultRevisionIDs(authority.CurrentGameResultRevisionIDs); err != nil {
		return forfeitError("invalid retained Game revisions: %v", err)
	}
	if authority.CurrentOrdinal > 0 && authority.Series.Series.CurrentScoreRevisionID == nil {
		return forfeitError("revision ordinal has no current score revision")
	}
	if authority.Current != nil {
		if authority.Current.Validate() != nil || authority.Current.Scope != authority.Scope ||
			!forfeitSeriesHeadsEqual(authority.Current.Series, authority.Series) {
			return forfeitError("invalid current resolution")
		}
	}
	return nil
}

func validForfeitAuthorityHeader(authority ForfeitAuthority) bool {
	return authority.Scope.IsValid() && authority.Revision >= 1 && authority.CurrentOrdinal >= 0 &&
		authority.CurrentSeriesResultOrdinal >= 0 &&
		authority.CurrentProjectionRevision >= 1 && authority.Series.Validate() == nil &&
		authority.Series.Series.ID == authority.Scope.SeriesID &&
		authority.Series.Series.TournamentID == authority.Scope.TournamentID &&
		((authority.Series.Series.CurrentResultRevisionID == nil && authority.CurrentSeriesResultOrdinal == 0) ||
			(authority.Series.Series.CurrentResultRevisionID != nil && authority.CurrentSeriesResultOrdinal > 0))
}

func validateForfeitAuthorityActors(authority ForfeitAuthority) error {
	connected := make(map[uuid.UUID]struct{}, len(authority.ConnectedParticipantIDs))
	for _, participantID := range authority.ConnectedParticipantIDs {
		if !seriesParticipant(authority.Series.Series, participantID) {
			return forfeitError("connected actor is not a Series participant")
		}
		if _, duplicate := connected[participantID]; duplicate {
			return forfeitError("duplicate connected participant")
		}
		connected[participantID] = struct{}{}
	}
	operators := make(map[uuid.UUID]struct{}, len(authority.AuthorizedOperatorIDs))
	for _, operatorID := range authority.AuthorizedOperatorIDs {
		if operatorID == uuid.Nil {
			return forfeitError("empty authorized operator identity")
		}
		if _, duplicate := operators[operatorID]; duplicate {
			return forfeitError("duplicate authorized operator")
		}
		operators[operatorID] = struct{}{}
	}
	return nil
}

func validateForfeitAuthorityForRequest(
	authority ForfeitAuthority,
	request forfeitRequest,
) error {
	series := authority.Series.Series
	if series.State.IsTerminal() || series.WinnerID != nil || series.CurrentResultRevisionID != nil ||
		series.Score.Winner(series.FirstParticipantID, series.SecondParticipantID, series.Format) != nil ||
		!seriesParticipant(series, request.forfeitingParticipantID) {
		return ErrForfeitUnavailable
	}
	if request.source == SourceSurrender {
		return validateSurrenderAuthority(authority, request)
	}
	return validateOperatorForfeitAuthority(authority, request)
}

func validateSurrenderAuthority(authority ForfeitAuthority, request forfeitRequest) error {
	if request.actorID != request.forfeitingParticipantID {
		return domain.ErrAssignmentParticipant
	}
	if !slices.Contains(authority.ConnectedParticipantIDs, request.actorID) {
		return ErrSurrenderDisconnected
	}
	if !liveForfeitSeriesState(authority.Series) || request.expectedGame == nil ||
		request.revisions.GameResultRevisionID == nil {
		return ErrForfeitUnavailable
	}
	game, _, found := currentForfeitGame(authority.Series.Series)
	if !found || !liveForfeitGameState(game.State) ||
		!forfeitGameMatchesExpectation(game, *request.expectedGame) {
		return ErrForfeitAuthorityConflict
	}
	return nil
}

func validateOperatorForfeitAuthority(authority ForfeitAuthority, request forfeitRequest) error {
	if !slices.Contains(authority.AuthorizedOperatorIDs, request.actorID) {
		return ErrOperatorForfeitUnauthorized
	}
	if request.operatorEvidence == nil || request.operatorEvidence.Validate() != nil {
		return forfeitError("invalid operator evidence")
	}
	series := authority.Series
	game, _, hasGame := currentForfeitGame(series.Series)
	if series.Series.State == domain.SeriesStateReady {
		return validatePreStartOperatorForfeit(game, hasGame, request)
	}
	if !liveForfeitSeriesState(series) || request.expectedGame == nil ||
		request.revisions.GameResultRevisionID == nil || !hasGame ||
		!liveForfeitGameState(game.State) || !forfeitGameMatchesExpectation(game, *request.expectedGame) {
		return ErrForfeitUnavailable
	}
	return nil
}

func validatePreStartOperatorForfeit(
	game domain.Game,
	hasGame bool,
	request forfeitRequest,
) error {
	if request.revisions.GameResultRevisionID != nil {
		return forfeitError("pre-start forfeit cannot invent a Game result")
	}
	if !hasGame {
		if request.expectedGame != nil {
			return ErrForfeitAuthorityConflict
		}
		return nil
	}
	if request.expectedGame == nil ||
		(game.State != domain.GameStatePlanned && game.State != domain.GameStateReady) ||
		!forfeitGameMatchesExpectation(game, *request.expectedGame) {
		return ErrForfeitAuthorityConflict
	}
	return nil
}

func validForfeitText(value string, maxBytes int) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validForfeitRuleID(value string) bool {
	if len(value) == 0 || len(value) > maxForfeitRuleIDBytes {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' ||
			character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func forfeitError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidForfeit, fmt.Sprintf(format, arguments...))
}
