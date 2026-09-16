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
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

type ExecutionWorkflow struct {
	transactions ExecutionTransactionManager
	repository   ExecutionWorkflowRepository
	normalPause  NormalPauseExecutionRepository
	waveStart    *gamestart.StartUseCase
	authority    ExecutionAuthorityProvider
}

func NewExecutionWorkflow(deps ExecutionWorkflowDependencies) *ExecutionWorkflow {
	return &ExecutionWorkflow{
		transactions: deps.Transactions,
		repository:   deps.Repository,
		normalPause:  deps.NormalPause,
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

//nolint:gocyclo // One locked workflow owns replay, strict pairing policy, persistence, and receipt publication.
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
		if errors.Is(err, swissusecase.ErrManualPairingRepeat) || errors.Is(err, swissusecase.ErrMatchingImpossible) {
			return SwissRoundView{}, fmt.Errorf("invalid Swiss pairing: %w: %w", domain.ErrValidation, err)
		}
		return SwissRoundView{}, err
	}
	view, err := w.repository.CommitPairing(ctx, plan)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return SwissRoundView{}, executionConflict(command.ExpectedProjectionRevision, authority)
		}
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

//nolint:gocyclo // Public dispatch keeps replay, authority, and transactional routing explicit.
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
	if command.Action == WaveActionStart || command.Action == WaveActionPause || command.Action == WaveActionResume {
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
	if authority.ProjectionRevision != command.ExpectedProjectionRevision {
		return WaveView{}, waveExecutionConflict(command.ExpectedProjectionRevision, authority)
	}
	mutatedAt, err := w.repository.ReadExecutionTime(ctx)
	if err != nil {
		return WaveView{}, fmt.Errorf("read Wave mutation time: %w", err)
	}
	if command.Action == WaveActionPause || command.Action == WaveActionResume {
		return w.controlNormalPauseLocked(ctx, command, digest, authority, executionAuthority, mutatedAt)
	}
	if !waveExecutionStateAllowed(authority.TournamentState) {
		return WaveView{}, waveExecutionConflict(command.ExpectedProjectionRevision, authority)
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

type executionPauseClock struct{ at time.Time }

func (clock executionPauseClock) Now() time.Time { return clock.at }

type normalPauseMillisecondRepository struct {
	NormalPauseExecutionRepository

	pausedAt time.Time
	pauseID  uuid.UUID
	seed     *gameusecase.NormalPauseAuthority
	seedUsed bool
}

//nolint:gocyclo // Canonicalization covers every deadline-bearing graph component.
func (repository *normalPauseMillisecondRepository) LoadNormalPauseAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
) (gameusecase.NormalPauseAuthority, error) {
	if repository.seed != nil && !repository.seedUsed {
		repository.seedUsed = true
		return *repository.seed, nil
	}
	authority, err := repository.NormalPauseExecutionRepository.LoadNormalPauseAuthority(ctx, scope)
	if err != nil {
		return gameusecase.NormalPauseAuthority{}, err
	}
	authority.Graph.Games = append([]gameusecase.PauseGame(nil), authority.Graph.Games...)
	for index := range authority.Graph.Games {
		game := &authority.Graph.Games[index]
		if game.Game.State != domain.GameStateActive || game.Deadline == nil {
			continue
		}
		remaining := game.Deadline.Sub(repository.pausedAt)
		remainingMilliseconds := remaining.Milliseconds()
		if remaining%time.Millisecond != 0 {
			remainingMilliseconds++
		}
		if remainingMilliseconds < 1 {
			return gameusecase.NormalPauseAuthority{}, gameusecase.ErrNormalPauseDeadline
		}
		deadline := repository.pausedAt.Add(time.Duration(remainingMilliseconds) * time.Millisecond)
		game.Deadline = &deadline
	}
	if authority.Graph.Draft != nil && authority.Graph.Draft.State == draftusecase.ExecutionStateActive &&
		authority.Graph.Draft.AbsoluteDeadline != nil {
		draft := draftusecase.CloneExecution(*authority.Graph.Draft)
		remaining := draft.AbsoluteDeadline.Sub(repository.pausedAt)
		remainingMilliseconds := remaining.Milliseconds()
		if remaining%time.Millisecond != 0 {
			remainingMilliseconds++
		}
		if remainingMilliseconds < 1 {
			return gameusecase.NormalPauseAuthority{}, gameusecase.ErrNormalPauseDeadline
		}
		deadline := repository.pausedAt.Add(time.Duration(remainingMilliseconds) * time.Millisecond)
		draft.TurnDeadline = deadline
		draft.AbsoluteDeadline = &deadline
		authority.Graph.Draft = &draft
	}
	authority.Graph.Counters = append([]pausedomain.PauseReconnectCounter(nil), authority.Graph.Counters...)
	for _, game := range authority.Graph.Games {
		if game.Game.State != domain.GameStateActive {
			continue
		}
		series := normalPauseSeriesByID(authority.Graph.Series, game.SeriesID)
		if series == nil {
			return gameusecase.NormalPauseAuthority{}, gameusecase.ErrNormalPauseGraphIncomplete
		}
		gamePauseID := executionID(repository.pauseID, "game:"+game.Game.ID.String())
		for _, participantID := range []uuid.UUID{
			series.Execution.Series.FirstParticipantID,
			series.Execution.Series.SecondParticipantID,
		} {
			if normalPauseCounterExists(authority.Graph.Counters, gamePauseID, participantID) {
				continue
			}
			authority.Graph.Counters = append(authority.Graph.Counters, pausedomain.PauseReconnectCounter{
				PauseID: gamePauseID, RosterID: scope.RosterID, ParticipantID: participantID,
				Limit: domain.ReconnectCycleLimit, Revision: 1,
			})
		}
	}
	sort.Slice(authority.Graph.Counters, func(i, j int) bool {
		left, right := authority.Graph.Counters[i], authority.Graph.Counters[j]
		if left.PauseID != right.PauseID {
			return left.PauseID.String() < right.PauseID.String()
		}
		return left.ParticipantID.String() < right.ParticipantID.String()
	})
	authority.Revisions = gameusecase.PauseGraphRevisionsFrom(authority.Graph)
	return authority, nil
}

func normalPauseSeriesByID(values []gameusecase.PauseSeries, id uuid.UUID) *gameusecase.PauseSeries {
	for index := range values {
		if values[index].Execution.Series.ID == id {
			return &values[index]
		}
	}
	return nil
}

func normalPauseCounterExists(values []pausedomain.PauseReconnectCounter, pauseID, participantID uuid.UUID) bool {
	for _, value := range values {
		if value.PauseID == pauseID && value.ParticipantID == participantID {
			return true
		}
	}
	return false
}

//nolint:gocyclo // Pause and resume share one atomic command receipt and recovery boundary.
func (w *ExecutionWorkflow) controlNormalPauseLocked(
	ctx context.Context,
	command WaveCommand,
	digest [sha256.Size]byte,
	authority WaveAuthority,
	executionAuthority authoritydomain.Identity,
	mutatedAt time.Time,
) (WaveView, error) {
	if w.normalPause == nil || executionAuthority.Validate() != nil ||
		executionAuthority.TournamentID != command.TournamentID {
		return WaveView{}, domain.ErrInternal
	}
	scope := pausedomain.GraphScope{
		TournamentID: command.TournamentID,
		RosterID:     authority.RosterID,
		WaveID:       command.WaveID,
		Authority:    executionAuthority,
	}
	clock := executionPauseClock{at: mutatedAt}
	var pauseRecord *gameusecase.NormalPauseRecord
	expectedCommittedWaveRevision := authority.View.Revision
	if command.Action == WaveActionPause {
		pauseID := executionID(command.CommandID, "normal-wave-pause")
		repository := &normalPauseMillisecondRepository{
			NormalPauseExecutionRepository: w.normalPause, pausedAt: mutatedAt, pauseID: pauseID,
		}
		seed, err := repository.LoadNormalPauseAuthority(ctx, scope)
		if err != nil {
			return WaveView{}, normalizeNormalPauseError(command.ExpectedProjectionRevision, authority, err)
		}
		draftRevisionID := uuid.Nil
		if seed.Revisions.Draft != nil {
			draftRevisionID = executionID(command.CommandID, "normal-pause-draft-revision")
		}
		repository.seed = &seed
		record, changed, err := gameusecase.NewNormalPauseGraphUseCase(w.transactions, repository, clock).Enter(ctx, gameusecase.NormalPauseCommand{
			Scope: scope, CommandID: command.CommandID, PauseID: pauseID,
			ActorID: command.Operator.ActorID, DraftResultRevisionID: draftRevisionID,
			Reason: gameusecase.PauseReasonOperator, Expected: seed.Revisions,
		})
		if err != nil {
			return WaveView{}, normalizeNormalPauseError(command.ExpectedProjectionRevision, authority, err)
		}
		if !changed || record == nil {
			return WaveView{}, fmt.Errorf("normal pause - missing committed result: %w", domain.ErrInternal)
		}
		pauseRecord = record
		expectedCommittedWaveRevision = record.Graph.Wave.Revision
	} else {
		pauseID, err := w.normalPause.ActiveNormalPauseID(ctx, scope)
		if err != nil {
			return WaveView{}, normalizeNormalPauseError(command.ExpectedProjectionRevision, authority, err)
		}
		seed, err := w.normalPause.LoadPauseResumeAuthority(ctx, scope, pauseID)
		if err != nil {
			return WaveView{}, normalizeNormalPauseError(command.ExpectedProjectionRevision, authority, err)
		}
		draftRevisionID := uuid.Nil
		if seed.Pause.Graph.Draft != nil && seed.Pause.Graph.Draft.State == "paused" {
			draftRevisionID = executionID(command.CommandID, "normal-resume-draft-revision")
		}
		resumeCommand := gameusecase.PauseResumeCommand{
			Scope: scope, PauseID: pauseID, CommandID: command.CommandID,
			ActorID: command.Operator.ActorID, DraftResultRevisionID: draftRevisionID,
			Expected: gameusecase.PauseResumeExpectationFrom(seed),
		}
		var changed bool
		if normalPauseHasDisconnectedPresence(seed.Presence) {
			changed, err = w.resumeNormalPauseWithPresence(ctx, resumeCommand, seed, clock)
			expectedCommittedWaveRevision = authority.View.Revision + 1
		} else {
			var record *gameusecase.PauseResumeRecord
			record, changed, err = gameusecase.NewPauseResumeUseCase(w.transactions, w.normalPause, clock).Resume(ctx, resumeCommand)
			if err == nil && record == nil {
				return WaveView{}, domain.ErrInternal
			}
			if record != nil {
				expectedCommittedWaveRevision = record.Graph.Wave.Revision
			}
		}
		if err != nil {
			return WaveView{}, normalizeNormalPauseError(command.ExpectedProjectionRevision, authority, err)
		}
		if !changed {
			return WaveView{}, domain.ErrInternal
		}
	}
	current, err := w.repository.LockWaveAuthority(ctx, command.TournamentID, command.WaveID)
	if err != nil {
		return WaveView{}, fmt.Errorf("normal pause - lock committed Wave authority: %w", err)
	}
	if !validWaveAuthority(current, command.TournamentID, command.WaveID) ||
		current.View.Revision != expectedCommittedWaveRevision {
		return WaveView{}, fmt.Errorf("normal pause - invalid committed Wave authority: %w", domain.ErrInternal)
	}
	receipt, err := newWaveCommandRecord(command, authority, digest, current.View, mutatedAt)
	if err != nil {
		return WaveView{}, fmt.Errorf("normal pause - build Wave command receipt: %w", err)
	}
	receipt.NormalPause = pauseRecord
	if err := w.repository.SaveWaveCommand(ctx, receipt); err != nil {
		return WaveView{}, fmt.Errorf("save Wave command record: %w", err)
	}
	return current.View, nil
}

func normalPauseHasDisconnectedPresence(values []pausedomain.PausePresence) bool {
	for _, value := range values {
		if value.State == pausedomain.PresenceStateDisconnected {
			return true
		}
	}
	return false
}

func (w *ExecutionWorkflow) resumeNormalPauseWithPresence(
	ctx context.Context,
	resume gameusecase.PauseResumeCommand,
	seed gameusecase.PauseResumeAuthority,
	clock executionPauseClock,
) (bool, error) {
	seriesID, gameID, ok := normalPauseDisconnectedExecution(seed)
	if !ok {
		return false, gameusecase.ErrPauseResumePresence
	}
	seriesPauseID := executionID(resume.PauseID, "series:"+seriesID.String())
	gamePauseID := executionID(resume.PauseID, "game:"+gameID.String())
	authority, err := w.normalPause.LoadPauseResumePresenceAuthority(ctx, resume.Scope, resume.PauseID, seriesPauseID, gamePauseID)
	if err != nil {
		return false, err
	}
	resume.Expected = gameusecase.PauseResumeExpectationFrom(authority.Resume)
	command := gameusecase.PauseResumePresenceCommand{
		Resume: resume, SeriesDecisionID: executionID(resume.CommandID, "resume-decision:"+seriesID.String()),
		GameDecisionID:  executionID(resume.CommandID, "resume-game-decision:"+gameID.String()),
		SeriesExpected:  gameusecase.PauseResumeDecisionExpectationFrom(authority.SeriesDecision),
		GameExpected:    gameusecase.PauseResumeDecisionExpectationFrom(authority.GameDecision),
		Presence:        append([]pausedomain.PausePresence(nil), authority.Resume.Presence...),
		Reconnect:       append([]pausedomain.PauseReconnectInterval(nil), authority.Resume.Reconnect...),
		Counters:        append([]pausedomain.PauseReconnectCounter(nil), authority.Resume.Counters...),
		FrozenDeadlines: append([]gameusecase.PauseFrozenDeadline(nil), authority.Resume.FrozenDeadlines...),
	}
	series := normalPauseSeriesByID(authority.Resume.Pause.Graph.Series, seriesID)
	if series == nil || authority.GameDecision.GameClock == nil {
		return false, gameusecase.ErrPauseResumePresenceIncomplete
	}
	command.FirstInterval, err = normalPauseReconnectInput(authority, series.Execution.Series.FirstParticipantID, resume.CommandID)
	if err != nil {
		return false, err
	}
	command.SecondInterval, err = normalPauseReconnectInput(authority, series.Execution.Series.SecondParticipantID, resume.CommandID)
	if err != nil {
		return false, err
	}
	record, changed, err := gameusecase.NewPauseResumePresenceUseCase(w.transactions, w.normalPause, clock).Resume(ctx, command)
	if err != nil {
		return false, err
	}
	if record == nil || record.GameDecision.Action == gameusecase.PauseResumeActionResume {
		return false, domain.ErrInternal
	}
	return changed, nil
}

func normalPauseDisconnectedExecution(seed gameusecase.PauseResumeAuthority) (uuid.UUID, uuid.UUID, bool) {
	seriesID := uuid.Nil
	for _, presence := range seed.Presence {
		if presence.State != pausedomain.PresenceStateDisconnected {
			continue
		}
		if seriesID != uuid.Nil && seriesID != presence.SeriesID {
			return uuid.Nil, uuid.Nil, false
		}
		seriesID = presence.SeriesID
	}
	if seriesID == uuid.Nil {
		return uuid.Nil, uuid.Nil, false
	}
	for _, game := range seed.Pause.Graph.Games {
		if game.SeriesID == seriesID && game.Game.State == domain.GameStatePaused {
			return seriesID, game.Game.ID, true
		}
	}
	return uuid.Nil, uuid.Nil, false
}

func normalPauseReconnectInput(
	authority gameusecase.PauseResumePresenceAuthority,
	participantID, commandID uuid.UUID,
) (*gameusecase.PauseResumeIntervalInput, error) {
	var live *pausedomain.PausePresence
	for index := range authority.Resume.Presence {
		if authority.Resume.Presence[index].ParticipantID == participantID {
			live = &authority.Resume.Presence[index]
			break
		}
	}
	if live == nil {
		return nil, gameusecase.ErrPauseResumePresenceIncomplete
	}
	if live.State == pausedomain.PresenceStateConnected {
		return nil, nil
	}
	window := authority.GameDecision.GameClock.Remaining
	for _, suspended := range authority.Resume.Pause.SuspendedReconnect {
		for _, interval := range authority.Resume.Pause.Graph.Reconnect {
			if interval.ID == suspended.ID && interval.ParticipantID == participantID &&
				interval.PresenceEpoch == live.PresenceEpoch {
				window = 0
			}
		}
	}
	if window < 0 {
		return nil, gameusecase.ErrPauseResumePresenceIncomplete
	}
	return &gameusecase.PauseResumeIntervalInput{
		ParticipantID: participantID,
		IntervalID:    executionID(commandID, "reconnect:"+participantID.String()),
		Window:        window,
	}, nil
}

func normalizeNormalPauseError(expected int64, authority WaveAuthority, err error) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, gameusecase.ErrNormalPauseGraphConflict) ||
		errors.Is(err, gameusecase.ErrPauseResumeConflict) || errors.Is(err, gameusecase.ErrPauseResumePresence) ||
		errors.Is(err, gameusecase.ErrPauseResumePresenceConflict) || errors.Is(err, gameusecase.ErrPauseResumePresenceIncomplete) ||
		errors.Is(err, gameusecase.ErrPauseResumePresenceIneligible) ||
		errors.Is(err, gameusecase.ErrNormalPauseGoldenActive) || errors.Is(err, gameusecase.ErrNormalPauseDeadline) {
		return waveExecutionConflict(expected, authority)
	}
	return err
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
	record, changed, err := w.waveStart.Start(ctx, gamestart.StartCommand{
		Scope: gamestart.StartScope{
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

func recordedWaveStartView(record gamestart.StartRecord) (WaveView, error) {
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
	if errors.Is(err, gamestart.ErrWaveStartAuthorityConflict) ||
		errors.Is(err, gamestart.ErrWaveStartConflict) || errors.Is(err, domain.ErrConflict) {
		return waveExecutionConflict(expectedProjectionRevision, authority)
	}
	if errors.Is(err, gamestart.ErrInvalidWaveStart) {
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
		if err := swissusecase.ValidateManualPairing(rosterIDs, manual, authority.PreviousMeetings); err != nil {
			return PairingPlan{}, err
		}
		plan.Pairs = append([]swissusecase.Pair(nil), manual.Pairings...)
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
	evidenceID := executionID(command.CommandID, "bye-evidence")
	if command.PairingMode == PairingModeManual {
		requested := uuid.Nil
		if command.ManualByeParticipantID != nil {
			requested = *command.ManualByeParticipantID
		}
		selection, err := swissusecase.SelectManualBye(
			evidenceID, roundID, candidates, requested, decidedAt,
		)
		if err == nil {
			return &selection, nil
		}
		if !errors.Is(err, swissusecase.ErrManualByeNotEligible) {
			return nil, err
		}
		automatic, automaticErr := swissusecase.SelectBye(evidenceID, roundID, candidates, decidedAt)
		if automaticErr == nil {
			return nil, &ManualByeMismatchError{
				RequestedParticipantID: requested, SelectedParticipantID: automatic.ParticipantID,
				ExpectedRevision: command.ExpectedProjectionRevision,
				CurrentRevision:  authority.ProjectionRevision, CurrentState: authority.TournamentState,
			}
		}
		return nil, err
	}
	selection, err := swissusecase.SelectBye(evidenceID, roundID, candidates, decidedAt)
	if err != nil {
		return nil, err
	}
	return &selection, nil
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
	return !authority.Graph.PendingDrafts && waveExecutionStateAllowed(authority.TournamentState) && authority.Graph.SeriesCount >= 1 &&
		authority.Graph.PlayableMemberCount == authority.Graph.SeriesCount*2
}

func waveGraphStartable(authority WaveAuthority) bool {
	graph := authority.Graph
	return !graph.PendingDrafts && waveExecutionStateAllowed(authority.TournamentState) && graph.SeriesCount >= 1 &&
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
	var (
		document []byte
		err      error
	)
	switch command := value.(type) {
	case PairingCommand:
		//nolint:musttag // The legacy command digest intentionally preserves Go's original exported field names.
		document, err = json.Marshal(newPairingRequestDigestDocument(command))
	case *PairingCommand:
		if command == nil {
			document, err = json.Marshal(value)
		} else {
			//nolint:musttag // The legacy command digest intentionally preserves Go's original exported field names.
			document, err = json.Marshal(newPairingRequestDigestDocument(*command))
		}
	default:
		document, err = json.Marshal(value)
	}
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("ExecutionWorkflow - encode request digest: %w", err)
	}
	return sha256.Sum256(document), nil
}

// pairingRequestDigestDocument retains the pre-removal JSON shape for the
// command ledger. Older commands submitted without an override encoded a nil
// RepeatOverride field, so exact replays must continue hashing that field as
// null even though active commands no longer expose an override input.
type pairingRequestDigestDocument struct {
	CommandScope

	ExpectedProjectionRevision int64
	RoundNumber                int
	PairingMode                PairingMode
	CategoryMode               domain.CategoryMode
	Categories                 []domain.Category
	ManualPairings             []ParticipantPair
	ManualPairingsProvided     bool
	ManualByeParticipantID     *uuid.UUID
	RepeatOverride             *struct{}
}

func newPairingRequestDigestDocument(command PairingCommand) pairingRequestDigestDocument {
	return pairingRequestDigestDocument{
		CommandScope:               command.CommandScope,
		ExpectedProjectionRevision: command.ExpectedProjectionRevision,
		RoundNumber:                command.RoundNumber,
		PairingMode:                command.PairingMode,
		CategoryMode:               command.CategoryMode,
		Categories:                 command.Categories,
		ManualPairings:             command.ManualPairings,
		ManualPairingsProvided:     command.ManualPairingsProvided,
		ManualByeParticipantID:     command.ManualByeParticipantID,
		RepeatOverride:             nil,
	}
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
