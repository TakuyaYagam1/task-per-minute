package swiss

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesgraph "github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
)

var (
	ErrInvalidSeriesFlow      = errors.New("invalid Swiss Series flow")
	ErrSeriesFlowConflict     = errors.New("swiss series flow conflict")
	ErrSeriesFlowCommandReuse = errors.New("swiss series flow command was reused")
)

// SeriesFlowInput contains the already-authorized evidence for one Swiss
// Series. Category selection and exact assignment evidence are carried by the
// embedded materializer input; this flow only binds them to the RoundLock
// pairing and derives the lock row consumed by the RoundLock use case.
type SeriesFlowInput struct {
	seriesgraph.MaterializeInput

	PairingID           uuid.UUID
	AssignmentRevision  int64
	ReservationRevision int64
}

// SeriesFlow is a detached executable graph and its canonical RoundLock
// membership evidence. The flow deliberately does not build a RoundLockProof;
// callers can include LockedSeries in the existing NewRoundLockProof input.
type SeriesFlow struct {
	LockedSeries LockedSeries
	Graph        seriesgraph.SeriesGraph
}

// Materialize binds one already-authorized Swiss BO1 Series to a materialized
// executable graph. Replaying an identical command returns changed=false and
// a detached graph. A changed source set or command identity is rejected by
// the materializer and retained through the flow's stable error identities.
func MaterializeSeries(existing *SeriesFlow, input SeriesFlowInput) (SeriesFlow, bool, error) {
	if err := validateSeriesFlowInput(input); err != nil {
		return SeriesFlow{}, false, err
	}

	var existingGraph *seriesgraph.SeriesGraph
	if existing != nil {
		if err := existing.Validate(); err != nil {
			return SeriesFlow{}, false, err
		}
		existingGraph = &existing.Graph
	}

	graph, changed, err := seriesgraph.Materialize(existingGraph, input.MaterializeInput)
	if err != nil {
		return SeriesFlow{}, false, seriesFlowMaterializeError(err)
	}
	locked, err := deriveLockedSeries(graph, input)
	if err != nil {
		return SeriesFlow{}, false, err
	}

	result := SeriesFlow{LockedSeries: locked, Graph: graph}
	if existing != nil && existing.LockedSeries != locked {
		return SeriesFlow{}, false, seriesFlowCommandReuse("lock evidence differs from the retained command")
	}
	if err := result.Validate(); err != nil {
		return SeriesFlow{}, false, err
	}
	return result, changed, nil
}

// Validate checks the complete cross-workflow binding needed before the
// returned LockedSeries is included in a RoundLockProof.
func (flow SeriesFlow) Validate() error {
	if err := flow.Graph.Validate(); err != nil {
		return fmt.Errorf("%w: graph: %w", ErrInvalidSeriesFlow, err)
	}
	if err := validateSeriesFlowGraph(flow.Graph); err != nil {
		return err
	}
	if err := validateSeriesFlowLock(flow); err != nil {
		return err
	}
	return validateSeriesFlowAssignment(flow)
}

func validateSeriesFlowGraph(graph seriesgraph.SeriesGraph) error {
	category := graph.CategoryRevision
	if graph.Series.Format != domain.SeriesFormatBO1 || category.Stage != domain.TournamentStageSwiss ||
		category.Format != domain.SeriesFormatBO1 || !validSeriesFlowCategoryMode(category.Mode) {
		return invalidSeriesFlow("Series must be Swiss BO1 with an authorized category mode")
	}
	return nil
}

func validateSeriesFlowLock(flow SeriesFlow) error {
	series := flow.Graph.Series
	locked := flow.LockedSeries
	if locked.PairingID == uuid.Nil || locked.AssignmentRevision < 1 || locked.ReservationRevision < 1 {
		return invalidSeriesFlow("RoundLock pairing and reservation revisions are required")
	}
	if locked.SeriesID != series.ID || locked.FirstParticipantID != series.FirstParticipantID ||
		locked.SecondParticipantID != series.SecondParticipantID {
		return invalidSeriesFlow("RoundLock pairing does not match the Series participants")
	}
	return nil
}

func validateSeriesFlowAssignment(flow SeriesFlow) error {
	if len(flow.Graph.Assignments) != 1 {
		return invalidSeriesFlow("materialized graph must contain one BO1 assignment")
	}
	aggregate := flow.Graph.Assignments[0]
	plan := aggregate.Plan
	category := flow.Graph.CategoryRevision
	locked := flow.LockedSeries
	if locked.CategoryRevisionID != category.ID || locked.CategoryRevision != category.Revision {
		return invalidSeriesFlow("RoundLock category revision does not match the graph")
	}
	if locked.AssignmentID != aggregate.ID || locked.AssignmentPlanID != plan.PlanID ||
		locked.AssignmentPlanRevisionID != plan.PlanRevisionID {
		return invalidSeriesFlow("RoundLock assignment plan does not match the graph")
	}
	if len(plan.SelectedEdges) != domain.AssignmentReserveCount+1 ||
		locked.ReservationID != plan.SelectedEdges[0].ReservationID {
		return invalidSeriesFlow("RoundLock primary reservation does not match the graph")
	}
	return nil
}

func validateSeriesFlowInput(input SeriesFlowInput) error {
	if input.PairingID == uuid.Nil || input.AssignmentRevision < 1 || input.ReservationRevision < 1 {
		return invalidSeriesFlow("RoundLock pairing and reservation revisions are required")
	}
	materialize := input.MaterializeInput
	if materialize.Series.Format != domain.SeriesFormatBO1 ||
		materialize.CategoryRevision.Stage != domain.TournamentStageSwiss ||
		materialize.CategoryRevision.Format != domain.SeriesFormatBO1 ||
		!validSeriesFlowCategoryMode(materialize.CategoryRevision.Mode) {
		return invalidSeriesFlow("Series must be Swiss BO1 with an authorized category mode")
	}
	if materialize.Series.FirstParticipantID == uuid.Nil || materialize.Series.SecondParticipantID == uuid.Nil ||
		materialize.Series.FirstParticipantID == materialize.Series.SecondParticipantID {
		return invalidSeriesFlow("Series participants are invalid")
	}
	return nil
}

func validSeriesFlowCategoryMode(mode domain.CategoryMode) bool {
	switch mode {
	case domain.CategoryModeRandom, domain.CategoryModeAdmin, domain.CategoryModeDraft:
		return true
	default:
		return false
	}
}

func deriveLockedSeries(graph seriesgraph.SeriesGraph, input SeriesFlowInput) (LockedSeries, error) {
	if err := graph.Validate(); err != nil {
		return LockedSeries{}, fmt.Errorf("%w: graph: %w", ErrInvalidSeriesFlow, err)
	}
	if len(graph.Assignments) != 1 || len(graph.Assignments[0].Plan.SelectedEdges) != domain.AssignmentReserveCount+1 {
		return LockedSeries{}, invalidSeriesFlow("materialized graph does not contain one complete BO1 assignment")
	}
	aggregate := graph.Assignments[0]
	primary := aggregate.Plan.SelectedEdges[0]
	return LockedSeries{
		SeriesID:                 graph.Series.ID,
		PairingID:                input.PairingID,
		FirstParticipantID:       graph.Series.FirstParticipantID,
		SecondParticipantID:      graph.Series.SecondParticipantID,
		CategoryRevisionID:       graph.CategoryRevision.ID,
		CategoryRevision:         graph.CategoryRevision.Revision,
		AssignmentID:             aggregate.ID,
		AssignmentRevision:       input.AssignmentRevision,
		AssignmentPlanID:         aggregate.Plan.PlanID,
		AssignmentPlanRevisionID: aggregate.Plan.PlanRevisionID,
		ReservationID:            primary.ReservationID,
		ReservationRevision:      input.ReservationRevision,
	}, nil
}

func seriesFlowMaterializeError(err error) error {
	switch {
	case errors.Is(err, seriesgraph.ErrSeriesGraphConflict):
		return fmt.Errorf("%w: %w", ErrSeriesFlowConflict, err)
	case errors.Is(err, seriesgraph.ErrSeriesGraphCommandReuse):
		return fmt.Errorf("%w: %w", ErrSeriesFlowCommandReuse, err)
	default:
		return fmt.Errorf("%w: %w", ErrInvalidSeriesFlow, err)
	}
}

func invalidSeriesFlow(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSeriesFlow, message)
}

func seriesFlowCommandReuse(message string) error {
	return fmt.Errorf("%w: %s", ErrSeriesFlowCommandReuse, message)
}
