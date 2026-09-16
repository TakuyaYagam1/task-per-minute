package recovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

var (
	ErrInvalidDeadlineAuthority = errors.New("invalid recovery deadline authority")
	ErrInvalidDeadlinePlan      = errors.New("invalid recovery deadline terminal plan")
	ErrDeadlineNotDue           = errors.New("recovery deadline has not elapsed")
)

var deadlineTerminalNamespace = uuid.MustParse("dfb8cdb7-6344-5dc8-89d3-6fcddc84d613")

// DeadlineTerminalAuthority is one transactionally loaded snapshot. Exactly
// one authority shape must match Deadline.Kind. Ready-window expiry contains
// every non-terminal Series linked to the window's Wave so it cannot partially
// expire a Wave after a process restart.
type DeadlineTerminalAuthority struct {
	Deadline         PendingDeadline
	GameTimeout      *attemptusecase.AttemptAuthority
	ReadyWindow      []gameusecase.NoShowAuthority
	ReconnectTimeout *reconnectusecase.ReconnectAuthority
}

// DeadlineTerminalPlan is a domain-decided mutation. The store only persists
// this plan behind the authority revisions carried by its records.
type DeadlineTerminalPlan struct {
	Deadline            PendingDeadline
	Authority           DeadlineTerminalAuthority
	ReceiptID           uuid.UUID
	CommandID           uuid.UUID
	ResultEvidence      *DeadlineResultEvidenceIDs
	ReadyWindow         []gameusecase.NoShowResolution
	ReadyWindowEvidence []DeadlineNoShowEvidenceIDs
	PauseRevisionID     uuid.UUID
	GameTimeout         *attemptusecase.AttemptRecord
	ReconnectTimeout    *reconnectusecase.ReconnectRecord
}

type DeadlineResultEvidenceIDs struct {
	CommitID                  uuid.UUID
	ResultEventID             uuid.UUID
	ResultEventIdempotencyKey uuid.UUID
	OutboxIdempotencyKey      uuid.UUID
	CommitIdempotencyKey      uuid.UUID
}

type DeadlineNoShowEvidenceIDs struct {
	SeriesID                  uuid.UUID
	CommitID                  uuid.UUID
	ResultEventID             uuid.UUID
	ResultEventIdempotencyKey uuid.UUID
	AuditEventID              uuid.UUID
	OutboxEventID             uuid.UUID
	OutboxIdempotencyKey      uuid.UUID
	ProjectionEvidenceID      uuid.UUID
}

// DeadlineTerminalStore owns the authoritative load, locks, receipt lookup and
// one atomic CAS commit. A false load is a verified stale replay. A false
// commit is allowed only when the exact durable receipt already exists.
type DeadlineTerminalStore interface {
	LoadDeadlineAuthority(
		ctx context.Context,
		deadline PendingDeadline,
	) (DeadlineTerminalAuthority, bool, error)
	CommitDeadlinePlan(ctx context.Context, plan DeadlineTerminalPlan) (bool, error)
}

// DeadlineAuthoritySource supplies the currently claimed tournament execution
// lease. Reconnect recovery must not invent a GraphScope authority after a
// restart; the store revalidates this lease before committing terminal state.
type DeadlineAuthoritySource interface {
	LoadAuthority(ctx context.Context, tournamentID uuid.UUID) (*authoritydomain.Lease, error)
}

func (authority DeadlineTerminalAuthority) Validate() error {
	if !validDeadlineTerminalAuthority(authority) {
		return ErrInvalidDeadlineAuthority
	}
	return nil
}

func (plan DeadlineTerminalPlan) Validate() error {
	if !validDeadlineTerminalPlan(plan) {
		return ErrInvalidDeadlinePlan
	}
	return nil
}

type TerminalDeadlineHandler struct {
	transactions      TransactionManager
	store             DeadlineTerminalStore
	clock             Clock
	terminalAdvancer  TerminalAdvancer
	reconnectObserver reconnectusecase.Observer
}

var _ DeadlineHandler = (*TerminalDeadlineHandler)(nil)

func NewTerminalDeadlineHandler(
	store DeadlineTerminalStore,
	clock Clock,
	reconnectObservers ...reconnectusecase.Observer,
) *TerminalDeadlineHandler {
	return NewTerminalDeadlineHandlerWithDependencies(
		nil, store, clock, nil, reconnectObservers...,
	)
}

// NewTerminalDeadlineHandlerWithDependencies builds the production deadline
// boundary. The store commit and any playoff advancement run inside the same
// transaction supplied here. The legacy constructor above remains useful for
// read-only planning tests and callers that do not provide terminal progress.
func NewTerminalDeadlineHandlerWithDependencies(
	transactions TransactionManager,
	store DeadlineTerminalStore,
	clock Clock,
	terminalAdvancer TerminalAdvancer,
	reconnectObservers ...reconnectusecase.Observer,
) *TerminalDeadlineHandler {
	return &TerminalDeadlineHandler{
		transactions: transactions, store: store, clock: clock,
		terminalAdvancer:  terminalAdvancer,
		reconnectObserver: firstDeadlineReconnectObserver(reconnectObservers...),
	}
}

func (handler *TerminalDeadlineHandler) HandleDeadline(
	ctx context.Context,
	deadline PendingDeadline,
) (bool, error) {
	if ctx == nil || handler == nil || handler.store == nil || handler.clock == nil ||
		deadline.Validate() != nil {
		return false, domain.ErrValidation
	}
	now := handler.clock.Now().Round(0).UTC()
	if !validRecoveryTime(now) {
		return false, domain.ErrValidation
	}
	if now.Before(deadline.DueAt) {
		return false, ErrDeadlineNotDue
	}
	var (
		plan    DeadlineTerminalPlan
		changed bool
	)
	commit := func(txCtx context.Context) error {
		var err error
		plan, changed, err = handler.commitDeadline(txCtx, deadline, now)
		return err
	}
	var err error
	if handler.transactions == nil {
		err = commit(ctx)
	} else {
		err = handler.transactions.Do(ctx, commit)
	}
	if err != nil {
		return false, err
	}
	if changed {
		handler.observeReconnectTimeout(ctx, plan)
	}
	return changed, nil
}

func (handler *TerminalDeadlineHandler) commitDeadline(
	ctx context.Context,
	deadline PendingDeadline,
	now time.Time,
) (DeadlineTerminalPlan, bool, error) {
	if ctx == nil {
		return DeadlineTerminalPlan{}, false, domain.ErrValidation
	}
	authority, pending, err := handler.store.LoadDeadlineAuthority(ctx, deadline)
	if err != nil {
		return DeadlineTerminalPlan{}, false, deadlineError("load terminal authority", err)
	}
	if !pending {
		return DeadlineTerminalPlan{}, false, nil
	}
	if !pendingDeadlinesEqual(authority.Deadline, deadline) {
		return DeadlineTerminalPlan{}, false, ErrInvalidDeadlineAuthority
	}
	plan, err := buildDeadlineTerminalPlan(ctx, authority, now)
	if err != nil {
		return DeadlineTerminalPlan{}, false, err
	}
	changed, err := handler.store.CommitDeadlinePlan(ctx, plan)
	if err != nil {
		return DeadlineTerminalPlan{}, false, deadlineError("commit terminal plan", err)
	}
	if changed {
		if err := handler.advanceTerminalPlan(ctx, plan); err != nil {
			return DeadlineTerminalPlan{}, false, deadlineError("advance terminal plan", err)
		}
	}
	return plan, changed, nil
}

func (handler *TerminalDeadlineHandler) advanceTerminalPlan(
	ctx context.Context,
	plan DeadlineTerminalPlan,
) error {
	if handler == nil || handler.terminalAdvancer == nil {
		return nil
	}
	if plan.ReconnectTimeout != nil && reconnectSettlementAdvancementEligible(plan.ReconnectTimeout) {
		if _, err := handler.terminalAdvancer.AdvanceAfterSeriesSettlement(ctx, playoff.TerminalSeriesCommand{
			TournamentID: plan.Deadline.TournamentID,
			SeriesID:     plan.Deadline.SeriesID,
		}); err != nil {
			return err
		}
	}
	for index := range plan.ReadyWindow {
		resolution := plan.ReadyWindow[index]
		if resolution.Series.Series.State != domain.SeriesStateCompleted {
			continue
		}
		if _, err := handler.terminalAdvancer.AdvanceAfterSeriesSettlement(ctx, playoff.TerminalSeriesCommand{
			TournamentID: resolution.Scope.TournamentID,
			SeriesID:     resolution.Scope.SeriesID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func reconnectSettlementAdvancementEligible(record *reconnectusecase.ReconnectRecord) bool {
	if record == nil || record.ReplayRoute != nil || record.VoidGameResultRevision != nil ||
		record.GameResultRevision == nil || record.ScoreRevision == nil || record.Evidence == nil {
		return false
	}
	state := record.ReconnectAuthority.Series.State
	return state == domain.SeriesStateActive || state == domain.SeriesStateCompleted
}

func buildDeadlineTerminalPlan(
	ctx context.Context,
	authority DeadlineTerminalAuthority,
	now time.Time,
) (DeadlineTerminalPlan, error) {
	if ctx == nil || !validDeadlineTerminalAuthority(authority) || !validRecoveryTime(now) {
		return DeadlineTerminalPlan{}, ErrInvalidDeadlineAuthority
	}
	plan := DeadlineTerminalPlan{
		Deadline:  authority.Deadline,
		Authority: authority,
		ReceiptID: deadlineTerminalID(authority.Deadline, uuid.Nil, "receipt"),
		CommandID: deadlineTerminalID(authority.Deadline, uuid.Nil, "command"),
	}
	var err error
	switch authority.Deadline.Kind {
	case DeadlineKindGame:
		plan.GameTimeout, err = planGameTimeout(ctx, *authority.GameTimeout, authority.Deadline, now)
		plan.ResultEvidence = deadlineResultEvidence(authority.Deadline)
	case DeadlineKindReadyWindow:
		plan.ReadyWindow, err = planReadyWindowExpiry(ctx, authority.ReadyWindow, authority.Deadline, now)
		plan.ReadyWindowEvidence = deadlineNoShowEvidence(authority.Deadline, plan.ReadyWindow)
	case DeadlineKindReconnect:
		plan.ReconnectTimeout, err = planReconnectTimeout(
			ctx,
			*authority.ReconnectTimeout,
			authority.Deadline,
			now,
		)
		if err == nil && plan.ReconnectTimeout.Evidence != nil {
			plan.ResultEvidence = deadlineResultEvidence(authority.Deadline)
			plan.PauseRevisionID = deadlineTerminalID(authority.Deadline, authority.Deadline.PauseID, "pause-revision")
		}
	default:
		err = ErrInvalidDeadlineAuthority
	}
	if err != nil {
		return DeadlineTerminalPlan{}, err
	}
	if !validDeadlineTerminalPlan(plan) {
		return DeadlineTerminalPlan{}, ErrInvalidDeadlinePlan
	}
	return plan, nil
}

func planGameTimeout(
	ctx context.Context,
	authority attemptusecase.AttemptAuthority,
	deadline PendingDeadline,
	now time.Time,
) (*attemptusecase.AttemptRecord, error) {
	game, category, found := failedAttemptExpectation(authority)
	if !found {
		return nil, ErrInvalidDeadlineAuthority
	}
	planner := &gameTimeoutPlanner{authority: authority}
	commandID := deadlineTerminalID(deadline, uuid.Nil, "command")
	command := attemptusecase.AttemptCommand{
		Scope: authority.Scope, CommandID: commandID, FailureClass: gamedomain.FailureNoSolve,
		Expected: attemptusecase.Expectation{
			AttemptNo: game.AttemptNo, State: game.State,
			SnapshotID: authority.ActiveSnapshotID, Category: category,
		},
		Revisions: attemptusecase.AttemptRevisionSet{
			GameResultRevisionID: domain.OfficialResultRevisionID(deadlineTerminalID(deadline, deadline.GameID, "game-result")),
			ScoreRevisionID:      domain.SeriesScoreRevisionID(deadlineTerminalID(deadline, deadline.SeriesID, "score")),
			RouteEvidenceID:      deadlineTerminalID(deadline, deadline.GameID, "route"),
			AuditEventID:         deadlineTerminalID(deadline, deadline.GameID, "audit"),
			OutboxEventID:        deadlineTerminalID(deadline, deadline.GameID, "outbox"),
			ProjectionRevisionID: deadlineTerminalID(deadline, deadline.GameID, "projection"),
		},
	}
	record, changed, err := attemptusecase.AttemptNewUseCase(planner, fixedRecoveryClock{at: now}).Terminalize(
		ctx,
		command,
	)
	if err != nil {
		return nil, fmt.Errorf("recovery game timeout plan: %w", err)
	}
	if !changed || record == nil {
		return nil, ErrInvalidDeadlinePlan
	}
	return record, nil
}

func planReadyWindowExpiry(
	ctx context.Context,
	authorities []gameusecase.NoShowAuthority,
	deadline PendingDeadline,
	now time.Time,
) ([]gameusecase.NoShowResolution, error) {
	ordered := append([]gameusecase.NoShowAuthority(nil), authorities...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Scope.SeriesID.String() < ordered[right].Scope.SeriesID.String()
	})
	resolutions := make([]gameusecase.NoShowResolution, 0, len(ordered))
	var action domain.NormalNoShowAction
	for index := range ordered {
		authority := ordered[index]
		planner := &readyWindowPlanner{authority: authority}
		seriesID := authority.Scope.SeriesID
		command := gameusecase.NoShowCommand{
			Scope:                    authority.Scope,
			CommandID:                deadlineTerminalID(deadline, seriesID, "command"),
			ExpectedWaveRevisionID:   authority.Wave.RevisionID,
			ExpectedWindowRevisionID: authority.Wave.ReadyWindow.RevisionID,
			ExpectedSeriesState:      authority.Series.Series.State,
			GameResultRevisionIDs:    readyWindowGameRevisionIDs(authority, deadline),
			ScoreRevisionID:          domain.SeriesScoreRevisionID(deadlineTerminalID(deadline, seriesID, "score")),
			SeriesResultRevisionID:   domain.OfficialResultRevisionID(deadlineTerminalID(deadline, seriesID, "series-result")),
		}
		resolution, changed, err := gameusecase.NoShowNewUseCase(planner, fixedRecoveryClock{at: now}).Resolve(
			ctx,
			command,
		)
		if err != nil {
			return nil, fmt.Errorf("recovery ready-window plan for Series %s: %w", seriesID, err)
		}
		if !changed || resolution == nil {
			return nil, ErrInvalidDeadlinePlan
		}
		if action != "" && action != resolution.Action {
			return nil, fmt.Errorf("%w: inconsistent Wave no-show actions", ErrInvalidDeadlinePlan)
		}
		action = resolution.Action
		resolutions = append(resolutions, *resolution)
	}
	return resolutions, nil
}

func planReconnectTimeout(
	ctx context.Context,
	authority reconnectusecase.ReconnectAuthority,
	deadline PendingDeadline,
	now time.Time,
) (*reconnectusecase.ReconnectRecord, error) {
	planner := &reconnectTimeoutPlanner{authority: authority}
	command := reconnectusecase.TimeoutCommand{
		Scope: authority.Scope, CommandID: deadlineTerminalID(deadline, uuid.Nil, "command"),
		ParticipantID: deadline.ParticipantID, IntervalID: deadline.ID,
		Settlement: reconnectusecase.SettlementIDs{
			GameResultRevisionID:   domain.OfficialResultRevisionID(deadlineTerminalID(deadline, deadline.GameID, "game-result")),
			ScoreRevisionID:        domain.SeriesScoreRevisionID(deadlineTerminalID(deadline, deadline.SeriesID, "score")),
			SeriesResultRevisionID: domain.OfficialResultRevisionID(deadlineTerminalID(deadline, deadline.SeriesID, "series-result")),
			ReplayRouteID:          deadlineTerminalID(deadline, deadline.GameID, "route"),
			AuditEventID:           deadlineTerminalID(deadline, deadline.GameID, "audit"),
			OutboxEventID:          deadlineTerminalID(deadline, deadline.GameID, "outbox"),
			ProjectionRevisionID:   deadlineTerminalID(deadline, deadline.GameID, "projection"),
		},
	}
	record, changed, err := reconnectusecase.NewTimeoutUseCase(
		planner,
		fixedRecoveryClock{at: now},
	).Expire(
		ctx,
		command,
	)
	if err != nil {
		return nil, fmt.Errorf("recovery reconnect timeout plan: %w", err)
	}
	if !changed || record == nil {
		return nil, ErrInvalidDeadlinePlan
	}
	return record, nil
}

func (handler *TerminalDeadlineHandler) observeReconnectTimeout(ctx context.Context, plan DeadlineTerminalPlan) {
	if handler == nil || plan.ReconnectTimeout == nil || plan.ReconnectTimeout.TimeoutCommand == nil {
		return
	}
	reconnectusecase.ObserveEvent(
		ctx,
		handler.reconnectObserver,
		reconnectusecase.TimeoutTerminalEvent(
			*plan.ReconnectTimeout.TimeoutCommand,
			plan.ReconnectTimeout,
			true,
			nil,
		),
	)
}

func firstDeadlineReconnectObserver(observers ...reconnectusecase.Observer) reconnectusecase.Observer {
	for _, observer := range observers {
		if observer != nil {
			return observer
		}
	}
	return nil
}

func validDeadlineTerminalAuthority(authority DeadlineTerminalAuthority) bool {
	if authority.Deadline.Validate() != nil {
		return false
	}
	switch authority.Deadline.Kind {
	case DeadlineKindGame:
		return authority.GameTimeout != nil && len(authority.ReadyWindow) == 0 &&
			authority.ReconnectTimeout == nil && gameAuthorityMatchesDeadline(*authority.GameTimeout, authority.Deadline)
	case DeadlineKindReadyWindow:
		return authority.GameTimeout == nil && len(authority.ReadyWindow) > 0 &&
			authority.ReconnectTimeout == nil && readyAuthoritiesMatchDeadline(authority.ReadyWindow, authority.Deadline)
	case DeadlineKindReconnect:
		return authority.GameTimeout == nil && len(authority.ReadyWindow) == 0 &&
			authority.ReconnectTimeout != nil && reconnectAuthorityMatchesDeadline(*authority.ReconnectTimeout, authority.Deadline)
	default:
		return false
	}
}

func gameAuthorityMatchesDeadline(authority attemptusecase.AttemptAuthority, deadline PendingDeadline) bool {
	return authority.Revision == deadline.ExpectedRevision &&
		authority.Scope.TournamentID == deadline.TournamentID && authority.Scope.WaveID == deadline.WaveID &&
		authority.Scope.SeriesID == deadline.SeriesID && authority.Scope.SlotID == deadline.SlotID &&
		authority.Scope.GameID == deadline.GameID
}

func readyAuthoritiesMatchDeadline(authorities []gameusecase.NoShowAuthority, deadline PendingDeadline) bool {
	seen := make(map[uuid.UUID]struct{}, len(authorities))
	for index := range authorities {
		authority := authorities[index]
		if authority.Revision < 1 || authority.Scope.TournamentID != deadline.TournamentID ||
			authority.Scope.WaveID != deadline.WaveID || authority.Scope.WindowID != deadline.ID ||
			authority.Wave.ReadyWindow == nil ||
			authority.Wave.ReadyWindow.RevisionID.UUID() != deadline.ReadyWindowRevisionID {
			return false
		}
		if _, exists := seen[authority.Scope.SeriesID]; exists {
			return false
		}
		seen[authority.Scope.SeriesID] = struct{}{}
	}
	return true
}

func reconnectAuthorityMatchesDeadline(authority reconnectusecase.ReconnectAuthority, deadline PendingDeadline) bool {
	if authority.Scope.TournamentID != deadline.TournamentID ||
		authority.Scope.RosterID != deadline.RosterID || authority.Scope.WaveID != deadline.WaveID ||
		authority.PauseID != deadline.PauseID || authority.Game.ID != deadline.GameID ||
		authority.Series.ID != deadline.SeriesID {
		return false
	}
	for index := range authority.Reconnect {
		interval := authority.Reconnect[index]
		if interval.ID == deadline.ID {
			return interval.Revision == deadline.ExpectedRevision && interval.ParticipantID == deadline.ParticipantID &&
				interval.State == pause.ReconnectStateOpen && interval.Deadline.Equal(deadline.DueAt)
		}
	}
	return false
}

func validDeadlineTerminalPlan(plan DeadlineTerminalPlan) bool {
	if plan.Deadline.Validate() != nil || plan.ReceiptID == uuid.Nil || plan.CommandID == uuid.Nil ||
		!pendingDeadlinesEqual(plan.Authority.Deadline, plan.Deadline) {
		return false
	}
	switch plan.Deadline.Kind {
	case DeadlineKindGame:
		return validGameDeadlinePlan(plan)
	case DeadlineKindReadyWindow:
		return validReadyWindowDeadlinePlan(plan)
	case DeadlineKindReconnect:
		return validReconnectDeadlinePlan(plan)
	default:
		return false
	}
}

func validGameDeadlinePlan(plan DeadlineTerminalPlan) bool {
	return plan.GameTimeout != nil && plan.GameTimeout.Validate() == nil &&
		validDeadlineResultEvidence(plan.ResultEvidence) && len(plan.ReadyWindow) == 0 &&
		len(plan.ReadyWindowEvidence) == 0 && plan.ReconnectTimeout == nil && plan.PauseRevisionID == uuid.Nil
}

func validReadyWindowDeadlinePlan(plan DeadlineTerminalPlan) bool {
	if plan.GameTimeout != nil || len(plan.ReadyWindow) == 0 || plan.ReconnectTimeout != nil ||
		plan.ResultEvidence != nil || plan.PauseRevisionID != uuid.Nil ||
		len(plan.ReadyWindowEvidence) != len(plan.ReadyWindow) {
		return false
	}
	for index := range plan.ReadyWindow {
		if plan.ReadyWindow[index].Validate() != nil ||
			!validDeadlineNoShowEvidence(plan.ReadyWindowEvidence[index], plan.ReadyWindow[index].Scope.SeriesID) {
			return false
		}
	}
	return true
}

func validReconnectDeadlinePlan(plan DeadlineTerminalPlan) bool {
	if plan.GameTimeout != nil || len(plan.ReadyWindow) != 0 || len(plan.ReadyWindowEvidence) != 0 ||
		plan.ReconnectTimeout == nil {
		return false
	}
	if plan.ReconnectTimeout.Evidence == nil {
		return plan.ResultEvidence == nil && plan.PauseRevisionID == uuid.Nil
	}
	return validDeadlineResultEvidence(plan.ResultEvidence) && plan.PauseRevisionID != uuid.Nil
}

func deadlineResultEvidence(deadline PendingDeadline) *DeadlineResultEvidenceIDs {
	return &DeadlineResultEvidenceIDs{
		CommitID:                  deadlineTerminalID(deadline, deadline.GameID, "result-commit"),
		ResultEventID:             deadlineTerminalID(deadline, deadline.GameID, "result-event"),
		ResultEventIdempotencyKey: deadlineTerminalID(deadline, deadline.GameID, "result-event-key"),
		OutboxIdempotencyKey:      deadlineTerminalID(deadline, deadline.GameID, "outbox-key"),
		CommitIdempotencyKey:      deadlineTerminalID(deadline, deadline.GameID, "result-commit-key"),
	}
}

func deadlineNoShowEvidence(
	deadline PendingDeadline,
	resolutions []gameusecase.NoShowResolution,
) []DeadlineNoShowEvidenceIDs {
	result := make([]DeadlineNoShowEvidenceIDs, len(resolutions))
	for index := range resolutions {
		seriesID := resolutions[index].Scope.SeriesID
		result[index] = DeadlineNoShowEvidenceIDs{
			SeriesID:      seriesID,
			CommitID:      deadlineTerminalID(deadline, seriesID, "no-show-commit"),
			ResultEventID: deadlineTerminalID(deadline, seriesID, "result-event"),
			ResultEventIdempotencyKey: deadlineTerminalID(
				deadline,
				seriesID,
				"result-event-key",
			),
			AuditEventID:         deadlineTerminalID(deadline, seriesID, "audit"),
			OutboxEventID:        deadlineTerminalID(deadline, seriesID, "outbox"),
			OutboxIdempotencyKey: deadlineTerminalID(deadline, seriesID, "outbox-key"),
			ProjectionEvidenceID: deadlineTerminalID(deadline, seriesID, "projection"),
		}
	}
	return result
}

func validDeadlineResultEvidence(ids *DeadlineResultEvidenceIDs) bool {
	if ids == nil {
		return false
	}
	return uniqueDeadlineIDs([]uuid.UUID{
		ids.CommitID,
		ids.ResultEventID,
		ids.ResultEventIdempotencyKey,
		ids.OutboxIdempotencyKey,
		ids.CommitIdempotencyKey,
	})
}

func validDeadlineNoShowEvidence(ids DeadlineNoShowEvidenceIDs, seriesID uuid.UUID) bool {
	return ids.SeriesID == seriesID && uniqueDeadlineIDs([]uuid.UUID{
		ids.SeriesID,
		ids.CommitID,
		ids.ResultEventID,
		ids.ResultEventIdempotencyKey,
		ids.AuditEventID,
		ids.OutboxEventID,
		ids.OutboxIdempotencyKey,
		ids.ProjectionEvidenceID,
	})
}

func uniqueDeadlineIDs(ids []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func failedAttemptExpectation(authority attemptusecase.AttemptAuthority) (domain.Game, domain.Category, bool) {
	for slotIndex := range authority.Series.Series.Slots {
		slot := authority.Series.Series.Slots[slotIndex]
		if slot.ID != authority.Scope.SlotID || len(slot.Attempts) == 0 {
			continue
		}
		game := slot.Attempts[len(slot.Attempts)-1]
		if game.ID == authority.Scope.GameID {
			return game, slot.Category, true
		}
	}
	return domain.Game{}, "", false
}

func readyWindowGameRevisionIDs(
	authority gameusecase.NoShowAuthority,
	deadline PendingDeadline,
) []domain.OfficialResultRevisionID {
	result := make([]domain.OfficialResultRevisionID, 0, len(authority.Series.Series.Slots))
	for slotIndex := range authority.Series.Series.Slots {
		slot := authority.Series.Series.Slots[slotIndex]
		if len(slot.Attempts) == 0 {
			continue
		}
		game := slot.Attempts[len(slot.Attempts)-1]
		if game.State == domain.GameStatePlanned || game.State == domain.GameStateReady {
			result = append(result, domain.OfficialResultRevisionID(
				deadlineTerminalID(deadline, game.ID, "game-result"),
			))
		}
	}
	return result
}

func deadlineTerminalID(deadline PendingDeadline, entityID uuid.UUID, role string) uuid.UUID {
	name := fmt.Sprintf("%s:%s:%d:%s:%s", deadline.Kind, deadline.ID, deadline.ExpectedRevision, entityID, role)
	return uuid.NewSHA1(deadlineTerminalNamespace, []byte(name))
}

type fixedRecoveryClock struct{ at time.Time }

func (clock fixedRecoveryClock) Now() time.Time { return clock.at }

type gameTimeoutPlanner struct {
	authority attemptusecase.AttemptAuthority
	record    *attemptusecase.AttemptRecord
}

func (planner *gameTimeoutPlanner) LoadFailedAttemptAuthority(
	context.Context,
	domain.FailedAttemptScope,
) (attemptusecase.AttemptAuthority, error) {
	return planner.authority, nil
}

func (planner *gameTimeoutPlanner) CommitFailedAttempt(
	_ context.Context,
	record attemptusecase.AttemptRecord,
) (*attemptusecase.AttemptRecord, bool, error) {
	planner.record = &record
	return &record, true, nil
}

type readyWindowPlanner struct {
	authority gameusecase.NoShowAuthority
	record    *gameusecase.NoShowResolution
}

func (planner *readyWindowPlanner) LoadNormalNoShowAuthority(
	context.Context,
	domain.NormalNoShowScope,
) (gameusecase.NoShowAuthority, error) {
	return planner.authority, nil
}

func (planner *readyWindowPlanner) CommitNormalNoShow(
	_ context.Context,
	record gameusecase.NoShowResolution,
) (*gameusecase.NoShowResolution, bool, error) {
	planner.record = &record
	return &record, true, nil
}

type reconnectTimeoutPlanner struct {
	authority reconnectusecase.ReconnectAuthority
	record    *reconnectusecase.ReconnectRecord
}

func (planner *reconnectTimeoutPlanner) FindCommand(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*reconnectusecase.ReconnectRecord, error) {
	return nil, nil
}

func (planner *reconnectTimeoutPlanner) LoadAuthority(
	_ context.Context,
	_ pause.GraphScope,
	participantID uuid.UUID,
) (reconnectusecase.ReconnectAuthority, error) {
	if participantID == uuid.Nil {
		return reconnectusecase.ReconnectAuthority{}, domain.ErrValidation
	}
	for _, interval := range planner.authority.Reconnect {
		if interval.ParticipantID == participantID {
			return planner.authority, nil
		}
	}
	return reconnectusecase.ReconnectAuthority{}, domain.ErrConflict
}

func (planner *reconnectTimeoutPlanner) CommitMutation(
	_ context.Context,
	_ int64,
	record reconnectusecase.ReconnectRecord,
) (*reconnectusecase.ReconnectRecord, bool, error) {
	planner.record = &record
	return &record, true, nil
}
