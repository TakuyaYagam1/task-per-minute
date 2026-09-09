package correction

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionDAGDigest(snapshot resultprojection.RevisionDAGSnapshot) [sha256.Size]byte {
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
func correctionReservationSetDigest(input []Reservation) [sha256.Size]byte {
	canonical := canonicalCorrectionReservations(input)
	payload, _ := json.Marshal(canonical)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionDecisionSetDigest(input []resultprojection.RecordedProjectionDecision) [sha256.Size]byte {
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
func correctionReadinessDigest(readiness Readiness) [sha256.Size]byte {
	participants := append([]uuid.UUID(nil), readiness.ParticipantIDs...)
	sort.Slice(participants, func(first, second int) bool {
		return participants[first].String() < participants[second].String()
	})
	document := struct {
		TournamentID, OwnerID, WaveID, WindowID, RevisionID uuid.UUID
		Revision                                            int64
		State                                               ReadinessState
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
func correctionUnlockIntentDigest(intent UnlockIntent) [sha256.Size]byte {
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
func correctionSolveMetadataDigest(metadata SolveMetadata) [sha256.Size]byte {
	solvedAt := ""
	if metadata.SolvedAt != nil {
		solvedAt = canonicalCorrectionTime(*metadata.SolvedAt)
	}
	document := struct {
		SolvedAt       string
		SubmissionID   *uuid.UUID
		EvidenceDigest [sha256.Size]byte
	}{
		SolvedAt: solvedAt, SubmissionID: cloneCorrectionUUIDPointer(metadata.SubmissionID),
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
	revision domain.DerivedRevision,
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
func correctionExpectationDigest(expectation Expectation) [sha256.Size]byte {
	document := struct {
		TournamentState                                                     domain.TournamentState
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
func correctionCommandDigest(command Command) [sha256.Size]byte {
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
		Reason                                  Reason
		Explanation                             string
		RequestedAt                             string
		Expectation                             [sha256.Size]byte
		PatchState                              domain.GameState
		PatchReason                             domain.GameResultReason
		PatchWinner                             *uuid.UUID
		HasSolvedAt                             bool
		SolvedAt                                string
		SubmissionID                            *uuid.UUID
		SolveDigest                             [sha256.Size]byte
		Fields                                  []Field
		NextResult, NextScore, NextSeriesResult uuid.UUID
		NextReadiness                           uuid.UUID
		Projections                             []projectionDocument
		Unlocks                                 []UnlockIntent
	}{
		TournamentID: command.TournamentID, SeriesID: command.SeriesID, GameID: command.GameID,
		CommandID: command.CommandID, CascadeCommandID: command.CascadeCommandID,
		OperatorID: command.OperatorID, Confirmed: command.Confirmed,
		Reason: command.Reason, Explanation: command.Explanation,
		RequestedAt: canonicalCorrectionTime(command.RequestedAt), Expectation: correctionExpectationDigest(command.Expected),
		PatchState: command.Patch.State, PatchReason: command.Patch.Reason,
		PatchWinner: cloneCorrectionUUIDPointer(command.Patch.WinnerID), HasSolvedAt: hasSolvedAt,
		SolvedAt: solvedAt, SubmissionID: cloneCorrectionUUIDPointer(command.Patch.SolveMetadata.SubmissionID),
		SolveDigest: command.Patch.SolveMetadata.EvidenceDigest,
		Fields:      append([]Field(nil), command.Fields...),
		NextResult:  command.NextResultRevisionID.UUID(), NextScore: command.NextScoreRevisionID.UUID(),
		NextSeriesResult: command.NextSeriesResultRevisionID.UUID(),
		NextReadiness:    command.NextReadinessRevisionID, Projections: projections,
		Unlocks: append([]UnlockIntent(nil), command.UnlockIntents...),
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionHeadDigest(head resultusecase.OfficialResultRevisionHead) [sha256.Size]byte {
	document := struct {
		Head   resultusecase.OfficialResultRevisionHead
		Source correctionRevisionDocument
	}{head.Clone(), correctionRevisionDigestDocument(head.SourceProjection)}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionScoreHeadDigest(head resultusecase.SeriesScoreRevisionHead) [sha256.Size]byte {
	document := struct {
		Head   resultusecase.SeriesScoreRevisionHead
		Source correctionRevisionDocument
	}{head.Clone(), correctionRevisionDigestDocument(head.SourceProjection)}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

//nolint:musttag // Private canonical documents are hashed, not exposed as a wire API.
func correctionCutoffEventSetDigest(events []CutoffEvent) [sha256.Size]byte {
	canonical := append([]CutoffEvent(nil), events...)
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
		Kind                     CutoffKind
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
	authority Authority,
	snapshot resultprojection.RevisionDAGSnapshot,
) [sha256.Size]byte {
	solvedAt := ""
	hasSolvedAt := authority.CurrentSolve.SolvedAt != nil
	if hasSolvedAt {
		solvedAt = canonicalCorrectionTime(*authority.CurrentSolve.SolvedAt)
	}
	seriesPayload, _ := json.Marshal(authority.Series)
	document := struct {
		TournamentState                 domain.TournamentState
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
		SubmissionID: cloneCorrectionUUIDPointer(authority.CurrentSolve.SubmissionID),
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
func correctionValidationDigest(validation Validation) [sha256.Size]byte {
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
		Unlocks            []UnlockIntent
	}{
		Command:          correctionCommandDigest(validation.command),
		Authority:        correctionAuthorityDigest(validation.authority, validation.snapshot),
		CutoffTournament: validation.cutoff.TournamentID(),
		CutoffTarget:     validation.cutoff.TargetRevisionID().UUID(),
		Descendants:      descendantDocuments,
		Target:           correctionRevisionDigestDocument(validation.target),
		Unlocks:          append([]UnlockIntent(nil), validation.unlocks...),
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

func correctionRevisionSet(snapshot resultprojection.RevisionDAGSnapshot) map[domain.DerivedRevisionID]struct{} {
	set := make(map[domain.DerivedRevisionID]struct{}, len(snapshot.Projections))
	for _, projection := range snapshot.Projections {
		set[projection.Revision().ID()] = struct{}{}
	}
	return set
}

func canonicalCorrectionReservations(input []Reservation) []Reservation {
	canonical := append([]Reservation(nil), input...)
	sort.Slice(canonical, func(first, second int) bool {
		return canonical[first].ID.String() < canonical[second].ID.String()
	})
	return canonical
}

func canonicalCorrectionDecisions(input []resultprojection.RecordedProjectionDecision) []resultprojection.RecordedProjectionDecision {
	canonical := cloneCorrectionRecordedProjectionDecisions(input)
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

func cloneCorrectionCommand(command Command) Command {
	clone := command
	clone.Patch.WinnerID = cloneCorrectionUUIDPointer(command.Patch.WinnerID)
	clone.Patch.SolveMetadata = command.Patch.SolveMetadata.Clone()
	clone.Fields = append([]Field(nil), command.Fields...)
	clone.ProjectionIntents = make([]ProjectionIntent, len(command.ProjectionIntents))
	for index := range command.ProjectionIntents {
		clone.ProjectionIntents[index] = command.ProjectionIntents[index].Clone()
	}
	clone.UnlockIntents = append([]UnlockIntent(nil), command.UnlockIntents...)
	return clone
}

func cloneCorrectionAuthority(authority Authority) Authority {
	clone := authority
	clone.Series = cloneCorrectionSeries(authority.Series)
	clone.GameResult = authority.GameResult.Clone()
	clone.Score = authority.Score.Clone()
	clone.SeriesResult = authority.SeriesResult.Clone()
	clone.CurrentSolve = authority.CurrentSolve.Clone()
	clone.Readiness.ParticipantIDs = append([]uuid.UUID(nil), authority.Readiness.ParticipantIDs...)
	clone.Reservations = append([]Reservation(nil), authority.Reservations...)
	clone.Decisions = cloneCorrectionRecordedProjectionDecisions(authority.Decisions)
	clone.CutoffEvents = append([]CutoffEvent(nil), authority.CutoffEvents...)
	return clone
}

func correctionSolveMetadataEqual(first, second SolveMetadata) bool {
	return timePointersEqual(first.SolvedAt, second.SolvedAt) &&
		correctionUUIDPointersEqual(first.SubmissionID, second.SubmissionID) &&
		bytes.Equal(first.EvidenceDigest[:], second.EvidenceDigest[:])
}
