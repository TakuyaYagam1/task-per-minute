package snapshot

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	executionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
)

type OperatorIdentity = operationusecase.OperatorIdentity
type RevisionConflictError = operationusecase.RevisionConflictError
type RosterParticipantView = rosterusecase.RosterParticipantView
type RosterView = rosterusecase.RosterView
type WaveView = executionusecase.WaveView

type OperatorCursor struct {
	ProjectionRevision int64
	AuthorityRevision  int64
	AuditSequence      int64
}

type SnapshotQuery struct {
	Operator     OperatorIdentity
	TournamentID uuid.UUID
	Cursor       *OperatorCursor
}

type PauseGraphView struct {
	Graph pauseusecase.PauseGraph
	Wave  WaveView
}

type RecoveryControlKind string

const (
	RecoveryControlReplay           RecoveryControlKind = "replay"
	RecoveryControlReserveExhausted RecoveryControlKind = "reserve_exhausted"
)

type RecoveryReplayDetails struct {
	Available                 bool
	ExpectedClosureRevisionID uuid.UUID
}

type RecoveryReserveCandidate struct {
	TaskID  uuid.UUID
	Version int
}

type RecoveryReserveExhaustedDetails struct {
	Candidates                    []RecoveryReserveCandidate
	CurrentSnapshotID             uuid.UUID
	ExpectedArtifactRevision      int64
	ExpectedArtifactRevisionID    uuid.UUID
	ExpectedAssignmentRevision    int64
	ExpectedCategoryRevision      int64
	ExpectedCategoryRevisionID    uuid.UUID
	ExpectedExhaustionCommandID   uuid.UUID
	ExpectedHistoryRevision       int64
	ExpectedHistoryRevisionID     uuid.UUID
	ExpectedPoolRevision          int64
	ExpectedPoolRevisionID        uuid.UUID
	ExpectedReservationRevision   int64
	ExpectedReservationRevisionID uuid.UUID
	ExpectedSnapshotID            uuid.UUID
}

type RecoveryControl struct {
	AssignmentID              uuid.UUID
	Attempts                  []domain.Game
	Category                  domain.Category
	ExpectedAuthorityRevision int64
	Kind                      RecoveryControlKind
	OldWaveID                 uuid.UUID
	PauseReason               *pauseusecase.PauseReason
	Reason                    string
	Replay                    *RecoveryReplayDetails
	ReserveExhausted          *RecoveryReserveExhaustedDetails
	SeriesID                  uuid.UUID
	SlotID                    uuid.UUID
}

type OperatorSnapshotView struct {
	Tournament       inbound.TournamentView
	Roster           RosterView
	Waves            []WaveView
	Series           []domain.Series
	PauseGraph       *PauseGraphView
	RecoveryControls []RecoveryControl
	NextCursor       OperatorCursor
}

type SnapshotPort interface {
	GetOperatorSnapshot(ctx context.Context, query SnapshotQuery) (OperatorSnapshotView, error)
}

func ValidSnapshotQuery(query SnapshotQuery) bool {
	if query.Operator.ActorID == uuid.Nil || query.TournamentID == uuid.Nil {
		return false
	}
	return query.Cursor == nil || (query.Cursor.ProjectionRevision >= 1 &&
		query.Cursor.AuthorityRevision >= 1 && query.Cursor.AuditSequence >= 0)
}

func ValidOperatorSnapshot(view OperatorSnapshotView, tournamentID uuid.UUID) bool {
	if !validSnapshotHeader(view, tournamentID) || !validSnapshotWaves(view.Waves, tournamentID) ||
		!validSnapshotSeries(view.Series, tournamentID) {
		return false
	}
	return validSnapshotPauseGraph(view.PauseGraph, tournamentID, view.Roster.ID) &&
		validSnapshotRecoveryControls(view.RecoveryControls)
}

func validSnapshotHeader(view OperatorSnapshotView, tournamentID uuid.UUID) bool {
	return validTournamentView(view.Tournament, tournamentID) &&
		rosterusecase.ValidRosterView(view.Roster, tournamentID) &&
		view.Tournament.RosterID == view.Roster.ID && view.Waves != nil && view.Series != nil &&
		view.NextCursor.ProjectionRevision >= 1 && view.NextCursor.AuthorityRevision >= 1 &&
		view.NextCursor.AuditSequence >= 0
}

func validTournamentView(view inbound.TournamentView, tournamentID uuid.UUID) bool {
	return lifecycleusecase.ValidTournamentView(view, tournamentID)
}

func validSnapshotWaves(waves []WaveView, tournamentID uuid.UUID) bool {
	for _, wave := range waves {
		if !executionusecase.ValidWaveView(wave, tournamentID, wave.Wave.ID) {
			return false
		}
	}
	return true
}

func validSnapshotSeries(seriesValues []domain.Series, tournamentID uuid.UUID) bool {
	for _, series := range seriesValues {
		if series.TournamentID != tournamentID || series.Validate() != nil {
			return false
		}
	}
	return true
}

func validSnapshotPauseGraph(view *PauseGraphView, tournamentID, rosterID uuid.UUID) bool {
	if view == nil {
		return true
	}
	graph := view.Graph
	return graph.Scope.Validate() == nil && graph.Scope.TournamentID == tournamentID &&
		graph.Scope.RosterID == rosterID && graph.Revision >= 1 &&
		executionusecase.ValidWaveView(view.Wave, tournamentID, graph.Wave.Wave.ID) &&
		view.Wave.Wave.ID == graph.Wave.Wave.ID
}

func validSnapshotRecoveryControls(controls []RecoveryControl) bool {
	type controlKey struct {
		kind       RecoveryControlKind
		assignment uuid.UUID
		series     uuid.UUID
		slot       uuid.UUID
	}
	seen := make(map[controlKey]struct{}, len(controls))
	for _, control := range controls {
		if !validRecoveryControl(control) {
			return false
		}
		key := controlKey{kind: control.Kind, assignment: control.AssignmentID, series: control.SeriesID, slot: control.SlotID}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

//nolint:gocyclo // Fail-closed control validation keeps each compare-and-set invariant explicit.
func validRecoveryControl(control RecoveryControl) bool {
	if control.AssignmentID == uuid.Nil || control.SeriesID == uuid.Nil || control.SlotID == uuid.Nil || control.OldWaveID == uuid.Nil ||
		!control.Category.IsValid() || control.ExpectedAuthorityRevision < 1 ||
		strings.TrimSpace(control.Reason) != control.Reason || len(control.Reason) == 0 || len(control.Reason) > 512 || len(control.Attempts) == 0 {
		return false
	}
	if control.PauseReason != nil && !validPauseReason(*control.PauseReason) {
		return false
	}
	for index, attempt := range control.Attempts {
		if attempt.SlotID != control.SlotID || attempt.Validate() != nil {
			return false
		}
		if attempt.AttemptNo != index+1 {
			return false
		}
	}
	lastAttempt := control.Attempts[len(control.Attempts)-1]
	if lastAttempt.State != domain.GameStateVoid || !lastAttempt.ResultReason.IsLegalFor(domain.GameStateVoid) {
		return false
	}
	switch control.Kind {
	case RecoveryControlReplay:
		return control.Replay != nil && control.Replay.Available &&
			control.Replay.ExpectedClosureRevisionID != uuid.Nil && control.ReserveExhausted == nil
	case RecoveryControlReserveExhausted:
		return control.Replay == nil && validRecoveryReserveExhausted(control.ReserveExhausted)
	default:
		return false
	}
}

func validPauseReason(reason pauseusecase.PauseReason) bool {
	switch reason {
	case pauseusecase.PauseReasonOperator,
		pauseusecase.PauseReasonDisconnect,
		pauseusecase.PauseReasonPlatform,
		pauseusecase.PauseReasonExecutionEpoch:
		return true
	default:
		return false
	}
}

//nolint:gocyclo // Durable reserve evidence has independent revision and identity guards.
func validRecoveryReserveExhausted(details *RecoveryReserveExhaustedDetails) bool {
	if details == nil || details.CurrentSnapshotID == uuid.Nil || details.ExpectedSnapshotID == uuid.Nil ||
		details.ExpectedExhaustionCommandID == uuid.Nil || details.ExpectedAssignmentRevision < 1 ||
		details.ExpectedPoolRevision < 1 || details.ExpectedHistoryRevision < 1 ||
		details.ExpectedArtifactRevision < 1 || details.ExpectedReservationRevision < 1 ||
		details.ExpectedCategoryRevision < 1 || details.ExpectedPoolRevisionID == uuid.Nil ||
		details.ExpectedHistoryRevisionID == uuid.Nil || details.ExpectedArtifactRevisionID == uuid.Nil ||
		details.ExpectedReservationRevisionID == uuid.Nil || details.ExpectedCategoryRevisionID == uuid.Nil {
		return false
	}
	if details.CurrentSnapshotID != details.ExpectedSnapshotID {
		return false
	}
	type candidateKey struct {
		taskID  uuid.UUID
		version int
	}
	seen := make(map[candidateKey]struct{}, len(details.Candidates))
	for _, candidate := range details.Candidates {
		if candidate.TaskID == uuid.Nil || candidate.Version < 1 {
			return false
		}
		key := candidateKey{taskID: candidate.TaskID, version: candidate.Version}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}
