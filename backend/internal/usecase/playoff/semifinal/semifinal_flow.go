package semifinal

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
)

var (
	ErrInvalidSemifinalFlow      = errors.New("invalid semifinal flow")
	ErrSemifinalFlowConflict     = errors.New("semifinal flow conflict")
	ErrSemifinalFlowCommandReuse = errors.New("semifinal flow command was reused")
)

// SemifinalFlowInput is the complete, immutable source set for the two
// semifinal graphs. The caller supplies both graph sources and all persisted
// identities; materialization does not allocate or load anything.
type SemifinalFlowInput struct {
	Authority   SemifinalAdvancementAuthority
	RosterID    uuid.UUID
	Materialize [2]seriesgraph.MaterializeInput
}

// SemifinalFlow is a detached pair of executable graphs. Graph index zero is
// always semifinal position 1 and graph index one is always position 2; the
// matching authority is retained separately so settlement can reuse the
// existing position-aware advancement logic.
type SemifinalFlow struct {
	Authority SemifinalAdvancementAuthority
	RosterID  uuid.UUID
	Graphs    [2]seriesgraph.SeriesGraph
}

// MaterializeSemifinals materializes exactly two independent semifinal BO1
// graphs. Replaying the same command and source returns changed=false. The
// returned graphs and authority are detached from both input and retained
// values.
func MaterializeSemifinals(
	existing *SemifinalFlow,
	input SemifinalFlowInput,
) (SemifinalFlow, bool, error) {
	if err := validateSemifinalFlowAuthority(input.Authority); err != nil {
		return SemifinalFlow{}, false, err
	}
	if input.RosterID == uuid.Nil {
		return SemifinalFlow{}, false, semifinalFlowInvalid("missing roster identity")
	}

	if existing != nil {
		if err := existing.Validate(); err != nil {
			return SemifinalFlow{}, false, fmt.Errorf("%w: retained flow: %w", ErrInvalidSemifinalFlow, err)
		}
		if existing.RosterID != input.RosterID ||
			!semifinalAdvancementAuthorityEqual(existing.Authority, input.Authority) {
			return SemifinalFlow{}, false, semifinalFlowConflict("authority belongs to another semifinal flow")
		}
	}

	var result SemifinalFlow
	result.Authority = cloneSemifinalAdvancementAuthority(input.Authority)
	result.RosterID = input.RosterID
	changed := false
	for index, source := range input.Materialize {
		normalized, err := normalizeSemifinalMaterializeInput(input.Authority.Semifinals[index], input.RosterID, source)
		if err != nil {
			return SemifinalFlow{}, false, err
		}
		var retained *seriesgraph.SeriesGraph
		if existing != nil {
			retained = &existing.Graphs[index]
		}
		graph, graphChanged, err := seriesgraph.Materialize(retained, normalized)
		if err != nil {
			return SemifinalFlow{}, false, semifinalFlowMaterializeError(err)
		}
		result.Graphs[index] = graph
		changed = changed || graphChanged
	}
	if err := result.Validate(); err != nil {
		return SemifinalFlow{}, false, err
	}
	return result, changed, nil
}

// Advance delegates settled-series ordering to SemifinalAdvancement. In
// particular, reversed settlement input still produces final participants in
// semifinal position order.
func (flow SemifinalFlow) Advance(
	current SemifinalAdvancement,
	settled []domain.Series,
) (SemifinalAdvancement, bool, error) {
	if err := flow.Validate(); err != nil {
		return SemifinalAdvancement{}, false, err
	}
	return AdvanceSemifinalEvidence(current, flow.Authority, settled)
}

func normalizeSemifinalMaterializeInput(
	match SemifinalMatch,
	rosterID uuid.UUID,
	input seriesgraph.MaterializeInput,
) (seriesgraph.MaterializeInput, error) {
	if input.Series.ID != match.Series.ID || input.Series.TournamentID != match.Series.TournamentID ||
		input.Series.FirstParticipantID != match.Series.FirstParticipantID ||
		input.Series.SecondParticipantID != match.Series.SecondParticipantID ||
		input.Series.Format != match.Series.Format {
		return seriesgraph.MaterializeInput{}, semifinalFlowInvalid(
			"Series does not match semifinal position %d authority", match.Position,
		)
	}
	if err := validateSemifinalInputSeries(input.Series); err != nil {
		return seriesgraph.MaterializeInput{}, err
	}
	if input.CategoryRevision.RosterID != rosterID {
		return seriesgraph.MaterializeInput{}, semifinalFlowInvalid(
			"category authority for semifinal position %d belongs to another roster", match.Position,
		)
	}
	if input.CategoryRevision.Stage != domain.TournamentStageSemifinal ||
		input.CategoryRevision.Format != domain.SeriesFormatBO1 ||
		input.CategoryRevision.CategoryPool.Format != domain.SeriesFormatBO1 {
		return seriesgraph.MaterializeInput{}, semifinalFlowInvalid(
			"category authority for semifinal position %d is not semifinal BO1", match.Position,
		)
	}

	// SeriesGraph requires the initial series to be planned and empty. The
	// bracket remains locked and is never changed while this detached planned
	// counterpart is prepared.
	planned := cloneSemifinalSeries(match.Series)
	planned.State = domain.SeriesStatePlanned
	planned.Score = domain.SeriesScore{}
	planned.WinnerID = nil
	planned.CurrentScoreRevisionID = nil
	planned.CurrentResultRevisionID = nil
	planned.Slots = nil
	input.Series = planned
	return input, nil
}

func validateSemifinalInputSeries(series domain.Series) error {
	if err := series.Validate(); err != nil {
		return semifinalFlowInvalid("input Series: %v", err)
	}
	if series.State != domain.SeriesStatePlanned && series.State != domain.SeriesStateLocked {
		return semifinalFlowInvalid("input Series must be planned or locked")
	}
	if series.Score != (domain.SeriesScore{}) || series.WinnerID != nil ||
		series.CurrentScoreRevisionID != nil || series.CurrentResultRevisionID != nil ||
		len(series.Slots) != 0 {
		return semifinalFlowInvalid("input Series must be empty")
	}
	return nil
}

func validateSemifinalFlowAuthority(authority SemifinalAdvancementAuthority) error {
	if err := authority.Validate(); err != nil {
		return semifinalFlowInvalid("authority: %v", err)
	}
	if len(authority.Semifinals) != 2 {
		return semifinalFlowInvalid("authority must contain exactly two semifinal positions")
	}
	seriesIDs := make(map[uuid.UUID]struct{}, 2)
	participants := make(map[uuid.UUID]struct{}, 4)
	for index, match := range authority.Semifinals {
		if err := validateSemifinalAuthorityMatch(index, match); err != nil {
			return err
		}
		if err := claimSemifinalAuthorityMatch(seriesIDs, participants, match); err != nil {
			return err
		}
	}
	return nil
}

func validateSemifinalAuthorityMatch(index int, match SemifinalMatch) error {
	if match.Position != index+1 || match.WinnerPath != SemifinalWinnerToFinal ||
		match.LoserPath != SemifinalLoserEliminated || match.Series.Format != domain.SeriesFormatBO1 ||
		match.Series.State != domain.SeriesStateLocked {
		return semifinalFlowInvalid("semifinal position %d must be a locked BO1", index+1)
	}
	if err := match.Series.Validate(); err != nil {
		return semifinalFlowInvalid("semifinal position %d Series: %v", index+1, err)
	}
	if match.Series.Score != (domain.SeriesScore{}) || match.Series.WinnerID != nil ||
		match.Series.CurrentScoreRevisionID != nil || match.Series.CurrentResultRevisionID != nil ||
		len(match.Series.Slots) != 0 {
		return semifinalFlowInvalid("semifinal position %d Series must be locked and empty", index+1)
	}
	return nil
}

func claimSemifinalAuthorityMatch(
	seriesIDs map[uuid.UUID]struct{},
	participants map[uuid.UUID]struct{},
	match SemifinalMatch,
) error {
	if _, exists := seriesIDs[match.Series.ID]; exists {
		return semifinalFlowInvalid("semifinal Series identities must be distinct")
	}
	seriesIDs[match.Series.ID] = struct{}{}
	for _, participantID := range [2]uuid.UUID{match.Series.FirstParticipantID, match.Series.SecondParticipantID} {
		if _, exists := participants[participantID]; exists {
			return semifinalFlowInvalid("semifinal participants must be distinct")
		}
		participants[participantID] = struct{}{}
	}
	return nil
}

func (flow SemifinalFlow) Validate() error {
	if flow.RosterID == uuid.Nil {
		return semifinalFlowInvalid("missing roster identity")
	}
	if err := validateSemifinalFlowAuthority(flow.Authority); err != nil {
		return err
	}
	for index, graph := range flow.Graphs {
		if err := validateSemifinalFlowGraph(index, graph, flow.Authority.Semifinals[index], flow.Authority.TournamentID, flow.RosterID); err != nil {
			return err
		}
	}
	if err := validateSemifinalFlowIdentityRegistry(flow.Graphs); err != nil {
		return err
	}
	return nil
}

func validateSemifinalFlowGraph(
	index int,
	graph seriesgraph.SeriesGraph,
	match SemifinalMatch,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
) error {
	if err := graph.Validate(); err != nil {
		return fmt.Errorf("%w: graph %d: %w", ErrInvalidSemifinalFlow, index+1, err)
	}
	if graph.CommandID == uuid.Nil || graph.Series.ID != match.Series.ID ||
		graph.Series.TournamentID != tournamentID ||
		graph.Series.FirstParticipantID != match.Series.FirstParticipantID ||
		graph.Series.SecondParticipantID != match.Series.SecondParticipantID ||
		graph.Series.Format != domain.SeriesFormatBO1 ||
		graph.CategoryRevision.Stage != domain.TournamentStageSemifinal ||
		graph.CategoryRevision.Format != domain.SeriesFormatBO1 ||
		graph.CategoryRevision.TournamentID != tournamentID ||
		graph.CategoryRevision.SeriesID != match.Series.ID ||
		graph.CategoryRevision.RosterID != rosterID {
		return semifinalFlowInvalid("graph %d is not bound to its semifinal authority", index+1)
	}
	return nil
}

// The registry mirrors seriesgraph's graph-owned identities. Shared external
// authority such as tournament, roster, participants, pools, tasks, players,
// and resources is intentionally excluded.
func validateSemifinalFlowIdentityRegistry(graphs [2]seriesgraph.SeriesGraph) error {
	owners := make(map[uuid.UUID]string)
	claim := func(id uuid.UUID, kind string, graphIndex int) error {
		if id == uuid.Nil {
			return semifinalFlowInvalid("graph %d has an empty %s identity", graphIndex, kind)
		}
		if previous, exists := owners[id]; exists {
			return semifinalFlowInvalid("graph-owned identity %s is shared by %s and graph %d %s", id, previous, graphIndex, kind)
		}
		owners[id] = fmt.Sprintf("graph %d %s", graphIndex, kind)
		return nil
	}
	for index, graph := range graphs {
		graphIndex := index + 1
		claims := []struct {
			id   uuid.UUID
			kind string
		}{
			{graph.CommandID, "command"},
			{graph.CategoryRevision.ID, "category revision"},
			{graph.Series.ID, "Series"},
		}
		for _, aggregate := range graph.Assignments {
			plan := aggregate.Plan
			claims = append(claims,
				struct {
					id   uuid.UUID
					kind string
				}{plan.PlanID, "assignment plan"},
				struct {
					id   uuid.UUID
					kind string
				}{plan.PlanRevisionID, "assignment plan revision"},
				struct {
					id   uuid.UUID
					kind string
				}{plan.BranchID, "assignment branch"},
				struct {
					id   uuid.UUID
					kind string
				}{plan.DecisionEvidence.ID, "decision"},
				struct {
					id   uuid.UUID
					kind string
				}{plan.Scope.SlotID, "slot"},
				struct {
					id   uuid.UUID
					kind string
				}{aggregate.AttemptID, "attempt"},
				struct {
					id   uuid.UUID
					kind string
				}{aggregate.ID, "assignment"},
			)
			for _, edge := range plan.SelectedEdges {
				claims = append(claims,
					struct {
						id   uuid.UUID
						kind string
					}{edge.ID, "assignment edge"},
					struct {
						id   uuid.UUID
						kind string
					}{edge.ReservationID, "task reservation"},
					struct {
						id   uuid.UUID
						kind string
					}{edge.Snapshot.SnapshotID, "snapshot"},
				)
			}
			for _, receipt := range aggregate.DeliveryReceipts {
				claims = append(claims, struct {
					id   uuid.UUID
					kind string
				}{receipt.ID, "delivery receipt"})
			}
		}
		for _, value := range claims {
			if err := claim(value.id, value.kind, graphIndex); err != nil {
				return err
			}
		}
	}
	return nil
}

func semifinalFlowMaterializeError(err error) error {
	switch {
	case errors.Is(err, seriesgraph.ErrSeriesGraphConflict):
		return fmt.Errorf("%w: %w", ErrSemifinalFlowConflict, err)
	case errors.Is(err, seriesgraph.ErrSeriesGraphCommandReuse):
		return fmt.Errorf("%w: %w", ErrSemifinalFlowCommandReuse, err)
	default:
		return fmt.Errorf("%w: %w", ErrInvalidSemifinalFlow, err)
	}
}

func semifinalFlowInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSemifinalFlow, fmt.Sprintf(format, args...))
}

func semifinalFlowConflict(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSemifinalFlowConflict, fmt.Sprintf(format, args...))
}

func cloneSemifinalAdvancementAuthority(
	authority SemifinalAdvancementAuthority,
) SemifinalAdvancementAuthority {
	clone := authority
	clone.Semifinals = CloneSemifinalMatches(authority.Semifinals)
	return clone
}

func semifinalAdvancementAuthorityEqual(
	left, right SemifinalAdvancementAuthority,
) bool {
	return reflect.DeepEqual(
		cloneSemifinalAdvancementAuthority(left),
		cloneSemifinalAdvancementAuthority(right),
	)
}
