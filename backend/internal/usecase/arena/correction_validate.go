package arena

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	maxCorrectionExplanationBytes = 512
	maxCorrectionFields           = 3
	maxCorrectionReservations     = 4096
	maxCorrectionUnlockIntents    = 4096
	maxCorrectionSeriesAttempts   = 16
)

type CorrectionReason string

const (
	CorrectionReasonScorekeepingError  CorrectionReason = "scorekeeping_error"
	CorrectionReasonVerifiedSubmission CorrectionReason = "verified_submission"
	CorrectionReasonOperatorRuling     CorrectionReason = "operator_ruling"
)

type CorrectionField string

const (
	CorrectionFieldWinner        CorrectionField = "winner"
	CorrectionFieldResultReason  CorrectionField = "result_reason"
	CorrectionFieldSolveMetadata CorrectionField = "solve_metadata"
)

type CorrectionSolveMetadata struct {
	SolvedAt       *time.Time
	SubmissionID   *uuid.UUID
	EvidenceDigest [sha256.Size]byte
}

func (m CorrectionSolveMetadata) Clone() CorrectionSolveMetadata {
	clone := m
	clone.SolvedAt = cloneTimePointer(m.SolvedAt)
	clone.SubmissionID = cloneUUIDPointer(m.SubmissionID)
	return clone
}

type CorrectionPatch struct {
	State         domain.ArenaGameState
	Reason        domain.ArenaGameResultReason
	WinnerID      *uuid.UUID
	SolveMetadata CorrectionSolveMetadata
}

type CorrectionReadinessState string

const (
	CorrectionReadinessOpen   CorrectionReadinessState = "open"
	CorrectionReadinessClosed CorrectionReadinessState = "closed"
)

type CorrectionReadiness struct {
	TournamentID   uuid.UUID
	OwnerID        uuid.UUID
	WaveID         uuid.UUID
	WindowID       uuid.UUID
	RevisionID     uuid.UUID
	Revision       int64
	State          CorrectionReadinessState
	ParticipantIDs []uuid.UUID
}

type CorrectionReservation struct {
	ID               uuid.UUID
	TournamentID     uuid.UUID
	OwnerID          uuid.UUID
	SourceRevisionID domain.ArenaDerivedRevisionID
	Revision         int64
	Used             bool
	Disclosed        bool
	EvidenceDigest   [sha256.Size]byte
}

type CorrectionUnlockIntent struct {
	ReservationID     uuid.UUID
	TournamentID      uuid.UUID
	OwnerID           uuid.UUID
	SourceRevisionID  domain.ArenaDerivedRevisionID
	ExpectedRevision  int64
	ExpectedUsed      bool
	ExpectedDisclosed bool
	EvidenceDigest    [sha256.Size]byte
	BindingDigest     [sha256.Size]byte
}

func NewCorrectionUnlockIntent(reservation CorrectionReservation) CorrectionUnlockIntent {
	intent := CorrectionUnlockIntent{
		ReservationID: reservation.ID, TournamentID: reservation.TournamentID,
		OwnerID: reservation.OwnerID, SourceRevisionID: reservation.SourceRevisionID,
		ExpectedRevision: reservation.Revision, ExpectedUsed: reservation.Used,
		ExpectedDisclosed: reservation.Disclosed, EvidenceDigest: reservation.EvidenceDigest,
	}
	intent.BindingDigest = correctionUnlockIntentDigest(intent)
	return intent
}

type CorrectionProjectionIntent struct {
	ExpectedRevision domain.ArenaDerivedRevision
	NextRevisionID   domain.ArenaDerivedRevisionID
	DecisionID       uuid.UUID
	Payload          []byte
	PayloadDigest    [sha256.Size]byte
}

func NewCorrectionProjectionIntent(
	expected domain.ArenaDerivedRevision,
	nextRevisionID domain.ArenaDerivedRevisionID,
	decisionID uuid.UUID,
	payload []byte,
) CorrectionProjectionIntent {
	return CorrectionProjectionIntent{
		ExpectedRevision: expected, NextRevisionID: nextRevisionID, DecisionID: decisionID,
		Payload: append([]byte(nil), payload...), PayloadDigest: sha256.Sum256(payload),
	}
}

func (i CorrectionProjectionIntent) Clone() CorrectionProjectionIntent {
	clone := i
	clone.Payload = append([]byte(nil), i.Payload...)
	return clone
}

type CorrectionExpectation struct {
	TournamentState        domain.ArenaTournamentState
	TournamentRevision     int64
	CutoffEventDigest      [sha256.Size]byte
	TargetProjection       domain.ArenaDerivedRevision
	ScoreProjection        domain.ArenaDerivedRevision
	SeriesProjection       domain.ArenaDerivedRevision
	ResultRevisionID       domain.ArenaOfficialResultRevisionID
	ScoreRevisionID        domain.ArenaSeriesScoreRevisionID
	SeriesResultRevisionID domain.ArenaOfficialResultRevisionID
	SeriesRevision         ArenaSeriesRowRevision
	AttemptRevision        ArenaAttemptRowRevision
	DAGDigest              [sha256.Size]byte
	ReservationDigest      [sha256.Size]byte
	DecisionDigest         [sha256.Size]byte
	ReadinessDigest        [sha256.Size]byte
	CurrentSolveDigest     [sha256.Size]byte
}

type CorrectionCommand struct {
	TournamentID               uuid.UUID
	SeriesID                   uuid.UUID
	GameID                     uuid.UUID
	CommandID                  uuid.UUID
	CascadeCommandID           uuid.UUID
	OperatorID                 uuid.UUID
	Confirmed                  bool
	Reason                     CorrectionReason
	Explanation                string
	RequestedAt                time.Time
	Expected                   CorrectionExpectation
	Patch                      CorrectionPatch
	Fields                     []CorrectionField
	NextResultRevisionID       domain.ArenaOfficialResultRevisionID
	NextScoreRevisionID        domain.ArenaSeriesScoreRevisionID
	NextSeriesResultRevisionID domain.ArenaOfficialResultRevisionID
	NextReadinessRevisionID    uuid.UUID
	ProjectionIntents          []CorrectionProjectionIntent
	UnlockIntents              []CorrectionUnlockIntent
}

type CorrectionAuthority struct {
	TournamentState    domain.ArenaTournamentState
	TournamentRevision int64
	DAG                RevisionDAG
	Series             domain.ArenaSeries
	GameResult         OfficialResultRevisionHead
	Score              SeriesScoreRevisionHead
	SeriesResult       OfficialResultRevisionHead
	SeriesRevision     ArenaSeriesRowRevision
	AttemptRevision    ArenaAttemptRowRevision
	CurrentSolve       CorrectionSolveMetadata
	Readiness          CorrectionReadiness
	Reservations       []CorrectionReservation
	Decisions          []RecordedProjectionDecision
	CutoffEvents       []CorrectionCutoffEvent
}

type CorrectionValidation struct {
	command       CorrectionCommand
	authority     CorrectionAuthority
	cutoff        CorrectionCutoff
	target        domain.ArenaDerivedRevision
	unlocks       []CorrectionUnlockIntent
	snapshot      RevisionDAGSnapshot
	bindingDigest [sha256.Size]byte
}

func (v CorrectionValidation) Validate() error {
	if err := preflightCorrectionInputs(v.command, v.authority); err != nil {
		return err
	}
	if correctionValidationDigest(v) != v.bindingDigest {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "correction validation was spliced",
		)
	}
	validated, err := validateCorrection(
		cloneCorrectionCommand(v.command), cloneCorrectionAuthority(v.authority),
	)
	if err != nil {
		return err
	}
	if validated.bindingDigest != v.bindingDigest ||
		!arenaDerivedRevisionsEqual(validated.target, v.target) {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "correction validation was spliced",
		)
	}
	return nil
}

func (v CorrectionValidation) TargetRevision() domain.ArenaDerivedRevision {
	return v.target
}

func (v CorrectionValidation) Descendants() []domain.ArenaDerivedRevision {
	return v.cutoff.Descendants()
}

func (v CorrectionValidation) UnlockIntents() []CorrectionUnlockIntent {
	return append([]CorrectionUnlockIntent(nil), v.unlocks...)
}

func ValidateCorrection(
	command CorrectionCommand,
	authority CorrectionAuthority,
) (CorrectionValidation, error) {
	if err := preflightCorrectionInputs(command, authority); err != nil {
		return CorrectionValidation{}, err
	}
	return validateCorrection(cloneCorrectionCommand(command), cloneCorrectionAuthority(authority))
}

func validateCorrection(
	command CorrectionCommand,
	authority CorrectionAuthority,
) (CorrectionValidation, error) {
	if err := validateCorrectionCommandShape(command); err != nil {
		return CorrectionValidation{}, err
	}
	snapshot := authority.DAG.Snapshot()
	cutoffInput := CorrectionCutoffInput{
		DAG: authority.DAG, TournamentID: command.TournamentID,
		TargetRevisionID: command.Expected.TargetProjection.ID(),
		TournamentState:  authority.TournamentState, Events: authority.CutoffEvents,
	}
	if err := preflightCorrectionCutoff(cutoffInput, snapshot); err != nil {
		return CorrectionValidation{}, err
	}
	if err := validateCorrectionAuthority(authority, snapshot); err != nil {
		return CorrectionValidation{}, err
	}
	cutoff, err := evaluateCorrectionCutoffPrepared(cutoffInput, snapshot)
	if err != nil {
		return CorrectionValidation{}, err
	}
	actual, target, err := buildCorrectionExpectation(authority, cutoff, snapshot)
	if err != nil {
		return CorrectionValidation{}, err
	}
	if !correctionExpectationsEqual(command.Expected, actual) {
		return CorrectionValidation{}, rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "correction expectation changed",
		)
	}
	if err := validateCorrectionPatch(command, authority); err != nil {
		return CorrectionValidation{}, err
	}
	intents, err := validateCorrectionProjectionIntents(command, cutoff, target)
	if err != nil {
		return CorrectionValidation{}, err
	}
	unlocks, err := validateCorrectionUnlockIntents(command, authority, cutoff, target)
	if err != nil {
		return CorrectionValidation{}, err
	}
	if err := validateCorrectionUUIDRoles(command, authority, intents, snapshot); err != nil {
		return CorrectionValidation{}, err
	}
	command.ProjectionIntents = intents
	command.UnlockIntents = unlocks
	command.Fields = canonicalCorrectionFields(command.Fields)
	authority.Reservations = canonicalCorrectionReservations(authority.Reservations)
	authority.Decisions = canonicalCorrectionDecisions(authority.Decisions)
	authority.Readiness.ParticipantIDs = canonicalCorrectionParticipants(
		authority.Readiness.ParticipantIDs,
	)
	validation := CorrectionValidation{
		command: command, authority: authority, cutoff: cutoff, target: target, unlocks: unlocks,
		snapshot: snapshot,
	}
	validation.bindingDigest = correctionValidationDigest(validation)
	return validation, nil
}

func NewCorrectionExpectation(
	authority CorrectionAuthority,
	targetRevisionID domain.ArenaDerivedRevisionID,
) (CorrectionExpectation, error) {
	if err := preflightCorrectionAuthority(authority); err != nil {
		return CorrectionExpectation{}, err
	}
	cloned := cloneCorrectionAuthority(authority)
	snapshot := cloned.DAG.Snapshot()
	cutoffInput := CorrectionCutoffInput{
		DAG: cloned.DAG, TournamentID: cloned.Series.TournamentID,
		TargetRevisionID: targetRevisionID, TournamentState: cloned.TournamentState,
		Events: cloned.CutoffEvents,
	}
	if err := preflightCorrectionCutoff(cutoffInput, snapshot); err != nil {
		return CorrectionExpectation{}, err
	}
	if err := validateCorrectionAuthority(cloned, snapshot); err != nil {
		return CorrectionExpectation{}, err
	}
	cutoff, err := evaluateCorrectionCutoffPrepared(cutoffInput, snapshot)
	if err != nil {
		return CorrectionExpectation{}, err
	}
	expectation, _, err := buildCorrectionExpectation(cloned, cutoff, snapshot)
	if err != nil {
		return CorrectionExpectation{}, err
	}
	return expectation, nil
}

//nolint:gocyclo // Cheap preflight intentionally checks every caller-controlled allocation dimension.
func preflightCorrectionInputs(command CorrectionCommand, authority CorrectionAuthority) error {
	if command.TournamentID != authority.Series.TournamentID ||
		command.SeriesID != authority.Series.ID ||
		command.GameID != authority.GameResult.Scope.GameID {
		return rejectCorrection(
			CorrectionRejectionCrossTournament, ErrInvalidCorrection, "correction command scope differs from authority",
		)
	}
	if !validCorrectionTime(command.RequestedAt) ||
		!validCorrectionTime(command.Expected.TargetProjection.CreatedAt()) ||
		!validCorrectionTime(command.Expected.ScoreProjection.CreatedAt()) ||
		!validCorrectionTime(command.Expected.SeriesProjection.CreatedAt()) ||
		(command.Patch.SolveMetadata.SolvedAt != nil &&
			!validCorrectionTime(*command.Patch.SolveMetadata.SolvedAt)) {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction command time bounds",
		)
	}
	if len(command.Explanation) == 0 || len(command.Explanation) > maxCorrectionExplanationBytes {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction explanation bounds",
		)
	}
	if command.Expected.TournamentRevision <= 0 {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid expected tournament revision",
		)
	}
	if len(command.Fields) == 0 || len(command.Fields) > maxCorrectionFields ||
		len(command.UnlockIntents) > maxCorrectionUnlockIntents ||
		len(command.ProjectionIntents) == 0 ||
		len(command.ProjectionIntents) > maxCorrectionDAGProjections {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction command bounds",
		)
	}
	totalPayload := 0
	for _, intent := range command.ProjectionIntents {
		payloadSize := len(intent.Payload)
		if payloadSize == 0 || payloadSize > MaxRecordedProjectionDecisionBytes ||
			payloadSize > maxCorrectionDAGPayloadBytes-totalPayload ||
			sha256.Sum256(intent.Payload) != intent.PayloadDigest ||
			!validCorrectionTime(intent.ExpectedRevision.CreatedAt()) {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid projection intent payload",
			)
		}
		totalPayload += payloadSize
	}
	return preflightCorrectionAuthority(authority)
}

//nolint:gocyclo // Cheap preflight intentionally checks every nested authority allocation dimension.
func preflightCorrectionAuthority(authority CorrectionAuthority) error {
	if err := preflightCorrectionDAGResults(authority.DAG); err != nil {
		return err
	}
	if authority.TournamentRevision <= 0 {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid tournament revision",
		)
	}
	maxSlots := authority.Series.Format.WinsRequired()*2 - 1
	if maxSlots <= 0 || len(authority.Series.Slots) > maxSlots ||
		len(authority.Score.Attempts) > maxSlots ||
		len(authority.Readiness.ParticipantIDs) != 2 ||
		len(authority.Reservations) > maxCorrectionReservations ||
		len(authority.Decisions) == 0 || len(authority.Decisions) > maxProjectionRebuildDecisions ||
		len(authority.CutoffEvents) > maxCorrectionCutoffEvents {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction authority bounds",
		)
	}
	if !validCorrectionTime(authority.GameResult.RecordedAt) ||
		!validCorrectionTime(authority.Score.RecordedAt) ||
		!validCorrectionTime(authority.SeriesResult.RecordedAt) ||
		!validCorrectionTime(authority.GameResult.SourceProjection.CreatedAt()) ||
		!validCorrectionTime(authority.Score.SourceProjection.CreatedAt()) ||
		!validCorrectionTime(authority.SeriesResult.SourceProjection.CreatedAt()) ||
		(authority.CurrentSolve.SolvedAt != nil && !validCorrectionTime(*authority.CurrentSolve.SolvedAt)) {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction authority time bounds",
		)
	}
	totalAttempts := 0
	for _, slot := range authority.Series.Slots {
		if len(slot.Attempts) > maxCorrectionSeriesAttempts-totalAttempts {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction attempt bounds",
			)
		}
		totalAttempts += len(slot.Attempts)
	}
	totalPayload := 0
	for _, decision := range authority.Decisions {
		payloadSize := len(decision.Payload)
		if payloadSize == 0 || payloadSize > MaxRecordedProjectionDecisionBytes ||
			payloadSize > maxProjectionRebuildDecisionBytes-totalPayload ||
			sha256.Sum256(decision.Payload) != decision.PayloadDigest ||
			!validCorrectionTime(decision.RecordedAt) {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid recorded decision payload",
			)
		}
		totalPayload += payloadSize
	}
	for _, event := range authority.CutoffEvents {
		if !validCorrectionTime(event.OccurredAt) {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction event time bounds",
			)
		}
	}
	return nil
}

//nolint:gocyclo // Command shape validation is a single fail-closed boundary.
func validateCorrectionCommandShape(command CorrectionCommand) error {
	if len(command.Explanation) == 0 || len(command.Explanation) > maxCorrectionExplanationBytes {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction explanation bounds",
		)
	}
	if command.TournamentID == uuid.Nil || command.SeriesID == uuid.Nil || command.GameID == uuid.Nil ||
		command.CommandID == uuid.Nil || command.CascadeCommandID == uuid.Nil || command.OperatorID == uuid.Nil ||
		command.NextResultRevisionID.IsZero() || command.NextScoreRevisionID.IsZero() ||
		command.NextSeriesResultRevisionID.IsZero() || command.NextReadinessRevisionID == uuid.Nil ||
		!validCorrectionTime(command.RequestedAt) || !validCorrectionReason(command.Reason) ||
		!utf8.ValidString(command.Explanation) || !operatorSafeString(command.Explanation) ||
		strings.TrimSpace(command.Explanation) != command.Explanation {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction command",
		)
	}
	if !command.Confirmed {
		return rejectCorrection(
			CorrectionRejectionIncomplete, ErrInvalidCorrection, "explicit confirmation is required",
		)
	}
	return nil
}

func validCorrectionReason(reason CorrectionReason) bool {
	switch reason {
	case CorrectionReasonScorekeepingError,
		CorrectionReasonVerifiedSubmission,
		CorrectionReasonOperatorRuling:
		return true
	default:
		return false
	}
}

//nolint:gocyclo // Authority validation binds all persisted heads and mutable CAS state together.
func validateCorrectionAuthority(
	authority CorrectionAuthority,
	snapshot RevisionDAGSnapshot,
) error {
	if !authority.TournamentState.IsValid() || authority.Series.Validate() != nil ||
		authority.GameResult.Validate() != nil || authority.Score.Validate() != nil ||
		authority.SeriesResult.Validate() != nil || authority.SeriesRevision <= 0 ||
		authority.AttemptRevision <= 0 || authority.SeriesRevision == ArenaSeriesRowRevision(math.MaxInt64) ||
		authority.AttemptRevision == ArenaAttemptRowRevision(math.MaxInt64) ||
		authority.GameResult.Ordinal == math.MaxInt || authority.Score.Ordinal == math.MaxInt ||
		authority.SeriesResult.Ordinal == math.MaxInt {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction authority",
		)
	}
	if !authority.Series.State.IsTerminal() || authority.GameResult.Scope.Kind != OfficialResultSubjectGame ||
		authority.SeriesResult.Scope.Kind != OfficialResultSubjectSeries ||
		authority.GameResult.Scope.TournamentID != authority.Series.TournamentID ||
		authority.GameResult.Scope.SeriesID != authority.Series.ID ||
		authority.SeriesResult.Scope.TournamentID != authority.Series.TournamentID ||
		authority.SeriesResult.Scope.SeriesID != authority.Series.ID ||
		authority.Score.Scope != (SeriesScoreRevisionScope{
			TournamentID: authority.Series.TournamentID, SeriesID: authority.Series.ID,
		}) {
		return rejectCorrection(
			CorrectionRejectionCrossTournament, ErrInvalidCorrection, "correction authority scopes differ",
		)
	}
	game, found := findArenaSeriesGame(authority.Series, authority.GameResult.Scope.GameID)
	if !found || game.ResultRevisionID == nil || *game.ResultRevisionID != authority.GameResult.ID ||
		game.State != authority.GameResult.Outcome.GameState ||
		game.ResultReason != authority.GameResult.Outcome.GameReason ||
		!uuidPointersEqual(game.WinnerID, authority.GameResult.Outcome.WinnerID) ||
		authority.Series.CurrentScoreRevisionID == nil ||
		*authority.Series.CurrentScoreRevisionID != authority.Score.ID ||
		authority.Series.CurrentResultRevisionID == nil ||
		*authority.Series.CurrentResultRevisionID != authority.SeriesResult.ID {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "authority heads do not match the current Series",
		)
	}
	if !correctionSolveMetadataValid(
		authority.CurrentSolve,
		game.ResultReason == domain.ArenaGameResultReasonSolved,
		authority.GameResult.SourceProjection.CreatedAt(),
		authority.GameResult.RecordedAt,
	) {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid current solve metadata",
		)
	}
	if validatePersistedSeriesScoreHead(SeriesScoreRevisionAuthority{
		Scope: authority.Score.Scope, PersistedSeries: authority.Series,
		CurrentHead: &authority.Score,
	}) != nil || validateOfficialPersistedHead(OfficialResultRevisionAuthority{
		Scope: authority.GameResult.Scope, PersistedSeries: authority.Series,
		CurrentHead: &authority.GameResult,
	}) != nil || validateOfficialPersistedHead(OfficialResultRevisionAuthority{
		Scope: authority.SeriesResult.Scope, PersistedSeries: authority.Series,
		CurrentHead: &authority.SeriesResult,
	}) != nil || !correctionDAGHeadsMatch(authority) {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "authority head payload changed",
		)
	}
	if err := validateCorrectionReadiness(authority.Readiness, authority.Series); err != nil {
		return err
	}
	if err := validateCorrectionReservations(authority, snapshot); err != nil {
		return err
	}
	if _, err := RebuildOfficialProjections(ProjectionRebuildInput{
		DAG: authority.DAG, Decisions: authority.Decisions,
	}); err != nil {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid recorded decisions",
		)
	}
	return nil
}

func correctionDAGHeadsMatch(authority CorrectionAuthority) bool {
	gameFound := false
	seriesFound := false
	for _, plan := range authority.DAG.results {
		input := plan.input
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
				!seriesScoreRevisionHeadsEqual(*input.Score, authority.Score) {
				return false
			}
			seriesFound = true
		}
	}
	return gameFound && seriesFound
}

func officialResultRevisionHeadsEqual(
	first OfficialResultRevisionHead,
	second OfficialResultRevisionHead,
) bool {
	return first.Scope == second.Scope && first.ID == second.ID &&
		officialResultRevisionIDPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.Ordinal == second.Ordinal && first.CommandID == second.CommandID &&
		arenaResultActorsEqual(first.Actor, second.Actor) &&
		officialResultOutcomesEqual(first.Outcome, second.Outcome) &&
		arenaDerivedRevisionsEqual(first.SourceProjection, second.SourceProjection) &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func validateCorrectionReadiness(readiness CorrectionReadiness, series domain.ArenaSeries) error {
	if readiness.TournamentID != series.TournamentID || readiness.OwnerID != series.ID ||
		readiness.WaveID == uuid.Nil ||
		readiness.WindowID == uuid.Nil || readiness.RevisionID == uuid.Nil || readiness.Revision <= 0 ||
		readiness.Revision == math.MaxInt64 || readiness.State != CorrectionReadinessOpen ||
		len(readiness.ParticipantIDs) != 2 {
		return rejectCorrection(
			CorrectionRejectionCrossTournament, ErrInvalidCorrection, "readiness does not belong to the corrected Series",
		)
	}
	seen := make(map[uuid.UUID]struct{}, len(readiness.ParticipantIDs))
	for _, participantID := range readiness.ParticipantIDs {
		if participantID == uuid.Nil {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid readiness participant",
			)
		}
		if _, duplicate := seen[participantID]; duplicate {
			return rejectCorrection(
				CorrectionRejectionIdentityAlias, ErrInvalidCorrection, "duplicate readiness participant",
			)
		}
		seen[participantID] = struct{}{}
	}
	if _, first := seen[series.FirstParticipantID]; !first {
		return rejectCorrection(
			CorrectionRejectionCrossTournament, ErrInvalidCorrection, "readiness participant scope differs",
		)
	}
	if _, second := seen[series.SecondParticipantID]; !second {
		return rejectCorrection(
			CorrectionRejectionCrossTournament, ErrInvalidCorrection, "readiness participant scope differs",
		)
	}
	return nil
}

func validateCorrectionReservations(
	authority CorrectionAuthority,
	snapshot RevisionDAGSnapshot,
) error {
	seen := make(map[uuid.UUID]struct{}, len(authority.Reservations))
	known := correctionRevisionSet(snapshot)
	for _, reservation := range authority.Reservations {
		if reservation.ID == uuid.Nil || reservation.SourceRevisionID.IsZero() ||
			reservation.Revision <= 0 || reservation.Revision == math.MaxInt64 ||
			reservation.EvidenceDigest == ([sha256.Size]byte{}) {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid correction reservation",
			)
		}
		if reservation.TournamentID != authority.Series.TournamentID ||
			reservation.OwnerID != authority.Series.ID {
			return rejectCorrection(
				CorrectionRejectionCrossTournament, ErrInvalidCorrection,
				"reservation does not belong to the corrected Series",
			)
		}
		if _, exists := known[reservation.SourceRevisionID]; !exists {
			return rejectCorrection(
				CorrectionRejectionStale, ErrInvalidCorrection, "reservation source is missing",
			)
		}
		if _, duplicate := seen[reservation.ID]; duplicate {
			return rejectCorrection(
				CorrectionRejectionIdentityAlias, ErrInvalidCorrection, "duplicate reservation identity",
			)
		}
		seen[reservation.ID] = struct{}{}
	}
	return nil
}

func buildCorrectionExpectation(
	authority CorrectionAuthority,
	cutoff CorrectionCutoff,
	snapshot RevisionDAGSnapshot,
) (CorrectionExpectation, domain.ArenaDerivedRevision, error) {
	current := make(map[domain.ArenaArtifactRef]domain.ArenaDerivedRevision)
	byID := make(map[domain.ArenaDerivedRevisionID]domain.ArenaDerivedRevision, len(snapshot.Projections))
	for _, projection := range snapshot.Projections {
		revision := projection.Revision()
		byID[revision.ID()] = revision
		prior, exists := current[revision.Artifact()]
		if !exists || prior.RevisionNo() < revision.RevisionNo() {
			current[revision.Artifact()] = revision
		}
	}
	target, exists := byID[cutoff.TargetRevisionID()]
	if !exists || target.Artifact() != (domain.ArenaArtifactRef{
		Kind: domain.ArenaArtifactKindGameResult, EntityID: authority.GameResult.Scope.GameID,
	}) || !arenaDerivedRevisionsEqual(target, authority.GameResult.SourceProjection) {
		return CorrectionExpectation{}, domain.ArenaDerivedRevision{}, rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "target result projection changed",
		)
	}
	score := current[domain.ArenaArtifactRef{
		Kind: domain.ArenaArtifactKindSeriesScore, EntityID: authority.Series.ID,
	}]
	series := current[domain.ArenaArtifactRef{
		Kind: domain.ArenaArtifactKindSeriesResult, EntityID: authority.Series.ID,
	}]
	if !arenaDerivedRevisionsEqual(score, authority.Score.SourceProjection) ||
		!arenaDerivedRevisionsEqual(series, authority.SeriesResult.SourceProjection) {
		return CorrectionExpectation{}, domain.ArenaDerivedRevision{}, rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "score or Series projection changed",
		)
	}
	expectation := CorrectionExpectation{
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

//nolint:gocyclo // Patch validation enumerates terminal and exact-field compatibility rules.
func validateCorrectionPatch(command CorrectionCommand, authority CorrectionAuthority) error {
	current := authority.GameResult.Outcome
	patch := command.Patch
	if command.RequestedAt.Before(authority.GameResult.RecordedAt) ||
		command.RequestedAt.Before(authority.Score.RecordedAt) ||
		command.RequestedAt.Before(authority.SeriesResult.RecordedAt) {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "correction predates a current authority head",
		)
	}
	if !current.GameState.IsTerminal() || current.GameState == domain.ArenaGameStateSuperseded ||
		!patch.State.IsTerminal() || patch.State != current.GameState ||
		!patch.Reason.IsLegalFor(patch.State) {
		return rejectCorrection(
			CorrectionRejectionTerminal, ErrInvalidCorrection, "correction must preserve a terminal result",
		)
	}
	if patch.State == domain.ArenaGameStateCompleted {
		if patch.WinnerID == nil || (*patch.WinnerID != authority.Series.FirstParticipantID &&
			*patch.WinnerID != authority.Series.SecondParticipantID) {
			return rejectCorrection(
				CorrectionRejectionTerminal, ErrInvalidCorrection, "completed correction has no valid winner",
			)
		}
	} else if patch.WinnerID != nil {
		return rejectCorrection(
			CorrectionRejectionTerminal, ErrInvalidCorrection, "non scoring correction has a winner",
		)
	}
	if !correctionSolveMetadataValid(
		patch.SolveMetadata,
		patch.Reason == domain.ArenaGameResultReasonSolved,
		authority.GameResult.SourceProjection.CreatedAt(),
		command.RequestedAt,
	) {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid replacement solve metadata",
		)
	}
	wantFields := correctionChangedFields(current, authority.CurrentSolve, patch)
	gotFields := canonicalCorrectionFields(command.Fields)
	if len(wantFields) == 0 || !correctionFieldsEqual(wantFields, gotFields) {
		return rejectCorrection(
			CorrectionRejectionMalformed, ErrInvalidCorrection, "correction fields are not exact",
		)
	}
	projected := cloneRevisionArenaSeries(authority.Series)
	game, found := findArenaSeriesGamePointer(&projected, command.GameID)
	if !found {
		return rejectCorrection(
			CorrectionRejectionStale, ErrInvalidCorrection, "correction Game is missing",
		)
	}
	game.State = patch.State
	game.ResultReason = patch.Reason
	game.WinnerID = cloneUUIDPointer(patch.WinnerID)
	references, err := seriesScoreAttemptReferencesFromSeries(projected)
	if err != nil {
		return rejectCorrection(
			CorrectionRejectionTerminal, ErrInvalidCorrection, "replacement score is invalid",
		)
	}
	score, err := scoreFromAttemptReferences(
		references, projected.FirstParticipantID, projected.SecondParticipantID, projected.Format,
	)
	if err != nil || (authority.Series.State == domain.ArenaSeriesStateCompleted &&
		score.Winner(projected.FirstParticipantID, projected.SecondParticipantID, projected.Format) == nil) {
		return rejectCorrection(
			CorrectionRejectionTerminal, ErrInvalidCorrection, "replacement makes the Series nonterminal",
		)
	}
	return nil
}

func correctionSolveMetadataValid(
	metadata CorrectionSolveMetadata,
	required bool,
	notBefore time.Time,
	notAfter time.Time,
) bool {
	if !required {
		return metadata.SolvedAt == nil && metadata.SubmissionID == nil &&
			metadata.EvidenceDigest == ([sha256.Size]byte{})
	}
	return metadata.SolvedAt != nil && metadata.SubmissionID != nil &&
		*metadata.SubmissionID != uuid.Nil && metadata.EvidenceDigest != ([sha256.Size]byte{}) &&
		validCorrectionTime(*metadata.SolvedAt) && !metadata.SolvedAt.Before(notBefore) &&
		!metadata.SolvedAt.After(notAfter)
}

func correctionChangedFields(
	current OfficialResultOutcome,
	currentSolve CorrectionSolveMetadata,
	patch CorrectionPatch,
) []CorrectionField {
	fields := make([]CorrectionField, 0, maxCorrectionFields)
	if !uuidPointersEqual(current.WinnerID, patch.WinnerID) {
		fields = append(fields, CorrectionFieldWinner)
	}
	if current.GameReason != patch.Reason {
		fields = append(fields, CorrectionFieldResultReason)
	}
	if !correctionSolveMetadataEqual(currentSolve, patch.SolveMetadata) {
		fields = append(fields, CorrectionFieldSolveMetadata)
	}
	return canonicalCorrectionFields(fields)
}

func canonicalCorrectionFields(fields []CorrectionField) []CorrectionField {
	canonical := append([]CorrectionField(nil), fields...)
	sort.Slice(canonical, func(first, second int) bool { return canonical[first] < canonical[second] })
	return canonical
}

func correctionFieldsEqual(first, second []CorrectionField) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] || !validCorrectionField(first[index]) ||
			(index > 0 && first[index] == first[index-1]) {
			return false
		}
	}
	return true
}

func validCorrectionField(field CorrectionField) bool {
	switch field {
	case CorrectionFieldWinner, CorrectionFieldResultReason, CorrectionFieldSolveMetadata:
		return true
	default:
		return false
	}
}

func validateCorrectionProjectionIntents(
	command CorrectionCommand,
	cutoff CorrectionCutoff,
	target domain.ArenaDerivedRevision,
) ([]CorrectionProjectionIntent, error) {
	want := correctionRebuildRevisions(cutoff, target)
	byExpected := make(map[domain.ArenaDerivedRevisionID]CorrectionProjectionIntent, len(command.ProjectionIntents))
	for _, intent := range command.ProjectionIntents {
		if intent.ExpectedRevision.ID().IsZero() || intent.NextRevisionID.IsZero() || intent.DecisionID == uuid.Nil ||
			intent.ExpectedRevision.RevisionNo() == math.MaxInt ||
			command.RequestedAt.Before(intent.ExpectedRevision.CreatedAt()) {
			return nil, rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "invalid projection intent metadata",
			)
		}
		if _, duplicate := byExpected[intent.ExpectedRevision.ID()]; duplicate {
			return nil, rejectCorrection(
				CorrectionRejectionIdentityAlias, ErrInvalidCorrection, "duplicate projection intent",
			)
		}
		byExpected[intent.ExpectedRevision.ID()] = intent.Clone()
	}
	canonical := make([]CorrectionProjectionIntent, len(want))
	for index, expected := range want {
		intent, exists := byExpected[expected.ID()]
		if !exists || !arenaDerivedRevisionsEqual(intent.ExpectedRevision, expected) {
			return nil, rejectCorrection(
				CorrectionRejectionIncomplete, ErrInvalidCorrection, "projection intents are incomplete",
			)
		}
		canonical[index] = intent
		delete(byExpected, expected.ID())
	}
	if len(byExpected) != 0 {
		return nil, rejectCorrection(
			CorrectionRejectionIncomplete, ErrInvalidCorrection, "projection intents contain unrelated targets",
		)
	}
	return canonical, nil
}

func correctionRebuildRevisions(
	cutoff CorrectionCutoff,
	target domain.ArenaDerivedRevision,
) []domain.ArenaDerivedRevision {
	descendants := cutoff.Descendants()
	current := make(map[domain.ArenaArtifactRef]domain.ArenaDerivedRevision, len(descendants))
	for _, descendant := range descendants {
		prior, exists := current[descendant.Artifact()]
		if !exists || prior.RevisionNo() < descendant.RevisionNo() {
			current[descendant.Artifact()] = descendant
		}
	}
	revisions := make([]domain.ArenaDerivedRevision, 1, len(current)+1)
	revisions[0] = target
	for _, descendant := range descendants {
		if head, exists := current[descendant.Artifact()]; exists &&
			arenaDerivedRevisionsEqual(head, descendant) {
			revisions = append(revisions, descendant)
			delete(current, descendant.Artifact())
		}
	}
	return revisions
}

func validateCorrectionUnlockIntents(
	command CorrectionCommand,
	authority CorrectionAuthority,
	cutoff CorrectionCutoff,
	target domain.ArenaDerivedRevision,
) ([]CorrectionUnlockIntent, error) {
	affected := make(map[domain.ArenaDerivedRevisionID]struct{}, len(cutoff.descendants)+1)
	affected[target.ID()] = struct{}{}
	for _, descendant := range cutoff.descendants {
		affected[descendant.ID()] = struct{}{}
	}
	want := make(map[uuid.UUID]CorrectionUnlockIntent)
	for _, reservation := range authority.Reservations {
		if _, included := affected[reservation.SourceRevisionID]; included &&
			!reservation.Used && !reservation.Disclosed {
			want[reservation.ID] = NewCorrectionUnlockIntent(reservation)
		}
	}
	got := make(map[uuid.UUID]CorrectionUnlockIntent, len(command.UnlockIntents))
	for _, intent := range command.UnlockIntents {
		if _, duplicate := got[intent.ReservationID]; duplicate {
			return nil, rejectCorrection(
				CorrectionRejectionIdentityAlias, ErrInvalidCorrection, "duplicate unlock intent",
			)
		}
		got[intent.ReservationID] = intent
	}
	if len(got) != len(want) {
		return nil, rejectCorrection(
			CorrectionRejectionIncomplete, ErrInvalidCorrection, "unlock intents are incomplete",
		)
	}
	canonical := make([]CorrectionUnlockIntent, 0, len(want))
	reservations := canonicalCorrectionReservations(authority.Reservations)
	for _, reservation := range reservations {
		expected, exists := want[reservation.ID]
		if !exists {
			continue
		}
		actual, exists := got[reservation.ID]
		if !exists || actual != expected || actual.BindingDigest != correctionUnlockIntentDigest(actual) {
			return nil, rejectCorrection(
				CorrectionRejectionIncomplete, ErrInvalidCorrection, "unlock intent binding changed",
			)
		}
		canonical = append(canonical, actual)
	}
	return canonical, nil
}

//nolint:gocyclo // Global identity ownership is intentionally enumerated role by role.
func validateCorrectionUUIDRoles(
	command CorrectionCommand,
	authority CorrectionAuthority,
	intents []CorrectionProjectionIntent,
	snapshot RevisionDAGSnapshot,
) error {
	roles := newRevisionDAGIdentityRegistry()
	for _, projection := range snapshot.Projections {
		if err := claimRevisionDAGGraphIdentity(roles, projection.Revision()); err != nil {
			return correctionIdentityAlias()
		}
	}
	for _, plan := range authority.DAG.results {
		if plan.input.NoGame != nil {
			if err := claimNoGameDAGIdentities(roles, *plan.input.NoGame); err != nil {
				return correctionIdentityAlias()
			}
			continue
		}
		if err := claimOrdinaryDAGIdentities(roles, plan.input); err != nil {
			return correctionIdentityAlias()
		}
	}
	if err := roles.validateCommandUses(); err != nil {
		return correctionIdentityAlias()
	}
	claim := func(id uuid.UUID, role string) error {
		if id == uuid.Nil || roles.claimRole(id, role) != nil {
			return correctionIdentityAlias()
		}
		return nil
	}
	for _, value := range []struct {
		id   uuid.UUID
		role string
	}{
		{authority.Readiness.OwnerID, "Series"},
		{authority.Readiness.WaveID, "Wave"},
		{authority.Readiness.WindowID, "window"},
		{authority.Readiness.RevisionID, "readiness revision"},
	} {
		if err := claim(value.id, value.role); err != nil {
			return err
		}
	}
	for _, participantID := range authority.Readiness.ParticipantIDs {
		if err := claim(participantID, "participant"); err != nil {
			return err
		}
	}
	for _, reservation := range authority.Reservations {
		for _, value := range []struct {
			id   uuid.UUID
			role string
		}{
			{reservation.ID, "reservation"},
			{reservation.OwnerID, "Series"},
			{reservation.SourceRevisionID.UUID(), "projection revision"},
		} {
			if err := claim(value.id, value.role); err != nil {
				return err
			}
		}
	}
	for _, decision := range authority.Decisions {
		if err := claim(decision.ID, "projection decision"); err != nil {
			return err
		}
		if err := claim(decision.ProjectionRevisionID.UUID(), "projection revision"); err != nil {
			return err
		}
	}
	for _, event := range authority.CutoffEvents {
		if err := claim(event.ID, "cutoff event"); err != nil {
			return err
		}
		if err := claim(event.SourceRevisionID.UUID(), "projection revision"); err != nil {
			return err
		}
	}
	if authority.CurrentSolve.SubmissionID != nil {
		if err := claim(*authority.CurrentSolve.SubmissionID, "submission"); err != nil {
			return err
		}
	}
	if command.Patch.SolveMetadata.SubmissionID != nil {
		if err := claim(*command.Patch.SolveMetadata.SubmissionID, "submission"); err != nil {
			return err
		}
	}

	authorityIDs := make(map[uuid.UUID]struct{}, len(roles.roles))
	for id := range roles.roles {
		authorityIDs[id] = struct{}{}
	}
	proposed := []struct {
		id   uuid.UUID
		role string
	}{
		{command.CommandID, "command"},
		{command.CascadeCommandID, "command"},
		{command.OperatorID, "actor"},
		{command.NextResultRevisionID.UUID(), "official result revision"},
		{command.NextScoreRevisionID.UUID(), "score revision"},
		{command.NextSeriesResultRevisionID.UUID(), "official result revision"},
		{command.NextReadinessRevisionID, "readiness revision"},
	}
	for _, intent := range intents {
		proposed = append(proposed,
			struct {
				id   uuid.UUID
				role string
			}{intent.NextRevisionID.UUID(), "projection revision"},
			struct {
				id   uuid.UUID
				role string
			}{intent.DecisionID, "projection decision"},
		)
	}
	seen := make(map[uuid.UUID]struct{}, len(proposed))
	for _, value := range proposed {
		if value.id == uuid.Nil {
			return rejectCorrection(
				CorrectionRejectionMalformed, ErrInvalidCorrection, "missing proposed identity",
			)
		}
		if _, duplicate := seen[value.id]; duplicate {
			return correctionIdentityAlias()
		}
		seen[value.id] = struct{}{}
		if value.id != command.OperatorID {
			if _, exists := authorityIDs[value.id]; exists {
				return correctionIdentityAlias()
			}
		}
		if err := claim(value.id, value.role); err != nil {
			return err
		}
	}
	return nil
}

func correctionIdentityAlias() error {
	return rejectCorrection(
		CorrectionRejectionIdentityAlias, ErrInvalidCorrection, "correction identity has multiple roles or owners",
	)
}

//nolint:gocyclo // Exact optimistic concurrency binding compares every persisted authority component.
func correctionExpectationsEqual(first, second CorrectionExpectation) bool {
	return first.TournamentState == second.TournamentState &&
		first.TournamentRevision == second.TournamentRevision &&
		first.CutoffEventDigest == second.CutoffEventDigest &&
		arenaDerivedRevisionsEqual(first.TargetProjection, second.TargetProjection) &&
		arenaDerivedRevisionsEqual(first.ScoreProjection, second.ScoreProjection) &&
		arenaDerivedRevisionsEqual(first.SeriesProjection, second.SeriesProjection) &&
		first.ResultRevisionID == second.ResultRevisionID &&
		first.ScoreRevisionID == second.ScoreRevisionID &&
		first.SeriesResultRevisionID == second.SeriesResultRevisionID &&
		first.SeriesRevision == second.SeriesRevision && first.AttemptRevision == second.AttemptRevision &&
		first.DAGDigest == second.DAGDigest && first.ReservationDigest == second.ReservationDigest &&
		first.DecisionDigest == second.DecisionDigest && first.ReadinessDigest == second.ReadinessDigest &&
		first.CurrentSolveDigest == second.CurrentSolveDigest
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionDAGDigest(snapshot RevisionDAGSnapshot) [sha256.Size]byte {
	type revisionDocument struct {
		ID, TournamentID, Kind, EntityID string
		Revision                         int
		Previous                         string
		CreatedAt                        string
		PayloadDigest                    [sha256.Size]byte
	}
	type edgeDocument struct{ Source, Derived string }
	document := struct {
		Revisions []revisionDocument
		Edges     []edgeDocument
	}{
		Revisions: make([]revisionDocument, len(snapshot.Projections)),
		Edges:     make([]edgeDocument, len(snapshot.Dependencies)),
	}
	for index, projection := range snapshot.Projections {
		revision := projection.Revision()
		previous := ""
		if predecessor := revision.PreviousRevisionID(); predecessor != nil {
			previous = predecessor.UUID().String()
		}
		document.Revisions[index] = revisionDocument{
			ID: revision.ID().UUID().String(), TournamentID: revision.TournamentID().String(),
			Kind: string(revision.Artifact().Kind), EntityID: revision.Artifact().EntityID.String(),
			Revision: revision.RevisionNo(), Previous: previous,
			CreatedAt: canonicalCorrectionTime(revision.CreatedAt()), PayloadDigest: revision.PayloadDigest(),
		}
	}
	for index, dependency := range snapshot.Dependencies {
		document.Edges[index] = edgeDocument{
			Source:  dependency.SourceRevisionID.UUID().String(),
			Derived: dependency.DerivedRevisionID.UUID().String(),
		}
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionReservationSetDigest(input []CorrectionReservation) [sha256.Size]byte {
	canonical := canonicalCorrectionReservations(input)
	payload, _ := json.Marshal(canonical)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionDecisionSetDigest(input []RecordedProjectionDecision) [sha256.Size]byte {
	canonical := canonicalCorrectionDecisions(input)
	type document struct {
		ID, Projection string
		Sequence       int
		RecordedAt     string
		PayloadDigest  [sha256.Size]byte
	}
	documents := make([]document, len(canonical))
	for index, decision := range canonical {
		documents[index] = document{
			ID: decision.ID.String(), Projection: decision.ProjectionRevisionID.UUID().String(),
			Sequence: decision.Sequence, RecordedAt: canonicalCorrectionTime(decision.RecordedAt),
			PayloadDigest: decision.PayloadDigest,
		}
	}
	payload, _ := json.Marshal(documents)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionReadinessDigest(readiness CorrectionReadiness) [sha256.Size]byte {
	participants := append([]uuid.UUID(nil), readiness.ParticipantIDs...)
	sort.Slice(participants, func(first, second int) bool {
		return participants[first].String() < participants[second].String()
	})
	document := struct {
		TournamentID, OwnerID, WaveID, WindowID, RevisionID uuid.UUID
		Revision                                            int64
		State                                               CorrectionReadinessState
		Participants                                        []uuid.UUID
	}{
		readiness.TournamentID, readiness.OwnerID, readiness.WaveID,
		readiness.WindowID, readiness.RevisionID,
		readiness.Revision, readiness.State, participants,
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionUnlockIntentDigest(intent CorrectionUnlockIntent) [sha256.Size]byte {
	document := struct {
		ReservationID, TournamentID, OwnerID uuid.UUID
		SourceRevisionID                     uuid.UUID
		ExpectedRevision                     int64
		ExpectedUsed, ExpectedDisclosed      bool
		EvidenceDigest                       [sha256.Size]byte
	}{
		intent.ReservationID, intent.TournamentID, intent.OwnerID,
		intent.SourceRevisionID.UUID(), intent.ExpectedRevision,
		intent.ExpectedUsed, intent.ExpectedDisclosed, intent.EvidenceDigest,
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionSolveMetadataDigest(metadata CorrectionSolveMetadata) [sha256.Size]byte {
	solvedAt := ""
	if metadata.SolvedAt != nil {
		solvedAt = canonicalCorrectionTime(*metadata.SolvedAt)
	}
	document := struct {
		SolvedAt       string
		SubmissionID   *uuid.UUID
		EvidenceDigest [sha256.Size]byte
	}{
		SolvedAt: solvedAt, SubmissionID: cloneUUIDPointer(metadata.SubmissionID),
		EvidenceDigest: metadata.EvidenceDigest,
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

type correctionRevisionDocument struct {
	ID, TournamentID, Kind, EntityID string
	Revision                         int
	Previous                         string
	CreatedAt                        string
	PayloadDigest                    [sha256.Size]byte
}

func correctionRevisionDigestDocument(
	revision domain.ArenaDerivedRevision,
) correctionRevisionDocument {
	previous := ""
	if predecessor := revision.PreviousRevisionID(); predecessor != nil {
		previous = predecessor.UUID().String()
	}
	return correctionRevisionDocument{
		ID: revision.ID().UUID().String(), TournamentID: revision.TournamentID().String(),
		Kind: string(revision.Artifact().Kind), EntityID: revision.Artifact().EntityID.String(),
		Revision: revision.RevisionNo(), Previous: previous,
		CreatedAt: canonicalCorrectionTime(revision.CreatedAt()), PayloadDigest: revision.PayloadDigest(),
	}
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionExpectationDigest(expectation CorrectionExpectation) [sha256.Size]byte {
	document := struct {
		TournamentState                                                     domain.ArenaTournamentState
		TournamentRevision                                                  int64
		Target, Score, Series                                               correctionRevisionDocument
		Result, ScoreHead, SeriesResult                                     string
		SeriesRevision, AttemptRevision                                     int64
		DAG, Reservations, Decisions, Readiness, CurrentSolve, CutoffEvents [sha256.Size]byte
	}{
		TournamentState: expectation.TournamentState, TournamentRevision: expectation.TournamentRevision,
		Target:          correctionRevisionDigestDocument(expectation.TargetProjection),
		Score:           correctionRevisionDigestDocument(expectation.ScoreProjection),
		Series:          correctionRevisionDigestDocument(expectation.SeriesProjection),
		Result:          expectation.ResultRevisionID.UUID().String(),
		ScoreHead:       expectation.ScoreRevisionID.UUID().String(),
		SeriesResult:    expectation.SeriesResultRevisionID.UUID().String(),
		SeriesRevision:  int64(expectation.SeriesRevision),
		AttemptRevision: int64(expectation.AttemptRevision),
		DAG:             expectation.DAGDigest, Reservations: expectation.ReservationDigest,
		Decisions: expectation.DecisionDigest, Readiness: expectation.ReadinessDigest,
		CurrentSolve: expectation.CurrentSolveDigest,
		CutoffEvents: expectation.CutoffEventDigest,
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionCommandDigest(command CorrectionCommand) [sha256.Size]byte {
	type projectionDocument struct {
		Expected       correctionRevisionDocument
		Next, Decision uuid.UUID
		Payload        []byte
		PayloadDigest  [sha256.Size]byte
	}
	projections := make([]projectionDocument, len(command.ProjectionIntents))
	for index, intent := range command.ProjectionIntents {
		projections[index] = projectionDocument{
			Expected: correctionRevisionDigestDocument(intent.ExpectedRevision),
			Next:     intent.NextRevisionID.UUID(), Decision: intent.DecisionID,
			Payload: append([]byte(nil), intent.Payload...), PayloadDigest: intent.PayloadDigest,
		}
	}
	solvedAt := ""
	hasSolvedAt := command.Patch.SolveMetadata.SolvedAt != nil
	if hasSolvedAt {
		solvedAt = canonicalCorrectionTime(*command.Patch.SolveMetadata.SolvedAt)
	}
	document := struct {
		TournamentID, SeriesID, GameID          uuid.UUID
		CommandID, CascadeCommandID, OperatorID uuid.UUID
		Confirmed                               bool
		Reason                                  CorrectionReason
		Explanation                             string
		RequestedAt                             string
		Expectation                             [sha256.Size]byte
		PatchState                              domain.ArenaGameState
		PatchReason                             domain.ArenaGameResultReason
		PatchWinner                             *uuid.UUID
		HasSolvedAt                             bool
		SolvedAt                                string
		SubmissionID                            *uuid.UUID
		SolveDigest                             [sha256.Size]byte
		Fields                                  []CorrectionField
		NextResult, NextScore, NextSeriesResult uuid.UUID
		NextReadiness                           uuid.UUID
		Projections                             []projectionDocument
		Unlocks                                 []CorrectionUnlockIntent
	}{
		TournamentID: command.TournamentID, SeriesID: command.SeriesID, GameID: command.GameID,
		CommandID: command.CommandID, CascadeCommandID: command.CascadeCommandID,
		OperatorID: command.OperatorID, Confirmed: command.Confirmed,
		Reason: command.Reason, Explanation: command.Explanation,
		RequestedAt: canonicalCorrectionTime(command.RequestedAt), Expectation: correctionExpectationDigest(command.Expected),
		PatchState: command.Patch.State, PatchReason: command.Patch.Reason,
		PatchWinner: cloneUUIDPointer(command.Patch.WinnerID), HasSolvedAt: hasSolvedAt,
		SolvedAt: solvedAt, SubmissionID: cloneUUIDPointer(command.Patch.SolveMetadata.SubmissionID),
		SolveDigest: command.Patch.SolveMetadata.EvidenceDigest,
		Fields:      append([]CorrectionField(nil), command.Fields...),
		NextResult:  command.NextResultRevisionID.UUID(), NextScore: command.NextScoreRevisionID.UUID(),
		NextSeriesResult: command.NextSeriesResultRevisionID.UUID(),
		NextReadiness:    command.NextReadinessRevisionID, Projections: projections,
		Unlocks: append([]CorrectionUnlockIntent(nil), command.UnlockIntents...),
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionHeadDigest(head OfficialResultRevisionHead) [sha256.Size]byte {
	document := struct {
		Head   OfficialResultRevisionHead
		Source correctionRevisionDocument
	}{head.Clone(), correctionRevisionDigestDocument(head.SourceProjection)}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionScoreHeadDigest(head SeriesScoreRevisionHead) [sha256.Size]byte {
	document := struct {
		Head   SeriesScoreRevisionHead
		Source correctionRevisionDocument
	}{head.Clone(), correctionRevisionDigestDocument(head.SourceProjection)}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionCutoffEventSetDigest(events []CorrectionCutoffEvent) [sha256.Size]byte {
	canonical := append([]CorrectionCutoffEvent(nil), events...)
	sort.Slice(canonical, func(first, second int) bool {
		if !canonical[first].OccurredAt.Equal(canonical[second].OccurredAt) {
			return canonical[first].OccurredAt.Before(canonical[second].OccurredAt)
		}
		if canonical[first].Kind != canonical[second].Kind {
			return canonical[first].Kind < canonical[second].Kind
		}
		return bytes.Compare(canonical[first].ID[:], canonical[second].ID[:]) < 0
	})
	type eventDocument struct {
		ID, TournamentID, Source uuid.UUID
		Kind                     CorrectionCutoffKind
		OccurredAt               string
	}
	documents := make([]eventDocument, len(canonical))
	for index, event := range canonical {
		documents[index] = eventDocument{
			ID: event.ID, TournamentID: event.TournamentID,
			Source: event.SourceRevisionID.UUID(), Kind: event.Kind,
			OccurredAt: canonicalCorrectionTime(event.OccurredAt),
		}
	}
	payload, _ := json.Marshal(documents)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionAuthorityDigest(
	authority CorrectionAuthority,
	snapshot RevisionDAGSnapshot,
) [sha256.Size]byte {
	solvedAt := ""
	hasSolvedAt := authority.CurrentSolve.SolvedAt != nil
	if hasSolvedAt {
		solvedAt = canonicalCorrectionTime(*authority.CurrentSolve.SolvedAt)
	}
	seriesPayload, _ := json.Marshal(authority.Series)
	document := struct {
		TournamentState                 domain.ArenaTournamentState
		TournamentRevision              int64
		DAG                             [sha256.Size]byte
		Series                          []byte
		GameResult                      [sha256.Size]byte
		Score                           [sha256.Size]byte
		SeriesResult                    [sha256.Size]byte
		SeriesRevision, AttemptRevision int64
		HasSolvedAt                     bool
		SolvedAt                        string
		SubmissionID                    *uuid.UUID
		SolveDigest                     [sha256.Size]byte
		Readiness                       [sha256.Size]byte
		Reservations                    [sha256.Size]byte
		Decisions                       [sha256.Size]byte
		CutoffEvents                    [sha256.Size]byte
	}{
		TournamentState: authority.TournamentState, TournamentRevision: authority.TournamentRevision,
		DAG: correctionDAGDigest(snapshot), Series: seriesPayload,
		GameResult:     correctionHeadDigest(authority.GameResult),
		Score:          correctionScoreHeadDigest(authority.Score),
		SeriesResult:   correctionHeadDigest(authority.SeriesResult),
		SeriesRevision: int64(authority.SeriesRevision), AttemptRevision: int64(authority.AttemptRevision),
		HasSolvedAt: hasSolvedAt, SolvedAt: solvedAt,
		SubmissionID: cloneUUIDPointer(authority.CurrentSolve.SubmissionID),
		SolveDigest:  authority.CurrentSolve.EvidenceDigest,
		Readiness:    correctionReadinessDigest(authority.Readiness),
		Reservations: correctionReservationSetDigest(authority.Reservations),
		Decisions:    correctionDecisionSetDigest(authority.Decisions),
		CutoffEvents: correctionCutoffEventSetDigest(authority.CutoffEvents),
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionValidationDigest(validation CorrectionValidation) [sha256.Size]byte {
	descendants := validation.cutoff.Descendants()
	descendantDocuments := make([]correctionRevisionDocument, len(descendants))
	for index, revision := range descendants {
		descendantDocuments[index] = correctionRevisionDigestDocument(revision)
	}
	document := struct {
		Command, Authority [sha256.Size]byte
		CutoffTournament   uuid.UUID
		CutoffTarget       uuid.UUID
		Descendants        []correctionRevisionDocument
		Target             correctionRevisionDocument
		Unlocks            []CorrectionUnlockIntent
	}{
		Command:          correctionCommandDigest(validation.command),
		Authority:        correctionAuthorityDigest(validation.authority, validation.snapshot),
		CutoffTournament: validation.cutoff.TournamentID(),
		CutoffTarget:     validation.cutoff.TargetRevisionID().UUID(),
		Descendants:      descendantDocuments,
		Target:           correctionRevisionDigestDocument(validation.target),
		Unlocks:          append([]CorrectionUnlockIntent(nil), validation.unlocks...),
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

func correctionRevisionSet(snapshot RevisionDAGSnapshot) map[domain.ArenaDerivedRevisionID]struct{} {
	set := make(map[domain.ArenaDerivedRevisionID]struct{}, len(snapshot.Projections))
	for _, projection := range snapshot.Projections {
		set[projection.Revision().ID()] = struct{}{}
	}
	return set
}

func canonicalCorrectionReservations(input []CorrectionReservation) []CorrectionReservation {
	canonical := append([]CorrectionReservation(nil), input...)
	sort.Slice(canonical, func(first, second int) bool {
		return canonical[first].ID.String() < canonical[second].ID.String()
	})
	return canonical
}

func canonicalCorrectionDecisions(input []RecordedProjectionDecision) []RecordedProjectionDecision {
	canonical := cloneRecordedProjectionDecisions(input)
	sort.Slice(canonical, func(first, second int) bool {
		return canonical[first].Sequence < canonical[second].Sequence
	})
	return canonical
}

func canonicalCorrectionParticipants(input []uuid.UUID) []uuid.UUID {
	canonical := append([]uuid.UUID(nil), input...)
	sort.Slice(canonical, func(first, second int) bool {
		return bytes.Compare(canonical[first][:], canonical[second][:]) < 0
	})
	return canonical
}

func cloneCorrectionCommand(command CorrectionCommand) CorrectionCommand {
	clone := command
	clone.Patch.WinnerID = cloneUUIDPointer(command.Patch.WinnerID)
	clone.Patch.SolveMetadata = command.Patch.SolveMetadata.Clone()
	clone.Fields = append([]CorrectionField(nil), command.Fields...)
	clone.ProjectionIntents = make([]CorrectionProjectionIntent, len(command.ProjectionIntents))
	for index := range command.ProjectionIntents {
		clone.ProjectionIntents[index] = command.ProjectionIntents[index].Clone()
	}
	clone.UnlockIntents = append([]CorrectionUnlockIntent(nil), command.UnlockIntents...)
	return clone
}

func cloneCorrectionAuthority(authority CorrectionAuthority) CorrectionAuthority {
	clone := authority
	clone.Series = cloneRevisionArenaSeries(authority.Series)
	clone.GameResult = authority.GameResult.Clone()
	clone.Score = authority.Score.Clone()
	clone.SeriesResult = authority.SeriesResult.Clone()
	clone.CurrentSolve = authority.CurrentSolve.Clone()
	clone.Readiness.ParticipantIDs = append([]uuid.UUID(nil), authority.Readiness.ParticipantIDs...)
	clone.Reservations = append([]CorrectionReservation(nil), authority.Reservations...)
	clone.Decisions = cloneRecordedProjectionDecisions(authority.Decisions)
	clone.CutoffEvents = append([]CorrectionCutoffEvent(nil), authority.CutoffEvents...)
	return clone
}

func correctionSolveMetadataEqual(first, second CorrectionSolveMetadata) bool {
	return timePointersEqual(first.SolvedAt, second.SolvedAt) &&
		uuidPointersEqual(first.SubmissionID, second.SubmissionID) &&
		bytes.Equal(first.EvidenceDigest[:], second.EvidenceDigest[:])
}
