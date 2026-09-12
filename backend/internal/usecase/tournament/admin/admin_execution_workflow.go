package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

type ExecutionWorkflow struct {
	transactions ExecutionTransactionManager
	repository   ExecutionWorkflowRepository
	waveStart    *gameusecase.StartUseCase
	authority    ExecutionAuthorityProvider
}

func NewExecutionWorkflow(deps ExecutionWorkflowDependencies) *ExecutionWorkflow {
	return &ExecutionWorkflow{
		transactions: deps.Transactions,
		repository:   deps.Repository,
		waveStart:    deps.WaveStart,
		authority:    deps.Authority,
	}
}

func (w *ExecutionWorkflow) ConfigurePairings(
	ctx context.Context,
	command PairingCommand,
) (SwissRoundView, error) {
	if ctx == nil || !validPairingCommand(command) {
		return SwissRoundView{}, domain.ErrValidation
	}
	if command.PairingMode == PairingModeAutomatic && command.RepeatOverride != nil {
		return SwissRoundView{}, domain.ErrValidation
	}
	if !w.available() {
		return SwissRoundView{}, domain.ErrInternal
	}
	digest, err := executionRequestDigest(command)
	if err != nil {
		return SwissRoundView{}, err
	}

	var result SwissRoundView
	err = w.transactions.Do(ctx, func(txCtx context.Context) error {
		view, configureErr := w.configurePairingsLocked(txCtx, command, digest)
		if configureErr != nil {
			return configureErr
		}
		result = view
		return nil
	})
	if err != nil {
		return SwissRoundView{}, err
	}
	return cloneExecutionSwissRoundView(result), nil
}

func (w *ExecutionWorkflow) configurePairingsLocked(
	ctx context.Context,
	command PairingCommand,
	digest [sha256.Size]byte,
) (SwissRoundView, error) {
	authority, err := w.repository.LockPairingAuthority(ctx, command.TournamentID)
	if err != nil {
		return SwissRoundView{}, err
	}
	if !validPairingAuthority(authority, command.TournamentID) {
		return SwissRoundView{}, fmt.Errorf("validate pairing authority: %w", domain.ErrInternal)
	}
	recorded, err := w.repository.FindPairingCommand(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		return SwissRoundView{}, err
	}
	if recorded != nil {
		return replayPairingAgainstAuthority(*recorded, command, digest, authority)
	}
	return w.createPairingsLocked(ctx, command, digest, authority)
}

func replayPairingAgainstAuthority(
	record PairingCommandRecord,
	command PairingCommand,
	digest [sha256.Size]byte,
	authority PairingAuthority,
) (SwissRoundView, error) {
	view, err := replayPairingCommand(record, command, digest)
	if errors.Is(err, domain.ErrConflict) {
		return SwissRoundView{}, executionConflict(command.ExpectedProjectionRevision, authority)
	}
	return view, err
}

func (w *ExecutionWorkflow) createPairingsLocked(
	ctx context.Context,
	command PairingCommand,
	digest [sha256.Size]byte,
	authority PairingAuthority,
) (SwissRoundView, error) {
	roundCount, roundErr := domain.TournamentPresetV1.SwissRounds(len(authority.Participants))
	if roundErr != nil || command.RoundNumber > roundCount {
		return SwissRoundView{}, domain.ErrValidation
	}
	if authority.ProjectionRevision != command.ExpectedProjectionRevision ||
		authority.TournamentState != domain.TournamentStateSwiss ||
		authority.RoundCount != command.RoundNumber-1 ||
		authority.CompletedRoundCount != command.RoundNumber-1 {
		return SwissRoundView{}, executionConflict(command.ExpectedProjectionRevision, authority)
	}
	decidedAt, err := w.repository.ReadExecutionTime(ctx)
	if err != nil {
		return SwissRoundView{}, err
	}
	plan, err := buildPairingPlan(command, authority, decidedAt)
	if err != nil {
		return SwissRoundView{}, err
	}
	view, err := w.repository.CommitPairing(ctx, plan)
	if err != nil {
		return SwissRoundView{}, fmt.Errorf("commit pairing graph: %w", err)
	}
	if !validSwissRoundView(view, command.TournamentID, command.RoundNumber) {
		return SwissRoundView{}, fmt.Errorf("validate committed pairing: %w", domain.ErrInternal)
	}
	record, err := newPairingCommandRecord(command, authority, digest, view, decidedAt)
	if err != nil {
		return SwissRoundView{}, err
	}
	if err := w.repository.SavePairingCommand(ctx, record); err != nil {
		return SwissRoundView{}, err
	}
	return view, nil
}

func (w *ExecutionWorkflow) ControlWave(ctx context.Context, command WaveCommand) (WaveView, error) {
	if ctx == nil || !validWaveCommand(command) {
		return WaveView{}, domain.ErrValidation
	}
	if !w.available() {
		return WaveView{}, domain.ErrInternal
	}
	digest, err := executionRequestDigest(command)
	if err != nil {
		return WaveView{}, err
	}
	// A previously committed command is an immutable client result. It must be
	// replayable by any HTTP replica even after the execution lease changes.
	// The lease is commit evidence, never part of request identity.
	recorded, err := w.repository.FindWaveCommand(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		return WaveView{}, fmt.Errorf("load Wave command: %w", err)
	}
	if recorded != nil {
		return replayWaveCommand(*recorded, command, digest)
	}
	var executionAuthority authoritydomain.Identity
	if command.Action == WaveActionStart || command.Action == WaveActionResume {
		if w.authority == nil {
			return WaveView{}, domain.ErrInternal
		}
		executionAuthority, err = w.authority.AuthorityFor(ctx, command.TournamentID)
		if err != nil {
			return WaveView{}, fmt.Errorf("execution authority: %w", err)
		}
		if executionAuthority.Validate() != nil || executionAuthority.TournamentID != command.TournamentID {
			return WaveView{}, domain.ErrInternal
		}
	}

	var result WaveView
	err = w.transactions.Do(ctx, func(txCtx context.Context) error {
		view, controlErr := w.controlWaveLocked(txCtx, command, digest, executionAuthority)
		if controlErr != nil {
			return controlErr
		}
		result = view
		return nil
	})
	if err != nil {
		return WaveView{}, err
	}
	return cloneExecutionWaveView(result), nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (w *ExecutionWorkflow) controlWaveLocked(
	ctx context.Context,
	command WaveCommand,
	digest [sha256.Size]byte,
	executionAuthority authoritydomain.Identity,
) (WaveView, error) {
	// Recheck inside the transaction to cover a command that committed between
	// the outside fast path and authority acquisition.
	recorded, err := w.repository.FindWaveCommand(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		return WaveView{}, err
	}
	if recorded != nil {
		return replayWaveCommand(*recorded, command, digest)
	}
	authority, err := w.repository.LockWaveAuthority(ctx, command.TournamentID, command.WaveID)
	if err != nil {
		return WaveView{}, fmt.Errorf("lock Wave authority: %w", err)
	}
	if !validWaveAuthority(authority, command.TournamentID, command.WaveID) {
		return WaveView{}, fmt.Errorf("validate Wave authority: %w", domain.ErrInternal)
	}
	if command.Action == WaveActionStart {
		return w.startWaveLocked(ctx, command, digest, authority, executionAuthority)
	}
	recorded, err = w.repository.FindWaveCommand(ctx, command.TournamentID, command.CommandID)
	if err != nil {
		return WaveView{}, fmt.Errorf("reload Wave command: %w", err)
	}
	if recorded != nil {
		view, replayErr := replayWaveCommand(*recorded, command, digest)
		if replayErr != nil {
			if errors.Is(replayErr, domain.ErrConflict) {
				return WaveView{}, waveExecutionConflict(command.ExpectedProjectionRevision, authority)
			}
			return WaveView{}, replayErr
		}
		return view, nil
	}
	if authority.ProjectionRevision != command.ExpectedProjectionRevision ||
		!waveExecutionStateAllowed(authority.TournamentState) {
		return WaveView{}, waveExecutionConflict(command.ExpectedProjectionRevision, authority)
	}
	mutatedAt, err := w.repository.ReadExecutionTime(ctx)
	if err != nil {
		return WaveView{}, fmt.Errorf("read Wave mutation time: %w", err)
	}
	next, err := planWaveMutation(command, authority, mutatedAt)
	if err != nil {
		return WaveView{}, fmt.Errorf("plan Wave mutation: %w", err)
	}
	view, err := w.repository.CommitWave(ctx, WaveMutation{
		Command: command, Authority: authority, ExecutionAuthority: executionAuthority,
		Next: next, MutatedAt: mutatedAt,
	})
	if err != nil {
		return WaveView{}, fmt.Errorf("commit Wave mutation: %w", err)
	}
	if !validWaveView(view, command.TournamentID, command.WaveID) {
		return WaveView{}, fmt.Errorf("validate committed Wave: %w", domain.ErrInternal)
	}
	record, err := newWaveCommandRecord(command, authority, digest, view, mutatedAt)
	if err != nil {
		return WaveView{}, fmt.Errorf("build Wave command record: %w", err)
	}
	if err := w.repository.SaveWaveCommand(ctx, record); err != nil {
		return WaveView{}, fmt.Errorf("save Wave command record: %w", err)
	}
	return view, nil
}

func (w *ExecutionWorkflow) startWaveLocked(
	ctx context.Context,
	command WaveCommand,
	digest [sha256.Size]byte,
	authority WaveAuthority,
	executionAuthority authoritydomain.Identity,
) (WaveView, error) {
	if !waveExecutionStateAllowed(authority.TournamentState) ||
		w.waveStart == nil || authority.View.Wave.ReadyWindow == nil ||
		executionAuthority.Validate() != nil || executionAuthority.TournamentID != command.TournamentID {
		return WaveView{}, domain.ErrInternal
	}
	record, changed, err := w.waveStart.Start(ctx, gameusecase.StartCommand{
		Scope: gameusecase.StartScope{
			TournamentID: command.TournamentID,
			WaveID:       command.WaveID,
			WindowID:     authority.View.Wave.ReadyWindow.ID,
		},
		CommandID:                  command.CommandID,
		ActorID:                    command.Operator.ActorID,
		ExecutionAuthority:         executionAuthority,
		ExpectedProjectionRevision: command.ExpectedProjectionRevision,
		ExpectedRevisions:          authority.SourceRevisions,
		RequestDigest:              digest,
	})
	if err != nil {
		return WaveView{}, normalizeWaveStartError(command.ExpectedProjectionRevision, authority, err)
	}
	if record == nil {
		return WaveView{}, domain.ErrInternal
	}
	if !changed {
		return recordedWaveStartView(*record)
	}
	current, err := w.repository.LockWaveAuthority(ctx, command.TournamentID, command.WaveID)
	if err != nil {
		return WaveView{}, err
	}
	if !validWaveAuthority(current, command.TournamentID, command.WaveID) ||
		current.View.Revision != record.ExpectedWaveRevision+1 ||
		current.View.Wave.State != domain.WaveStateActive ||
		current.View.Wave.StartedAt == nil ||
		!current.View.Wave.StartedAt.Equal(record.StartedAt) {
		return WaveView{}, domain.ErrInternal
	}
	return current.View, nil
}

func recordedWaveStartView(record gameusecase.StartRecord) (WaveView, error) {
	if record.Validate() != nil {
		return WaveView{}, domain.ErrInternal
	}
	seriesIDs := make(map[uuid.UUID]uuid.UUID, len(record.Games)*2)
	for _, game := range record.Games {
		for _, participantID := range game.ParticipantIDs {
			if _, duplicate := seriesIDs[participantID]; duplicate {
				return WaveView{}, domain.ErrInternal
			}
			seriesIDs[participantID] = game.Scope.SeriesID
		}
	}
	view := WaveView{
		Wave:               record.Wave,
		Revision:           record.ExpectedWaveRevision + 1,
		ReadinessRevisions: cloneWaveStartReadiness(record.ReadinessRevisions),
		SeriesIDs:          seriesIDs,
	}
	if !validWaveView(view, record.Scope.TournamentID, record.Scope.WaveID) {
		return WaveView{}, domain.ErrInternal
	}
	return cloneExecutionWaveView(view), nil
}

func cloneWaveStartReadiness(source map[uuid.UUID]int64) map[uuid.UUID]int64 {
	if source == nil {
		return nil
	}
	clone := make(map[uuid.UUID]int64, len(source))
	for participantID, revision := range source {
		clone[participantID] = revision
	}
	return clone
}

func normalizeWaveStartError(expectedProjectionRevision int64, authority WaveAuthority, err error) error {
	if errors.Is(err, gameusecase.ErrWaveStartAuthorityConflict) ||
		errors.Is(err, gameusecase.ErrWaveStartConflict) || errors.Is(err, domain.ErrConflict) {
		return waveExecutionConflict(expectedProjectionRevision, authority)
	}
	if errors.Is(err, gameusecase.ErrInvalidWaveStart) {
		return domain.ErrInternal
	}
	return err
}

func (w *ExecutionWorkflow) available() bool {
	return w != nil && w.transactions != nil && w.repository != nil
}

func buildPairingPlan(
	command PairingCommand,
	authority PairingAuthority,
	decidedAt time.Time,
) (PairingPlan, error) {
	if !domain.IsValidServerTime(decidedAt) {
		return PairingPlan{}, domain.ErrInternal
	}
	roundID := executionID(command.CommandID, "swiss-round")
	pairingEvidenceID := executionID(command.CommandID, "pairing-evidence")
	bye, err := selectPairingBye(command, authority, roundID, decidedAt)
	if err != nil {
		return PairingPlan{}, err
	}
	rosterIDs := pairingParticipantIDs(authority.Participants)
	eligible := withoutBye(rosterIDs, bye)
	previousEligible := eligiblePreviousMeetings(authority.PreviousMeetings, eligible)

	plan := PairingPlan{
		Command: command, Authority: clonePairingAuthority(authority), RoundID: roundID,
		PairingEvidenceID: pairingEvidenceID, WaveID: executionID(command.CommandID, "swiss-wave"),
		WaveRevisionID: domain.WaveRevisionID(executionID(command.CommandID, "swiss-wave-revision")),
		Bye:            bye, DecidedAt: decidedAt,
	}
	switch command.PairingMode {
	case PairingModeAutomatic:
		automatic, pairingErr := swissusecase.GenerateAutomaticPairing(
			pairingEvidenceID, roundID, eligible, previousEligible, decidedAt,
		)
		if pairingErr != nil {
			return PairingPlan{}, pairingErr
		}
		plan.Automatic = &automatic
		plan.Pairs = append([]swissusecase.Pair(nil), automatic.Pairings...)
	case PairingModeManual:
		manual := swissusecase.ManualRound{
			ID: roundID, Pairings: manualPairs(command.ManualPairings),
			ByeParticipantID: byeParticipantID(bye),
		}
		override, overrideErr := buildRepeatOverride(command, authority, manual, decidedAt)
		if overrideErr != nil {
			return PairingPlan{}, overrideErr
		}
		if err := swissusecase.ValidateManualPairing(rosterIDs, manual, authority.PreviousMeetings, override); err != nil {
			return PairingPlan{}, err
		}
		plan.Pairs = append([]swissusecase.Pair(nil), manual.Pairings...)
		plan.Override = override
	default:
		return PairingPlan{}, domain.ErrValidation
	}
	plan.PairingIDs = make([]uuid.UUID, len(plan.Pairs))
	plan.SeriesIDs = make([]uuid.UUID, len(plan.Pairs))
	plan.InitialScoreRevisionIDs = make([]domain.SeriesScoreRevisionID, len(plan.Pairs))
	for index := range plan.Pairs {
		plan.PairingIDs[index] = executionID(command.CommandID, fmt.Sprintf("pairing-%d", index+1))
		plan.SeriesIDs[index] = executionID(command.CommandID, fmt.Sprintf("series-%d", index+1))
		plan.InitialScoreRevisionIDs[index] = domain.SeriesScoreRevisionID(
			executionID(command.CommandID, fmt.Sprintf("series-score-initial-%d", index+1)),
		)
	}
	return plan, nil
}

func selectPairingBye(
	command PairingCommand,
	authority PairingAuthority,
	roundID uuid.UUID,
	decidedAt time.Time,
) (*swissusecase.ByeSelection, error) {
	if len(authority.Participants)%2 == 0 {
		if command.ManualByeParticipantID != nil {
			return nil, domain.ErrValidation
		}
		return nil, nil
	}
	candidates := make([]swissusecase.ByeCandidate, len(authority.Standings))
	for index, standing := range authority.Standings {
		candidates[index] = swissusecase.ByeCandidate{
			ParticipantID: standing.ParticipantID, Points: standing.Points,
			ProvisionalBuchholz: standing.Buchholz, HeadToHeadPoints: standing.HeadToHeadPoints,
			HeadToHeadApplicable: standing.HeadToHeadApplied,
			EffectiveTime:        time.Duration(standing.EffectiveTimeMS) * time.Millisecond,
			ReceivedBye:          authority.ReceivedBye[standing.ParticipantID],
		}
	}
	selection, err := swissusecase.SelectBye(
		executionID(command.CommandID, "bye-evidence"), roundID, candidates, decidedAt,
	)
	if err != nil {
		return nil, err
	}
	if command.PairingMode == PairingModeManual &&
		(command.ManualByeParticipantID == nil || *command.ManualByeParticipantID != selection.ParticipantID) {
		requested := uuid.Nil
		if command.ManualByeParticipantID != nil {
			requested = *command.ManualByeParticipantID
		}
		return nil, &ManualByeMismatchError{
			RequestedParticipantID: requested, SelectedParticipantID: selection.ParticipantID,
			ExpectedRevision: command.ExpectedProjectionRevision,
			CurrentRevision:  authority.ProjectionRevision, CurrentState: authority.TournamentState,
		}
	}
	return &selection, nil
}

func buildRepeatOverride(
	command PairingCommand,
	authority PairingAuthority,
	round swissusecase.ManualRound,
	decidedAt time.Time,
) (*swissusecase.RepeatOverride, error) {
	if command.RepeatOverride == nil {
		return nil, nil
	}
	override, _, err := swissusecase.ConfirmRepeatOverride(nil, swissusecase.RepeatOverrideCommand{
		ID: command.CommandID, ActorID: command.Operator.ActorID,
		Confirmed: command.RepeatOverride.Confirmed, Reason: command.RepeatOverride.Reason,
		ConfirmedAt: decidedAt, RosterParticipantIDs: pairingParticipantIDs(authority.Participants),
		Round: round, PreviousMeetings: authority.PreviousMeetings,
	})
	if err != nil {
		return nil, err
	}
	return &override, nil
}

func planWaveMutation(command WaveCommand, authority WaveAuthority, mutatedAt time.Time) (domain.Wave, error) {
	if !domain.IsValidServerTime(mutatedAt) {
		return domain.Wave{}, domain.ErrInternal
	}
	wave := cloneDomainWave(authority.View.Wave)
	var err error
	switch command.Action {
	case WaveActionOpenReadyWindow:
		err = planOpenReadyWindow(command, authority, &wave, mutatedAt)
	case WaveActionStart:
		err = planStartWave(command, authority, &wave, mutatedAt)
	case WaveActionPause:
		err = planPauseWave(command, authority, &wave, mutatedAt)
	case WaveActionResume:
		err = planResumeWave(command, authority, &wave)
	case WaveActionComplete:
		err = planCompleteWave(command, authority, &wave)
	case WaveActionCancel:
		err = planCancelWave(command, authority, &wave)
	default:
		return domain.Wave{}, domain.ErrValidation
	}
	if err != nil {
		return domain.Wave{}, err
	}
	if err := wave.Validate(); err != nil {
		return domain.Wave{}, domain.WrapError(err, domain.ErrConflict)
	}
	return wave, nil
}

func planOpenReadyWindow(
	command WaveCommand,
	authority WaveAuthority,
	wave *domain.Wave,
	mutatedAt time.Time,
) error {
	if !authority.SourceRevisions.IsValid() || !waveGraphPlanned(authority) {
		return waveExecutionConflict(command.ExpectedProjectionRevision, authority)
	}
	if err := wave.OpenReadyWindow(
		executionID(command.CommandID, "ready-window"),
		domain.ReadyWindowRevisionID(executionID(command.CommandID, "ready-window-revision")),
		mutatedAt, mutatedAt.Add(domain.ReadyWindowDuration),
	); err != nil {
		return domain.WrapError(err, domain.ErrConflict)
	}
	return nil
}

func planStartWave(command WaveCommand, authority WaveAuthority, wave *domain.Wave, mutatedAt time.Time) error {
	if !authority.SourceRevisions.IsValid() || !waveGraphStartable(authority) || wave.ReadyWindow == nil {
		return waveExecutionConflict(command.ExpectedProjectionRevision, authority)
	}
	if _, err := wave.Start(wave.ReadyWindow.ID, mutatedAt); err != nil {
		return domain.WrapError(err, domain.ErrConflict)
	}
	return nil
}

func planPauseWave(command WaveCommand, authority WaveAuthority, wave *domain.Wave, mutatedAt time.Time) error {
	if !waveGraphActive(authority) {
		return waveExecutionConflict(command.ExpectedProjectionRevision, authority)
	}
	if err := wave.Pause(mutatedAt); err != nil {
		return domain.WrapError(err, domain.ErrConflict)
	}
	return nil
}

func planResumeWave(command WaveCommand, authority WaveAuthority, wave *domain.Wave) error {
	if !waveGraphPaused(authority) {
		return waveExecutionConflict(command.ExpectedProjectionRevision, authority)
	}
	if err := wave.Resume(); err != nil {
		return domain.WrapError(err, domain.ErrConflict)
	}
	return nil
}

func planCompleteWave(command WaveCommand, authority WaveAuthority, wave *domain.Wave) error {
	if wave.State != domain.WaveStateActive || !waveGraphTerminal(authority) {
		return waveExecutionConflict(command.ExpectedProjectionRevision, authority)
	}
	closureRevisionID := domain.WaveRevisionID(executionID(command.CommandID, "wave-closure-revision"))
	if closureRevisionID == wave.RevisionID {
		return domain.ErrConflict
	}
	wave.RevisionID = closureRevisionID
	wave.State = domain.WaveStateCompleted
	return nil
}

func planCancelWave(command WaveCommand, authority WaveAuthority, wave *domain.Wave) error {
	if wave.State == domain.WaveStateCompleted || wave.State == domain.WaveStateSuperseded ||
		wave.ReadyWindow == nil || wave.ReadyWindow.State != domain.ReadyWindowStateOpen ||
		!waveGraphTerminal(authority) {
		return waveExecutionConflict(command.ExpectedProjectionRevision, authority)
	}
	for index := range wave.Members {
		wave.Members[index].Ready = false
	}
	wave.ReadyWindow.State = domain.ReadyWindowStateSuperseded
	wave.ReadyWindow.ConsumedAt = nil
	wave.PausedAt = nil
	wave.State = domain.WaveStateSuperseded
	return nil
}

func waveGraphPlanned(authority WaveAuthority) bool {
	return waveExecutionStateAllowed(authority.TournamentState) && authority.Graph.SeriesCount >= 1 &&
		authority.Graph.PlayableMemberCount == authority.Graph.SeriesCount*2
}

func waveGraphStartable(authority WaveAuthority) bool {
	graph := authority.Graph
	return waveExecutionStateAllowed(authority.TournamentState) && graph.SeriesCount >= 1 &&
		graph.PlayableMemberCount == graph.SeriesCount*2 &&
		graph.ReadySeriesCount+graph.ContinuingSeriesCount == graph.SeriesCount &&
		graph.CurrentGameCount == graph.SeriesCount &&
		graph.ReadyGameCount == graph.CurrentGameCount && graph.AssignmentCount == graph.CurrentGameCount &&
		graph.DeliveryMemberCount == graph.PlayableMemberCount
}

func waveGraphActive(authority WaveAuthority) bool {
	graph := authority.Graph
	return waveExecutionStateAllowed(authority.TournamentState) && graph.SeriesCount >= 1 &&
		graph.ActiveSeriesCount == graph.SeriesCount &&
		graph.CurrentGameCount == graph.SeriesCount && graph.ActiveGameCount == graph.CurrentGameCount
}

func waveGraphPaused(authority WaveAuthority) bool {
	graph := authority.Graph
	return waveExecutionStateAllowed(authority.TournamentState) && graph.SeriesCount >= 1 &&
		graph.PausedSeriesCount == graph.SeriesCount &&
		graph.CurrentGameCount == graph.SeriesCount && graph.PausedGameCount == graph.CurrentGameCount
}

func waveGraphTerminal(authority WaveAuthority) bool {
	graph := authority.Graph
	return waveExecutionStateAllowed(authority.TournamentState) && graph.SeriesCount >= 1 &&
		graph.TerminalSeriesCount+graph.ContinuingSeriesCount == graph.SeriesCount &&
		graph.CurrentGameCount >= graph.SeriesCount && graph.TerminalGameCount == graph.CurrentGameCount
}

func waveExecutionStateAllowed(state domain.TournamentState) bool {
	return state == domain.TournamentStateSwiss || state == domain.TournamentStatePlayoffs
}

func validPairingAuthority(authority PairingAuthority, tournamentID uuid.UUID) bool {
	if !validPairingAuthorityHeader(authority, tournamentID) {
		return false
	}
	return validPairingAuthorityParticipants(authority)
}

func validPairingAuthorityHeader(authority PairingAuthority, tournamentID uuid.UUID) bool {
	return validPairingTournamentHead(authority, tournamentID) && validPairingRosterHead(authority) &&
		validPairingProjectionHead(authority) && validPairingGraphShape(authority)
}

func validPairingTournamentHead(authority PairingAuthority, tournamentID uuid.UUID) bool {
	return authority.TournamentID == tournamentID && authority.TournamentState.IsValid() &&
		authority.TournamentRevision >= 1
}

func validPairingRosterHead(authority PairingAuthority) bool {
	return authority.RosterID != uuid.Nil && authority.RosterRevision >= 1 &&
		domain.IsValidServerTime(authority.RosterLockedAt)
}

func validPairingProjectionHead(authority PairingAuthority) bool {
	return authority.ProjectionRevisionID != uuid.Nil && authority.ProjectionRevision >= 1 &&
		authority.HistoryRevision >= 0
}

func validPairingGraphShape(authority PairingAuthority) bool {
	if authority.RoundCount < 0 || authority.CompletedRoundCount < 0 ||
		authority.CompletedRoundCount > authority.RoundCount ||
		len(authority.Participants) < domain.TournamentMinParticipants ||
		len(authority.Participants) > domain.TournamentMaxParticipants ||
		len(authority.Standings) != len(authority.Participants) || authority.PriorMeetingCounts == nil ||
		authority.ReceivedBye == nil {
		return false
	}
	return true
}

func validPairingAuthorityParticipants(authority PairingAuthority) bool {
	ids := make([]uuid.UUID, len(authority.Participants))
	seeds := make(map[int]struct{}, len(authority.Participants))
	for index, participant := range authority.Participants {
		if participant.ID == uuid.Nil || participant.StableSeed < 1 || !addUniqueInt(seeds, participant.StableSeed) {
			return false
		}
		ids[index] = participant.ID
	}
	if !validUniqueIDs(ids, domain.TournamentMinParticipants, domain.TournamentMaxParticipants) {
		return false
	}
	return validSwissStandings(SwissRoundView{
		RosterParticipantIDs: ids, Standings: authority.Standings,
	}, idSet(ids))
}

func validWaveAuthority(authority WaveAuthority, tournamentID, waveID uuid.UUID) bool {
	return authority.TournamentState.IsValid() && authority.TournamentRevision >= 1 &&
		authority.RosterID != uuid.Nil && authority.RosterRevision >= 1 &&
		authority.ProjectionRevisionID != uuid.Nil && authority.ProjectionRevision >= 1 &&
		validWaveView(authority.View, tournamentID, waveID) &&
		authority.SourceRevisions.WaveRevisionID == authority.View.Wave.RevisionID &&
		authority.SourceRevisions.WaveRevision == authority.View.Revision
}

func newPairingCommandRecord(
	command PairingCommand,
	authority PairingAuthority,
	digest [sha256.Size]byte,
	view SwissRoundView,
	executedAt time.Time,
) (PairingCommandRecord, error) {
	//nolint:musttag // The command ledger stores a versioned usecase-owned result document.
	document, err := json.Marshal(view)
	if err != nil {
		return PairingCommandRecord{}, fmt.Errorf("ExecutionWorkflow - encode pairing result: %w", err)
	}
	return PairingCommandRecord{
		CommandScope: command.CommandScope, RosterID: authority.RosterID,
		RoundNumber: command.RoundNumber, Mode: command.PairingMode,
		CategoryMode: command.CategoryMode, Categories: canonicalCategories(command.Categories),
		SourceProjectionRevisionID: authority.ProjectionRevisionID,
		SourceProjectionRevision:   authority.ProjectionRevision,
		SourceTournamentRevision:   authority.TournamentRevision, SourceRosterRevision: authority.RosterRevision,
		SourceHistoryRevision: authority.HistoryRevision, RequestDigest: digest,
		ResultDocument: document, ExecutedAt: executedAt,
	}, nil
}

func newWaveCommandRecord(
	command WaveCommand,
	authority WaveAuthority,
	digest [sha256.Size]byte,
	view WaveView,
	executedAt time.Time,
) (WaveCommandRecord, error) {
	//nolint:musttag // The command ledger stores a versioned usecase-owned result document.
	document, err := json.Marshal(view)
	if err != nil {
		return WaveCommandRecord{}, fmt.Errorf("ExecutionWorkflow - encode Wave result: %w", err)
	}
	return WaveCommandRecord{
		CommandScope: command.CommandScope, RosterID: authority.RosterID,
		WaveID: command.WaveID, Action: command.Action,
		SourceProjectionRevisionID: authority.ProjectionRevisionID,
		SourceProjectionRevision:   authority.ProjectionRevision,
		SourceTournamentRevision:   authority.TournamentRevision, SourceRosterRevision: authority.RosterRevision,
		SourceWaveRevision: authority.View.Revision, ResultingWaveRevision: view.Revision,
		SourceRevisions: authority.SourceRevisions, SourceGraph: authority.Graph,
		RequestDigest: digest, Reason: command.Reason, ResultDocument: document, ExecutedAt: executedAt,
	}, nil
}

func replayPairingCommand(
	record PairingCommandRecord,
	command PairingCommand,
	digest [sha256.Size]byte,
) (SwissRoundView, error) {
	if record.CommandScope != command.CommandScope || record.RoundNumber != command.RoundNumber ||
		record.Mode != command.PairingMode || record.SourceProjectionRevision != command.ExpectedProjectionRevision ||
		record.RequestDigest != digest {
		return SwissRoundView{}, domain.ErrConflict
	}
	var view SwissRoundView
	//nolint:musttag // The command ledger stores a versioned usecase-owned result document.
	if err := json.Unmarshal(record.ResultDocument, &view); err != nil ||
		!validSwissRoundView(view, command.TournamentID, command.RoundNumber) {
		return SwissRoundView{}, domain.ErrInternal
	}
	return cloneExecutionSwissRoundView(view), nil
}

func replayWaveCommand(
	record WaveCommandRecord,
	command WaveCommand,
	digest [sha256.Size]byte,
) (WaveView, error) {
	if record.CommandScope != command.CommandScope || record.WaveID != command.WaveID ||
		record.Action != command.Action || record.SourceProjectionRevision != command.ExpectedProjectionRevision ||
		record.RequestDigest != digest {
		return WaveView{}, domain.ErrConflict
	}
	var view WaveView
	//nolint:musttag // The command ledger stores a versioned usecase-owned result document.
	if err := json.Unmarshal(record.ResultDocument, &view); err != nil ||
		!validWaveView(view, command.TournamentID, command.WaveID) {
		return WaveView{}, domain.ErrInternal
	}
	return cloneExecutionWaveView(view), nil
}

func executionRequestDigest(value any) ([sha256.Size]byte, error) {
	document, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("ExecutionWorkflow - encode request digest: %w", err)
	}
	return sha256.Sum256(document), nil
}

func executionConflict(expected int64, authority PairingAuthority) error {
	return &RevisionConflictError{
		ExpectedRevision: expected, CurrentRevision: authority.ProjectionRevision,
		CurrentState: authority.TournamentState,
	}
}

func waveExecutionConflict(expected int64, authority WaveAuthority) error {
	return &RevisionConflictError{
		ExpectedRevision: expected, CurrentRevision: authority.ProjectionRevision,
		CurrentState: authority.TournamentState,
	}
}

func executionID(namespace uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(role))
}

func pairingParticipantIDs(participants []PairingParticipant) []uuid.UUID {
	ordered := append([]PairingParticipant(nil), participants...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StableSeed != ordered[j].StableSeed {
			return ordered[i].StableSeed < ordered[j].StableSeed
		}
		return bytes.Compare(ordered[i].ID[:], ordered[j].ID[:]) < 0
	})
	result := make([]uuid.UUID, len(ordered))
	for index := range ordered {
		result[index] = ordered[index].ID
	}
	return result
}

func manualPairs(pairs []ParticipantPair) []swissusecase.Pair {
	result := make([]swissusecase.Pair, len(pairs))
	for index, pair := range pairs {
		result[index] = swissusecase.Pair{
			FirstParticipantID: pair.FirstParticipantID, SecondParticipantID: pair.SecondParticipantID,
		}
	}
	return result
}

func byeParticipantID(bye *swissusecase.ByeSelection) uuid.UUID {
	if bye == nil {
		return uuid.Nil
	}
	return bye.ParticipantID
}

func withoutBye(participants []uuid.UUID, bye *swissusecase.ByeSelection) []uuid.UUID {
	if bye == nil {
		return append([]uuid.UUID(nil), participants...)
	}
	result := make([]uuid.UUID, 0, len(participants)-1)
	for _, participantID := range participants {
		if participantID != bye.ParticipantID {
			result = append(result, participantID)
		}
	}
	return result
}

func eligiblePreviousMeetings(previous []swissusecase.Pair, eligible []uuid.UUID) []swissusecase.Pair {
	eligibleSet := idSet(eligible)
	result := make([]swissusecase.Pair, 0, len(previous))
	for _, pair := range previous {
		if containsID(eligibleSet, pair.FirstParticipantID) && containsID(eligibleSet, pair.SecondParticipantID) {
			result = append(result, pair)
		}
	}
	return result
}

func clonePairingAuthority(source PairingAuthority) PairingAuthority {
	result := source
	result.Participants = append([]PairingParticipant(nil), source.Participants...)
	result.Standings = append([]SwissStandingView(nil), source.Standings...)
	result.PreviousMeetings = append([]swissusecase.Pair(nil), source.PreviousMeetings...)
	result.PriorMeetingCounts = make(map[swissusecase.PairKey]int, len(source.PriorMeetingCounts))
	for key, value := range source.PriorMeetingCounts {
		result.PriorMeetingCounts[key] = value
	}
	result.ReceivedBye = make(map[uuid.UUID]bool, len(source.ReceivedBye))
	for key, value := range source.ReceivedBye {
		result.ReceivedBye[key] = value
	}
	return result
}

func cloneExecutionSwissRoundView(source SwissRoundView) SwissRoundView {
	result := source
	result.RosterParticipantIDs = append([]uuid.UUID(nil), source.RosterParticipantIDs...)
	result.Pairings = append([]SwissPairingView(nil), source.Pairings...)
	result.Standings = append([]SwissStandingView(nil), source.Standings...)
	if source.PairingEvidence != nil {
		evidence := *source.PairingEvidence
		evidence.NormalizedInputs = append([]string(nil), source.PairingEvidence.NormalizedInputs...)
		evidence.Result = append([]string(nil), source.PairingEvidence.Result...)
		result.PairingEvidence = &evidence
	}
	if source.Bye != nil {
		bye := *source.Bye
		result.Bye = &bye
	}
	return result
}

func cloneExecutionWaveView(source WaveView) WaveView {
	result := source
	result.Wave = cloneDomainWave(source.Wave)
	result.ReadinessRevisions = make(map[uuid.UUID]int64, len(source.ReadinessRevisions))
	for key, value := range source.ReadinessRevisions {
		result.ReadinessRevisions[key] = value
	}
	result.SeriesIDs = make(map[uuid.UUID]uuid.UUID, len(source.SeriesIDs))
	for key, value := range source.SeriesIDs {
		result.SeriesIDs[key] = value
	}
	if source.ByeParticipantID != nil {
		bye := *source.ByeParticipantID
		result.ByeParticipantID = &bye
	}
	return result
}

func cloneDomainWave(source domain.Wave) domain.Wave {
	result := source
	result.Members = append([]domain.WaveMember(nil), source.Members...)
	if source.ReadyWindow != nil {
		window := *source.ReadyWindow
		if source.ReadyWindow.ConsumedAt != nil {
			consumedAt := *source.ReadyWindow.ConsumedAt
			window.ConsumedAt = &consumedAt
		}
		result.ReadyWindow = &window
	}
	if source.StartedAt != nil {
		startedAt := *source.StartedAt
		result.StartedAt = &startedAt
	}
	if source.PausedAt != nil {
		pausedAt := *source.PausedAt
		result.PausedAt = &pausedAt
	}
	return result
}

func canonicalCategories(categories []domain.Category) []domain.Category {
	result := append([]domain.Category(nil), categories...)
	slices.Sort(result)
	return result
}

var (
	_ PairingPort = (*ExecutionWorkflow)(nil)
	_ WavePort    = (*ExecutionWorkflow)(nil)
)
