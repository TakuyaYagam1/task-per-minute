package terminal

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	semifinalusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff/semifinal"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/publication"
)

// TerminalCoordinator is the production use case boundary joining exact
// settlement evidence to playoff progression. Callers must invoke it inside
// their already-open settlement transaction.
type TerminalCoordinator struct {
	repository TerminalRepository
	publisher  resultprojection.FinalPublicationRepository
	planner    FinalDraftAssignmentPlanner
	rehydrator FinalBindingRehydrator
}

func NewTerminalCoordinator(deps TerminalCoordinatorDependencies) *TerminalCoordinator {
	return &TerminalCoordinator{
		repository: deps.Repository,
		publisher:  deps.Publisher,
		planner:    deps.DraftPlanner,
		rehydrator: deps.Rehydrator,
	}
}

// AdvanceAfterSeriesSettlement consumes a just-settled semifinal or final
// series. Ordinary series are an explicit no-op. A final Game 1 or 2 creates
// exactly one next graph; a terminal final publishes the champion atomically.
func (c *TerminalCoordinator) AdvanceAfterSeriesSettlement(
	ctx context.Context,
	command TerminalSeriesCommand,
) (TerminalReceipt, error) {
	if ctx == nil || !validTerminalSeriesCommand(command) {
		return TerminalReceipt{}, domain.ErrValidation
	}
	if c == nil || c.repository == nil || c.planner == nil {
		return TerminalReceipt{}, domain.ErrInternal
	}

	semifinals, err := c.repository.LoadSemifinalStage(ctx, command)
	if err != nil {
		return TerminalReceipt{}, err
	}
	if semifinals != nil {
		return c.advanceSemifinals(ctx, *semifinals)
	}

	settlement, err := c.repository.LoadFinalSettlement(ctx, command)
	if err != nil {
		return TerminalReceipt{}, err
	}
	if settlement == nil {
		return TerminalReceipt{}, nil
	}
	if c.rehydrator == nil {
		return TerminalReceipt{}, domain.ErrInternal
	}
	bindings, err := c.rehydrator.RehydrateFinalBindings(ctx, *settlement)
	if err != nil {
		return TerminalReceipt{}, err
	}
	rehydrated := *settlement
	rehydrated.Bindings = bindings
	return c.advanceFinalSettlement(ctx, rehydrated)
}

// ActivateFinalAfterDraft reuses the planned Series' exact initial score head
// and creates the first final Game after the draft is authoritatively complete.
// The Wave then follows the ordinary readiness pipeline before Game 1 starts.
//
//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (c *TerminalCoordinator) ActivateFinalAfterDraft(
	ctx context.Context,
	command TerminalDraftCommand,
) (TerminalReceipt, error) {
	if ctx == nil || !validTerminalDraftCommand(command) {
		return TerminalReceipt{}, domain.ErrValidation
	}
	if c == nil || c.repository == nil || c.planner == nil {
		return TerminalReceipt{}, domain.ErrInternal
	}
	authority, err := c.repository.LoadFinalDraft(ctx, command)
	if err != nil {
		return TerminalReceipt{}, err
	}
	if authority == nil {
		return TerminalReceipt{}, nil
	}
	if err := authority.valid(command); err != nil {
		return TerminalReceipt{}, err
	}

	advancement, err := rehydrateSemifinalAdvancement(authority.Bracket, authority.Advancement)
	if err != nil {
		return TerminalReceipt{}, err
	}
	draft, err := authority.Draft.DomainDraft()
	if err != nil || draft.State != domain.DraftStateCompleted {
		return TerminalReceipt{}, terminalConflict("final draft is not completed")
	}
	final, err := NewFinal(FinalCommand{
		SeriesID: authority.IDs.FinalSeriesID, Advancement: advancement, Draft: draft,
		InitialScoreRevisionID: authority.IDs.InitialScoreRevisionID,
		FirstSlotID:            authority.IDs.FirstSlotID, FirstGameID: authority.IDs.FirstGameID,
	})
	if err != nil {
		return TerminalReceipt{}, err
	}
	bindings, assignmentsChanged, err := c.planner.ActivateFinalDraft(ctx, *authority, command)
	if err != nil {
		return TerminalReceipt{}, err
	}
	binding, found := finalBinding(bindings, authority.IDs.FirstGameID)
	if !found {
		return TerminalReceipt{}, terminalConflict("activated final first Game binding is absent")
	}
	wave, err := plannedInitialFinalWave(final, authority.IDs.FirstWaveID, authority.IDs.FirstWaveRevisionID)
	if err != nil {
		return TerminalReceipt{}, err
	}
	changed, err := c.repository.PersistFinalInitial(ctx, FinalInitialPlan{
		StageCommandID: authority.StageCommandID, RosterID: authority.RosterID,
		ExpectedSeriesRevision: authority.ExpectedSeriesRevision, Execution: final.Execution(),
		InitialScoreRevisionID: authority.IDs.InitialScoreRevisionID,
		CurrentWave:            wave, Binding: binding, ActivatedAt: authority.RecordedAt,
	})
	if err != nil {
		return TerminalReceipt{}, err
	}
	return TerminalReceipt{
		FinalSeriesID: authority.IDs.FinalSeriesID,
		Changed:       changed || assignmentsChanged,
	}, nil
}

func (c *TerminalCoordinator) advanceSemifinals(
	ctx context.Context,
	authority SemifinalStageAuthority,
) (TerminalReceipt, error) {
	if err := authority.valid(); err != nil {
		return TerminalReceipt{}, err
	}
	advancement, _, err := semifinalusecase.AdvanceSemifinalEvidence(
		SemifinalAdvancement{}, authority.Bracket, authority.Series,
	)
	if err != nil {
		return TerminalReceipt{}, err
	}
	if !advancement.Complete() {
		return TerminalReceipt{}, nil
	}
	ids, err := FinalStageIdentity(authority.StageCommandID)
	if err != nil {
		return TerminalReceipt{}, err
	}
	category, _, err := draftusecase.DeriveCategoryRevision(nil, draftusecase.CategoryRevisionCommand{
		ID: ids.CategoryRevisionID, SeriesID: ids.FinalSeriesID, RosterID: authority.RosterID,
		SeriesState: domain.SeriesStatePlanned, Stage: domain.TournamentStageFinal,
		Configuration: authority.Configuration, CreatedAt: authority.RecordedAt,
	})
	if err != nil {
		return TerminalReceipt{}, err
	}
	draft, err := draftusecase.StartExecution(draftusecase.ExecutionStartCommand{
		CategoryRevision: category, DraftID: ids.DraftID, InitialRevisionID: ids.DraftInitialRevisionID,
		DecisionEvidenceID: ids.DraftOrderDecisionID,
		ParticipantIDs:     finalParticipantPair(advancement), ServiceEpoch: ids.DraftServiceEpochID,
		CommandID: authority.StageCommandID, StartedAt: authority.RecordedAt,
	})
	if err != nil {
		return TerminalReceipt{}, err
	}
	series, err := plannedFinalSeries(ids.FinalSeriesID, authority.Bracket.TournamentID, advancement)
	if err != nil {
		return TerminalReceipt{}, err
	}
	plan := FinalDraftPlan{
		StageCommandID: authority.StageCommandID, RosterID: authority.RosterID,
		ReserveCount: authority.Configuration.ReserveCount,
		Advancement:  advancement.Results(), Series: series, Category: category, Draft: draft,
		IDs: ids, CreatedAt: authority.RecordedAt,
	}
	if err := plan.valid(); err != nil {
		return TerminalReceipt{}, err
	}
	changed, err := c.repository.PersistFinalDraft(ctx, plan)
	if err != nil {
		return TerminalReceipt{}, err
	}
	assignmentsChanged, err := c.planner.PlanFinalDraft(ctx, plan)
	if err != nil {
		return TerminalReceipt{}, err
	}
	return TerminalReceipt{
		FinalSeriesID: ids.FinalSeriesID,
		Changed:       changed || assignmentsChanged,
	}, nil
}

func (c *TerminalCoordinator) advanceFinalSettlement(
	ctx context.Context,
	authority FinalSettlementAuthority,
) (TerminalReceipt, error) {
	if err := authority.valid(); err != nil {
		return TerminalReceipt{}, err
	}
	current, err := rehydrateFinal(authority)
	if err != nil {
		return TerminalReceipt{}, err
	}
	next, changed, err := ProgressFinal(current, authority.Progression)
	if err != nil {
		return TerminalReceipt{}, err
	}
	if !changed {
		return TerminalReceipt{}, terminalConflict("final settlement is not the current head")
	}
	series := next.Execution().Series
	if series.State.IsTerminal() {
		return c.publishTerminalFinal(ctx, authority, next)
	}
	if authority.Progression.Progression.Next == nil || authority.Publication != nil {
		return TerminalReceipt{}, terminalConflict("continuing final has terminal publication evidence")
	}
	if len(series.Slots) < 2 {
		return TerminalReceipt{}, terminalConflict("continuing final has no next Game")
	}
	nextSlot := series.Slots[len(series.Slots)-1]
	nextWave, err := plannedFinalWave(series, *authority.Progression.Progression.Next)
	if err != nil {
		return TerminalReceipt{}, err
	}
	binding, found := finalBinding(authority.Bindings, authority.Progression.Progression.Next.GameID)
	if !found {
		return TerminalReceipt{}, terminalConflict("next final Game binding is absent")
	}
	changed, err = c.repository.PersistFinalContinuation(ctx, FinalContinuationPlan{
		StageCommandID: authority.StageCommandID, RosterID: authority.RosterID, SeriesID: series.ID,
		SourceScoreRevision:  authority.Progression.Progression.ScoreRevision.ID,
		SourceResultRevision: *authority.Progression.Progression.Game.ResultRevisionID,
		Next:                 *authority.Progression.Progression.Next, Slot: nextSlot, Wave: nextWave,
		Binding: binding, CreatedAt: authority.RecordedAt,
	})
	if err != nil {
		return TerminalReceipt{}, err
	}
	return TerminalReceipt{FinalSeriesID: series.ID, Changed: changed}, nil
}

func (c *TerminalCoordinator) publishTerminalFinal(
	ctx context.Context,
	authority FinalSettlementAuthority,
	final Final,
) (TerminalReceipt, error) {
	if c.publisher == nil || authority.Publication == nil {
		return TerminalReceipt{}, domain.ErrInternal
	}
	publication := authority.Publication.Snapshot()
	if err := publication.Validate(); err != nil || !terminalPublicationMatches(final, authority.Progression, publication) {
		return TerminalReceipt{}, terminalConflict("terminal final publication is stale")
	}
	receipt, err := c.publisher.PublishFinal(ctx, publication)
	if err != nil {
		return TerminalReceipt{}, err
	}
	if receipt.ProjectionRevisionID != publication.IDs.RevisionID || receipt.ProjectionRevision < 1 ||
		receipt.TournamentRevision < publication.Expected.TournamentRevision+1 || receipt.OutboxEventID == uuid.Nil ||
		receipt.OutboxOrdinal < 1 {
		return TerminalReceipt{}, domain.ErrInternal
	}
	return TerminalReceipt{
		FinalSeriesID: final.Execution().Series.ID, ProjectionRevision: receipt.ProjectionRevision,
		Changed: receipt.Changed,
	}, nil
}

func rehydrateFinal(authority FinalSettlementAuthority) (Final, error) {
	advancement, err := rehydrateSemifinalAdvancement(authority.Bracket, authority.Advancement)
	if err != nil {
		return Final{}, err
	}
	draft, err := authority.Draft.DomainDraft()
	if err != nil {
		return Final{}, terminalConflict("final draft cannot be rehydrated")
	}
	current, err := NewFinal(FinalCommand{
		SeriesID: authority.IDs.FinalSeriesID, Advancement: advancement, Draft: draft,
		InitialScoreRevisionID: authority.IDs.InitialScoreRevisionID,
		FirstSlotID:            authority.IDs.FirstSlotID, FirstGameID: authority.IDs.FirstGameID,
	})
	if err != nil {
		return Final{}, err
	}
	for _, progression := range authority.History {
		next, changed, progressErr := ProgressFinal(current, progression)
		if progressErr != nil || !changed || next.Execution().Series.State.IsTerminal() {
			return Final{}, terminalConflict("final progression history is not exact")
		}
		current = next
	}
	return current, nil
}

func rehydrateSemifinalAdvancement(
	authority SemifinalAdvancementAuthority,
	results []SemifinalAdvancementResult,
) (SemifinalAdvancement, error) {
	if err := authority.Validate(); err != nil || len(results) != 2 {
		return SemifinalAdvancement{}, terminalConflict("final semifinal advancement is incomplete")
	}
	seen := make(map[int]struct{}, len(results))
	for _, result := range results {
		if result.Position < 1 || result.Position > 2 {
			return SemifinalAdvancement{}, terminalConflict("final semifinal advancement is ambiguous")
		}
		if _, exists := seen[result.Position]; exists {
			return SemifinalAdvancement{}, terminalConflict("final semifinal advancement is ambiguous")
		}
		seen[result.Position] = struct{}{}
	}
	advancement, err := semifinalusecase.RehydrateSemifinalAdvancement(authority, results)
	if err != nil || !advancement.Complete() {
		return SemifinalAdvancement{}, terminalConflict("final semifinal advancement is invalid")
	}
	return advancement, nil
}

func plannedFinalSeries(
	seriesID, tournamentID uuid.UUID,
	advancement SemifinalAdvancement,
) (domain.Series, error) {
	participants := finalParticipantPair(advancement)
	series := domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: participants[0], SecondParticipantID: participants[1],
		Format: domain.SeriesFormatBO3, State: domain.SeriesStatePlanned,
	}
	if err := series.Validate(); err != nil {
		return domain.Series{}, err
	}
	return series, nil
}

func finalParticipantPair(advancement SemifinalAdvancement) [2]uuid.UUID {
	participants := advancement.FinalParticipants()
	if len(participants) != 2 {
		return [2]uuid.UUID{}
	}
	return [2]uuid.UUID{participants[0], participants[1]}
}

func plannedInitialFinalWave(
	final Final,
	waveID uuid.UUID,
	revisionID domain.WaveRevisionID,
) (domain.Wave, error) {
	series := final.Execution().Series
	wave := domain.Wave{
		ID: waveID, TournamentID: series.TournamentID, RevisionID: revisionID,
		State: domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: series.FirstParticipantID}, {ParticipantID: series.SecondParticipantID},
		},
	}
	if err := wave.Validate(); err != nil {
		return domain.Wave{}, terminalConflict("planned initial final Wave: %v", err)
	}
	return wave, nil
}

func plannedFinalWave(
	series domain.Series,
	next seriesdomain.NextGameWave,
) (domain.Wave, error) {
	wave := domain.Wave{
		ID: next.WaveID, TournamentID: series.TournamentID, RevisionID: next.WaveRevisionID,
		State: domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: series.FirstParticipantID}, {ParticipantID: series.SecondParticipantID},
		},
	}
	if err := wave.Validate(); err != nil {
		return domain.Wave{}, terminalConflict("next final Wave: %v", err)
	}
	return wave, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func terminalPublicationMatches(
	final Final,
	progression FinalProgressionCommand,
	publication resultprojection.FinalPublication,
) bool {
	series := final.Execution().Series
	if series.State != domain.SeriesStateCompleted || series.WinnerID == nil ||
		progression.Progression.Game.ResultRevisionID == nil || progression.Progression.TerminalResultRevisionID == nil ||
		progression.ChampionRevisionID.IsZero() || publication.Scope.SeriesID != series.ID ||
		publication.Scope.TournamentID != series.TournamentID ||
		publication.Scope.GameAttemptID != progression.Progression.Game.ID ||
		publication.Expected.WinnerID != *series.WinnerID ||
		publication.Expected.ScoreRevisionID != progression.Progression.ScoreRevision.ID ||
		publication.Expected.GameResultRevisionID != *progression.Progression.Game.ResultRevisionID ||
		publication.Expected.SeriesResultRevisionID != *progression.Progression.TerminalResultRevisionID {
		return false
	}
	ids, err := FinalPublicationIdentity(*progression.Progression.Game.ResultRevisionID)
	if err != nil || progression.ChampionRevisionID != ids.ChampionRevisionID ||
		publication.IDs.RevisionID != ids.ProjectionRevisionID || publication.IDs.CutoffID != ids.CutoffID {
		return false
	}
	for _, artifact := range publication.Artifacts {
		if artifact.Kind == domain.ArtifactKindChampion {
			return artifact.ID == ids.ChampionArtifactID
		}
	}
	return false
}

func finalBinding(bindings []FinalGameBinding, gameID uuid.UUID) (FinalGameBinding, bool) {
	for _, binding := range bindings {
		if binding.GameID == gameID && binding.valid() {
			return binding, true
		}
	}
	return FinalGameBinding{}, false
}

func validTerminalSeriesCommand(command TerminalSeriesCommand) bool {
	return command.TournamentID != uuid.Nil && command.SeriesID != uuid.Nil
}

func validTerminalDraftCommand(command TerminalDraftCommand) bool {
	return command.TournamentID != uuid.Nil && command.SeriesID != uuid.Nil && command.DraftID != uuid.Nil &&
		command.CommandID != uuid.Nil
}

func terminalConflict(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrConflict, fmt.Sprintf(format, arguments...))
}
