package arena

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidCorrectionStage  = errors.New("invalid Arena correction stage rollback")
	ErrCorrectionStageConflict = errors.New("arena correction stage commit conflict")
)

type CorrectionStageMode string

const (
	CorrectionStagePlayoff CorrectionStageMode = "playoff"
	CorrectionStageGolden  CorrectionStageMode = "golden"
)

type CorrectionStageTransition string

const (
	CorrectionStageUnchanged           CorrectionStageTransition = "unchanged"
	CorrectionStagePlayoffToGolden     CorrectionStageTransition = "playoff_to_golden"
	CorrectionStageGoldenToPlayoff     CorrectionStageTransition = "golden_to_playoff"
	CorrectionStageGoldenGroupsChanged CorrectionStageTransition = "golden_groups_changed"
)

type CorrectionStageLayout struct {
	Mode         CorrectionStageMode
	GoldenGroups []domain.ArenaGoldenGroupState
	Paused       []RetainedGoldenPrestartExpectation
}

type CorrectionStageSnapshot struct {
	TournamentID       uuid.UUID
	TournamentState    domain.ArenaTournamentState
	TournamentRevision int64
	CutoffEvents       []CorrectionCutoffEvent
	Layout             CorrectionStageLayout
}

type CorrectionStageGroupSupersessionIntent struct {
	GroupID    uuid.UUID
	RevisionID domain.ArenaDerivedRevisionID
}

type CorrectionStageCommand struct {
	Correction         AtomicCorrectionPlan
	Corrected          CorrectionStageLayout
	GroupSupersessions []CorrectionStageGroupSupersessionIntent
}

type CorrectionStageGroupSupersession struct {
	GroupID            uuid.UUID
	PreviousRevisionID domain.ArenaDerivedRevisionID
	RevisionID         domain.ArenaDerivedRevisionID
	ReplacementGroupID *uuid.UUID
	Previous           domain.ArenaGoldenGroupState
}

type CorrectionStageResult struct {
	Transition         CorrectionStageTransition
	Corrected          CorrectionStageLayout
	GroupSupersessions []CorrectionStageGroupSupersession
	CancelledAttempts  []domain.ArenaGoldenAttempt
	WithdrawPlayoff    bool
	CreatePlayoff      bool
}

type CorrectionStageCommit struct {
	ExpectedTournamentState    domain.ArenaTournamentState
	ExpectedTournamentRevision int64
	Correction                 []byte
	Result                     CorrectionStageResult
}

type CorrectionStageRepository interface {
	LoadCorrectionStage(ctx context.Context, tournamentID uuid.UUID) (CorrectionStageSnapshot, error)
	CommitCorrectionStage(ctx context.Context, commit CorrectionStageCommit) (bool, error)
}

type CorrectionStageUseCase struct {
	transactions TransactionManager
	repository   CorrectionStageRepository
	clock        Clock
}

func NewCorrectionStageUseCase(
	transactions TransactionManager,
	repository CorrectionStageRepository,
	clock Clock,
) *CorrectionStageUseCase {
	return &CorrectionStageUseCase{transactions: transactions, repository: repository, clock: clock}
}

func (u *CorrectionStageUseCase) Rollback(
	ctx context.Context,
	command CorrectionStageCommand,
) (CorrectionStageResult, error) {
	if u == nil || u.transactions == nil || u.repository == nil || u.clock == nil {
		return CorrectionStageResult{}, invalidCorrectionStage("missing transaction dependency", nil)
	}
	command = cloneCorrectionStageCommand(command)
	if err := command.Correction.Validate(); err != nil {
		return CorrectionStageResult{}, invalidCorrectionStage("invalid correction plan", err)
	}
	condition := command.Correction.CutoffCondition()
	var committed CorrectionStageResult
	err := u.transactions.Do(ctx, func(txCtx context.Context) error {
		snapshot, err := u.repository.LoadCorrectionStage(txCtx, condition.TournamentID())
		if err != nil {
			return err
		}
		snapshot = cloneCorrectionStageSnapshot(snapshot)
		if snapshot.TournamentID != condition.TournamentID() {
			return invalidCorrectionStage("loaded another Tournament", nil)
		}
		if err := condition.ValidateCurrent(
			snapshot.TournamentState,
			snapshot.TournamentRevision,
			snapshot.CutoffEvents,
		); err != nil {
			return err
		}
		changedAt := u.clock.Now()
		if !validArenaServerTime(changedAt) {
			return invalidCorrectionStage("invalid rollback clock", nil)
		}
		result, err := planCorrectionStageRollback(snapshot, command, changedAt)
		if err != nil {
			return err
		}
		changed, err := u.repository.CommitCorrectionStage(txCtx, CorrectionStageCommit{
			ExpectedTournamentState:    snapshot.TournamentState,
			ExpectedTournamentRevision: snapshot.TournamentRevision,
			Correction:                 command.Correction.Bytes(), Result: result,
		})
		if err != nil {
			return err
		}
		if !changed {
			return ErrCorrectionStageConflict
		}
		committed = cloneCorrectionStageResult(result)
		return nil
	})
	if err != nil {
		return CorrectionStageResult{}, err
	}
	return committed, nil
}

func planCorrectionStageRollback(
	snapshot CorrectionStageSnapshot,
	command CorrectionStageCommand,
	changedAt time.Time,
) (CorrectionStageResult, error) {
	correctedSource, err := correctionStageStandingsRevision(command.Correction)
	if err != nil {
		return CorrectionStageResult{}, err
	}
	if err := validateCorrectionStageLayout(snapshot.TournamentID, snapshot.Layout, false, domain.ArenaDerivedRevisionID{}); err != nil {
		return CorrectionStageResult{}, err
	}
	if err := validateCorrectionStageLayout(snapshot.TournamentID, command.Corrected, true, correctedSource); err != nil {
		return CorrectionStageResult{}, err
	}

	affected := correctionStageAffectedGroups(snapshot.Layout.GoldenGroups, command.Corrected.GoldenGroups)
	intents, err := correctionStageSupersessionIntents(command.GroupSupersessions, affected)
	if err != nil {
		return CorrectionStageResult{}, err
	}
	if err := validateCorrectionStageFreshIdentities(
		snapshot.Layout.GoldenGroups,
		command.Corrected.GoldenGroups,
		intents,
	); err != nil {
		return CorrectionStageResult{}, err
	}

	result := CorrectionStageResult{
		Transition:      correctionStageTransition(snapshot.Layout, command.Corrected, len(affected) > 0),
		Corrected:       cloneCorrectionStageLayout(command.Corrected),
		WithdrawPlayoff: snapshot.Layout.Mode == CorrectionStagePlayoff && command.Corrected.Mode == CorrectionStageGolden,
		CreatePlayoff:   snapshot.Layout.Mode == CorrectionStageGolden && command.Corrected.Mode == CorrectionStagePlayoff,
	}
	for _, group := range affected {
		intent := intents[group.ID]
		replacementID := correctionStageReplacementGroup(group, command.Corrected.GoldenGroups)
		supersession := CorrectionStageGroupSupersession{
			GroupID: group.ID, PreviousRevisionID: group.RevisionID,
			RevisionID: intent.RevisionID, ReplacementGroupID: replacementID,
			Previous: cloneCorrectionStageGoldenGroup(group),
		}
		result.GroupSupersessions = append(result.GroupSupersessions, supersession)
		attempt, found := correctionStageUnstartedAttempt(group)
		if !found {
			if len(group.Attempts) != 0 {
				return CorrectionStageResult{}, invalidCorrectionStage(
					"Golden Attempt already has execution evidence", nil,
				)
			}
			continue
		}
		if !correctionStageHasPause(snapshot.Layout.Paused, group, attempt) {
			return CorrectionStageResult{}, invalidCorrectionStage(
				"Golden Attempt is not in an eligible retained technical pause", nil,
			)
		}
		attempt.GroupRevisionID = intent.RevisionID
		attempt.State = domain.ArenaGoldenAttemptStateCancelled
		attempt.FinishedAt = cloneTimePointer(&changedAt)
		if err := attempt.Validate(); err != nil {
			return CorrectionStageResult{}, invalidCorrectionStage("cancel retained Golden Attempt", err)
		}
		result.CancelledAttempts = append(result.CancelledAttempts, attempt)
	}
	return result, nil
}

func validateCorrectionStageLayout(
	tournamentID uuid.UUID,
	layout CorrectionStageLayout,
	corrected bool,
	correctedSource domain.ArenaDerivedRevisionID,
) error {
	if tournamentID == uuid.Nil || (layout.Mode != CorrectionStagePlayoff && layout.Mode != CorrectionStageGolden) {
		return invalidCorrectionStage("invalid stage layout", nil)
	}
	if layout.Mode == CorrectionStagePlayoff {
		if len(layout.GoldenGroups) != 0 || len(layout.Paused) != 0 {
			return invalidCorrectionStage("playoff layout contains Golden execution", nil)
		}
		return nil
	}
	if len(layout.GoldenGroups) == 0 {
		return invalidCorrectionStage("Golden layout has no tie groups", nil)
	}
	return validateCorrectionStageGoldenGroups(tournamentID, layout, corrected, correctedSource)
}

func validateCorrectionStageGoldenGroups(
	tournamentID uuid.UUID,
	layout CorrectionStageLayout,
	corrected bool,
	correctedSource domain.ArenaDerivedRevisionID,
) error {
	seenGroups := make(map[uuid.UUID]struct{}, len(layout.GoldenGroups))
	seenRevisions := make(map[domain.ArenaDerivedRevisionID]struct{}, len(layout.GoldenGroups))
	for _, group := range layout.GoldenGroups {
		if err := validateCorrectionStageGoldenGroup(
			tournamentID, group, corrected, correctedSource, seenGroups, seenRevisions,
		); err != nil {
			return err
		}
		seenGroups[group.ID] = struct{}{}
		seenRevisions[group.RevisionID] = struct{}{}
	}
	if corrected && len(layout.Paused) != 0 {
		return invalidCorrectionStage("corrected layout retained a technical pause", nil)
	}
	return nil
}

func validateCorrectionStageGoldenGroup(
	tournamentID uuid.UUID,
	group domain.ArenaGoldenGroupState,
	corrected bool,
	correctedSource domain.ArenaDerivedRevisionID,
	seenGroups map[uuid.UUID]struct{},
	seenRevisions map[domain.ArenaDerivedRevisionID]struct{},
) error {
	if group.TournamentID != tournamentID {
		return invalidCorrectionStage("Golden group belongs to another Tournament", nil)
	}
	if _, err := domain.NewArenaGoldenGroup(group); err != nil {
		return invalidCorrectionStage("invalid Golden group", err)
	}
	if _, duplicate := seenGroups[group.ID]; duplicate {
		return invalidCorrectionStage("duplicate Golden group", nil)
	}
	if _, duplicate := seenRevisions[group.RevisionID]; duplicate {
		return invalidCorrectionStage("duplicate Golden group revision", nil)
	}
	if corrected && (group.SourceProjectionRevisionID != correctedSource ||
		group.ParticipationEstablished || len(group.Attempts) != 0) {
		return invalidCorrectionStage("corrected Golden group is not a fresh seed", nil)
	}
	return nil
}

func correctionStageAffectedGroups(
	current []domain.ArenaGoldenGroupState,
	corrected []domain.ArenaGoldenGroupState,
) []domain.ArenaGoldenGroupState {
	affected := make([]domain.ArenaGoldenGroupState, 0)
	for _, group := range current {
		retained := false
		for _, candidate := range corrected {
			if correctionStageGroupTopologyEqual(group, candidate) {
				retained = true
				break
			}
		}
		if !retained {
			affected = append(affected, cloneCorrectionStageGoldenGroup(group))
		}
	}
	return affected
}

func correctionStageSupersessionIntents(
	input []CorrectionStageGroupSupersessionIntent,
	affected []domain.ArenaGoldenGroupState,
) (map[uuid.UUID]CorrectionStageGroupSupersessionIntent, error) {
	if len(input) != len(affected) {
		return nil, invalidCorrectionStage("Golden supersession coverage is incomplete", nil)
	}
	affectedIDs := make(map[uuid.UUID]struct{}, len(affected))
	for _, group := range affected {
		affectedIDs[group.ID] = struct{}{}
	}
	intents := make(map[uuid.UUID]CorrectionStageGroupSupersessionIntent, len(input))
	for _, intent := range input {
		if intent.GroupID == uuid.Nil || intent.RevisionID.IsZero() {
			return nil, invalidCorrectionStage("invalid Golden supersession identity", nil)
		}
		if _, exists := affectedIDs[intent.GroupID]; !exists {
			return nil, invalidCorrectionStage("supersession targets an unaffected Golden group", nil)
		}
		if _, duplicate := intents[intent.GroupID]; duplicate {
			return nil, invalidCorrectionStage("duplicate Golden supersession", nil)
		}
		intents[intent.GroupID] = intent
	}
	return intents, nil
}

func validateCorrectionStageFreshIdentities(
	current []domain.ArenaGoldenGroupState,
	corrected []domain.ArenaGoldenGroupState,
	intents map[uuid.UUID]CorrectionStageGroupSupersessionIntent,
) error {
	reserved := make(map[uuid.UUID]struct{})
	for _, group := range current {
		reserved[group.ID] = struct{}{}
		reserved[group.RevisionID.UUID()] = struct{}{}
		for _, attempt := range group.Attempts {
			reserved[attempt.ID] = struct{}{}
		}
	}
	for _, group := range corrected {
		if correctionStageMatchesAnyTopology(group, current) {
			continue
		}
		for _, id := range []uuid.UUID{group.ID, group.RevisionID.UUID()} {
			if _, reused := reserved[id]; reused {
				return invalidCorrectionStage("replacement Golden group reused an identity", nil)
			}
			reserved[id] = struct{}{}
		}
	}
	for _, intent := range intents {
		if _, reused := reserved[intent.RevisionID.UUID()]; reused {
			return invalidCorrectionStage("superseded Golden revision reused an identity", nil)
		}
		reserved[intent.RevisionID.UUID()] = struct{}{}
	}
	return nil
}

func correctionStageTransition(
	current CorrectionStageLayout,
	corrected CorrectionStageLayout,
	groupsChanged bool,
) CorrectionStageTransition {
	switch {
	case current.Mode == CorrectionStagePlayoff && corrected.Mode == CorrectionStageGolden:
		return CorrectionStagePlayoffToGolden
	case current.Mode == CorrectionStageGolden && corrected.Mode == CorrectionStagePlayoff:
		return CorrectionStageGoldenToPlayoff
	case groupsChanged:
		return CorrectionStageGoldenGroupsChanged
	default:
		return CorrectionStageUnchanged
	}
}

func correctionStageStandingsRevision(plan AtomicCorrectionPlan) (domain.ArenaDerivedRevisionID, error) {
	var current domain.ArenaDerivedRevision
	for _, projection := range plan.ProjectionRevisions() {
		revision := projection.Revision()
		if revision.Artifact().Kind == domain.ArenaArtifactKindStandings && revision.RevisionNo() > current.RevisionNo() {
			current = revision
		}
	}
	if current.ID().IsZero() {
		return domain.ArenaDerivedRevisionID{}, invalidCorrectionStage("correction has no standings successor", nil)
	}
	return current.ID(), nil
}

func correctionStageUnstartedAttempt(
	group domain.ArenaGoldenGroupState,
) (domain.ArenaGoldenAttempt, bool) {
	if len(group.Attempts) == 0 {
		return domain.ArenaGoldenAttempt{}, false
	}
	attempt := cloneGoldenAttempt(group.Attempts[len(group.Attempts)-1])
	if attempt.State != domain.ArenaGoldenAttemptStatePlanned &&
		attempt.State != domain.ArenaGoldenAttemptStateWaitingReady {
		return domain.ArenaGoldenAttempt{}, false
	}
	return attempt, true
}

func correctionStageHasPause(
	paused []RetainedGoldenPrestartExpectation,
	group domain.ArenaGoldenGroupState,
	attempt domain.ArenaGoldenAttempt,
) bool {
	for _, expectation := range paused {
		if expectation.Scope == (GoldenStateScope{
			TournamentID:    group.TournamentID,
			GroupID:         group.ID,
			GroupRevisionID: group.RevisionID,
		}) && expectation.SessionID != uuid.Nil && expectation.RevisionID != uuid.Nil &&
			expectation.Revision > 0 && expectation.State == RetainedGoldenPrestartPaused &&
			expectation.PayloadDigest != ([sha256.Size]byte{}) && attempt.StartedAt == nil &&
			attempt.FinishedAt == nil && attempt.RetainedAt != nil {
			return true
		}
	}
	return false
}

func correctionStageReplacementGroup(
	previous domain.ArenaGoldenGroupState,
	corrected []domain.ArenaGoldenGroupState,
) *uuid.UUID {
	for _, group := range corrected {
		if correctionStageGroupsOverlap(previous, group) {
			id := group.ID
			return &id
		}
	}
	return nil
}

func correctionStageGroupsOverlap(first, second domain.ArenaGoldenGroupState) bool {
	if first.PositionFrom <= second.PositionTo && second.PositionFrom <= first.PositionTo {
		return true
	}
	for _, member := range first.Members {
		for _, candidate := range second.Members {
			if member.ParticipantID == candidate.ParticipantID {
				return true
			}
		}
	}
	return false
}

func correctionStageMatchesAnyTopology(
	group domain.ArenaGoldenGroupState,
	groups []domain.ArenaGoldenGroupState,
) bool {
	for _, candidate := range groups {
		if correctionStageGroupTopologyEqual(group, candidate) {
			return true
		}
	}
	return false
}

func correctionStageGroupTopologyEqual(first, second domain.ArenaGoldenGroupState) bool {
	if first.PositionFrom != second.PositionFrom || first.PositionTo != second.PositionTo ||
		len(first.Members) != len(second.Members) {
		return false
	}
	firstIDs := correctionStageMemberIDs(first.Members)
	secondIDs := correctionStageMemberIDs(second.Members)
	for index := range firstIDs {
		if firstIDs[index] != secondIDs[index] {
			return false
		}
	}
	return true
}

func correctionStageMemberIDs(members []domain.ArenaGoldenMember) []uuid.UUID {
	ids := make([]uuid.UUID, len(members))
	for index, member := range members {
		ids[index] = member.ParticipantID
	}
	for left := 0; left < len(ids); left++ {
		for right := left + 1; right < len(ids); right++ {
			if bytes.Compare(ids[right][:], ids[left][:]) < 0 {
				ids[left], ids[right] = ids[right], ids[left]
			}
		}
	}
	return ids
}

func cloneCorrectionStageCommand(command CorrectionStageCommand) CorrectionStageCommand {
	clone := command
	clone.Corrected = cloneCorrectionStageLayout(command.Corrected)
	clone.GroupSupersessions = append(
		[]CorrectionStageGroupSupersessionIntent(nil),
		command.GroupSupersessions...,
	)
	return clone
}

func cloneCorrectionStageSnapshot(snapshot CorrectionStageSnapshot) CorrectionStageSnapshot {
	clone := snapshot
	clone.CutoffEvents = append([]CorrectionCutoffEvent(nil), snapshot.CutoffEvents...)
	clone.Layout = cloneCorrectionStageLayout(snapshot.Layout)
	return clone
}

func cloneCorrectionStageLayout(layout CorrectionStageLayout) CorrectionStageLayout {
	clone := layout
	clone.GoldenGroups = make([]domain.ArenaGoldenGroupState, len(layout.GoldenGroups))
	for index, group := range layout.GoldenGroups {
		clone.GoldenGroups[index] = cloneCorrectionStageGoldenGroup(group)
	}
	clone.Paused = append([]RetainedGoldenPrestartExpectation(nil), layout.Paused...)
	return clone
}

func cloneCorrectionStageGoldenGroup(group domain.ArenaGoldenGroupState) domain.ArenaGoldenGroupState {
	clone := group
	clone.Members = append([]domain.ArenaGoldenMember(nil), group.Members...)
	clone.Attempts = make([]domain.ArenaGoldenAttempt, len(group.Attempts))
	for index, attempt := range group.Attempts {
		clone.Attempts[index] = cloneGoldenAttempt(attempt)
	}
	return clone
}

func cloneCorrectionStageResult(result CorrectionStageResult) CorrectionStageResult {
	clone := result
	clone.Corrected = cloneCorrectionStageLayout(result.Corrected)
	clone.GroupSupersessions = make([]CorrectionStageGroupSupersession, len(result.GroupSupersessions))
	for index, supersession := range result.GroupSupersessions {
		clone.GroupSupersessions[index] = supersession
		clone.GroupSupersessions[index].Previous = cloneCorrectionStageGoldenGroup(supersession.Previous)
		clone.GroupSupersessions[index].ReplacementGroupID = cloneUUIDPointer(supersession.ReplacementGroupID)
	}
	clone.CancelledAttempts = make([]domain.ArenaGoldenAttempt, len(result.CancelledAttempts))
	for index, attempt := range result.CancelledAttempts {
		clone.CancelledAttempts[index] = cloneGoldenAttempt(attempt)
	}
	return clone
}

func invalidCorrectionStage(detail string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrInvalidCorrectionStage, detail)
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalidCorrectionStage, detail, cause)
}
