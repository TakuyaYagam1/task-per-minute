package correction

import (
	"crypto/sha256"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

func validateCorrectionPatch(command Command, authority Authority) error {
	current := authority.GameResult.Outcome
	patch := command.Patch
	if correctionPredatesAuthority(command.RequestedAt, authority) {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "correction predates a current authority head",
		)
	}
	if !validTerminalCorrection(current, patch) {
		return rejectCorrection(
			RejectionTerminal, ErrInvalid, "correction must preserve a terminal result",
		)
	}
	if err := validateCorrectionWinner(patch, authority.Series); err != nil {
		return err
	}
	if !correctionSolveMetadataValid(
		patch.SolveMetadata,
		patch.Reason == domain.GameResultReasonSolved,
		authority.GameResult.SourceProjection.CreatedAt(),
		command.RequestedAt,
	) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "invalid replacement solve metadata",
		)
	}
	if err := validateCorrectionFields(command, authority); err != nil {
		return err
	}
	return validateCorrectionProjectedSeries(command, authority)
}

func correctionPredatesAuthority(requestedAt time.Time, authority Authority) bool {
	return requestedAt.Before(authority.GameResult.RecordedAt) ||
		requestedAt.Before(authority.Score.RecordedAt) ||
		requestedAt.Before(authority.SeriesResult.RecordedAt)
}

func validTerminalCorrection(current resultusecase.OfficialResultOutcome, patch Patch) bool {
	return current.GameState.IsTerminal() && current.GameState != domain.GameStateSuperseded &&
		patch.State.IsTerminal() && patch.State == current.GameState &&
		patch.Reason.IsLegalFor(patch.State)
}

func validateCorrectionWinner(patch Patch, series domain.Series) error {
	if patch.State == domain.GameStateCompleted {
		if patch.WinnerID == nil || (*patch.WinnerID != series.FirstParticipantID &&
			*patch.WinnerID != series.SecondParticipantID) {
			return rejectCorrection(
				RejectionTerminal, ErrInvalid, "completed correction has no valid winner",
			)
		}
		return nil
	}
	if patch.WinnerID != nil {
		return rejectCorrection(
			RejectionTerminal, ErrInvalid, "non scoring correction has a winner",
		)
	}
	return nil
}

func validateCorrectionFields(command Command, authority Authority) error {
	wantFields := correctionChangedFields(
		authority.GameResult.Outcome,
		authority.CurrentSolve,
		command.Patch,
	)
	gotFields := canonicalCorrectionFields(command.Fields)
	if len(wantFields) == 0 || !correctionFieldsEqual(wantFields, gotFields) {
		return rejectCorrection(
			RejectionMalformed, ErrInvalid, "correction fields are not exact",
		)
	}
	return nil
}

func validateCorrectionProjectedSeries(command Command, authority Authority) error {
	projected := cloneCorrectionSeries(authority.Series)
	game, found := findCorrectionSeriesGamePointer(&projected, command.GameID)
	if !found {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "correction Game is missing",
		)
	}
	game.State = command.Patch.State
	game.ResultReason = command.Patch.Reason
	game.WinnerID = cloneCorrectionUUIDPointer(command.Patch.WinnerID)
	references, err := correctionSeriesScoreAttemptReferencesFromSeries(projected)
	if err != nil {
		return rejectCorrection(
			RejectionTerminal, ErrInvalid, "replacement score is invalid",
		)
	}
	score, err := correctionScoreFromAttemptReferences(
		references, projected.FirstParticipantID, projected.SecondParticipantID, projected.Format,
	)
	if err != nil || (authority.Series.State == domain.SeriesStateCompleted &&
		score.Winner(projected.FirstParticipantID, projected.SecondParticipantID, projected.Format) == nil) {
		return rejectCorrection(
			RejectionTerminal, ErrInvalid, "replacement makes the Series nonterminal",
		)
	}
	return nil
}

func correctionSolveMetadataValid(
	metadata SolveMetadata,
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
	current resultusecase.OfficialResultOutcome,
	currentSolve SolveMetadata,
	patch Patch,
) []Field {
	fields := make([]Field, 0, maxCorrectionFields)
	if !correctionUUIDPointersEqual(current.WinnerID, patch.WinnerID) {
		fields = append(fields, FieldWinner)
	}
	if current.GameReason != patch.Reason {
		fields = append(fields, FieldResultReason)
	}
	if !correctionSolveMetadataEqual(currentSolve, patch.SolveMetadata) {
		fields = append(fields, FieldSolveMetadata)
	}
	return canonicalCorrectionFields(fields)
}

func canonicalCorrectionFields(fields []Field) []Field {
	canonical := append([]Field(nil), fields...)
	sort.Slice(canonical, func(first, second int) bool { return canonical[first] < canonical[second] })
	return canonical
}

func correctionFieldsEqual(first, second []Field) bool {
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

func validCorrectionField(field Field) bool {
	switch field {
	case FieldWinner, FieldResultReason, FieldSolveMetadata:
		return true
	default:
		return false
	}
}

func validateCorrectionProjectionIntents(
	command Command,
	cutoff Cutoff,
	target domain.DerivedRevision,
) ([]ProjectionIntent, error) {
	want := correctionRebuildRevisions(cutoff, target)
	byExpected := make(map[domain.DerivedRevisionID]ProjectionIntent, len(command.ProjectionIntents))
	for _, intent := range command.ProjectionIntents {
		if intent.ExpectedRevision.ID().IsZero() || intent.NextRevisionID.IsZero() || intent.DecisionID == uuid.Nil ||
			intent.ExpectedRevision.RevisionNo() == math.MaxInt ||
			command.RequestedAt.Before(intent.ExpectedRevision.CreatedAt()) {
			return nil, rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid projection intent metadata",
			)
		}
		if _, duplicate := byExpected[intent.ExpectedRevision.ID()]; duplicate {
			return nil, rejectCorrection(
				RejectionIdentityAlias, ErrInvalid, "duplicate projection intent",
			)
		}
		byExpected[intent.ExpectedRevision.ID()] = intent.Clone()
	}
	canonical := make([]ProjectionIntent, len(want))
	for index, expected := range want {
		intent, exists := byExpected[expected.ID()]
		if !exists || !correctionDerivedRevisionsEqual(intent.ExpectedRevision, expected) {
			return nil, rejectCorrection(
				RejectionIncomplete, ErrInvalid, "projection intents are incomplete",
			)
		}
		canonical[index] = intent
		delete(byExpected, expected.ID())
	}
	if len(byExpected) != 0 {
		return nil, rejectCorrection(
			RejectionIncomplete, ErrInvalid, "projection intents contain unrelated targets",
		)
	}
	return canonical, nil
}

func correctionRebuildRevisions(
	cutoff Cutoff,
	target domain.DerivedRevision,
) []domain.DerivedRevision {
	descendants := cutoff.Descendants()
	current := make(map[domain.ArtifactRef]domain.DerivedRevision, len(descendants))
	for _, descendant := range descendants {
		prior, exists := current[descendant.Artifact()]
		if !exists || prior.RevisionNo() < descendant.RevisionNo() {
			current[descendant.Artifact()] = descendant
		}
	}
	revisions := make([]domain.DerivedRevision, 1, len(current)+1)
	revisions[0] = target
	for _, descendant := range descendants {
		if head, exists := current[descendant.Artifact()]; exists &&
			correctionDerivedRevisionsEqual(head, descendant) {
			revisions = append(revisions, descendant)
			delete(current, descendant.Artifact())
		}
	}
	return revisions
}

func validateCorrectionUnlockIntents(
	command Command,
	authority Authority,
	cutoff Cutoff,
	target domain.DerivedRevision,
) ([]UnlockIntent, error) {
	descendants := cutoff.Descendants()
	affected := make(map[domain.DerivedRevisionID]struct{}, len(descendants)+1)
	affected[target.ID()] = struct{}{}
	for _, descendant := range descendants {
		affected[descendant.ID()] = struct{}{}
	}
	want := make(map[uuid.UUID]UnlockIntent)
	for _, reservation := range authority.Reservations {
		if _, included := affected[reservation.SourceRevisionID]; included &&
			!reservation.Used && !reservation.Disclosed {
			want[reservation.ID] = NewUnlockIntent(reservation)
		}
	}
	got := make(map[uuid.UUID]UnlockIntent, len(command.UnlockIntents))
	for _, intent := range command.UnlockIntents {
		if _, duplicate := got[intent.ReservationID]; duplicate {
			return nil, rejectCorrection(
				RejectionIdentityAlias, ErrInvalid, "duplicate unlock intent",
			)
		}
		got[intent.ReservationID] = intent
	}
	if len(got) != len(want) {
		return nil, rejectCorrection(
			RejectionIncomplete, ErrInvalid, "unlock intents are incomplete",
		)
	}
	canonical := make([]UnlockIntent, 0, len(want))
	reservations := canonicalCorrectionReservations(authority.Reservations)
	for _, reservation := range reservations {
		expected, exists := want[reservation.ID]
		if !exists {
			continue
		}
		actual, exists := got[reservation.ID]
		if !exists || actual != expected || actual.BindingDigest != correctionUnlockIntentDigest(actual) {
			return nil, rejectCorrection(
				RejectionIncomplete, ErrInvalid, "unlock intent binding changed",
			)
		}
		canonical = append(canonical, actual)
	}
	return canonical, nil
}
