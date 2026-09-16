package game

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/google/uuid"
)

type ExecutionClock interface {
	Now() time.Time
}

// AuthorityTimeSource supplies the database time used to prove durable
// execution authority. It is deliberately context-bound: an unavailable
// authoritative clock must prevent recovery mutations rather than falling
// back to the process wall clock.
type AuthorityTimeSource interface {
	AuthorityTime(ctx context.Context) (time.Time, error)
}

// EpochReplayRepository owns the atomic revalidation boundary. CommitEpochReplay
// must prove that the expected lease is still live, then persist the failed
// attempt records and the old-wave route in one transaction.
type EpochReplayRepository interface {
	FindEpochReplay(
		ctx context.Context,
		scope domain.FailedAttemptScope,
	) (*EpochReplayRecord, error)
	LoadEpochReplayAuthority(
		ctx context.Context,
		scope domain.FailedAttemptScope,
		rosterID uuid.UUID,
	) (EpochReplayAuthority, error)
	CommitEpochReplay(
		ctx context.Context,
		condition EpochReplayCommitCondition,
		record EpochReplayRecord,
	) (*EpochReplayRecord, bool, error)
}

type RecoveryCommand struct {
	TournamentID uuid.UUID
	Authority    authoritydomain.Identity
}

type RecoveryCandidate struct {
	Scope          domain.FailedAttemptScope
	RosterID       uuid.UUID
	AttemptNo      int
	State          domain.GameState
	SnapshotID     uuid.UUID
	Category       domain.Category
	BoundAuthority authoritydomain.Stamp
	Deadline       time.Time
	EpochReplay    *EpochReplayCommand
}

type DeadlineArm struct {
	Scope     domain.FailedAttemptScope
	RosterID  uuid.UUID
	AttemptNo int
	Authority authoritydomain.Stamp
	Deadline  time.Time
}

type RecoveryReport struct {
	Rearmed          int
	TechnicalReplays int
	Paused           int
	Changed          int
}

type RecoveryAuthorityReader interface {
	LoadAuthority(ctx context.Context, tournamentID uuid.UUID) (*authoritydomain.Lease, error)
}

type RecoverySource interface {
	ListActiveGames(
		ctx context.Context,
		tournamentID uuid.UUID,
		authority authoritydomain.Identity,
	) ([]RecoveryCandidate, error)
}

// DeadlineRearmer owns the deadline-arm linearization boundary. RearmDeadline
// must fence by tournament scope and authority stamp, prove the durable lease
// is live using authoritative time, and arm the persisted deadline atomically.
type DeadlineRearmer interface {
	RearmDeadline(ctx context.Context, arm DeadlineArm) error
}

type RecoveryEpochReplayer interface {
	ReplayEpoch(ctx context.Context, command EpochReplayCommand) (bool, error)
}

// RecoveryTournamentSource lists durable tournament work. It must not infer
// ownership from process-local state.
type RecoveryTournamentSource interface {
	ListRecoveryTournaments(ctx context.Context) ([]uuid.UUID, error)
}

// RecoveryAuthorityProvider distinguishes a live foreign lease from a local
// infrastructure failure. Foreign work is skipped without takeover.
type RecoveryAuthorityProvider interface {
	RecoveryAuthorityFor(
		ctx context.Context,
		tournamentID uuid.UUID,
	) (authority authoritydomain.Identity, owned bool, err error)
}
type ForfeitClock interface {
	Now() time.Time
}

type ForfeitRepository interface {
	LoadForfeitAuthority(ctx context.Context, scope Scope) (ForfeitAuthority, error)
	CommitForfeitResolution(
		ctx context.Context,
		resolution ForfeitResolution,
	) (*ForfeitResolution, bool, error)
}
type NoShowClock interface {
	Now() time.Time
}

type NoShowRepository interface {
	LoadNormalNoShowAuthority(
		ctx context.Context,
		scope domain.NormalNoShowScope,
	) (NoShowAuthority, error)
	CommitNormalNoShow(
		ctx context.Context,
		resolution NoShowResolution,
	) (*NoShowResolution, bool, error)
}
type PauseClock interface {
	Now() time.Time
}

// NormalPauseRepository participates in the caller transaction. Load locks the
// complete authority. The stable lock order is execution-authority, pause and
// graph, Tournament, Wave, Series, Games, Draft, Presence, Reconnect, counters,
// frozen deadlines and terminal actions, with each collection ordered by its
// durable identity. Commit revalidates every expectation and publishes the
// complete graph and command result atomically or makes no write.
type NormalPauseRepository interface {
	FindNormalPauseCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*NormalPauseRecord, error)
	LoadNormalPauseAuthority(ctx context.Context, scope pausedomain.GraphScope) (NormalPauseAuthority, error)
	CommitNormalPause(ctx context.Context, expected PauseGraphRevisions, record NormalPauseRecord) (*NormalPauseRecord, bool, error)
}

func PauseGraphRevisionsFrom(graph PauseGraph) PauseGraphRevisions {
	revisions := PauseGraphRevisions{
		GraphRevision: graph.Revision, TournamentState: graph.Tournament.State, TournamentRevision: graph.Tournament.Revision,
		WaveRevision: graph.Wave.Revision, TerminalActionRevision: graph.TerminalActionRevision,
		Series: make([]PauseChildRevision, len(graph.Series)), Games: make([]PauseChildRevision, len(graph.Games)),
		Presence: make([]PausePresenceRevision, len(graph.Presence)), Reconnect: make([]PauseChildRevision, len(graph.Reconnect)),
		Counters: make([]PauseReconnectCounterRevision, len(graph.Counters)), FrozenDeadlines: make([]PauseFrozenDeadlineRevision, len(graph.FrozenDeadlines)),
	}
	for index := range graph.Series {
		revisions.Series[index] = PauseChildRevision{ID: graph.Series[index].Execution.Series.ID, Revision: graph.Series[index].Revision}
	}
	for index := range graph.Games {
		revisions.Games[index] = PauseChildRevision{ID: graph.Games[index].Game.ID, Revision: graph.Games[index].Revision}
	}
	if graph.Draft != nil {
		expected := draftExpectation(*graph.Draft)
		revisions.Draft = &expected
		revisions.DraftPreviousRevisionID = graph.Draft.PreviousRevisionID
	}
	for index := range graph.Presence {
		presence := graph.Presence[index]
		revisions.Presence[index] = PausePresenceRevision{ID: presence.ID, TournamentID: presence.TournamentID, RosterID: presence.RosterID, SeriesID: presence.SeriesID, ParticipantID: presence.ParticipantID, PresenceEpoch: presence.PresenceEpoch, Revision: presence.Revision}
	}
	for index := range graph.Reconnect {
		revisions.Reconnect[index] = PauseChildRevision{ID: graph.Reconnect[index].ID, Revision: graph.Reconnect[index].Revision}
	}
	for index, counter := range graph.Counters {
		revisions.Counters[index] = PauseReconnectCounterRevision{PauseID: counter.PauseID, RosterID: counter.RosterID, ParticipantID: counter.ParticipantID, Revision: counter.Revision}
	}
	for index, frozen := range graph.FrozenDeadlines {
		revisions.FrozenDeadlines[index] = PauseFrozenDeadlineRevision{Kind: frozen.Kind, OwnerID: frozen.OwnerID, Revision: frozen.Revision}
	}
	return revisions
}

func (kind PauseDeadlineKind) isValid() bool {
	return kind == PauseDeadlineReadyWindow || kind == PauseDeadlineGame || kind == PauseDeadlineDraft
}

func validatePauseGraphRevisions(value PauseGraphRevisions) error {
	if !validPauseRootRevisions(value) {
		return normalPauseError("invalid root revisions")
	}
	if !validUniqueChildRevisions(value.Series) || !validUniqueChildRevisions(value.Games) || !validUniqueChildRevisions(value.Reconnect) {
		return normalPauseError("invalid or duplicate child revision")
	}
	if err := validatePausePresenceRevisions(value.Presence); err != nil {
		return err
	}
	if !validPauseDraftRevisionContract(value.Draft, value.DraftPreviousRevisionID) {
		return normalPauseError("invalid Draft revision")
	}
	if !validCounterRevisions(value.Counters) || !validFrozenDeadlineRevisions(value.FrozenDeadlines) || value.TerminalActionRevision < 0 {
		return normalPauseError("invalid aggregate revision")
	}
	return nil
}

func validPauseRootRevisions(value PauseGraphRevisions) bool {
	return value.GraphRevision >= 1 && normalPauseTournamentState(value.TournamentState) &&
		value.TournamentRevision >= 1 && value.WaveRevision >= 1 && len(value.Series) > 0
}

func normalPauseTournamentState(value domain.TournamentState) bool {
	return value == domain.TournamentStateSwiss || value == domain.TournamentStatePlayoffs
}

func validatePausePresenceRevisions(values []PausePresenceRevision) error {
	seenPresence := make(map[uuid.UUID]struct{}, len(values))
	for _, presence := range values {
		if presence.ID == uuid.Nil || presence.TournamentID == uuid.Nil || presence.RosterID == uuid.Nil ||
			presence.SeriesID == uuid.Nil || presence.ParticipantID == uuid.Nil || presence.PresenceEpoch < 1 || presence.Revision < 1 {
			return normalPauseError("invalid Presence revision")
		}
		if _, duplicate := seenPresence[presence.ParticipantID]; duplicate {
			return normalPauseError("duplicate Presence revision")
		}
		seenPresence[presence.ParticipantID] = struct{}{}
	}
	return nil
}

func validPauseDraftRevision(value draftusecase.RevisionExpectation) bool {
	return value.RevisionID != uuid.Nil && value.Revision >= 1 && value.ServiceEpoch != uuid.Nil
}

func validPauseDraftRevisionContract(expected *draftusecase.RevisionExpectation, previousRevisionID uuid.UUID) bool {
	if expected == nil {
		return previousRevisionID == uuid.Nil
	}
	return validPauseDraftRevision(*expected) && validDraftPreviousRevision(*expected, previousRevisionID)
}

func validDraftPreviousRevision(expected draftusecase.RevisionExpectation, previousRevisionID uuid.UUID) bool {
	if expected.Revision == 1 {
		return previousRevisionID == uuid.Nil
	}
	return previousRevisionID != uuid.Nil && previousRevisionID != expected.RevisionID &&
		previousRevisionID != expected.ServiceEpoch
}

func validUniqueChildRevisions(values []PauseChildRevision) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value.ID == uuid.Nil || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return false
		}
		seen[value.ID] = struct{}{}
	}
	return true
}

func validCounterRevisions(values []PauseReconnectCounterRevision) bool {
	seen := make(map[[3]uuid.UUID]struct{}, len(values))
	for _, value := range values {
		key := [3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}
		if value.PauseID == uuid.Nil || value.RosterID == uuid.Nil || value.ParticipantID == uuid.Nil || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validFrozenDeadlineRevisions(values []PauseFrozenDeadlineRevision) bool {
	seen := make(map[pauseDeadlineIdentity]struct{}, len(values))
	for _, value := range values {
		key := pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}
		if !value.Kind.isValid() || value.OwnerID == uuid.Nil || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func pauseGraphRevisionsEqual(first, second PauseGraphRevisions) bool {
	return revisionMapEqual(first.Series, second.Series) && revisionMapEqual(first.Games, second.Games) &&
		revisionMapEqual(first.Reconnect, second.Reconnect) && presenceRevisionMapEqual(first.Presence, second.Presence) &&
		counterRevisionMapEqual(first.Counters, second.Counters) && frozenRevisionMapEqual(first.FrozenDeadlines, second.FrozenDeadlines) &&
		first.GraphRevision == second.GraphRevision && first.TournamentRevision == second.TournamentRevision &&
		first.TournamentState == second.TournamentState && first.WaveRevision == second.WaveRevision &&
		first.DraftPreviousRevisionID == second.DraftPreviousRevisionID &&
		first.TerminalActionRevision == second.TerminalActionRevision && reflect.DeepEqual(first.Draft, second.Draft)
}

func counterRevisionMapEqual(first, second []PauseReconnectCounterRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[[3]uuid.UUID]int64, len(first))
	for _, value := range first {
		values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}] = value.Revision
	}
	for _, value := range second {
		if values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}] != value.Revision {
			return false
		}
	}
	return true
}

func frozenRevisionMapEqual(first, second []PauseFrozenDeadlineRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[pauseDeadlineIdentity]int64, len(first))
	for _, value := range first {
		values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] = value.Revision
	}
	for _, value := range second {
		if values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] != value.Revision {
			return false
		}
	}
	return true
}

func revisionMapEqual(first, second []PauseChildRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]int64, len(first))
	for _, value := range first {
		values[value.ID] = value.Revision
	}
	for _, value := range second {
		if values[value.ID] != value.Revision {
			return false
		}
	}
	return true
}

func presenceRevisionMapEqual(first, second []PausePresenceRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]PausePresenceRevision, len(first))
	for _, value := range first {
		values[value.ParticipantID] = value
	}
	for _, value := range second {
		if values[value.ParticipantID] != value {
			return false
		}
	}
	return true
}

// PausedPresenceRepository participates in the caller transaction. Load locks
// in this stable order: execution-authority, pause and graph, Tournament, Wave,
// Series, Games, Draft, Presence, Reconnect, counters, frozen deadlines and
// terminal actions, with collection rows ordered by durable identity. Commit
// revalidates the complete expectation, retains the command result atomically
// and changes only the selected Presence row.
type PausedPresenceRepository interface {
	FindPausedPresenceCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PausedPresenceRecord, error)
	LoadPausedPresenceAuthority(ctx context.Context, scope pausedomain.GraphScope, participantID uuid.UUID) (PausedPresenceAuthority, error)
	CommitPausedPresence(ctx context.Context, expected PausedPresenceExpectation, record PausedPresenceRecord) (*PausedPresenceRecord, bool, error)
}

// PauseResumePresenceRepository commits the normal Wave pause, old Series/Game
// pause heads, Game clock, decision rows and reconnect rows in one transaction.
type PauseResumePresenceRepository interface {
	FindPauseResumePresenceCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PauseResumePresenceRecord, error)
	LoadPauseResumePresenceAuthority(ctx context.Context, scope pausedomain.GraphScope, normalPauseID, seriesPauseID, gamePauseID uuid.UUID) (PauseResumePresenceAuthority, error)
	CommitPauseResumePresence(ctx context.Context, expected PauseResumePresenceExpectation, record PauseResumePresenceRecord) (*PauseResumePresenceRecord, bool, error)
}

// PauseResumeRepository participates in the caller transaction. Load locks in
// this stable order: execution-authority, pause and graph, Tournament, Wave,
// Series, Games, Draft, Presence, Reconnect, counters, frozen deadlines and
// terminal actions, with collection rows ordered by durable identity. Commit
// revalidates the complete expectation and atomically stores the restored graph
// and command result. A conflict makes no write.
type PauseResumeRepository interface {
	FindPauseResumeCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PauseResumeRecord, error)
	LoadPauseResumeAuthority(ctx context.Context, scope pausedomain.GraphScope, pauseID uuid.UUID) (PauseResumeAuthority, error)
	CommitPauseResume(ctx context.Context, expected PauseResumeExpectation, record PauseResumeRecord) (*PauseResumeRecord, bool, error)
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}
type AttemptClock interface {
	Now() time.Time
}

type AttemptRepository interface {
	LoadFailedAttemptAuthority(
		ctx context.Context,
		scope domain.FailedAttemptScope,
	) (AttemptAuthority, error)
	CommitFailedAttempt(
		ctx context.Context,
		record AttemptRecord,
	) (*AttemptRecord, bool, error)
}
type ReconnectClock interface {
	Now() time.Time
}

type Observer interface {
	Observe(ctx context.Context, event ReconnectEvent)
}

// ReconnectRepository owns command receipts and the aggregate compare-and-set.
// CommitMutation stores every state change and the receipt atomically.
type ReconnectRepository interface {
	FindCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*ReconnectRecord, error)
	LoadAuthority(ctx context.Context, scope pausedomain.GraphScope, participantID uuid.UUID) (ReconnectAuthority, error)
	CommitMutation(ctx context.Context, expectedRevision int64, record ReconnectRecord) (*ReconnectRecord, bool, error)
}
type ReplayClock interface {
	Now() time.Time
}

const replayReserveExhaustionAttempts = 2

var (
	ErrInvalidReplayReserveExhaustion  = errors.New("invalid replay reserve exhaustion")
	ErrReplayReserveExhaustionConflict = errors.New("replay reserve exhaustion conflict")
	ErrReplayReserveExhaustionReuse    = errors.New("replay reserve exhaustion command was reused")
)

type ReplayReserveExhaustionCommand struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedClosureRevisionID domain.WaveRevisionID
	ExpectedActiveSnapshotID  uuid.UUID
}

type ReplayReserveExhaustionAuthority struct {
	Scope          ReplayReplacementScope
	Revision       int64
	FailedAttempt  AttemptRecord
	OldWaveClosure Closure
	ReserveChain   ReplayReserveChain
	Current        *ReplayReserveExhaustion
}

type ReplayReserveExhaustion struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	ClosureRevisionID         domain.WaveRevisionID
	FailedAttemptCommandID    uuid.UUID
	AssignmentAttemptID       uuid.UUID
	GameID                    uuid.UUID
	ActiveSnapshotID          uuid.UUID
	ReservePosition           int
	Category                  domain.Category
	PreviousSeries            seriesdomain.Execution
	Series                    seriesdomain.Execution
	OldWave                   domain.Wave
}

// ReplayReserveExhaustionRepository owns one transaction that revalidates the
// failed attempt, completed old Wave and fully consumed reserve chain before
// pausing the replay-required Series without creating a replacement Wave.
type ReplayReserveExhaustionRepository interface {
	LoadReplayReserveExhaustionAuthority(
		ctx context.Context,
		scope ReplayReplacementScope,
	) (ReplayReserveExhaustionAuthority, error)
	CommitReplayReserveExhaustion(
		ctx context.Context,
		record ReplayReserveExhaustion,
	) (*ReplayReserveExhaustion, bool, error)
}

type FailedAttemptTerminalizer interface {
	Terminalize(
		ctx context.Context,
		command AttemptCommand,
	) (*AttemptRecord, bool, error)
}

type OldWaveCloser interface {
	Close(
		ctx context.Context,
		command CloseCommand,
	) (*Closure, bool, error)
}

type ReplayReplacementPlanner interface {
	Replace(
		ctx context.Context,
		command ReplayReplacementCommand,
	) (*ReplayReplacement, bool, error)
}

const operatorReserveAttempts = 2

var (
	ErrInvalidOperatorReserve  = errors.New("invalid operator reserve")
	ErrOperatorReserveConflict = errors.New("operator reserve conflict")
	ErrOperatorReserveReuse    = errors.New("operator reserve command was reused")
)

type OperatorReserveCommand struct {
	Scope                       ReplayReplacementScope
	CommandID                   uuid.UUID
	ExpectedExhaustionCommandID uuid.UUID
	ExpectedRevisions           assignmentusecase.ReserveAssignmentSourceRevisions
	ProposedTaskID              uuid.UUID
	ProposedVersion             int
	ProposedSnapshotID          uuid.UUID
	Reserve                     assignmentusecase.ReserveAssignmentCommand
}

type OperatorReserveAuthority struct {
	Scope      ReplayReplacementScope
	Revision   int64
	Exhaustion ReplayReserveExhaustion
	Reserve    assignmentusecase.ReserveAssignmentAuthority
	Current    *OperatorReserve
}

type OperatorReserve struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	Exhaustion                ReplayReserveExhaustion
	Reserve                   assignmentusecase.ReserveAssignmentRecord
	Series                    seriesdomain.Execution
}

// OperatorReserveRepository owns one transaction that locks the paused Series
// and reserve-exhaustion evidence, revalidates the current reserve candidate,
// commits its assignment, and resumes the Series atomically.
type OperatorReserveRepository interface {
	LoadOperatorReserveAuthority(
		ctx context.Context,
		scope ReplayReplacementScope,
	) (OperatorReserveAuthority, error)
	CommitOperatorReserve(
		ctx context.Context,
		record OperatorReserve,
	) (*OperatorReserve, bool, error)
}

const (
	replayReplacementAttempts      = 2
	baseReplayReserveSnapshots     = domain.AssignmentReserveCount + 1
	operatorReplayReserveSnapshots = baseReplayReserveSnapshots + 1
)

var (
	ErrInvalidReplayReplacement  = errors.New("invalid replay replacement")
	ErrReplayReplacementConflict = errors.New("replay replacement conflict")
	ErrReplayReplacementReuse    = errors.New("replay replacement command was reused")
	ErrReplayReservesExhausted   = errors.New("replay reserves exhausted")
)

type ReplayReplacementScope struct {
	TournamentID uuid.UUID
	OldWaveID    uuid.UUID
	SeriesID     uuid.UUID
	SlotID       uuid.UUID
	AssignmentID uuid.UUID
}

type ReplayReserveChain struct {
	AssignmentID uuid.UUID
	ActiveIndex  int
	Snapshots    []domain.AssignmentTaskSnapshot
}

type ReplayReplacementAuthority struct {
	Scope          ReplayReplacementScope
	Revision       int64
	FailedAttempt  AttemptRecord
	OldWaveClosure Closure
	ReserveChain   ReplayReserveChain
	ParticipantIDs [2]uuid.UUID
	Current        *ReplayReplacement
}

type ReplayReplacementCommand struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedClosureRevisionID domain.WaveRevisionID
	AssignmentAttemptID       uuid.UUID
	GameID                    uuid.UUID
	WaveID                    uuid.UUID
	WaveRevisionID            domain.WaveRevisionID
	ReadyWindowID             uuid.UUID
	ReadyWindowRevisionID     domain.ReadyWindowRevisionID
}

type ReplayReplacement struct {
	Scope                     ReplayReplacementScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	ClosureRevisionID         domain.WaveRevisionID
	FromSnapshotID            uuid.UUID
	AssignmentAttemptID       uuid.UUID
	ReservePosition           int
	Snapshot                  domain.AssignmentTaskSnapshot
	Category                  domain.Category
	Slot                      domain.GameSlot
	Game                      domain.Game
	Wave                      domain.Wave
	OpenedAt                  time.Time
}

type NoSolveReplayCommand struct {
	Terminalize AttemptCommand
	Close       CloseCommand
	Replace     ReplayReplacementCommand
}

type NoSolveReplayResult struct {
	FailedAttempt  *AttemptRecord
	OldWaveClosure *Closure
	Replacement    *ReplayReplacement
	Exhausted      bool
}

// ReplayReplacementRepository revalidates committed failure and old-Wave
// evidence, advances one planned reserve, and writes the fresh execution
// identities atomically.
type ReplayReplacementRepository interface {
	LoadReplayReplacementAuthority(
		ctx context.Context,
		scope ReplayReplacementScope,
	) (ReplayReplacementAuthority, error)
	CommitReplayReplacement(
		ctx context.Context,
		replacement ReplayReplacement,
	) (*ReplayReplacement, bool, error)
}
type SettlementRepository interface {
	LoadConcurrentWinnerAuthority(
		ctx context.Context,
		scope gamedomain.SubmissionScope,
	) (SettlementAuthority, error)
	CommitConcurrentWinnerSettlement(
		ctx context.Context,
		settlement SettlementRecord,
	) (*SettlementRecord, bool, error)
}
type WaveClock interface {
	Now() time.Time
}

type CloseRepository interface {
	LoadCloseAuthority(ctx context.Context, scope CloseScope) (CloseAuthority, error)
	CommitClosure(ctx context.Context, closure Closure) (*Closure, bool, error)
}
