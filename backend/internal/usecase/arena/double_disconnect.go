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

const reconnectMutationAttempts = 2

type reconnectRecordBuilder func(ReconnectAuthority, time.Time) (ReconnectRecord, bool, error)

func runClockedReconnectMutation(
	ctx context.Context,
	repository ReconnectRepository,
	clock Clock,
	scope PauseGraphScope,
	commandID uuid.UUID,
	match func(ReconnectRecord) bool,
	build reconnectRecordBuilder,
) (*ReconnectRecord, bool, error) {
	for range reconnectMutationAttempts {
		receipt, found, err := findReconnectReceipt(ctx, repository, scope.TournamentID, commandID, match)
		if err != nil || found {
			return receipt, false, err
		}
		authority, err := loadReconnectMutationAuthority(ctx, repository, scope)
		if err != nil {
			return nil, false, err
		}
		record, commit, err := buildClockedReconnectRecord(authority, clock, build)
		if err != nil {
			return nil, false, err
		}
		if !commit {
			return cloneReconnectRecord(&record), false, nil
		}
		result, conflict, err := commitReconnectRecord(ctx, repository, authority.Revision, record)
		if conflict {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		return result, true, nil
	}
	return nil, false, ErrReconnectConflict
}

func findReconnectReceipt(ctx context.Context, repository ReconnectRepository, tournamentID, commandID uuid.UUID, match func(ReconnectRecord) bool) (*ReconnectRecord, bool, error) {
	recorded, err := repository.FindReconnectCommand(ctx, tournamentID, commandID)
	if err != nil {
		return nil, false, fmt.Errorf("arena reconnect - find command: %w", err)
	}
	if recorded == nil {
		return nil, false, nil
	}
	if validateReconnectRecord(*recorded) != nil || recorded.Authority.Revision != recorded.ExpectedAuthorityRevision+1 {
		return nil, true, domain.ErrInternal
	}
	if !match(*recorded) {
		return nil, true, ErrReconnectCommandReuse
	}
	return cloneReconnectRecord(recorded), true, nil
}

func loadReconnectMutationAuthority(ctx context.Context, repository ReconnectRepository, scope PauseGraphScope) (ReconnectAuthority, error) {
	authority, err := repository.LoadReconnectAuthority(ctx, scope)
	if err != nil {
		return ReconnectAuthority{}, fmt.Errorf("arena reconnect - load authority: %w", err)
	}
	if authority.Scope != scope {
		return ReconnectAuthority{}, reconnectError("authority scope does not match command")
	}
	if err := validateReconnectAuthority(authority); err != nil {
		return ReconnectAuthority{}, err
	}
	return authority, nil
}

func buildClockedReconnectRecord(authority ReconnectAuthority, clock Clock, build reconnectRecordBuilder) (ReconnectRecord, bool, error) {
	now := clock.Now().Round(0).UTC()
	if !validArenaServerTime(now) {
		return ReconnectRecord{}, false, domain.ErrValidation
	}
	if authority.Current == nil {
		if err := validateReconnectMutationTime(authority, now); err != nil {
			return ReconnectRecord{}, false, err
		}
	}
	record, commit, err := build(authority, now)
	if err != nil {
		return ReconnectRecord{}, false, err
	}
	if err := validateReconnectRecord(record); err != nil {
		return ReconnectRecord{}, false, err
	}
	return record, commit, nil
}

func validateReconnectMutationTime(authority ReconnectAuthority, now time.Time) error {
	if !timeAtOrBefore(now, authority.GameClock.FrozenAt) || !timePointerAtOrBefore(now, authority.GameClock.ResumedAt) {
		return reconnectError("clock rollback before Game clock history")
	}
	for _, presence := range authority.Presence {
		if !timeCoversPresenceHistory(now, presence) {
			return reconnectError("clock rollback before Presence history")
		}
	}
	for _, interval := range authority.Reconnect {
		if !timeCoversReconnectHistory(now, interval) {
			return reconnectError("clock rollback before reconnect history")
		}
	}
	return nil
}

func commitReconnectRecord(ctx context.Context, repository ReconnectRepository, expectedRevision int64, record ReconnectRecord) (*ReconnectRecord, bool, error) {
	committed, changed, err := repository.CommitReconnectMutation(ctx, expectedRevision, cloneReconnectRecordValue(record))
	if errors.Is(err, domain.ErrConflict) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("arena reconnect - commit mutation: %w", err)
	}
	if !changed || committed == nil {
		return nil, false, domain.ErrInternal
	}
	result := cloneReconnectRecord(committed)
	if validateReconnectRecord(*result) != nil || !reflect.DeepEqual(*result, record) {
		return nil, false, domain.ErrInternal
	}
	return cloneReconnectRecord(result), false, nil
}

func advanceReconnectAuthority(authority *ReconnectAuthority) {
	authority.Revision++
}

func validateReconnectAuthority(authority ReconnectAuthority) error {
	if err := validateReconnectAuthorityHeader(authority); err != nil {
		return err
	}
	stableSlotGames, err := reconnectStableSlotGames(authority)
	if err != nil {
		return err
	}
	if err := validateReconnectPresenceSet(authority); err != nil {
		return err
	}
	counters, err := validateReconnectCounterSet(authority)
	if err != nil {
		return err
	}
	if err := validateReconnectIntervalSet(authority, stableSlotGames, counters); err != nil {
		return err
	}
	if err := validateReconnectIntervalLineage(authority.Reconnect); err != nil {
		return err
	}
	return validateReconnectCurrentOutcome(authority)
}

func validateReconnectAuthorityHeader(authority ReconnectAuthority) error {
	if !validReconnectAuthorityIdentity(authority) {
		return reconnectError("incomplete authority")
	}
	if !validReconnectAuthorityTopology(authority) {
		return reconnectError("invalid Series topology")
	}
	return nil
}

func validReconnectAuthorityIdentity(authority ReconnectAuthority) bool {
	return validPauseGraphScope(authority.Scope) && authority.Revision >= 1 && authority.PauseID != uuid.Nil &&
		authority.GameRevision >= 1 && authority.SeriesRevision >= 1 && authority.Game.ID != uuid.Nil &&
		authority.Series.ID != uuid.Nil && authority.GameClock.GameID == authority.Game.ID &&
		authority.GameClock.PauseID == authority.PauseID && len(authority.Presence) == 2 && len(authority.Counters) == 2 &&
		authority.CurrentOrdinal >= 0 && authority.CurrentProjectionRevision >= 1 &&
		validateCurrentGameResultRevisionIDs(authority.CurrentGameResultRevisionIDs) == nil
}

func validReconnectAuthorityTopology(authority ReconnectAuthority) bool {
	return authority.Game.Validate() == nil && authority.Series.Validate() == nil &&
		authority.Series.TournamentID == authority.Scope.TournamentID && authority.Series.ID == reconnectSeriesID(authority) &&
		authority.Series.FirstParticipantID != authority.Series.SecondParticipantID && reconnectAuthorityGameMirrored(authority) &&
		validReconnectGameClock(authority)
}

func reconnectStableSlotGames(authority ReconnectAuthority) (map[uuid.UUID]struct{}, error) {
	currentSlot, ok := reconnectCurrentGameSlot(authority.Series, authority.Game.ID)
	if !ok {
		return nil, reconnectError("current Game is not the latest stable slot attempt")
	}
	games := make(map[uuid.UUID]struct{}, len(currentSlot.Attempts))
	for _, game := range currentSlot.Attempts {
		games[game.ID] = struct{}{}
	}
	return games, nil
}

func validateReconnectPresenceSet(authority ReconnectAuthority) error {
	seen := make(map[uuid.UUID]struct{}, 2)
	for _, presence := range authority.Presence {
		if err := validateReconnectPresence(authority, presence); err != nil {
			return err
		}
		if _, exists := seen[presence.ParticipantID]; exists {
			return reconnectError("duplicate Presence")
		}
		seen[presence.ParticipantID] = struct{}{}
	}
	return nil
}

func validateReconnectPresence(authority ReconnectAuthority, presence PausePresence) error {
	if presence.TournamentID != authority.Scope.TournamentID || presence.RosterID != authority.Scope.RosterID ||
		presence.SeriesID != authority.Series.ID || !reconnectSeriesHasParticipant(authority.Series, presence.ParticipantID) {
		return reconnectError("foreign Presence")
	}
	if validatePausePresence(presence) != nil {
		return reconnectError("invalid Presence")
	}
	return nil
}

func reconnectSeriesHasParticipant(series domain.ArenaSeries, participantID uuid.UUID) bool {
	return participantID == series.FirstParticipantID || participantID == series.SecondParticipantID
}

func validateReconnectCounterSet(authority ReconnectAuthority) (map[uuid.UUID]PauseReconnectCounter, error) {
	counters := make(map[uuid.UUID]PauseReconnectCounter, 2)
	for _, counter := range authority.Counters {
		if err := validateReconnectCounter(authority, counter, counters); err != nil {
			return nil, err
		}
		counters[counter.ParticipantID] = counter
	}
	if _, ok := counters[authority.Series.FirstParticipantID]; !ok {
		return nil, reconnectError("first participant counter is missing")
	}
	if _, ok := counters[authority.Series.SecondParticipantID]; !ok {
		return nil, reconnectError("second participant counter is missing")
	}
	return counters, nil
}

func validateReconnectCounter(authority ReconnectAuthority, counter PauseReconnectCounter, seen map[uuid.UUID]PauseReconnectCounter) error {
	if !validPauseReconnectCounter(counter) || counter.PauseID != authority.PauseID || counter.RosterID != authority.Scope.RosterID ||
		counter.Limit != ReconnectCycleLimit || !reconnectSeriesHasParticipant(authority.Series, counter.ParticipantID) {
		return reconnectError("invalid reconnect counter")
	}
	if _, exists := seen[counter.ParticipantID]; exists {
		return reconnectError("duplicate reconnect counter")
	}
	return nil
}

type reconnectCycle struct {
	participantID uuid.UUID
	number        int
	continuation  int
}

type reconnectRootEpoch struct {
	participantID uuid.UUID
	presenceEpoch int64
}

type reconnectIntervalValidation struct {
	seenIDs          map[uuid.UUID]struct{}
	seenSegments     map[reconnectCycle]struct{}
	openParticipants map[uuid.UUID]struct{}
	rootCounts       map[uuid.UUID]int
	rootEpochs       map[reconnectRootEpoch]struct{}
}

func newReconnectIntervalValidation(size int) reconnectIntervalValidation {
	return reconnectIntervalValidation{seenIDs: make(map[uuid.UUID]struct{}, size), seenSegments: make(map[reconnectCycle]struct{}, size),
		openParticipants: make(map[uuid.UUID]struct{}, 2), rootCounts: make(map[uuid.UUID]int, 2),
		rootEpochs: make(map[reconnectRootEpoch]struct{}, size)}
}

func validateReconnectIntervalSet(authority ReconnectAuthority, stableGames map[uuid.UUID]struct{}, counters map[uuid.UUID]PauseReconnectCounter) error {
	validation := newReconnectIntervalValidation(len(authority.Reconnect))
	for _, interval := range authority.Reconnect {
		if err := validateReconnectInterval(authority, interval, stableGames, counters, &validation); err != nil {
			return err
		}
	}
	for participantID, counter := range counters {
		if validation.rootCounts[participantID] != counter.Used {
			return reconnectError("reconnect roots do not match stable counter")
		}
	}
	return nil
}

func validateReconnectInterval(authority ReconnectAuthority, interval PauseReconnectInterval, stableGames map[uuid.UUID]struct{}, counters map[uuid.UUID]PauseReconnectCounter, validation *reconnectIntervalValidation) error {
	if err := validateReconnectIntervalBinding(authority, interval, stableGames, counters); err != nil {
		return err
	}
	if err := validation.rememberIdentity(interval); err != nil {
		return err
	}
	if err := validation.rememberRoot(interval); err != nil {
		return err
	}
	return validation.rememberOpen(authority, interval)
}

func validateReconnectIntervalBinding(authority ReconnectAuthority, interval PauseReconnectInterval, stableGames map[uuid.UUID]struct{}, counters map[uuid.UUID]PauseReconnectCounter) error {
	if validatePauseReconnect(interval) != nil || interval.PauseID != authority.PauseID || interval.RosterID != authority.Scope.RosterID ||
		interval.SeriesID != authority.Series.ID || !reconnectSeriesHasParticipant(authority.Series, interval.ParticipantID) {
		return reconnectError("invalid reconnect interval")
	}
	if _, belongs := stableGames[interval.GameID]; !belongs {
		return reconnectError("reconnect interval belongs to a foreign Game slot")
	}
	counter, counterFound := counters[interval.ParticipantID]
	presence := reconnectPresenceByParticipant(authority.Presence, interval.ParticipantID)
	if !counterFound || presence == nil || interval.Number > counter.Used || interval.PresenceEpoch > presence.PresenceEpoch {
		return reconnectError("reconnect interval exceeds stable counter")
	}
	if interval.PresenceEpoch == presence.PresenceEpoch && reconnectIntervalSelectable(interval) && interval.GameID != authority.Game.ID {
		return reconnectError("current reconnect interval belongs to predecessor Game")
	}
	return nil
}

func reconnectIntervalSelectable(interval PauseReconnectInterval) bool {
	return interval.State == ReconnectStateOpen || interval.State == ReconnectStateExpired
}

func (validation *reconnectIntervalValidation) rememberIdentity(interval PauseReconnectInterval) error {
	if _, exists := validation.seenIDs[interval.ID]; exists {
		return reconnectError("duplicate reconnect interval")
	}
	validation.seenIDs[interval.ID] = struct{}{}
	segment := reconnectCycle{participantID: interval.ParticipantID, number: interval.Number, continuation: interval.ContinuationNumber}
	if _, exists := validation.seenSegments[segment]; exists {
		return reconnectError("duplicate reconnect cycle segment")
	}
	validation.seenSegments[segment] = struct{}{}
	return nil
}

func (validation *reconnectIntervalValidation) rememberRoot(interval PauseReconnectInterval) error {
	if interval.ContinuationNumber != 0 {
		return nil
	}
	epoch := reconnectRootEpoch{participantID: interval.ParticipantID, presenceEpoch: interval.PresenceEpoch}
	if _, exists := validation.rootEpochs[epoch]; exists {
		return reconnectError("duplicate reconnect root Presence epoch")
	}
	validation.rootEpochs[epoch] = struct{}{}
	validation.rootCounts[interval.ParticipantID]++
	return nil
}

func (validation *reconnectIntervalValidation) rememberOpen(authority ReconnectAuthority, interval PauseReconnectInterval) error {
	if interval.State != ReconnectStateOpen {
		return nil
	}
	if _, exists := validation.openParticipants[interval.ParticipantID]; exists {
		return reconnectError("multiple open intervals for participant")
	}
	validation.openParticipants[interval.ParticipantID] = struct{}{}
	presence := reconnectPresenceByParticipant(authority.Presence, interval.ParticipantID)
	if interval.GameID != authority.Game.ID || presence == nil || presence.State != PresenceStateDisconnected || interval.PresenceEpoch != presence.PresenceEpoch {
		return reconnectError("open reconnect interval does not match current Presence")
	}
	return nil
}

func validateReconnectIntervalLineage(intervals []PauseReconnectInterval) error {
	byID := make(map[uuid.UUID]PauseReconnectInterval, len(intervals))
	for _, interval := range intervals {
		byID[interval.ID] = interval
	}
	if validateReconnectLineage(intervals, byID) != nil {
		return reconnectError("invalid reconnect lineage")
	}
	return nil
}

func validateReconnectCurrentOutcome(authority ReconnectAuthority) error {
	if authority.Game.State.IsTerminal() {
		if authority.Current == nil || validateReconnectTerminalOutcome(authority, *authority.Current) != nil {
			return reconnectError("terminal Game lacks current outcome")
		}
		return nil
	}
	if authority.Current != nil {
		return reconnectError("live Game carries terminal outcome")
	}
	return nil
}

func validateReconnectRecord(record ReconnectRecord) error {
	if err := validateReconnectRecordHeader(record); err != nil {
		return err
	}
	if err := validateReconnectRecordCommand(record); err != nil {
		return err
	}
	if record.Authority.Current == nil {
		return validateLiveReconnectRecord(record)
	}
	if err := validateTerminalReconnectRecord(record); err != nil {
		return err
	}
	if record.Authority.Revision == record.ExpectedAuthorityRevision+1 && !reconnectRecordSettlementMatches(record) {
		return reconnectError("terminal outcome does not match command identities")
	}
	return nil
}

func validateReconnectRecordHeader(record ReconnectRecord) error {
	if record.ExpectedAuthorityRevision < 1 || !validArenaServerTime(record.RecordedAt) || validateReconnectAuthority(record.Authority) != nil {
		return reconnectError("invalid record header")
	}
	if record.Authority.Revision != record.ExpectedAuthorityRevision && record.Authority.Revision != record.ExpectedAuthorityRevision+1 {
		return reconnectError("invalid record command or revision")
	}
	return nil
}

func validateReconnectRecordCommand(record ReconnectRecord) error {
	if reconnectRecordCommandCount(record) != 1 {
		return reconnectError("invalid record command or revision")
	}
	switch record.Kind {
	case ReconnectMutationReconnect:
		if record.ReconnectCommand == nil || !validReconnectCommand(*record.ReconnectCommand) || record.ReconnectCommand.Scope != record.Authority.Scope {
			return reconnectError("invalid reconnect receipt")
		}
	case ReconnectMutationTimeout:
		if record.TimeoutCommand == nil || !validReconnectTimeoutCommand(*record.TimeoutCommand) || record.TimeoutCommand.Scope != record.Authority.Scope {
			return reconnectError("invalid timeout receipt")
		}
	case ReconnectMutationDisconnect:
		if record.DisconnectCommand == nil || !validDisconnectCommand(*record.DisconnectCommand) || record.DisconnectCommand.Scope != record.Authority.Scope {
			return reconnectError("invalid disconnect receipt")
		}
	default:
		return reconnectError("invalid record command or revision")
	}
	return nil
}

func reconnectRecordCommandCount(record ReconnectRecord) int {
	commands := 0
	if record.ReconnectCommand != nil {
		commands++
	}
	if record.TimeoutCommand != nil {
		commands++
	}
	if record.DisconnectCommand != nil {
		commands++
	}
	return commands
}

func validateLiveReconnectRecord(record ReconnectRecord) error {
	if record.GameResultRevision != nil || record.VoidGameResultRevision != nil || record.ScoreRevision != nil ||
		record.SeriesResultRevision != nil || record.ReplayRoute != nil || record.Evidence != nil {
		return reconnectError("live mutation carries terminal evidence")
	}
	return nil
}

func validateTerminalReconnectRecord(record ReconnectRecord) error {
	current := record.Authority.Current
	if !reflect.DeepEqual(record.GameResultRevision, current.GameResultRevision) ||
		!reflect.DeepEqual(record.VoidGameResultRevision, current.VoidGameResultRevision) ||
		record.ScoreRevision == nil || !reflect.DeepEqual(*record.ScoreRevision, current.ScoreRevision) ||
		!reflect.DeepEqual(record.SeriesResultRevision, current.SeriesResultRevision) ||
		!reflect.DeepEqual(record.ReplayRoute, current.ReplayRoute) || record.Evidence == nil ||
		!reflect.DeepEqual(*record.Evidence, current.Evidence) || !record.RecordedAt.Equal(current.TerminalizedAt) {
		return reconnectError("record lost terminal outcome")
	}
	return nil
}

func validateReconnectTerminalOutcome(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) error {
	if err := validateReconnectTerminalHeader(authority, outcome); err != nil {
		return err
	}
	winnerRoute := outcome.GameResultRevision != nil && outcome.VoidGameResultRevision == nil && outcome.ReplayRoute == nil
	replayRoute := outcome.GameResultRevision == nil && outcome.VoidGameResultRevision != nil && outcome.SeriesResultRevision == nil && outcome.ReplayRoute != nil
	if !winnerRoute && !replayRoute {
		return reconnectError("ambiguous terminal route")
	}
	if winnerRoute {
		if err := validateReconnectWinnerOutcome(authority, outcome); err != nil {
			return err
		}
	} else if err := validateReconnectReplayOutcome(authority, outcome); err != nil {
		return err
	}
	if err := validateReconnectTerminalLineage(authority, outcome); err != nil {
		return err
	}
	return validateReconnectTerminalIdentities(authority, outcome)
}

func validateReconnectTerminalHeader(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) error {
	if !validReconnectTerminalEvidenceHeader(authority, outcome) || !validReconnectTerminalScoreHeader(authority, outcome.ScoreRevision) ||
		outcome.ScoreRevision.ScoreBefore.Validate(outcome.ScoreRevision.Format) != nil ||
		outcome.ScoreRevision.ScoreAfter.Validate(outcome.ScoreRevision.Format) != nil {
		return reconnectError("invalid terminal outcome header")
	}
	if outcome.ScoreRevision.PreviousRevisionID != nil &&
		(outcome.ScoreRevision.PreviousRevisionID.IsZero() || *outcome.ScoreRevision.PreviousRevisionID == outcome.ScoreRevision.ID) {
		return reconnectError("invalid score previous revision")
	}
	return nil
}

func validReconnectTerminalEvidenceHeader(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) bool {
	return validArenaServerTime(outcome.TerminalizedAt) && outcome.Evidence.ProjectionRevision == authority.CurrentProjectionRevision &&
		outcome.Evidence.SourceProjectionRevision+1 == outcome.Evidence.ProjectionRevision && outcome.Evidence.RecordedAt.Equal(outcome.TerminalizedAt)
}

func validReconnectTerminalScoreHeader(authority ReconnectAuthority, score ArenaSettlementScoreRevision) bool {
	return !score.ID.IsZero() && reflect.DeepEqual(score.GameResultRevisionIDs, authority.CurrentGameResultRevisionIDs) &&
		score.SeriesID == authority.Series.ID && score.FirstParticipantID == authority.Series.FirstParticipantID &&
		score.SecondParticipantID == authority.Series.SecondParticipantID && score.Format == authority.Series.Format &&
		score.ScoreAfter == authority.Series.Score && authority.Series.CurrentScoreRevisionID != nil &&
		*authority.Series.CurrentScoreRevisionID == score.ID && len(score.GameResultRevisionIDs) > 0
}

func validateReconnectWinnerOutcome(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) error {
	if !validReconnectWinnerGame(authority, outcome) {
		return reconnectError("invalid winner terminal route")
	}
	if !validReconnectWinnerScoreDelta(authority, outcome.ScoreRevision) {
		return reconnectError("winner score delta is invalid")
	}
	if authority.Series.State == domain.ArenaSeriesStateCompleted {
		return validateReconnectCompletedSeries(authority, outcome)
	}
	if outcome.SeriesResultRevision != nil || authority.Series.CurrentResultRevisionID != nil || authority.CurrentOrdinal != outcome.ScoreRevision.Ordinal {
		return reconnectError("ongoing Series carries terminal revision")
	}
	return nil
}

func validReconnectWinnerGame(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) bool {
	return authority.Game.State == domain.ArenaGameStateCompleted && authority.Game.ResultReason == domain.ArenaGameResultReasonOperatorForfeit &&
		authority.Game.WinnerID != nil && outcome.GameResultRevision.ID == *authority.Game.ResultRevisionID &&
		outcome.GameResultRevision.GameID == authority.Game.ID && outcome.GameResultRevision.WinnerID == *authority.Game.WinnerID &&
		outcome.GameResultRevision.Reason == domain.ArenaGameResultReasonOperatorForfeit &&
		outcome.GameResultRevision.Ordinal+1 == outcome.ScoreRevision.Ordinal &&
		outcome.GameResultRevision.RecordedAt.Equal(outcome.TerminalizedAt)
}

func validReconnectWinnerScoreDelta(authority ReconnectAuthority, score ArenaSettlementScoreRevision) bool {
	firstDelta := score.ScoreAfter.FirstParticipantWins - score.ScoreBefore.FirstParticipantWins
	secondDelta := score.ScoreAfter.SecondParticipantWins - score.ScoreBefore.SecondParticipantWins
	switch *authority.Game.WinnerID {
	case authority.Series.FirstParticipantID:
		return firstDelta == 1 && secondDelta == 0
	case authority.Series.SecondParticipantID:
		return firstDelta == 0 && secondDelta == 1
	default:
		return false
	}
}

func validateReconnectCompletedSeries(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) error {
	if !validReconnectCompletedSeries(authority, outcome) {
		return reconnectError("invalid completed Series result")
	}
	previous := outcome.SeriesResultRevision.PreviousRevisionID
	if previous != nil && (previous.IsZero() || *previous == outcome.SeriesResultRevision.ID) {
		return reconnectError("invalid Series previous revision")
	}
	return nil
}

func validReconnectCompletedSeries(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) bool {
	result := outcome.SeriesResultRevision
	return result != nil && result.Ordinal == outcome.ScoreRevision.Ordinal+1 && result.State == domain.ArenaSeriesStateCompleted &&
		authority.Series.CurrentResultRevisionID != nil && *authority.Series.CurrentResultRevisionID == result.ID &&
		result.ID != outcome.GameResultRevision.ID && result.SeriesID == authority.Series.ID && result.WinnerID != nil &&
		authority.Series.WinnerID != nil && *result.WinnerID == *authority.Series.WinnerID &&
		result.ScoreRevisionID == outcome.ScoreRevision.ID && result.Reason == domain.ArenaGameResultReasonOperatorForfeit &&
		result.RecordedAt.Equal(outcome.TerminalizedAt) && authority.CurrentOrdinal == result.Ordinal
}

func validateReconnectReplayOutcome(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) error {
	if !validReconnectVoidGame(authority, outcome) || !validReconnectReplaySeries(authority, outcome) {
		return reconnectError("invalid replay terminal route")
	}
	slot, ok := reconnectCurrentGameSlot(authority.Series, authority.Game.ID)
	if !ok || outcome.ReplayRoute.SlotID != slot.ID || outcome.ReplayRoute.Category != slot.Category {
		return reconnectError("replay route changed Game category")
	}
	return nil
}

func validReconnectVoidGame(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) bool {
	result := outcome.VoidGameResultRevision
	return authority.Game.State == domain.ArenaGameStateVoid && authority.Game.ResultReason == domain.ArenaGameResultReasonDisconnect &&
		authority.Game.WinnerID == nil && result.ID == *authority.Game.ResultRevisionID && result.GameID == authority.Game.ID &&
		result.Reason == domain.ArenaGameResultReasonDisconnect && result.RecordedAt.Equal(outcome.TerminalizedAt) &&
		result.Ordinal+1 == outcome.ScoreRevision.Ordinal
}

func validReconnectReplaySeries(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) bool {
	return authority.Series.State == domain.ArenaSeriesStateReplayRequired && outcome.ScoreRevision.ScoreBefore == outcome.ScoreRevision.ScoreAfter &&
		authority.CurrentOrdinal == outcome.ScoreRevision.Ordinal && outcome.ReplayRoute.WaveID == authority.Scope.WaveID &&
		outcome.ReplayRoute.SeriesID == authority.Series.ID && outcome.ReplayRoute.GameID == authority.Game.ID &&
		outcome.ReplayRoute.RoutedAt.Equal(outcome.TerminalizedAt)
}

func validateReconnectTerminalLineage(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) error {
	lastResultID := authority.CurrentGameResultRevisionIDs[len(authority.CurrentGameResultRevisionIDs)-1]
	if authority.Game.ResultRevisionID == nil || *authority.Game.ResultRevisionID != lastResultID ||
		!outcome.ScoreRevision.RecordedAt.Equal(outcome.TerminalizedAt) {
		return reconnectError("terminal result lineage is not current")
	}
	return nil
}

func validateReconnectTerminalIdentities(authority ReconnectAuthority, outcome ReconnectTerminalOutcome) error {
	lastResultID := authority.CurrentGameResultRevisionIDs[len(authority.CurrentGameResultRevisionIDs)-1]
	identities := []uuid.UUID{lastResultID.UUID(), outcome.ScoreRevision.ID.UUID(), outcome.Evidence.AuditEventID,
		outcome.Evidence.OutboxEventID, outcome.Evidence.ProjectionRevisionID}
	if outcome.SeriesResultRevision != nil {
		identities = append(identities, outcome.SeriesResultRevision.ID.UUID())
	}
	if outcome.ReplayRoute != nil {
		identities = append(identities, outcome.ReplayRoute.ID)
	}
	if !uniqueNonZeroUUIDs(identities) {
		return reconnectError("terminal evidence identities collide")
	}
	return nil
}

func reconnectRecordSettlementMatches(record ReconnectRecord) bool {
	ids, ok := reconnectRecordSettlementIDs(record)
	if !ok || !reconnectCommonSettlementMatches(record, ids) {
		return false
	}
	if record.GameResultRevision != nil {
		return reconnectWinnerSettlementMatches(record, ids)
	}
	return reconnectReplaySettlementMatches(record, ids)
}

func reconnectRecordSettlementIDs(record ReconnectRecord) (ReconnectSettlementIDs, bool) {
	switch {
	case record.ReconnectCommand != nil:
		return record.ReconnectCommand.Settlement, true
	case record.TimeoutCommand != nil:
		return record.TimeoutCommand.Settlement, true
	case record.DisconnectCommand != nil:
		return record.DisconnectCommand.Settlement, true
	default:
		return ReconnectSettlementIDs{}, false
	}
}

func reconnectCommonSettlementMatches(record ReconnectRecord, ids ReconnectSettlementIDs) bool {
	return record.ScoreRevision != nil && record.ScoreRevision.ID == ids.ScoreRevisionID && record.Evidence != nil &&
		record.Evidence.AuditEventID == ids.AuditEventID && record.Evidence.OutboxEventID == ids.OutboxEventID &&
		record.Evidence.ProjectionRevisionID == ids.ProjectionRevisionID
}

func reconnectWinnerSettlementMatches(record ReconnectRecord, ids ReconnectSettlementIDs) bool {
	if record.GameResultRevision.ID != ids.GameResultRevisionID || record.ReplayRoute != nil {
		return false
	}
	return record.SeriesResultRevision == nil || record.SeriesResultRevision.ID == ids.SeriesResultRevisionID
}

func reconnectReplaySettlementMatches(record ReconnectRecord, ids ReconnectSettlementIDs) bool {
	return record.VoidGameResultRevision != nil && record.VoidGameResultRevision.ID == ids.GameResultRevisionID &&
		record.SeriesResultRevision == nil && record.ReplayRoute != nil && record.ReplayRoute.ID == ids.ReplayRouteID
}

func validReconnectGameClock(authority ReconnectAuthority) bool {
	clock := authority.GameClock
	if clock.PauseID != authority.PauseID || clock.GameID != authority.Game.ID || clock.Revision < 1 || !validArenaServerTime(clock.OriginalDeadline) {
		return false
	}
	if clock.FrozenAt.IsZero() {
		return (authority.Game.State == domain.ArenaGameStateActive || authority.Game.State.IsTerminal()) &&
			clock.Remaining == 0 && clock.ResumedAt == nil && clock.ResumedDeadline == nil
	}
	if authority.Game.State == domain.ArenaGameStateActive {
		return validatePauseResumeGameClock(clock, false) == nil
	}
	return validatePauseResumeGameClock(clock, true) == nil
}

func reconnectSeriesID(authority ReconnectAuthority) uuid.UUID {
	if len(authority.Presence) == 0 {
		return uuid.Nil
	}
	return authority.Presence[0].SeriesID
}

func reconnectPresenceByParticipant(values []PausePresence, participantID uuid.UUID) *PausePresence {
	for index := range values {
		if values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func reconnectCounterByParticipant(values []PauseReconnectCounter, participantID uuid.UUID) *PauseReconnectCounter {
	for index := range values {
		if values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func reconnectMutationIntervalByID(values []PauseReconnectInterval, intervalID uuid.UUID) *PauseReconnectInterval {
	for index := range values {
		if values[index].ID == intervalID {
			return &values[index]
		}
	}
	return nil
}

func reconnectOpponentConnected(authority ReconnectAuthority, participantID uuid.UUID) bool {
	for _, presence := range authority.Presence {
		if presence.ParticipantID != participantID {
			return presence.State == PresenceStateConnected
		}
	}
	return false
}

func reconnectOpponentExpired(authority ReconnectAuthority, participantID uuid.UUID) bool {
	opponentID, ok := reconnectOpponentID(authority.Series, participantID)
	if !ok {
		return false
	}
	presence := reconnectPresenceByParticipant(authority.Presence, opponentID)
	if presence == nil || presence.State != PresenceStateDisconnected {
		return false
	}
	interval := reconnectCurrentInterval(authority, opponentID)
	return interval != nil && interval.State == ReconnectStateExpired
}

func reconnectOpponentMust(series domain.ArenaSeries, participantID uuid.UUID) uuid.UUID {
	opponentID, _ := reconnectOpponentID(series, participantID)
	return opponentID
}

func reconnectOpponentID(series domain.ArenaSeries, participantID uuid.UUID) (uuid.UUID, bool) {
	switch participantID {
	case series.FirstParticipantID:
		return series.SecondParticipantID, true
	case series.SecondParticipantID:
		return series.FirstParticipantID, true
	default:
		return uuid.Nil, false
	}
}

func reconnectAuthorityGameMirrored(authority ReconnectAuthority) bool {
	for _, slot := range authority.Series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == authority.Game.ID {
				return reflect.DeepEqual(game, authority.Game)
			}
		}
	}
	return false
}

func transitionReconnectGame(authority *ReconnectAuthority, nextState domain.ArenaGameState, terminal *GameTerminalEvidence) error {
	for slotIndex := range authority.Series.Slots {
		slot := authority.Series.Slots[slotIndex]
		if len(slot.Attempts) == 0 || slot.Attempts[len(slot.Attempts)-1].ID != authority.Game.ID {
			continue
		}
		transitioned, changed, err := TransitionGameSlotAttempt(slot, GameAttemptTransitionCommand{
			GameID: authority.Game.ID, ExpectedAttemptNo: authority.Game.AttemptNo, ExpectedState: authority.Game.State,
			NextState: nextState, Terminal: terminal,
		})
		if err != nil || !changed {
			return reconnectError("transition Game: %v", err)
		}
		authority.Series.Slots[slotIndex] = transitioned
		authority.Game = cloneArenaGame(transitioned.Attempts[len(transitioned.Attempts)-1])
		if authority.GameRevision == math.MaxInt64 || authority.SeriesRevision == math.MaxInt64 {
			return reconnectError("revision overflow")
		}
		authority.GameRevision++
		authority.SeriesRevision++
		return nil
	}
	return reconnectError("current Game is missing from Series")
}

func cloneReconnectCommand(value ReconnectCommand) ReconnectCommand { return value }

func cloneReconnectTimeoutCommand(value ReconnectTimeoutCommand) ReconnectTimeoutCommand {
	return value
}

func cloneDisconnectCommand(value DisconnectCommand) DisconnectCommand {
	clone := value
	if value.ContinuedFromID != nil {
		continued := *value.ContinuedFromID
		clone.ContinuedFromID = &continued
	}
	return clone
}

func cloneReconnectAuthority(value ReconnectAuthority) ReconnectAuthority {
	clone := value
	clone.Game = cloneArenaGame(value.Game)
	clone.Series = cloneArenaSeries(value.Series)
	clone.GameClock.ResumedAt = cloneArenaTimePointer(value.GameClock.ResumedAt)
	clone.GameClock.ResumedDeadline = cloneArenaTimePointer(value.GameClock.ResumedDeadline)
	clone.Presence = append([]PausePresence(nil), value.Presence...)
	for index := range clone.Presence {
		clone.Presence[index].DisconnectedAt = cloneArenaTimePointer(value.Presence[index].DisconnectedAt)
	}
	clone.Reconnect = append([]PauseReconnectInterval(nil), value.Reconnect...)
	for index := range clone.Reconnect {
		clone.Reconnect[index].ContinuedFromID = cloneUUIDPointer(value.Reconnect[index].ContinuedFromID)
		clone.Reconnect[index].SuspendedByPauseID = cloneUUIDPointer(value.Reconnect[index].SuspendedByPauseID)
		clone.Reconnect[index].ClosedAt = cloneArenaTimePointer(value.Reconnect[index].ClosedAt)
	}
	clone.Counters = append([]PauseReconnectCounter(nil), value.Counters...)
	clone.CurrentGameResultRevisionIDs = append([]domain.ArenaOfficialResultRevisionID(nil), value.CurrentGameResultRevisionIDs...)
	clone.Current = cloneReconnectTerminalOutcome(value.Current)
	return clone
}

func cloneReconnectTerminalOutcome(value *ReconnectTerminalOutcome) *ReconnectTerminalOutcome {
	if value == nil {
		return nil
	}
	clone := *value
	if value.GameResultRevision != nil {
		revision := *value.GameResultRevision
		clone.GameResultRevision = &revision
	}
	if value.VoidGameResultRevision != nil {
		revision := *value.VoidGameResultRevision
		clone.VoidGameResultRevision = &revision
	}
	clone.ScoreRevision = cloneArenaSettlementScoreRevision(value.ScoreRevision)
	if value.SeriesResultRevision != nil {
		revision := *value.SeriesResultRevision
		revision.PreviousRevisionID = cloneOfficialResultRevisionIDPointer(value.SeriesResultRevision.PreviousRevisionID)
		revision.WinnerID = cloneUUIDPointer(value.SeriesResultRevision.WinnerID)
		clone.SeriesResultRevision = &revision
	}
	if value.ReplayRoute != nil {
		route := *value.ReplayRoute
		clone.ReplayRoute = &route
	}
	return &clone
}

func cloneReconnectRecord(record *ReconnectRecord) *ReconnectRecord {
	if record == nil {
		return nil
	}
	clone := cloneReconnectRecordValue(*record)
	return &clone
}

func cloneReconnectRecordValue(value ReconnectRecord) ReconnectRecord {
	clone := value
	if value.ReconnectCommand != nil {
		command := cloneReconnectCommand(*value.ReconnectCommand)
		clone.ReconnectCommand = &command
	}
	if value.TimeoutCommand != nil {
		command := cloneReconnectTimeoutCommand(*value.TimeoutCommand)
		clone.TimeoutCommand = &command
	}
	if value.DisconnectCommand != nil {
		command := cloneDisconnectCommand(*value.DisconnectCommand)
		clone.DisconnectCommand = &command
	}
	clone.Authority = cloneReconnectAuthority(value.Authority)
	if value.GameResultRevision != nil {
		revision := *value.GameResultRevision
		clone.GameResultRevision = &revision
	}
	if value.VoidGameResultRevision != nil {
		revision := *value.VoidGameResultRevision
		clone.VoidGameResultRevision = &revision
	}
	if value.ScoreRevision != nil {
		revision := cloneArenaSettlementScoreRevision(*value.ScoreRevision)
		clone.ScoreRevision = &revision
	}
	if value.SeriesResultRevision != nil {
		revision := *value.SeriesResultRevision
		revision.PreviousRevisionID = cloneOfficialResultRevisionIDPointer(value.SeriesResultRevision.PreviousRevisionID)
		revision.WinnerID = cloneUUIDPointer(value.SeriesResultRevision.WinnerID)
		clone.SeriesResultRevision = &revision
	}
	if value.ReplayRoute != nil {
		route := *value.ReplayRoute
		clone.ReplayRoute = &route
	}
	if value.Evidence != nil {
		evidence := *value.Evidence
		clone.Evidence = &evidence
	}
	return clone
}
