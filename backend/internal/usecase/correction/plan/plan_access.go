package plan

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func (p Plan) GameResultRevision() resultusecase.OfficialResultRevisionPlan {
	return p.gameResult
}

func (p Plan) Audit() AuditRecord {
	return cloneCorrectionAuditRecord(p.audit)
}

func (p Plan) CutoffCondition() CutoffCondition {
	return cloneCorrectionCutoffCondition(p.cutoff)
}

func (p Plan) ScoreRevision() resultusecase.SeriesScoreRevisionPlan {
	return p.score
}

func (p Plan) SeriesResultRevision() resultusecase.OfficialResultRevisionPlan {
	return p.seriesResult
}

func (p Plan) Series() domain.Series {
	return cloneCorrectionSeries(p.series)
}

func (p Plan) SolveMetadata() SolveMetadata {
	return p.solve.Next.Clone()
}

func (p Plan) SolveTransition() SolveTransition {
	return cloneCorrectionSolveTransition(p.solve)
}

func (p Plan) ProjectionRevisions() []domain.ProjectionRevision {
	return append([]domain.ProjectionRevision(nil), p.projections...)
}

func (p Plan) Supersessions() []ProjectionSupersession {
	return cloneCorrectionSupersessions(p.superseded)
}

func (p Plan) Readiness() ReadinessTransition {
	return cloneCorrectionReadinessTransition(p.readiness)
}

func (p Plan) Releases() []ReservationRelease {
	return append([]ReservationRelease(nil), p.releases...)
}

func (p Plan) Decisions() []resultprojection.RecordedProjectionDecision {
	return cloneCorrectionRecordedProjectionDecisions(p.decisions)
}

func (p Plan) DAGSnapshot() resultprojection.RevisionDAGSnapshot {
	return p.dag.Snapshot()
}

func (p Plan) RebuildBytes() []byte {
	return p.rebuild.Bytes()
}

func (p Plan) Bytes() []byte {
	return append([]byte(nil), p.payload...)
}
