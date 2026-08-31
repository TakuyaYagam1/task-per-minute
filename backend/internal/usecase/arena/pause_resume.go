package arena

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const pauseResumeAttempts = 2

var (
	ErrInvalidPauseResume      = errors.New("invalid pause resume")
	ErrPauseResumeConflict     = errors.New("pause resume conflict")
	ErrPauseResumeCommandReuse = errors.New("pause resume command reuse")
	ErrPauseResumeIncomplete   = errors.New("pause resume evidence incomplete")
	ErrPauseResumePresence     = errors.New("pause resume requires connected participants")
	ErrPauseResumeOverflow     = errors.New("pause resume revision overflow")
)

type PauseResumeExpectation struct {
	PauseID                 uuid.UUID
	GraphRevision           int64
	PauseRevision           int64
	Authority               ExecutionAuthorityIdentity
	TournamentState         domain.ArenaTournamentState
	TournamentRevision      int64
	WaveRevision            int64
	Series                  []PauseChildRevision
	Games                   []PauseChildRevision
	Draft                   *DraftRevisionExpectation
	DraftPreviousRevisionID uuid.UUID
	Presence                []PausePresenceRevision
	Reconnect               []PauseChildRevision
	Counters                []PauseReconnectCounterRevision
	FrozenDeadlines         []PauseFrozenDeadlineRevision
	TerminalActionRevision  int64
}

type PauseResumeCommand struct {
	Scope                 PauseGraphScope
	PauseID               uuid.UUID
	CommandID             uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	Expected              PauseResumeExpectation
}

type PauseResumeAuthority struct {
	Pause                  NormalPauseRecord
	Presence               []PausePresence
	Reconnect              []PauseReconnectInterval
	Counters               []PauseReconnectCounter
	FrozenDeadlines        []PauseFrozenDeadline
	TerminalActionRevision int64
}

type PauseResumeRecord struct {
	Scope                 PauseGraphScope
	PauseID               uuid.UUID
	CommandID             uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	State                 PauseState
	Revision              int64
	Expected              PauseResumeExpectation
	Graph                 PauseGraph
	ResumedAt             time.Time
}

// PauseResumeRepository participates in the caller transaction. Load locks in
// this stable order: execution-authority, pause and graph, Tournament, Wave,
// Series, Games, Draft, Presence, Reconnect, counters, frozen deadlines and
// terminal actions, with collection rows ordered by durable identity. Commit
// revalidates the complete expectation and atomically stores the restored graph
// and command result. A conflict makes no write.
type PauseResumeRepository interface {
	FindPauseResumeCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PauseResumeRecord, error)
	LoadPauseResumeAuthority(ctx context.Context, scope PauseGraphScope, pauseID uuid.UUID) (PauseResumeAuthority, error)
	CommitPauseResume(ctx context.Context, expected PauseResumeExpectation, record PauseResumeRecord) (*PauseResumeRecord, bool, error)
}

type PauseResumeUseCase struct {
	transactions TransactionManager
	repository   PauseResumeRepository
	clock        Clock
}

func NewPauseResumeUseCase(transactions TransactionManager, repository PauseResumeRepository, clock Clock) *PauseResumeUseCase {
	return &PauseResumeUseCase{transactions: transactions, repository: repository, clock: clock}
}

func (u *PauseResumeUseCase) Resume(ctx context.Context, command PauseResumeCommand) (*PauseResumeRecord, bool, error) {
	command.Expected = clonePauseResumeExpectation(command.Expected)
	if u == nil || u.transactions == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validatePauseResumeCommand(command); err != nil {
		return nil, false, err
	}
	for range pauseResumeAttempts {
		record, changed, retry, err := u.resumeAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrPauseResumeConflict
}

func (u *PauseResumeUseCase) resumeAttempt(ctx context.Context, command PauseResumeCommand) (*PauseResumeRecord, bool, bool, error) {
	var outcome pauseResumeAttemptOutcome
	err := u.transactions.Do(ctx, func(txCtx context.Context) error {
		var err error
		outcome, err = u.resumeLocked(txCtx, command)
		return err
	})
	if err != nil {
		return nil, false, false, fmt.Errorf("pause resume - transaction: %w", err)
	}
	return outcome.record, outcome.changed, outcome.retry, nil
}

type pauseResumeAttemptOutcome struct {
	record  *PauseResumeRecord
	changed bool
	retry   bool
}

func (u *PauseResumeUseCase) resumeLocked(ctx context.Context, command PauseResumeCommand) (pauseResumeAttemptOutcome, error) {
	recorded, err := u.findPauseResumeCommand(ctx, command, "find command")
	if err != nil || recorded != nil {
		return reconcilePauseResumeOutcome(recorded, command, err)
	}
	authority, err := u.repository.LoadPauseResumeAuthority(ctx, command.Scope, command.PauseID)
	if err != nil {
		return pauseResumeAttemptOutcome{}, fmt.Errorf("pause resume - load authority: %w", err)
	}
	recorded, err = u.findPauseResumeCommand(ctx, command, "find locked command")
	if err != nil || recorded != nil {
		return reconcilePauseResumeOutcome(recorded, command, err)
	}
	resumedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(resumedAt) {
		return pauseResumeAttemptOutcome{}, domain.ErrValidation
	}
	if err := validatePauseResumeAuthority(authority); err != nil {
		return pauseResumeAttemptOutcome{}, err
	}
	if authority.Pause.Scope != command.Scope || authority.Pause.PauseID != command.PauseID ||
		!pauseResumeExpectationEqual(PauseResumeExpectationFrom(authority), command.Expected) {
		return pauseResumeAttemptOutcome{}, ErrPauseResumeConflict
	}
	built, err := buildPauseResumeRecord(authority, command, resumedAt)
	if err != nil {
		return pauseResumeAttemptOutcome{}, err
	}
	return u.commitPauseResume(ctx, command, built)
}

func (u *PauseResumeUseCase) findPauseResumeCommand(ctx context.Context, command PauseResumeCommand, operation string) (*PauseResumeRecord, error) {
	recorded, err := u.repository.FindPauseResumeCommand(ctx, command.Scope.TournamentID, command.CommandID)
	if err != nil {
		return nil, fmt.Errorf("pause resume - %s: %w", operation, err)
	}
	return recorded, nil
}

func reconcilePauseResumeOutcome(recorded *PauseResumeRecord, command PauseResumeCommand, err error) (pauseResumeAttemptOutcome, error) {
	if err != nil {
		return pauseResumeAttemptOutcome{}, err
	}
	result, err := reconcilePauseResume(*recorded, command)
	return pauseResumeAttemptOutcome{record: result}, err
}

func (u *PauseResumeUseCase) commitPauseResume(ctx context.Context, command PauseResumeCommand, built PauseResumeRecord) (pauseResumeAttemptOutcome, error) {
	committed, changed, err := u.repository.CommitPauseResume(ctx, clonePauseResumeExpectation(command.Expected), built)
	if errors.Is(err, domain.ErrConflict) {
		return pauseResumeAttemptOutcome{retry: true}, nil
	}
	if err != nil {
		return pauseResumeAttemptOutcome{}, fmt.Errorf("pause resume - commit graph: %w", err)
	}
	if committed == nil {
		return pauseResumeAttemptOutcome{}, domain.ErrInternal
	}
	result, err := reconcilePauseResume(*committed, command)
	if err != nil || (changed && !reflect.DeepEqual(*result, built)) {
		return pauseResumeAttemptOutcome{}, domain.ErrInternal
	}
	return pauseResumeAttemptOutcome{record: result, changed: changed}, nil
}

func validatePauseResumeCommand(command PauseResumeCommand) error {
	if !validPauseGraphScope(command.Scope) || command.PauseID == uuid.Nil || command.CommandID == uuid.Nil || command.ActorID == uuid.Nil ||
		command.PauseID == command.CommandID || command.CommandID == command.ActorID || command.PauseID == command.ActorID {
		return pauseResumeError("invalid command identity")
	}
	if err := validatePauseResumeExpectation(command.Expected); err != nil {
		return err
	}
	if command.Expected.PauseID != command.PauseID || command.Expected.Authority != command.Scope.Authority {
		return pauseResumeError("command expectation does not match scope")
	}
	if !validDraftResultRevisionIdentity(command.Expected.Draft, command.Expected.DraftPreviousRevisionID, command.DraftResultRevisionID,
		command.CommandID, command.PauseID, command.ActorID) {
		return pauseResumeError("invalid Draft result revision identity")
	}
	return nil
}

func validatePauseResumeAuthority(authority PauseResumeAuthority) error {
	if err := validatePauseResumeFrozenRevisions(authority.FrozenDeadlines); err != nil {
		return err
	}
	for _, interval := range authority.Reconnect {
		if interval.State == ReconnectStateOpen {
			return ErrPauseResumePresence
		}
	}
	if validateNormalPauseRecord(authority.Pause) != nil || authority.Pause.State != PauseStateActive {
		return pauseResumeError("pause is not active")
	}
	if err := validateLivePausePresence(authority); err != nil {
		return err
	}
	if err := validateLivePauseReconnect(authority); err != nil {
		return err
	}
	if !reflect.DeepEqual(authority.Counters, authority.Pause.Graph.Counters) ||
		!reflect.DeepEqual(authority.FrozenDeadlines, authority.Pause.Graph.FrozenDeadlines) ||
		authority.TerminalActionRevision != authority.Pause.Graph.TerminalActionRevision {
		return ErrPauseResumeIncomplete
	}
	return nil
}

func validatePauseResumeFrozenRevisions(values []PauseFrozenDeadline) error {
	for _, frozen := range values {
		if frozen.Revision == math.MaxInt64 {
			return ErrPauseResumeOverflow
		}
	}
	return nil
}

func validateLivePausePresence(authority PauseResumeAuthority) error {
	required := make(map[uuid.UUID]PausePresence, len(authority.Pause.Graph.Presence))
	for _, snapshot := range authority.Pause.Graph.Presence {
		required[snapshot.ParticipantID] = snapshot
	}
	seen := make(map[uuid.UUID]struct{}, len(authority.Presence))
	for _, presence := range authority.Presence {
		snapshot, exists := required[presence.ParticipantID]
		if err := validateLivePausePresenceRow(authority, snapshot, presence, exists); err != nil {
			return err
		}
		if _, duplicate := seen[presence.ParticipantID]; duplicate {
			return ErrPauseResumeIncomplete
		}
		seen[presence.ParticipantID] = struct{}{}
	}
	if len(seen) != len(required) {
		return ErrPauseResumeIncomplete
	}
	return nil
}

func validateLivePausePresenceRow(authority PauseResumeAuthority, snapshot, presence PausePresence, exists bool) error {
	if validatePausePresence(presence) != nil || presence.TournamentID != authority.Pause.Scope.TournamentID ||
		presence.RosterID != authority.Pause.Scope.RosterID {
		return pauseResumeError("invalid live Presence")
	}
	if !exists || !samePausePresenceIdentity(snapshot, presence) || presence.PresenceEpoch < snapshot.PresenceEpoch ||
		presence.Revision < snapshot.Revision || presence.PresenceEpoch-snapshot.PresenceEpoch != presence.Revision-snapshot.Revision {
		return ErrPauseResumeIncomplete
	}
	if presence.PresenceEpoch == snapshot.PresenceEpoch && !reflect.DeepEqual(presence, snapshot) {
		return ErrPauseResumeIncomplete
	}
	if presence.PresenceEpoch > snapshot.PresenceEpoch && presence.UpdatedAt.Before(authority.Pause.PausedAt) {
		return ErrPauseResumeIncomplete
	}
	return nil
}

func validateLivePauseReconnect(authority PauseResumeAuthority) error {
	pausedReconnect := make(map[uuid.UUID]PauseReconnectInterval, len(authority.Pause.Graph.Reconnect))
	for _, interval := range authority.Pause.Graph.Reconnect {
		pausedReconnect[interval.ID] = interval
	}
	seenReconnect := make(map[uuid.UUID]struct{}, len(authority.Reconnect))
	for _, interval := range authority.Reconnect {
		snapshot, exists := pausedReconnect[interval.ID]
		if validatePauseReconnect(interval) != nil {
			return pauseResumeError("invalid Reconnect evidence")
		}
		if !exists || !reflect.DeepEqual(interval, snapshot) {
			return ErrPauseResumeIncomplete
		}
		if _, duplicate := seenReconnect[interval.ID]; duplicate {
			return ErrPauseResumeIncomplete
		}
		seenReconnect[interval.ID] = struct{}{}
	}
	if len(seenReconnect) != len(pausedReconnect) {
		return ErrPauseResumeIncomplete
	}
	return nil
}

func buildPauseResumeRecord(authority PauseResumeAuthority, command PauseResumeCommand, resumedAt time.Time) (PauseResumeRecord, error) {
	if resumedAt.Before(authority.Pause.PausedAt) || !resumeTimeCoversAuthorityHistory(authority, resumedAt) {
		return PauseResumeRecord{}, pauseResumeError("resume time precedes pause")
	}
	for _, presence := range authority.Presence {
		if presence.State != PresenceStateConnected {
			return PauseResumeRecord{}, ErrPauseResumePresence
		}
		if !timeCoversPresenceHistory(resumedAt, presence) {
			return PauseResumeRecord{}, pauseResumeError("resume time precedes Presence")
		}
	}
	for _, interval := range authority.Reconnect {
		if interval.State == ReconnectStateOpen {
			return PauseResumeRecord{}, ErrPauseResumePresence
		}
		if !timeCoversReconnectHistory(resumedAt, interval) {
			return PauseResumeRecord{}, pauseResumeError("resume time precedes Reconnect")
		}
	}
	pause := authority.Pause
	if pause.Revision == math.MaxInt64 || pause.Graph.Revision == math.MaxInt64 {
		return PauseResumeRecord{}, ErrPauseResumeOverflow
	}
	graph := clonePauseGraph(pause.Graph)
	graph.Presence = clonePausePresenceSlice(authority.Presence)
	graph.Reconnect = clonePauseReconnectSlice(authority.Reconnect)
	graph.Counters = append([]PauseReconnectCounter(nil), authority.Counters...)
	graph.FrozenDeadlines = clonePauseFrozenDeadlineSlice(authority.FrozenDeadlines)
	graph.TerminalActionRevision = authority.TerminalActionRevision
	if err := shiftPauseDeadlines(&graph, resumedAt); err != nil {
		return PauseResumeRecord{}, err
	}
	if err := restorePauseGraph(&graph, command, resumedAt); err != nil {
		return PauseResumeRecord{}, err
	}
	graph.Revision++
	graph.ActivePauseID = uuid.Nil
	graph.PausedAt = nil
	graph.DeadlinesSuppressed = false
	record := PauseResumeRecord{
		Scope: command.Scope, PauseID: command.PauseID, CommandID: command.CommandID, ActorID: command.ActorID,
		DraftResultRevisionID: command.DraftResultRevisionID,
		State:                 PauseStateResumed, Revision: pause.Revision + 1, Expected: clonePauseResumeExpectation(command.Expected),
		Graph: graph, ResumedAt: resumedAt,
	}
	if err := validatePauseResumeRecord(record); err != nil {
		return PauseResumeRecord{}, err
	}
	return clonePauseResumeRecord(record), nil
}

func resumeTimeCoversAuthorityHistory(authority PauseResumeAuthority, resumedAt time.Time) bool {
	if !pauseTimeCoversGraphHistory(authority.Pause.Graph, resumedAt) {
		return false
	}
	for _, presence := range authority.Presence {
		if !timeCoversPresenceHistory(resumedAt, presence) {
			return false
		}
	}
	for _, interval := range authority.Reconnect {
		if !timeCoversReconnectHistory(resumedAt, interval) {
			return false
		}
	}
	for _, frozen := range authority.FrozenDeadlines {
		if !timeAtOrBefore(resumedAt, frozen.FrozenAt) || !timePointerAtOrBefore(resumedAt, frozen.ResumedAt) {
			return false
		}
	}
	return true
}

func shiftPauseDeadlines(graph *PauseGraph, resumedAt time.Time) error {
	seen := make(map[PauseDeadlineKind]map[uuid.UUID]struct{})
	for index := range graph.FrozenDeadlines {
		frozen := &graph.FrozenDeadlines[index]
		if pauseDeadlineSeen(seen, frozen.Kind, frozen.OwnerID) {
			return ErrPauseResumeIncomplete
		}
		if err := shiftPauseDeadline(graph, frozen, resumedAt); err != nil {
			return err
		}
	}
	return nil
}

func pauseDeadlineSeen(seen map[PauseDeadlineKind]map[uuid.UUID]struct{}, kind PauseDeadlineKind, ownerID uuid.UUID) bool {
	if seen[kind] == nil {
		seen[kind] = make(map[uuid.UUID]struct{})
	}
	if _, duplicate := seen[kind][ownerID]; duplicate {
		return true
	}
	seen[kind][ownerID] = struct{}{}
	return false
}

func shiftPauseDeadline(graph *PauseGraph, frozen *PauseFrozenDeadline, resumedAt time.Time) error {
	if validateFrozenDeadline(*frozen, true) != nil || frozen.Revision == math.MaxInt64 {
		return ErrPauseResumeOverflow
	}
	deadline, ok := safePauseTimeAdd(resumedAt, frozen.Remaining)
	if !ok {
		return ErrPauseResumeOverflow
	}
	if err := applyResumedDeadline(graph, frozen.Kind, frozen.OwnerID, deadline); err != nil {
		return err
	}
	frozen.ResumedAt = cloneTimePointer(&resumedAt)
	frozen.ResumedDeadline = cloneTimePointer(&deadline)
	frozen.Revision++
	return nil
}

func applyResumedDeadline(graph *PauseGraph, kind PauseDeadlineKind, ownerID uuid.UUID, deadline time.Time) error {
	switch kind {
	case PauseDeadlineReadyWindow:
		if graph.Wave.Wave.ReadyWindow == nil || graph.Wave.Wave.ReadyWindow.ID != ownerID {
			return ErrPauseResumeIncomplete
		}
		graph.Wave.Wave.ReadyWindow.Deadline = deadline
	case PauseDeadlineGame:
		game := pauseGameByID(graph.Games, ownerID)
		if game == nil {
			return ErrPauseResumeIncomplete
		}
		game.Deadline = cloneTimePointer(&deadline)
	case PauseDeadlineDraft:
		if graph.Draft == nil || graph.Draft.ID != ownerID {
			return ErrPauseResumeIncomplete
		}
		graph.Draft.TurnDeadline = deadline
		graph.Draft.AbsoluteDeadline = cloneTimePointer(&deadline)
	default:
		return ErrPauseResumeIncomplete
	}
	return nil
}

func restorePauseGraph(graph *PauseGraph, command PauseResumeCommand, resumedAt time.Time) error {
	if graph.Tournament.Revision == math.MaxInt64 {
		return ErrPauseResumeOverflow
	}
	tournament := domain.ArenaTournament{State: graph.Tournament.State, PausedFromState: graph.Tournament.PausedFromState}
	if tournament.PausedFromState == nil {
		return ErrPauseResumeIncomplete
	}
	changed, err := tournament.TransitionTo(*tournament.PausedFromState)
	if err != nil || !changed {
		return pauseResumeError("restore Tournament: %v", err)
	}
	graph.Tournament.State = tournament.State
	graph.Tournament.PausedFromState = nil
	graph.Tournament.Revision++
	graph.Tournament.UpdatedAt = resumedAt
	if err := restoreWave(graph); err != nil {
		return err
	}
	if err := restoreSeriesAndGames(graph); err != nil {
		return err
	}
	return restoreDraft(graph, command, resumedAt)
}

func restoreWave(graph *PauseGraph) error {
	switch graph.Wave.Wave.State {
	case domain.ArenaWaveStatePaused:
		if graph.Wave.Revision == math.MaxInt64 {
			return ErrPauseResumeOverflow
		}
		if err := graph.Wave.Wave.Resume(); err != nil {
			return pauseResumeError("resume Wave: %v", err)
		}
		graph.Wave.Revision++
	case domain.ArenaWaveStateReadyWindowOpen, domain.ArenaWaveStateReady:
		if graph.Wave.Revision == math.MaxInt64 {
			return ErrPauseResumeOverflow
		}
		graph.Wave.Revision++
	case domain.ArenaWaveStatePlanned, domain.ArenaWaveStateCompleted,
		domain.ArenaWaveStateReadyWindowExpired, domain.ArenaWaveStateSuperseded:
		return nil
	case domain.ArenaWaveStateActive:
		return ErrPauseResumeIncomplete
	default:
		return ErrPauseResumeIncomplete
	}
	return nil
}

func restoreSeriesAndGames(graph *PauseGraph) error {
	seriesByID := make(map[uuid.UUID]*PauseSeries, len(graph.Series))
	for index := range graph.Series {
		series := &graph.Series[index]
		seriesByID[series.Execution.Series.ID] = series
		if series.Execution.Series.State != domain.ArenaSeriesStateTechnicalPause {
			continue
		}
		if series.Revision == math.MaxInt64 || series.Execution.ResumeState == nil {
			return ErrPauseResumeOverflow
		}
		next, changed, err := TransitionSeriesExecution(series.Execution, SeriesExecutionTransitionCommand{NextState: *series.Execution.ResumeState})
		if err != nil || !changed {
			return pauseResumeError("resume Series: %v", err)
		}
		series.Execution = next
		series.Revision++
	}
	for index := range graph.Games {
		game := &graph.Games[index]
		if game.Game.State != domain.ArenaGameStatePaused {
			continue
		}
		if game.Revision == math.MaxInt64 {
			return ErrPauseResumeOverflow
		}
		if game.ResumeState == nil || *game.ResumeState != domain.ArenaGameStateActive || game.Deadline == nil {
			return ErrPauseResumeIncomplete
		}
		game.Game.State = *game.ResumeState
		game.ResumeState = nil
		game.Revision++
		series := seriesByID[game.SeriesID]
		if series == nil || !replaceSeriesGame(&series.Execution.Series, game.Game) {
			return ErrPauseResumeIncomplete
		}
	}
	return nil
}

func restoreDraft(graph *PauseGraph, command PauseResumeCommand, resumedAt time.Time) error {
	if graph.Draft == nil || graph.Draft.State != DraftExecutionStatePaused {
		return nil
	}
	draft := cloneDraftExecution(*graph.Draft)
	if draft.Revision == math.MaxInt64 || draft.AbsoluteDeadline == nil || draft.PausedRemaining <= 0 || draft.Recovery == nil {
		return ErrPauseResumeOverflow
	}
	if command.Expected.DraftPreviousRevisionID != draft.PreviousRevisionID ||
		command.DraftResultRevisionID == draft.ID || command.DraftResultRevisionID == draft.RevisionID ||
		command.DraftResultRevisionID == draft.PreviousRevisionID {
		return pauseResumeError("Draft result revision identity matches Draft")
	}
	advanceDraftRevision(&draft, command.DraftResultRevisionID, command.CommandID, draft.ServiceEpoch)
	draft.State = DraftExecutionStateActive
	draft.PausedRemaining = 0
	draft.Recovery = nil
	draft.Transition = &DraftTransitionEvidence{Operation: DraftTransitionResume, ActorID: command.ActorID, Reason: "pause resumed", OccurredAt: resumedAt}
	if err := draft.Validate(); err != nil {
		return pauseResumeError("resume Draft: %v", err)
	}
	graph.Draft = &draft
	return nil
}

func validatePauseResumeRecord(record PauseResumeRecord) error {
	if !validPauseResumeRecordIdentity(record) || !validPauseResumeRecordGraph(record) ||
		!validDraftResultRevisionIdentity(record.Expected.Draft, record.Expected.DraftPreviousRevisionID,
			record.DraftResultRevisionID, record.CommandID, record.PauseID, record.ActorID) {
		return pauseResumeError("invalid resolved record identity")
	}
	if err := validatePauseGraph(record.Graph, false); err != nil {
		return err
	}
	if !resolvedGraphMatchesExpectation(record.Graph, record.Expected, record.ResumedAt,
		record.DraftResultRevisionID, record.CommandID, record.ActorID) {
		return pauseResumeError("resolved graph does not match expectation")
	}
	return nil
}

func validPauseResumeRecordIdentity(record PauseResumeRecord) bool {
	return validPauseGraphScope(record.Scope) && record.PauseID != uuid.Nil && record.CommandID != uuid.Nil &&
		record.ActorID != uuid.Nil && record.State == PauseStateResumed && record.Expected.PauseRevision < math.MaxInt64 &&
		record.Revision == record.Expected.PauseRevision+1 && validArenaServerTime(record.ResumedAt)
}

func validPauseResumeRecordGraph(record PauseResumeRecord) bool {
	return record.Graph.Scope == record.Scope && record.Graph.ActivePauseID == uuid.Nil && record.Graph.PausedAt == nil &&
		!record.Graph.DeadlinesSuppressed && resolvedGraphEvidenceAt(record.Graph, record.ResumedAt)
}

func resolvedGraphEvidenceAt(graph PauseGraph, resumedAt time.Time) bool {
	if !pauseTimeCoversGraphHistory(graph, resumedAt) {
		return false
	}
	for _, presence := range graph.Presence {
		if presence.State != PresenceStateConnected {
			return false
		}
	}
	for _, interval := range graph.Reconnect {
		if interval.State == ReconnectStateOpen {
			return false
		}
	}
	for _, frozen := range graph.FrozenDeadlines {
		if frozen.ResumedAt == nil || !frozen.ResumedAt.Equal(resumedAt) {
			return false
		}
	}
	return true
}

func resolvedGraphMatchesExpectation(
	graph PauseGraph,
	expected PauseResumeExpectation,
	resumedAt time.Time,
	draftResultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
) bool {
	if !resolvedRootMatchesExpectation(graph, expected, resumedAt) {
		return false
	}
	current := PauseGraphRevisionsFrom(graph)
	return resolvedMutableChildrenMatch(graph, expected, resumedAt, draftResultRevisionID, commandID, actorID) &&
		presenceRevisionMapEqual(current.Presence, expected.Presence) &&
		revisionMapEqual(current.Reconnect, expected.Reconnect) && counterRevisionMapEqual(current.Counters, expected.Counters) &&
		frozenRevisionsAdvanced(current.FrozenDeadlines, expected.FrozenDeadlines) &&
		current.TerminalActionRevision == expected.TerminalActionRevision
}

func resolvedRootMatchesExpectation(graph PauseGraph, expected PauseResumeExpectation, resumedAt time.Time) bool {
	return validatePauseResumeExpectation(expected) == nil && expected.GraphRevision < math.MaxInt64 &&
		expected.TournamentRevision < math.MaxInt64 && graph.Revision == expected.GraphRevision+1 &&
		graph.Tournament.Revision == expected.TournamentRevision+1 && graph.Tournament.PausedFromState == nil &&
		graph.Tournament.UpdatedAt.Equal(resumedAt) && graph.Tournament.State == expected.TournamentState &&
		resolvedWaveRevisionMatches(graph.Wave, expected.WaveRevision)
}

func resolvedMutableChildrenMatch(
	graph PauseGraph,
	expected PauseResumeExpectation,
	resumedAt time.Time,
	draftResultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
) bool {
	return resolvedSeriesRevisionsMatch(graph.Series, expected.Series) &&
		resolvedGameRevisionsMatch(graph.Games, expected.Games) &&
		resolvedDraftRevisionMatches(graph.Draft, expected.Draft, expected.DraftPreviousRevisionID,
			draftResultRevisionID, commandID, actorID, resumedAt)
}

func resolvedWaveRevisionMatches(current PauseWave, expected int64) bool {
	switch current.Wave.State {
	case domain.ArenaWaveStateActive, domain.ArenaWaveStateReadyWindowOpen, domain.ArenaWaveStateReady:
		return nextRevisionMatches(current.Revision, expected)
	case domain.ArenaWaveStatePlanned, domain.ArenaWaveStateCompleted,
		domain.ArenaWaveStateReadyWindowExpired, domain.ArenaWaveStateSuperseded:
		return current.Revision == expected
	case domain.ArenaWaveStatePaused:
		return false
	default:
		return false
	}
}

func resolvedSeriesRevisionsMatch(current []PauseSeries, expected []PauseChildRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	for _, series := range current {
		revision, ok := childRevision(expected, series.Execution.Series.ID)
		if !ok || !resolvedSeriesRevisionMatches(series, revision) {
			return false
		}
	}
	return true
}

func resolvedSeriesRevisionMatches(current PauseSeries, expected int64) bool {
	switch current.Execution.Series.State {
	case domain.ArenaSeriesStateDraft, domain.ArenaSeriesStateReady,
		domain.ArenaSeriesStateActive, domain.ArenaSeriesStateReplayRequired:
		return nextRevisionMatches(current.Revision, expected)
	case domain.ArenaSeriesStatePlanned, domain.ArenaSeriesStateLocked,
		domain.ArenaSeriesStateCompleted, domain.ArenaSeriesStateCancelled:
		return current.Revision == expected
	case domain.ArenaSeriesStateTechnicalPause:
		return false
	default:
		return false
	}
}

func resolvedGameRevisionsMatch(current []PauseGame, expected []PauseChildRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	for _, game := range current {
		revision, ok := childRevision(expected, game.Game.ID)
		if !ok || !resolvedGameRevisionMatches(game, revision) {
			return false
		}
	}
	return true
}

func resolvedGameRevisionMatches(current PauseGame, expected int64) bool {
	switch current.Game.State {
	case domain.ArenaGameStateActive:
		return nextRevisionMatches(current.Revision, expected)
	case domain.ArenaGameStatePlanned, domain.ArenaGameStateReady, domain.ArenaGameStateCompleted,
		domain.ArenaGameStateVoid, domain.ArenaGameStateCancelled, domain.ArenaGameStateSuperseded:
		return current.Revision == expected
	case domain.ArenaGameStatePaused:
		return false
	default:
		return false
	}
}

func resolvedDraftRevisionMatches(
	current *DraftExecution,
	expected *DraftRevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	resumedAt time.Time,
) bool {
	if current == nil || expected == nil {
		return absentDraftRevisionMatches(current, expected, resultRevisionID)
	}
	if current.ServiceEpoch != expected.ServiceEpoch {
		return false
	}
	switch current.State {
	case DraftExecutionStateActive:
		return resolvedDraftMutationMatches(current, expected, expectedPreviousRevisionID,
			resultRevisionID, commandID, actorID, resumedAt)
	case DraftExecutionStateRecoveryRequired, DraftExecutionStateCompleted, DraftExecutionStateSuperseded:
		return unchangedDraftRevisionMatches(current, expected, expectedPreviousRevisionID, resultRevisionID)
	case DraftExecutionStatePaused:
		return false
	default:
		return false
	}
}

func resolvedDraftMutationMatches(
	current *DraftExecution,
	expected *DraftRevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	resumedAt time.Time,
) bool {
	lineageMatches := nextRevisionMatches(current.Revision, expected.Revision) &&
		current.PreviousRevisionID == expected.RevisionID && current.RevisionID == resultRevisionID
	identityMatches := current.CommandID == commandID && current.ID != resultRevisionID &&
		resultRevisionID != expectedPreviousRevisionID
	return lineageMatches && identityMatches && draftTransitionMatches(current.Transition,
		DraftTransitionResume, actorID, "pause resumed", resumedAt)
}

func frozenRevisionsAdvanced(current, expected []PauseFrozenDeadlineRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	values := make(map[pauseDeadlineIdentity]int64, len(expected))
	for _, value := range expected {
		values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] = value.Revision
	}
	for _, value := range current {
		expectedRevision, exists := values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}]
		if !exists || expectedRevision == math.MaxInt64 || value.Revision != expectedRevision+1 {
			return false
		}
	}
	return true
}

func reconcilePauseResume(record PauseResumeRecord, command PauseResumeCommand) (*PauseResumeRecord, error) {
	if validatePauseResumeRecord(record) != nil || record.Scope != command.Scope || record.PauseID != command.PauseID ||
		record.CommandID != command.CommandID || record.ActorID != command.ActorID ||
		record.DraftResultRevisionID != command.DraftResultRevisionID ||
		!pauseResumeExpectationEqual(record.Expected, command.Expected) {
		return nil, ErrPauseResumeCommandReuse
	}
	clone := clonePauseResumeRecord(record)
	return &clone, nil
}

func PauseResumeExpectationFrom(authority PauseResumeAuthority) PauseResumeExpectation {
	expected := PauseResumeExpectation{
		PauseID: authority.Pause.PauseID, GraphRevision: authority.Pause.Graph.Revision,
		PauseRevision: authority.Pause.Revision, Authority: authority.Pause.Scope.Authority,
		TournamentRevision: authority.Pause.Graph.Tournament.Revision, WaveRevision: authority.Pause.Graph.Wave.Revision,
		TerminalActionRevision: authority.TerminalActionRevision,
		Series:                 make([]PauseChildRevision, len(authority.Pause.Graph.Series)),
		Games:                  make([]PauseChildRevision, len(authority.Pause.Graph.Games)),
		Presence:               make([]PausePresenceRevision, len(authority.Presence)),
		Reconnect:              make([]PauseChildRevision, len(authority.Reconnect)),
		Counters:               make([]PauseReconnectCounterRevision, len(authority.Counters)),
		FrozenDeadlines:        make([]PauseFrozenDeadlineRevision, len(authority.FrozenDeadlines)),
	}
	for index, series := range authority.Pause.Graph.Series {
		expected.Series[index] = PauseChildRevision{ID: series.Execution.Series.ID, Revision: series.Revision}
	}
	for index, game := range authority.Pause.Graph.Games {
		expected.Games[index] = PauseChildRevision{ID: game.Game.ID, Revision: game.Revision}
	}
	for index, presence := range authority.Presence {
		expected.Presence[index] = PausePresenceRevision{ID: presence.ID, TournamentID: presence.TournamentID, RosterID: presence.RosterID, SeriesID: presence.SeriesID, ParticipantID: presence.ParticipantID, PresenceEpoch: presence.PresenceEpoch, Revision: presence.Revision}
	}
	for index, interval := range authority.Reconnect {
		expected.Reconnect[index] = PauseChildRevision{ID: interval.ID, Revision: interval.Revision}
	}
	for index, counter := range authority.Counters {
		expected.Counters[index] = PauseReconnectCounterRevision{PauseID: counter.PauseID, RosterID: counter.RosterID, ParticipantID: counter.ParticipantID, Revision: counter.Revision}
	}
	for index, frozen := range authority.FrozenDeadlines {
		expected.FrozenDeadlines[index] = PauseFrozenDeadlineRevision{Kind: frozen.Kind, OwnerID: frozen.OwnerID, Revision: frozen.Revision}
	}
	if authority.Pause.Graph.Draft != nil {
		draft := draftExpectation(*authority.Pause.Graph.Draft)
		expected.Draft = &draft
		expected.DraftPreviousRevisionID = authority.Pause.Graph.Draft.PreviousRevisionID
	}
	if authority.Pause.Graph.Tournament.PausedFromState != nil {
		expected.TournamentState = *authority.Pause.Graph.Tournament.PausedFromState
	}
	return expected
}

func validatePauseResumeExpectation(value PauseResumeExpectation) error {
	if !validPauseResumeRootExpectation(value) || !validPauseResumeChildExpectation(value) {
		return pauseResumeError("invalid expected revision")
	}
	if !validPauseDraftRevisionContract(value.Draft, value.DraftPreviousRevisionID) {
		return pauseResumeError("invalid expected Draft")
	}
	if err := validatePausePresenceRevisions(value.Presence); err != nil {
		return pauseResumeError("invalid expected Presence: %v", err)
	}
	return nil
}

func validPauseResumeRootExpectation(value PauseResumeExpectation) bool {
	return value.PauseID != uuid.Nil && value.GraphRevision >= 1 && value.PauseRevision >= 1 &&
		value.Authority.Validate() == nil && normalPauseTournamentState(value.TournamentState) &&
		value.TournamentRevision >= 1 && value.WaveRevision >= 1 &&
		value.TerminalActionRevision >= 0
}

func validPauseResumeChildExpectation(value PauseResumeExpectation) bool {
	return validUniqueChildRevisions(value.Series) && validUniqueChildRevisions(value.Games) &&
		validUniqueChildRevisions(value.Reconnect) && validCounterRevisions(value.Counters) &&
		validFrozenDeadlineRevisions(value.FrozenDeadlines)
}

func pauseResumeExpectationEqual(first, second PauseResumeExpectation) bool {
	return pauseResumeRootExpectationEqual(first, second) && pauseResumeChildExpectationEqual(first, second)
}

func pauseResumeRootExpectationEqual(first, second PauseResumeExpectation) bool {
	return first.PauseID == second.PauseID && first.GraphRevision == second.GraphRevision &&
		first.PauseRevision == second.PauseRevision && first.Authority == second.Authority &&
		first.TournamentState == second.TournamentState && first.TournamentRevision == second.TournamentRevision &&
		first.WaveRevision == second.WaveRevision && first.DraftPreviousRevisionID == second.DraftPreviousRevisionID &&
		first.TerminalActionRevision == second.TerminalActionRevision && reflect.DeepEqual(first.Draft, second.Draft)
}

func pauseResumeChildExpectationEqual(first, second PauseResumeExpectation) bool {
	return revisionMapEqual(first.Series, second.Series) && revisionMapEqual(first.Games, second.Games) &&
		revisionMapEqual(first.Reconnect, second.Reconnect) && presenceRevisionMapEqual(first.Presence, second.Presence) &&
		counterRevisionMapEqual(first.Counters, second.Counters) && frozenRevisionMapEqual(first.FrozenDeadlines, second.FrozenDeadlines)
}

func pauseGameByID(games []PauseGame, id uuid.UUID) *PauseGame {
	for index := range games {
		if games[index].Game.ID == id {
			return &games[index]
		}
	}
	return nil
}

func clonePauseResumeExpectation(value PauseResumeExpectation) PauseResumeExpectation {
	clone := value
	clone.Games = append([]PauseChildRevision(nil), value.Games...)
	clone.Series = append([]PauseChildRevision(nil), value.Series...)
	clone.Presence = append([]PausePresenceRevision(nil), value.Presence...)
	clone.Reconnect = append([]PauseChildRevision(nil), value.Reconnect...)
	clone.Counters = append([]PauseReconnectCounterRevision(nil), value.Counters...)
	clone.FrozenDeadlines = append([]PauseFrozenDeadlineRevision(nil), value.FrozenDeadlines...)
	if value.Draft != nil {
		draft := *value.Draft
		clone.Draft = &draft
	}
	return clone
}

func clonePauseResumeRecord(value PauseResumeRecord) PauseResumeRecord {
	clone := value
	clone.Expected = clonePauseResumeExpectation(value.Expected)
	clone.Graph = clonePauseGraph(value.Graph)
	return clone
}

func pauseResumeError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPauseResume, fmt.Sprintf(format, arguments...))
}
