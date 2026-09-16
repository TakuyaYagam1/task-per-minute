package admin

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
)

const maxAuditEvents = 200

type AuditQuery struct {
	Operator OperatorIdentity
	Filter   audit.AuditFilter
}

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
	Tournament usecase.TournamentView
	Roster     RosterView
	Waves      []WaveView
	Series     []domain.Series
	PauseGraph *PauseGraphView
	NextCursor OperatorCursor
}

type SnapshotPort interface {
	GetOperatorSnapshot(ctx context.Context, query SnapshotQuery) (OperatorSnapshotView, error)
}

func validAuditQuery(query AuditQuery) bool {
	if !validOperator(query.Operator) {
		return false
	}
	_, err := audit.LookupAudit(nil, query.Filter)
	return err == nil
}

func validSnapshotQuery(query SnapshotQuery) bool {
	if !validOperator(query.Operator) || query.TournamentID == uuid.Nil {
		return false
	}
	return query.Cursor == nil || (query.Cursor.ProjectionRevision >= 1 &&
		query.Cursor.AuthorityRevision >= 1 && query.Cursor.AuditSequence >= 0)
}

func validAuditPage(page audit.AuditPage, tournamentID uuid.UUID) bool {
	if page.Events == nil || len(page.Events) > maxAuditEvents {
		return false
	}
	for _, event := range page.Events {
		if event.TournamentID != tournamentID || !json.Valid(event.RedactedPayload) {
			return false
		}
		if _, err := audit.LookupAudit([]audit.AuditEvent{event}, audit.AuditFilter{
			TournamentID: tournamentID, PageSize: 1,
		}); err != nil {
			return false
		}
	}
	if page.NextCursor != nil {
		_, err := audit.LookupAudit(nil, audit.AuditFilter{
			TournamentID: tournamentID, Cursor: page.NextCursor,
		})
		return err == nil
	}
	return true
}

func validOperatorSnapshot(view OperatorSnapshotView, tournamentID uuid.UUID) bool {
	if !validSnapshotHeader(view, tournamentID) || !validSnapshotWaves(view.Waves, tournamentID) ||
		!validSnapshotSeries(view.Series, tournamentID) {
		return false
	}
	return validSnapshotPauseGraph(view.PauseGraph, tournamentID, view.Roster.ID)
}

func validSnapshotHeader(view OperatorSnapshotView, tournamentID uuid.UUID) bool {
	return validTournamentView(view.Tournament, tournamentID) && validRosterView(view.Roster, tournamentID) &&
		view.Tournament.RosterID == view.Roster.ID && view.Waves != nil && view.Series != nil &&
		view.NextCursor.ProjectionRevision >= 1 && view.NextCursor.AuthorityRevision >= 1 &&
		view.NextCursor.AuditSequence >= 0
}

func validSnapshotWaves(waves []WaveView, tournamentID uuid.UUID) bool {
	for _, wave := range waves {
		if !validWaveView(wave, tournamentID, wave.Wave.ID) {
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
		validWaveView(view.Wave, tournamentID, graph.Wave.Wave.ID) &&
		view.Wave.Wave.ID == graph.Wave.Wave.ID
}
