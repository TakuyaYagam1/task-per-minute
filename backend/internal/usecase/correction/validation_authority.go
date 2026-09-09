package correction

import (
	"crypto/sha256"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func preflightCorrectionInputs(command Command, authority Authority) error {
	if !correctionInputScopeMatches(command, authority) {
		return rejectCorrection(
			RejectionCrossTournament, ErrInvalid, "correction command scope differs from authority",
		)
	}
	if !validCorrectionInputTimes(command) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid correction command time bounds",
		)
	}
	if len(command.Explanation) == 0 || len(command.Explanation) > maxCorrectionExplanationBytes {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid correction explanation bounds",
		)
	}
	if command.Expected.TournamentRevision <= 0 {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid expected tournament revision",
		)
	}
	if !validCorrectionInputBounds(command) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid correction command bounds",
		)
	}
	if err := validateCorrectionIntentPayloads(command); err != nil {
		return err
	}
	return preflightCorrectionAuthority(authority)
}

func correctionInputScopeMatches(command Command, authority Authority) bool {
	return command.TournamentID == authority.Series.TournamentID &&
		command.SeriesID == authority.Series.ID &&
		command.GameID == authority.GameResult.Scope.GameID
}

func validCorrectionInputTimes(command Command) bool {
	if !validCorrectionTime(command.RequestedAt) ||
		!validCorrectionTime(command.Expected.TargetProjection.CreatedAt()) ||
		!validCorrectionTime(command.Expected.ScoreProjection.CreatedAt()) ||
		!validCorrectionTime(command.Expected.SeriesProjection.CreatedAt()) {
		return false
	}
	return command.Patch.SolveMetadata.SolvedAt == nil ||
		validCorrectionTime(*command.Patch.SolveMetadata.SolvedAt)
}

func validCorrectionInputBounds(command Command) bool {
	return len(command.Fields) > 0 && len(command.Fields) <= maxCorrectionFields &&
		len(command.UnlockIntents) <= maxCorrectionUnlockIntents &&
		len(command.ProjectionIntents) > 0 &&
		len(command.ProjectionIntents) <= maxCorrectionDAGProjections
}

func validateCorrectionIntentPayloads(command Command) error {
	totalPayload := 0
	for _, intent := range command.ProjectionIntents {
		payloadSize := len(intent.Payload)
		if payloadSize == 0 || payloadSize > resultprojection.MaxRecordedProjectionDecisionBytes ||
			payloadSize > maxCorrectionDAGPayloadBytes-totalPayload ||
			sha256.Sum256(intent.Payload) != intent.PayloadDigest ||
			!validCorrectionTime(intent.ExpectedRevision.CreatedAt()) {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid projection intent payload",
			)
		}
		totalPayload += payloadSize
	}
	return nil
}

func preflightCorrectionAuthority(authority Authority) error {
	if err := preflightCorrectionDAGResults(authority.DAG); err != nil {
		return err
	}
	if authority.TournamentRevision <= 0 {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid tournament revision",
		)
	}
	maxSlots := authority.Series.Format.WinsRequired()*2 - 1
	if !validCorrectionAuthorityBounds(authority, maxSlots) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid correction authority bounds",
		)
	}
	if !validCorrectionAuthorityTimes(authority) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid correction authority time bounds",
		)
	}
	if err := validateCorrectionAttemptBounds(authority); err != nil {
		return err
	}
	if err := validateCorrectionDecisionPayloads(authority); err != nil {
		return err
	}
	return validateCorrectionEventTimes(authority.CutoffEvents)
}

func validCorrectionAuthorityBounds(authority Authority, maxSlots int) bool {
	readinessParticipants := len(authority.Readiness.ParticipantIDs)
	validReadinessBounds := readinessParticipants == 2 ||
		(readinessParticipants == 0 && correctionCanOmitReadiness(authority.TournamentState))
	return maxSlots > 0 && len(authority.Series.Slots) <= maxSlots &&
		len(authority.Score.Attempts) <= maxSlots &&
		validReadinessBounds &&
		len(authority.Reservations) <= maxCorrectionReservations &&
		len(authority.Decisions) > 0 && len(authority.Decisions) <= maxCorrectionProjectionDecisions &&
		len(authority.CutoffEvents) <= maxCorrectionCutoffEvents
}

func validCorrectionAuthorityTimes(authority Authority) bool {
	if !validCorrectionTime(authority.GameResult.RecordedAt) ||
		!validCorrectionTime(authority.Score.RecordedAt) ||
		!validCorrectionTime(authority.SeriesResult.RecordedAt) ||
		!validCorrectionTime(authority.GameResult.SourceProjection.CreatedAt()) ||
		!validCorrectionTime(authority.Score.SourceProjection.CreatedAt()) ||
		!validCorrectionTime(authority.SeriesResult.SourceProjection.CreatedAt()) {
		return false
	}
	return authority.CurrentSolve.SolvedAt == nil ||
		validCorrectionTime(*authority.CurrentSolve.SolvedAt)
}

func validateCorrectionAttemptBounds(authority Authority) error {
	totalAttempts := 0
	for _, slot := range authority.Series.Slots {
		if len(slot.Attempts) > maxCorrectionSeriesAttempts-totalAttempts {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid correction attempt bounds",
			)
		}
		totalAttempts += len(slot.Attempts)
	}
	return nil
}

func validateCorrectionDecisionPayloads(authority Authority) error {
	totalPayload := 0
	for _, decision := range authority.Decisions {
		payloadSize := len(decision.Payload)
		if payloadSize == 0 || payloadSize > resultprojection.MaxRecordedProjectionDecisionBytes ||
			payloadSize > maxCorrectionDAGPayloadBytes-totalPayload ||
			sha256.Sum256(decision.Payload) != decision.PayloadDigest ||
			!validCorrectionTime(decision.RecordedAt) {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid recorded decision payload",
			)
		}
		totalPayload += payloadSize
	}
	return nil
}

func validateCorrectionEventTimes(events []CutoffEvent) error {
	for _, event := range events {
		if !validCorrectionTime(event.OccurredAt) {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid correction event time bounds",
			)
		}
	}
	return nil
}

func validateCorrectionCommandShape(command Command) error {
	if len(command.Explanation) == 0 || len(command.Explanation) > maxCorrectionExplanationBytes {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid correction explanation bounds",
		)
	}
	if !validCorrectionCommandShape(command) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid correction command",
		)
	}
	if !command.Confirmed {
		return rejectCorrection(
			RejectionIncomplete, ErrInvalid, "explicit confirmation is required",
		)
	}
	return nil
}

func validCorrectionCommandShape(command Command) bool {
	return command.TournamentID != uuid.Nil && command.SeriesID != uuid.Nil && command.GameID != uuid.Nil &&
		command.CommandID != uuid.Nil && command.CascadeCommandID != uuid.Nil && command.OperatorID != uuid.Nil &&
		!command.NextResultRevisionID.IsZero() && !command.NextScoreRevisionID.IsZero() &&
		!command.NextSeriesResultRevisionID.IsZero() && command.NextReadinessRevisionID != uuid.Nil &&
		validCorrectionTime(command.RequestedAt) && validCorrectionReason(command.Reason) &&
		utf8.ValidString(command.Explanation) && correctionSafeString(command.Explanation) &&
		strings.TrimSpace(command.Explanation) == command.Explanation
}

func correctionSafeString(value string) bool {
	return value != "" && strings.IndexFunc(value, unicode.IsControl) == -1
}

func validCorrectionReason(reason Reason) bool {
	switch reason {
	case ReasonScorekeepingError,
		ReasonVerifiedSubmission,
		ReasonOperatorRuling:
		return true
	default:
		return false
	}
}

func validateCorrectionAuthority(
	authority Authority,
	snapshot resultprojection.RevisionDAGSnapshot,
) error {
	if !validCorrectionAuthorityShape(authority) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid correction authority",
		)
	}
	if !correctionAuthorityScopesMatch(authority) {
		return rejectCorrection(
			RejectionCrossTournament, ErrInvalid, "correction authority scopes differ",
		)
	}
	game, found := findCorrectionSeriesGame(authority.Series, authority.GameResult.Scope.GameID)
	if !correctionAuthorityHeadsMatch(authority, game, found) {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "authority heads do not match the current Series",
		)
	}
	if !correctionSolveMetadataValid(
		authority.CurrentSolve,
		game.ResultReason == domain.GameResultReasonSolved,
		authority.GameResult.SourceProjection.CreatedAt(),
		authority.GameResult.RecordedAt,
	) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid current solve metadata",
		)
	}
	if !correctionAuthorityPayloadHeadsMatch(authority) {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "authority head payload changed",
		)
	}
	if err := validateCorrectionReadiness(authority.Readiness, authority.Series, authority.TournamentState); err != nil {
		return err
	}
	if err := validateCorrectionReservations(authority, snapshot); err != nil {
		return err
	}
	if _, err := resultprojection.RebuildOfficialProjections(resultprojection.ProjectionRebuildInput{
		DAG: authority.DAG, Decisions: authority.Decisions,
	}); err != nil {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid recorded decisions",
		)
	}
	return nil
}

func validCorrectionAuthorityShape(authority Authority) bool {
	return authority.TournamentState.IsValid() && authority.Series.Validate() == nil &&
		authority.GameResult.Validate() == nil && authority.Score.Validate() == nil &&
		authority.SeriesResult.Validate() == nil && authority.SeriesRevision > 0 &&
		authority.AttemptRevision > 0 &&
		authority.SeriesRevision != resultusecase.SeriesRowRevision(math.MaxInt64) &&
		authority.AttemptRevision != resultusecase.AttemptRowRevision(math.MaxInt64) &&
		authority.GameResult.Ordinal != math.MaxInt && authority.Score.Ordinal != math.MaxInt &&
		authority.SeriesResult.Ordinal != math.MaxInt
}

func correctionAuthorityScopesMatch(authority Authority) bool {
	return authority.Series.State.IsTerminal() &&
		authority.GameResult.Scope.Kind == resultusecase.OfficialResultSubjectGame &&
		authority.SeriesResult.Scope.Kind == resultusecase.OfficialResultSubjectSeries &&
		authority.GameResult.Scope.TournamentID == authority.Series.TournamentID &&
		authority.GameResult.Scope.SeriesID == authority.Series.ID &&
		authority.SeriesResult.Scope.TournamentID == authority.Series.TournamentID &&
		authority.SeriesResult.Scope.SeriesID == authority.Series.ID &&
		authority.Score.Scope == (resultusecase.SeriesScoreRevisionScope{
			TournamentID: authority.Series.TournamentID, SeriesID: authority.Series.ID,
		})
}

func correctionAuthorityHeadsMatch(authority Authority, game domain.Game, found bool) bool {
	return found && game.ResultRevisionID != nil && *game.ResultRevisionID == authority.GameResult.ID &&
		game.State == authority.GameResult.Outcome.GameState &&
		game.ResultReason == authority.GameResult.Outcome.GameReason &&
		correctionUUIDPointersEqual(game.WinnerID, authority.GameResult.Outcome.WinnerID) &&
		authority.Series.CurrentScoreRevisionID != nil &&
		*authority.Series.CurrentScoreRevisionID == authority.Score.ID &&
		authority.Series.CurrentResultRevisionID != nil &&
		*authority.Series.CurrentResultRevisionID == authority.SeriesResult.ID
}

func correctionAuthorityPayloadHeadsMatch(authority Authority) bool {
	return persistedSeriesScoreHeadMatches(resultusecase.SeriesScoreRevisionAuthority{
		Scope: authority.Score.Scope, PersistedSeries: authority.Series,
		CurrentHead: &authority.Score,
	}) && officialPersistedHeadMatches(resultusecase.OfficialResultRevisionAuthority{
		Scope: authority.GameResult.Scope, PersistedSeries: authority.Series,
		CurrentHead: &authority.GameResult,
	}) && officialPersistedHeadMatches(resultusecase.OfficialResultRevisionAuthority{
		Scope: authority.SeriesResult.Scope, PersistedSeries: authority.Series,
		CurrentHead: &authority.SeriesResult,
	}) && correctionDAGHeadsMatch(authority)
}

func correctionDAGHeadsMatch(authority Authority) bool {
	gameFound := false
	seriesFound := false
	for _, input := range authority.DAG.Inputs() {
		if input.NoGame != nil {
			continue
		}
		switch input.Result.Scope {
		case authority.GameResult.Scope:
			if gameFound || !officialResultRevisionHeadsEqual(input.Result, authority.GameResult) {
				return false
			}
			gameFound = true
		case authority.SeriesResult.Scope:
			if seriesFound || input.Score == nil ||
				!officialResultRevisionHeadsEqual(input.Result, authority.SeriesResult) ||
				!correctionSeriesScoreRevisionHeadsEqual(*input.Score, authority.Score) {
				return false
			}
			seriesFound = true
		}
	}
	return gameFound && seriesFound
}

func officialResultRevisionHeadsEqual(
	first resultusecase.OfficialResultRevisionHead,
	second resultusecase.OfficialResultRevisionHead,
) bool {
	return first.Scope == second.Scope && first.ID == second.ID &&
		correctionOfficialResultRevisionIDPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.Ordinal == second.Ordinal && first.CommandID == second.CommandID &&
		correctionResultActorsEqual(first.Actor, second.Actor) &&
		correctionOfficialResultOutcomesEqual(first.Outcome, second.Outcome) &&
		correctionDerivedRevisionsEqual(first.SourceProjection, second.SourceProjection) &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func validateCorrectionReadiness(
	readiness Readiness,
	series domain.Series,
	tournamentState domain.TournamentState,
) error {
	if correctionReadinessIsEmpty(readiness) && correctionCanOmitReadiness(tournamentState) {
		return nil
	}
	if readiness.TournamentID != series.TournamentID || readiness.OwnerID != series.ID ||
		readiness.WaveID == uuid.Nil ||
		readiness.WindowID == uuid.Nil || readiness.RevisionID == uuid.Nil || readiness.Revision <= 0 ||
		readiness.Revision == math.MaxInt64 || readiness.State != ReadinessOpen ||
		len(readiness.ParticipantIDs) != 2 {
		return rejectCorrection(
			RejectionCrossTournament, ErrInvalid, "readiness does not belong to the corrected Series",
		)
	}
	seen := make(map[uuid.UUID]struct{}, len(readiness.ParticipantIDs))
	for _, participantID := range readiness.ParticipantIDs {
		if participantID == uuid.Nil {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid readiness participant",
			)
		}
		if _, duplicate := seen[participantID]; duplicate {
			return rejectCorrection(
				RejectionIdentityAlias, ErrInvalid, "duplicate readiness participant",
			)
		}
		seen[participantID] = struct{}{}
	}
	if _, first := seen[series.FirstParticipantID]; !first {
		return rejectCorrection(
			RejectionCrossTournament, ErrInvalid, "readiness participant scope differs",
		)
	}
	if _, second := seen[series.SecondParticipantID]; !second {
		return rejectCorrection(
			RejectionCrossTournament, ErrInvalid, "readiness participant scope differs",
		)
	}
	return nil
}

func correctionCanOmitReadiness(tournamentState domain.TournamentState) bool {
	return tournamentState == domain.TournamentStateGolden || tournamentState == domain.TournamentStatePlayoffs
}

func correctionReadinessIsEmpty(readiness Readiness) bool {
	return readiness.TournamentID == uuid.Nil && readiness.OwnerID == uuid.Nil &&
		readiness.WaveID == uuid.Nil && readiness.WindowID == uuid.Nil &&
		readiness.RevisionID == uuid.Nil && readiness.Revision == 0 &&
		readiness.State == "" && len(readiness.ParticipantIDs) == 0
}

func validateCorrectionReservations(
	authority Authority,
	snapshot resultprojection.RevisionDAGSnapshot,
) error {
	seen := make(map[uuid.UUID]struct{}, len(authority.Reservations))
	known := correctionRevisionSet(snapshot)
	for _, reservation := range authority.Reservations {
		if reservation.ID == uuid.Nil || reservation.SourceRevisionID.IsZero() ||
			reservation.Revision <= 0 || reservation.Revision == math.MaxInt64 ||
			reservation.EvidenceDigest == ([sha256.Size]byte{}) {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid correction reservation",
			)
		}
		if reservation.TournamentID != authority.Series.TournamentID || reservation.OwnerID == uuid.Nil {
			return rejectCorrection(
				RejectionCrossTournament, ErrInvalid,
				"reservation does not belong to the corrected tournament",
			)
		}
		if _, exists := known[reservation.SourceRevisionID]; !exists {
			return rejectCorrection(
				RejectionStale, ErrInvalid, "reservation source is missing",
			)
		}
		if _, duplicate := seen[reservation.ID]; duplicate {
			return rejectCorrection(
				RejectionIdentityAlias, ErrInvalid, "duplicate reservation identity",
			)
		}
		seen[reservation.ID] = struct{}{}
	}
	return nil
}

func buildCorrectionExpectation(
	authority Authority,
	cutoff Cutoff,
	snapshot resultprojection.RevisionDAGSnapshot,
) (Expectation, domain.DerivedRevision, error) {
	current := make(map[domain.ArtifactRef]domain.DerivedRevision)
	byID := make(map[domain.DerivedRevisionID]domain.DerivedRevision, len(snapshot.Projections))
	for _, projection := range snapshot.Projections {
		revision := projection.Revision()
		byID[revision.ID()] = revision
		prior, exists := current[revision.Artifact()]
		if !exists || prior.RevisionNo() < revision.RevisionNo() {
			current[revision.Artifact()] = revision
		}
	}
	target, exists := byID[cutoff.TargetRevisionID()]
	if !exists || target.Artifact() != (domain.ArtifactRef{
		Kind: domain.ArtifactKindGameResult, EntityID: authority.GameResult.Scope.GameID,
	}) || !correctionDerivedRevisionsEqual(target, authority.GameResult.SourceProjection) {
		return Expectation{}, domain.DerivedRevision{}, rejectCorrection(
			RejectionStale, ErrInvalid, "target result projection changed",
		)
	}
	score := current[domain.ArtifactRef{
		Kind: domain.ArtifactKindSeriesScore, EntityID: authority.Series.ID,
	}]
	series := current[domain.ArtifactRef{
		Kind: domain.ArtifactKindSeriesResult, EntityID: authority.Series.ID,
	}]
	if !correctionDerivedRevisionsEqual(score, authority.Score.SourceProjection) ||
		!correctionDerivedRevisionsEqual(series, authority.SeriesResult.SourceProjection) {
		return Expectation{}, domain.DerivedRevision{}, rejectCorrection(
			RejectionStale, ErrInvalid, "score or Series projection changed",
		)
	}
	expectation := Expectation{
		TournamentState: authority.TournamentState, TournamentRevision: authority.TournamentRevision,
		CutoffEventDigest: correctionCutoffEventSetDigest(authority.CutoffEvents),
		TargetProjection:  target, ScoreProjection: score, SeriesProjection: series,
		ResultRevisionID: authority.GameResult.ID, ScoreRevisionID: authority.Score.ID,
		SeriesResultRevisionID: authority.SeriesResult.ID,
		SeriesRevision:         authority.SeriesRevision, AttemptRevision: authority.AttemptRevision,
		DAGDigest:          correctionDAGDigest(snapshot),
		ReservationDigest:  correctionReservationSetDigest(authority.Reservations),
		DecisionDigest:     correctionDecisionSetDigest(authority.Decisions),
		ReadinessDigest:    correctionReadinessDigest(authority.Readiness),
		CurrentSolveDigest: correctionSolveMetadataDigest(authority.CurrentSolve),
	}
	return expectation, target, nil
}
