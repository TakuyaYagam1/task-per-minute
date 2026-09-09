package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
)

const correctionProjectionDocumentSchema = "tournament-correction-projection-v1"

type CorrectionWorkflow struct {
	transactions CorrectionTransactionManager
	repository   CorrectionWorkflowRepository
}

func NewCorrectionWorkflow(deps CorrectionWorkflowDependencies) *CorrectionWorkflow {
	return &CorrectionWorkflow{transactions: deps.Transactions, repository: deps.Repository}
}

func (w *CorrectionWorkflow) CorrectGameResult(
	ctx context.Context,
	command CorrectionCommand,
) (CorrectionEvidence, error) {
	if ctx == nil || !validCorrectionCommand(command) {
		return CorrectionEvidence{}, domain.ErrValidation
	}
	if !w.available() {
		return CorrectionEvidence{}, domain.ErrInternal
	}
	requestDigest, err := correctionRequestDigest(command)
	if err != nil {
		return CorrectionEvidence{}, err
	}

	var committed CorrectionEvidence
	err = w.transactions.Do(ctx, func(txCtx context.Context) error {
		recorded, findErr := w.repository.FindCorrectionCommand(txCtx, command.CommandID)
		if findErr != nil {
			return findErr
		}
		if recorded != nil {
			if !validCorrectionCommandRecord(*recorded) {
				return domain.ErrInternal
			}
			if correctionCommandRecordMatches(*recorded, command, recorded.RosterID, requestDigest) {
				committed = cloneCorrectionEvidence(recorded.Evidence)
				return nil
			}
		}

		authority, loadErr := w.repository.LockCorrectionAuthority(
			txCtx, command.TournamentID, command.SeriesID, command.GameID,
		)
		if loadErr != nil {
			return correctionWorkflowError(loadErr, command, authority)
		}
		if !validCorrectionWorkflowAuthority(authority, command) {
			return domain.ErrInternal
		}

		if recorded != nil {
			return newCorrectionConflict(command, authority)
		}
		if authority.ProjectionRevision != command.ExpectedProjectionRevision {
			return newCorrectionConflict(command, authority)
		}

		requestedAt, timeErr := w.readTime(txCtx)
		if timeErr != nil {
			return timeErr
		}
		coreCommand, buildErr := buildCorrectionCommand(command, authority.Core, requestedAt)
		if buildErr != nil {
			return correctionWorkflowError(buildErr, command, authority)
		}
		plan, planErr := correctionusecase.BuildPlan(coreCommand, authority.Core)
		if planErr != nil {
			return correctionWorkflowError(planErr, command, authority)
		}
		stage, stageErr := correctionusecase.PlanServerOwnedStageRollback(plan, authority.Stage, requestedAt)
		if stageErr != nil {
			return correctionWorkflowError(stageErr, command, authority)
		}
		expected, evidenceErr := correctionEvidence(command, plan)
		if evidenceErr != nil {
			return evidenceErr
		}
		stored, _, commitErr := w.repository.CommitCorrection(txCtx, CorrectionMutation{
			Command: command, Authority: authority, RequestDigest: requestDigest,
			Plan: plan, Stage: stage, Evidence: expected,
		})
		if commitErr != nil {
			return correctionWorkflowError(commitErr, command, authority)
		}
		if !reflect.DeepEqual(stored, expected) || !validCorrectionEvidence(stored, command) {
			return newCorrectionConflict(command, authority)
		}
		committed = cloneCorrectionEvidence(stored)
		return nil
	})
	if err != nil {
		return CorrectionEvidence{}, err
	}
	return committed, nil
}

func (w *CorrectionWorkflow) available() bool {
	return w != nil && w.transactions != nil && w.repository != nil
}

func (w *CorrectionWorkflow) readTime(ctx context.Context) (time.Time, error) {
	value, err := w.repository.ReadCorrectionTime(ctx)
	if err != nil {
		return time.Time{}, err
	}
	value = value.Round(0).UTC()
	if !domain.IsValidServerTime(value) {
		return time.Time{}, domain.ErrInternal
	}
	return value, nil
}

func buildCorrectionCommand(
	command CorrectionCommand,
	authority correctionusecase.Authority,
	requestedAt time.Time,
) (correctionusecase.Command, error) {
	target := authority.GameResult.SourceProjection.ID()
	expected, err := correctionusecase.NewExpectation(authority, target)
	if err != nil {
		return correctionusecase.Command{}, fmt.Errorf("CorrectionWorkflow - build expectation: %w", err)
	}
	intents, err := correctionProjectionIntents(command, authority)
	if err != nil {
		return correctionusecase.Command{}, err
	}
	fields := make([]correctionusecase.Field, len(command.Fields))
	for index, field := range command.Fields {
		fields[index] = correctionusecase.Field(field)
	}
	unlocks := make([]correctionusecase.UnlockIntent, len(command.UnlockIntents))
	for index, intent := range command.UnlockIntents {
		unlocks[index] = correctionusecase.UnlockIntent{
			ReservationID: intent.ReservationID, TournamentID: intent.TournamentID,
			OwnerID: intent.OwnerID, SourceRevisionID: domain.DerivedRevisionID(intent.SourceRevisionID),
			ExpectedRevision: intent.ExpectedRevision, ExpectedUsed: intent.ExpectedUsed,
			ExpectedDisclosed: intent.ExpectedDisclosed, EvidenceDigest: intent.EvidenceDigest,
			BindingDigest: intent.BindingDigest,
		}
	}
	return correctionusecase.Command{
		TournamentID: command.TournamentID, SeriesID: command.SeriesID, GameID: command.GameID,
		CommandID:        command.CommandID,
		CascadeCommandID: correctionWorkflowID(command.CommandID, "cascade-command"),
		OperatorID:       command.Operator.ActorID, Confirmed: command.Confirmed,
		Reason: correctionusecase.Reason(command.Reason), Explanation: command.Explanation,
		RequestedAt: requestedAt, Expected: expected,
		Patch: correctionusecase.Patch{
			State: command.Patch.State, Reason: command.Patch.Reason,
			WinnerID: cloneCorrectionUUID(command.Patch.WinnerID),
			SolveMetadata: correctionusecase.SolveMetadata{
				SolvedAt:       cloneCorrectionTime(command.Patch.SolvedAt),
				SubmissionID:   cloneCorrectionUUID(command.Patch.SubmissionID),
				EvidenceDigest: command.Patch.EvidenceDigest,
			},
		},
		Fields: fields,
		NextResultRevisionID: domain.OfficialResultRevisionID(
			correctionWorkflowID(command.CommandID, "game-result-revision"),
		),
		NextScoreRevisionID: domain.SeriesScoreRevisionID(
			correctionWorkflowID(command.CommandID, "series-score-revision"),
		),
		NextSeriesResultRevisionID: domain.OfficialResultRevisionID(
			correctionWorkflowID(command.CommandID, "series-result-revision"),
		),
		NextReadinessRevisionID: correctionWorkflowID(command.CommandID, "readiness-revision"),
		ProjectionIntents:       intents, UnlockIntents: unlocks,
	}, nil
}

func correctionProjectionIntents(
	command CorrectionCommand,
	authority correctionusecase.Authority,
) ([]correctionusecase.ProjectionIntent, error) {
	current := correctionCurrentProjectionRevisions(authority.DAG.Snapshot().Projections)
	intents := make([]correctionusecase.ProjectionIntent, len(command.ProjectionIntents))
	for index, intent := range command.ProjectionIntents {
		expected, exists := current[domain.ArtifactRef{
			Kind: domain.ArtifactKind(intent.ExpectedRevision.ArtifactKind), EntityID: intent.ExpectedRevision.ArtifactID,
		}]
		if !exists || !correctionProjectionExpectationMatches(intent.ExpectedRevision, expected) {
			return nil, domain.ErrConflict
		}
		payload, err := marshalCorrectionProjectionDocument(command, intent)
		if err != nil {
			return nil, err
		}
		if sha256.Sum256(payload) != intent.PayloadDigest {
			return nil, domain.ErrValidation
		}
		intents[index] = correctionusecase.NewProjectionIntent(
			expected, domain.DerivedRevisionID(intent.NextRevisionID), intent.DecisionID, payload,
		)
	}
	return intents, nil
}

func correctionCurrentProjectionRevisions(
	projections []domain.ProjectionRevision,
) map[domain.ArtifactRef]domain.DerivedRevision {
	current := make(map[domain.ArtifactRef]domain.DerivedRevision, len(projections))
	for _, projection := range projections {
		revision := projection.Revision()
		previous, found := current[revision.Artifact()]
		if !found || revision.RevisionNo() > previous.RevisionNo() {
			current[revision.Artifact()] = revision
		}
	}
	return current
}

func correctionProjectionExpectationMatches(
	view ProjectionRevisionExpectation,
	revision domain.DerivedRevision,
) bool {
	if view.ID != revision.ID().UUID() || view.TournamentID != revision.TournamentID() ||
		view.ArtifactKind != string(revision.Artifact().Kind) || view.ArtifactID != revision.Artifact().EntityID ||
		view.RevisionNo != revision.RevisionNo() || view.PayloadDigest != revision.PayloadDigest() ||
		!view.CreatedAt.Equal(revision.CreatedAt()) {
		return false
	}
	previous := revision.PreviousRevisionID()
	if view.PreviousRevisionID == nil || previous == nil {
		return view.PreviousRevisionID == nil && previous == nil
	}
	return *view.PreviousRevisionID == previous.UUID()
}

type correctionProjectionArtifactDocument struct {
	Kind                     string     `json:"kind"`
	ID                       uuid.UUID  `json:"id"`
	ExpectedRevisionID       uuid.UUID  `json:"expected_revision_id"`
	ExpectedRevisionNo       int        `json:"expected_revision_no"`
	ExpectedPreviousRevision *uuid.UUID `json:"expected_previous_revision_id"`
	ExpectedPayloadDigest    string     `json:"expected_payload_digest"`
	NextRevisionID           uuid.UUID  `json:"next_revision_id"`
	DecisionID               uuid.UUID  `json:"decision_id"`
}

type correctionProjectionPatchDocument struct {
	State          domain.GameState        `json:"state"`
	Reason         domain.GameResultReason `json:"reason"`
	WinnerID       *uuid.UUID              `json:"winner_id"`
	SolvedAt       *time.Time              `json:"solved_at"`
	SubmissionID   *uuid.UUID              `json:"submission_id"`
	EvidenceDigest string                  `json:"evidence_digest"`
}

type correctionProjectionDocument struct {
	Schema       string                               `json:"schema"`
	TournamentID uuid.UUID                            `json:"tournament_id"`
	SeriesID     uuid.UUID                            `json:"series_id"`
	GameID       uuid.UUID                            `json:"game_id"`
	CommandID    uuid.UUID                            `json:"command_id"`
	Reason       string                               `json:"reason"`
	Explanation  string                               `json:"explanation"`
	Fields       []string                             `json:"fields"`
	Artifact     correctionProjectionArtifactDocument `json:"artifact"`
	Patch        correctionProjectionPatchDocument    `json:"patch"`
}

func marshalCorrectionProjectionDocument(
	command CorrectionCommand,
	intent CorrectionProjectionIntent,
) ([]byte, error) {
	fields := append([]string(nil), command.Fields...)
	slices.Sort(fields)
	document := correctionProjectionDocument{
		Schema: correctionProjectionDocumentSchema, TournamentID: command.TournamentID,
		SeriesID: command.SeriesID, GameID: command.GameID, CommandID: command.CommandID,
		Reason: command.Reason, Explanation: command.Explanation, Fields: fields,
		Artifact: correctionProjectionArtifactDocument{
			Kind: intent.ExpectedRevision.ArtifactKind, ID: intent.ExpectedRevision.ArtifactID,
			ExpectedRevisionID:       intent.ExpectedRevision.ID,
			ExpectedRevisionNo:       intent.ExpectedRevision.RevisionNo,
			ExpectedPreviousRevision: cloneCorrectionUUID(intent.ExpectedRevision.PreviousRevisionID),
			ExpectedPayloadDigest:    hex.EncodeToString(intent.ExpectedRevision.PayloadDigest[:]),
			NextRevisionID:           intent.NextRevisionID, DecisionID: intent.DecisionID,
		},
		Patch: correctionProjectionPatchDocument{
			State: command.Patch.State, Reason: command.Patch.Reason,
			WinnerID:       cloneCorrectionUUID(command.Patch.WinnerID),
			SolvedAt:       cloneCorrectionTime(command.Patch.SolvedAt),
			SubmissionID:   cloneCorrectionUUID(command.Patch.SubmissionID),
			EvidenceDigest: hex.EncodeToString(command.Patch.EvidenceDigest[:]),
		},
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("CorrectionWorkflow - encode projection: %w", err)
	}
	return payload, nil
}

func correctionRequestDigest(command CorrectionCommand) ([sha256.Size]byte, error) {
	canonical := command
	canonical.Fields = append([]string(nil), command.Fields...)
	slices.Sort(canonical.Fields)
	canonical.ProjectionIntents = append([]CorrectionProjectionIntent(nil), command.ProjectionIntents...)
	sort.Slice(canonical.ProjectionIntents, func(first, second int) bool {
		return canonical.ProjectionIntents[first].ExpectedRevision.ID.String() <
			canonical.ProjectionIntents[second].ExpectedRevision.ID.String()
	})
	canonical.UnlockIntents = append([]CorrectionUnlockIntent(nil), command.UnlockIntents...)
	sort.Slice(canonical.UnlockIntents, func(first, second int) bool {
		return canonical.UnlockIntents[first].ReservationID.String() <
			canonical.UnlockIntents[second].ReservationID.String()
	})
	payload, err := json.Marshal(canonical)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("CorrectionWorkflow - encode command: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func correctionEvidence(
	command CorrectionCommand,
	plan correctionusecase.Plan,
) (CorrectionEvidence, error) {
	audit := plan.Audit()
	if audit.CommandID != command.CommandID || audit.TournamentID != command.TournamentID ||
		audit.SeriesID != command.SeriesID || audit.GameID != command.GameID ||
		audit.OperatorID != command.Operator.ActorID || string(audit.Reason) != command.Reason {
		return CorrectionEvidence{}, domain.ErrInternal
	}
	supersessions := plan.Supersessions()
	views := make([]ProjectionSupersessionView, len(supersessions))
	for index, item := range supersessions {
		views[index] = ProjectionSupersessionView{
			ArtifactKind: string(item.Artifact.Kind), ArtifactID: item.Artifact.EntityID,
			PreviousRevisionID:    item.PreviousRevisionID.UUID(),
			SuccessorRevisionID:   item.SuccessorRevisionID.UUID(),
			PreviousDecisionID:    cloneCorrectionUUID(item.PreviousDecisionID),
			ReplacementDecisionID: item.ReplacementDecisionID,
		}
	}
	unlocksByID := make(map[uuid.UUID]CorrectionUnlockIntent, len(command.UnlockIntents))
	for _, intent := range command.UnlockIntents {
		unlocksByID[intent.ReservationID] = intent
	}
	releases := plan.Releases()
	unlocks := make([]CorrectionUnlockIntent, len(releases))
	for index, release := range releases {
		intent, exists := unlocksByID[release.ReservationID]
		if !exists || intent.ExpectedRevision != release.ExpectedRevision ||
			intent.TournamentID != release.TournamentID || intent.OwnerID != release.OwnerID ||
			intent.SourceRevisionID != release.SourceRevisionID.UUID() ||
			intent.ExpectedUsed != release.ExpectedUsed || intent.ExpectedDisclosed != release.ExpectedDisclosed ||
			intent.EvidenceDigest != release.EvidenceDigest {
			return CorrectionEvidence{}, domain.ErrInternal
		}
		unlocks[index] = intent
	}
	evidence := CorrectionEvidence{
		CommandID: command.CommandID, TournamentID: command.TournamentID,
		SeriesID: command.SeriesID, GameID: command.GameID, OperatorID: command.Operator.ActorID,
		Reason: command.Reason, Fields: append([]string(nil), command.Fields...),
		RequestedAt: audit.RequestedAt, ValidationDigest: audit.ValidationDigest,
		Supersessions: views, UnlockIntents: unlocks,
	}
	if !validCorrectionEvidence(evidence, command) {
		return CorrectionEvidence{}, domain.ErrInternal
	}
	return evidence, nil
}

func validCorrectionWorkflowAuthority(
	authority CorrectionWorkflowAuthority,
	command CorrectionCommand,
) bool {
	core := authority.Core
	return authority.RosterID != uuid.Nil && authority.ProjectionRevisionID != uuid.Nil &&
		authority.ProjectionRevision >= 1 && core.TournamentState.IsValid() && core.TournamentRevision >= 1 &&
		authority.Stage.TournamentID == command.TournamentID &&
		authority.Stage.TournamentState == core.TournamentState &&
		authority.Stage.TournamentRevision == core.TournamentRevision &&
		core.Series.TournamentID == command.TournamentID && core.Series.ID == command.SeriesID &&
		core.GameResult.Scope.TournamentID == command.TournamentID &&
		core.GameResult.Scope.SeriesID == command.SeriesID && core.GameResult.Scope.GameID == command.GameID &&
		core.Score.Scope.TournamentID == command.TournamentID && core.Score.Scope.SeriesID == command.SeriesID &&
		core.SeriesResult.Scope.TournamentID == command.TournamentID &&
		core.SeriesResult.Scope.SeriesID == command.SeriesID
}

func validCorrectionCommandRecord(record CorrectionCommandRecord) bool {
	if record.CommandID == uuid.Nil || record.TournamentID == uuid.Nil || record.RosterID == uuid.Nil ||
		record.SeriesID == uuid.Nil || record.GameID == uuid.Nil || record.OperatorID == uuid.Nil ||
		record.ExpectedProjectionRevision < 1 || record.RequestDigest == ([sha256.Size]byte{}) ||
		!domain.IsValidServerTime(record.ExecutedAt) || !record.ExecutedAt.Equal(record.Evidence.RequestedAt) {
		return false
	}
	command := CorrectionCommand{
		CommandScope: CommandScope{
			Operator:     OperatorIdentity{ActorID: record.OperatorID},
			TournamentID: record.TournamentID, CommandID: record.CommandID,
		},
		SeriesID: record.SeriesID, GameID: record.GameID, Reason: record.Evidence.Reason,
		Fields: append([]string(nil), record.Evidence.Fields...),
	}
	return validCorrectionEvidence(record.Evidence, command)
}

func correctionCommandRecordMatches(
	record CorrectionCommandRecord,
	command CorrectionCommand,
	rosterID uuid.UUID,
	digest [sha256.Size]byte,
) bool {
	return record.CommandID == command.CommandID && record.TournamentID == command.TournamentID &&
		record.RosterID == rosterID && record.SeriesID == command.SeriesID && record.GameID == command.GameID &&
		record.OperatorID == command.Operator.ActorID &&
		record.ExpectedProjectionRevision == command.ExpectedProjectionRevision &&
		record.RequestDigest == digest && validCorrectionEvidence(record.Evidence, command)
}

func correctionWorkflowError(
	err error,
	command CorrectionCommand,
	authority CorrectionWorkflowAuthority,
) error {
	if err == nil {
		return nil
	}
	var conflict *RevisionConflictError
	if errors.As(err, &conflict) {
		return err
	}
	code := correctionusecase.Code(err)
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, correctionusecase.ErrCutoff) ||
		code == correctionusecase.RejectionStale || code == correctionusecase.RejectionCutoff {
		return newCorrectionConflict(command, authority)
	}
	if errors.Is(err, correctionusecase.ErrInvalid) {
		return domain.ErrValidation
	}
	return err
}

func newCorrectionConflict(
	command CorrectionCommand,
	authority CorrectionWorkflowAuthority,
) error {
	return &RevisionConflictError{
		ExpectedRevision: command.ExpectedProjectionRevision,
		CurrentRevision:  authority.ProjectionRevision,
		CurrentState:     authority.Core.TournamentState,
	}
}

func correctionWorkflowID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("tournament-correction:"+role))
}

func cloneCorrectionEvidence(value CorrectionEvidence) CorrectionEvidence {
	clone := value
	clone.Fields = append([]string{}, value.Fields...)
	clone.Supersessions = make([]ProjectionSupersessionView, len(value.Supersessions))
	for index, item := range value.Supersessions {
		clone.Supersessions[index] = item
		clone.Supersessions[index].PreviousDecisionID = cloneCorrectionUUID(item.PreviousDecisionID)
	}
	clone.UnlockIntents = append([]CorrectionUnlockIntent{}, value.UnlockIntents...)
	return clone
}

func cloneCorrectionUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneCorrectionTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

var _ CorrectionPort = (*CorrectionWorkflow)(nil)
