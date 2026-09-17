package snapshot

import (
	"context"

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

type OperatorSnapshotView struct {
	Tournament inbound.TournamentView
	Roster     RosterView
	Waves      []WaveView
	Series     []domain.Series
	PauseGraph *PauseGraphView
	NextCursor OperatorCursor
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
	return validSnapshotPauseGraph(view.PauseGraph, tournamentID, view.Roster.ID)
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
