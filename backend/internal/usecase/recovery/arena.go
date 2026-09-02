package recovery

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const ArenaRecoveryCursorSchemaVersion = 1

type ArenaRecoveryFailReason string

const (
	ArenaRecoveryFailReasonMissingResult      ArenaRecoveryFailReason = "missing_result"
	ArenaRecoveryFailReasonMissingScore       ArenaRecoveryFailReason = "missing_score"
	ArenaRecoveryFailReasonMissingPause       ArenaRecoveryFailReason = "missing_pause"
	ArenaRecoveryFailReasonMissingRevision    ArenaRecoveryFailReason = "missing_revision"
	ArenaRecoveryFailReasonMissingReservation ArenaRecoveryFailReason = "missing_reservation"
	ArenaRecoveryFailReasonMissingLease       ArenaRecoveryFailReason = "missing_lease"
	ArenaRecoveryFailReasonMissingDeadline    ArenaRecoveryFailReason = "missing_deadline"
	ArenaRecoveryFailReasonInvalidGraph       ArenaRecoveryFailReason = "invalid_graph"
	ArenaRecoveryFailReasonInvalidDAG         ArenaRecoveryFailReason = "invalid_dag"
	ArenaRecoveryFailReasonInvalidCursor      ArenaRecoveryFailReason = "invalid_cursor"
)

type ArenaRecoveryWorkKind string

const (
	ArenaRecoveryWorkGame        ArenaRecoveryWorkKind = "game"
	ArenaRecoveryWorkReadyWindow ArenaRecoveryWorkKind = "ready_window"
)

type ArenaRecoveryPauseKind string

const (
	ArenaRecoveryPauseTournament ArenaRecoveryPauseKind = "tournament"
	ArenaRecoveryPauseWave       ArenaRecoveryPauseKind = "wave"
	ArenaRecoveryPauseSeries     ArenaRecoveryPauseKind = "series"
	ArenaRecoveryPauseGame       ArenaRecoveryPauseKind = "game"
)

type ArenaRecoveryCursor struct {
	SchemaVersion      int
	TournamentID       uuid.UUID
	LastSequence       int64
	ProjectionRevision int64
	DerivedRevisionID  domain.ArenaDerivedRevisionID
}

type ArenaRecoverySeries struct {
	WaveID uuid.UUID
	Series domain.ArenaSeries
}

type ArenaRecoveryAssignment struct {
	WaveID     uuid.UUID
	SeriesID   uuid.UUID
	SlotID     uuid.UUID
	GameID     uuid.UUID
	Assignment domain.ArenaAssignment
}

type ArenaRecoveryResultEvidence struct {
	Artifact          domain.ArenaArtifactRef
	Reason            domain.ArenaGameResultReason
	WinnerID          *uuid.UUID
	RevisionID        domain.ArenaOfficialResultRevisionID
	DerivedRevisionID domain.ArenaDerivedRevisionID
}

type ArenaRecoveryScoreEvidence struct {
	SeriesID          uuid.UUID
	Score             domain.ArenaSeriesScore
	RevisionID        domain.ArenaSeriesScoreRevisionID
	DerivedRevisionID domain.ArenaDerivedRevisionID
}

type ArenaRecoveryPauseEvidence struct {
	Kind     ArenaRecoveryPauseKind
	EntityID uuid.UUID
	PausedAt time.Time
}

type ArenaRecoveryLease struct {
	TournamentID uuid.UUID
	LeaseID      uuid.UUID
	HolderID     uuid.UUID
	Revision     int64
	AcquiredAt   time.Time
	RenewedAt    time.Time
	ExpiresAt    time.Time
}

type ArenaRecoveryWork struct {
	Kind          ArenaRecoveryWorkKind
	TournamentID  uuid.UUID
	WaveID        uuid.UUID
	SeriesID      uuid.UUID
	SlotID        uuid.UUID
	GameID        uuid.UUID
	ReadyWindowID uuid.UUID
}

type ArenaRecoveryDeadlineEvidence struct {
	Work     ArenaRecoveryWork
	Deadline time.Time
}

type ArenaRecoveryGraph struct {
	TournamentID uuid.UUID
	Tournament   domain.ArenaTournament
	Roster       domain.ArenaRoster
	Waves        []domain.ArenaWave
	Series       []ArenaRecoverySeries
	Assignments  []ArenaRecoveryAssignment
	Results      []ArenaRecoveryResultEvidence
	Scores       []ArenaRecoveryScoreEvidence
	Pauses       []ArenaRecoveryPauseEvidence
	Revisions    domain.ArenaRevisionGraph
	Reservations []domain.ParticipantReservation
	Lease        *ArenaRecoveryLease
	Deadlines    []ArenaRecoveryDeadlineEvidence
	Cursor       ArenaRecoveryCursor
	LastSequence int64
}

type ArenaRecoveryRearmPlan struct {
	TournamentID uuid.UUID
	Cursor       ArenaRecoveryCursor
	Lease        ArenaRecoveryLease
	Work         []ArenaRecoveryDeadlineEvidence
}

type ArenaRecoveryResult struct {
	FailReason ArenaRecoveryFailReason
	Plan       ArenaRecoveryRearmPlan
	Rearmed    int
}

type ArenaRecoveryRearmer interface {
	RearmArenaRecovery(ctx context.Context, plan ArenaRecoveryRearmPlan) error
}

type ArenaRecoveryReconciler struct {
	rearmer ArenaRecoveryRearmer
	clock   Clock
}

func NewArenaRecoveryReconciler(
	rearmer ArenaRecoveryRearmer,
	clock Clock,
) *ArenaRecoveryReconciler {
	return &ArenaRecoveryReconciler{rearmer: rearmer, clock: clock}
}

func (r *ArenaRecoveryReconciler) Reconcile(
	ctx context.Context,
	graph ArenaRecoveryGraph,
) (ArenaRecoveryResult, error) {
	if r == nil || r.rearmer == nil || r.clock == nil {
		return ArenaRecoveryResult{}, domain.ErrValidation
	}
	now := r.clock.Now().Round(0).UTC()
	if now.IsZero() {
		return ArenaRecoveryResult{}, domain.ErrValidation
	}

	indexed, failReason := validateArenaRecoveryGraph(graph, now)
	if failReason != "" {
		return ArenaRecoveryResult{FailReason: failReason}, nil
	}
	plan := ArenaRecoveryRearmPlan{
		TournamentID: graph.TournamentID,
		Cursor:       graph.Cursor,
		Work:         indexed.deadlines,
	}
	if len(plan.Work) == 0 {
		return ArenaRecoveryResult{Plan: plan}, nil
	}
	plan.Lease = *graph.Lease
	if err := r.rearmer.RearmArenaRecovery(ctx, plan); err != nil {
		return ArenaRecoveryResult{}, fmt.Errorf("ArenaRecoveryReconciler - rearm: %w", err)
	}
	return ArenaRecoveryResult{Plan: plan, Rearmed: len(plan.Work)}, nil
}

type arenaRecoveryGame struct {
	waveID   uuid.UUID
	seriesID uuid.UUID
	slotID   uuid.UUID
	game     domain.ArenaGame
}

type arenaRecoveryIndex struct {
	participants map[uuid.UUID]domain.ArenaParticipant
	waves        map[uuid.UUID]domain.ArenaWave
	series       map[uuid.UUID]ArenaRecoverySeries
	games        map[uuid.UUID]arenaRecoveryGame
	results      map[domain.ArenaArtifactRef]ArenaRecoveryResultEvidence
	scores       map[uuid.UUID]ArenaRecoveryScoreEvidence
	pauses       map[[2]string]ArenaRecoveryPauseEvidence
	deadlines    []ArenaRecoveryDeadlineEvidence
}

func validateArenaRecoveryGraph(
	graph ArenaRecoveryGraph,
	now time.Time,
) (arenaRecoveryIndex, ArenaRecoveryFailReason) {
	indexed, ok := indexArenaRecoveryChildren(graph)
	if !ok {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonInvalidGraph
	}
	if !indexed.hasResults() {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonMissingResult
	}
	if !indexed.hasScores() {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonMissingScore
	}
	if !indexed.hasPauses(graph) {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonMissingPause
	}
	if !indexed.hasRevisions(graph) {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonMissingRevision
	}
	if !indexed.hasReservations(graph) {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonMissingReservation
	}
	expectedWork := indexed.expectedWork(graph)
	if len(expectedWork) > 0 && !validArenaRecoveryLease(graph.Lease, graph.TournamentID, now) {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonMissingLease
	}
	deadlines, deadlineStatus := matchArenaRecoveryDeadlines(graph.Deadlines, expectedWork)
	if deadlineStatus == arenaRecoveryEvidenceMissing {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonMissingDeadline
	}
	if deadlineStatus == arenaRecoveryEvidenceInvalid {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonInvalidGraph
	}
	indexed.deadlines = deadlines

	if !indexed.validDomainGraph(graph) {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonInvalidGraph
	}
	if !indexed.validRevisionDAG(graph) {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonInvalidDAG
	}
	if !validArenaRecoveryCursor(graph) {
		return arenaRecoveryIndex{}, ArenaRecoveryFailReasonInvalidCursor
	}
	return indexed, ""
}

//nolint:gocyclo // A single indexing pass rejects every cross-child identity before recovery side effects.
func indexArenaRecoveryChildren(graph ArenaRecoveryGraph) (arenaRecoveryIndex, bool) {
	indexed := arenaRecoveryIndex{
		participants: make(map[uuid.UUID]domain.ArenaParticipant, len(graph.Roster.Participants)),
		waves:        make(map[uuid.UUID]domain.ArenaWave, len(graph.Waves)),
		series:       make(map[uuid.UUID]ArenaRecoverySeries, len(graph.Series)),
		games:        make(map[uuid.UUID]arenaRecoveryGame),
		results:      make(map[domain.ArenaArtifactRef]ArenaRecoveryResultEvidence, len(graph.Results)),
		scores:       make(map[uuid.UUID]ArenaRecoveryScoreEvidence, len(graph.Scores)),
		pauses:       make(map[[2]string]ArenaRecoveryPauseEvidence, len(graph.Pauses)),
	}
	if graph.TournamentID == uuid.Nil || graph.Roster.TournamentID != graph.TournamentID ||
		graph.Roster.Validate() != nil {
		return arenaRecoveryIndex{}, false
	}
	for _, participant := range graph.Roster.Participants {
		indexed.participants[participant.ID] = participant
	}
	for _, wave := range graph.Waves {
		if wave.ID == uuid.Nil || wave.TournamentID != graph.TournamentID {
			return arenaRecoveryIndex{}, false
		}
		if _, exists := indexed.waves[wave.ID]; exists {
			return arenaRecoveryIndex{}, false
		}
		for _, member := range wave.Members {
			participant, exists := indexed.participants[member.ParticipantID]
			if !exists || participant.Attendance != domain.ArenaAttendanceStateCheckedIn {
				return arenaRecoveryIndex{}, false
			}
		}
		indexed.waves[wave.ID] = wave
	}
	for _, child := range graph.Series {
		series := child.Series
		wave, exists := indexed.waves[child.WaveID]
		if !exists || series.ID == uuid.Nil || series.TournamentID != graph.TournamentID {
			return arenaRecoveryIndex{}, false
		}
		if _, exists := indexed.series[series.ID]; exists ||
			!arenaRecoveryWaveHasParticipant(wave, series.FirstParticipantID) ||
			!arenaRecoveryWaveHasParticipant(wave, series.SecondParticipantID) {
			return arenaRecoveryIndex{}, false
		}
		indexed.series[series.ID] = child
		for _, slot := range series.Slots {
			for _, game := range slot.Attempts {
				if _, exists := indexed.games[game.ID]; exists {
					return arenaRecoveryIndex{}, false
				}
				indexed.games[game.ID] = arenaRecoveryGame{
					waveID: child.WaveID, seriesID: series.ID, slotID: slot.ID, game: game,
				}
			}
		}
	}
	if !indexed.indexAssignments(graph.Assignments) || !indexed.indexEvidence(graph) {
		return arenaRecoveryIndex{}, false
	}
	return indexed, true
}

func (i arenaRecoveryIndex) indexAssignments(assignments []ArenaRecoveryAssignment) bool {
	seenAssignments := make(map[uuid.UUID]struct{}, len(assignments))
	assignedGames := make(map[uuid.UUID]struct{}, len(assignments))
	for _, link := range assignments {
		game, exists := i.games[link.GameID]
		if !exists || game.waveID != link.WaveID || game.seriesID != link.SeriesID ||
			game.slotID != link.SlotID || link.Assignment.Validate() != nil ||
			link.Assignment.AttemptID() != link.GameID {
			return false
		}
		if _, exists := seenAssignments[link.Assignment.ID()]; exists {
			return false
		}
		if _, exists := assignedGames[link.GameID]; exists {
			return false
		}
		seenAssignments[link.Assignment.ID()] = struct{}{}
		assignedGames[link.GameID] = struct{}{}
	}
	for gameID, child := range i.games {
		if child.game.State != domain.ArenaGameStateActive &&
			child.game.State != domain.ArenaGameStatePaused {
			continue
		}
		if _, exists := assignedGames[gameID]; !exists {
			return false
		}
	}
	return true
}

func (i *arenaRecoveryIndex) indexEvidence(graph ArenaRecoveryGraph) bool {
	for _, evidence := range graph.Results {
		if evidence.Artifact.Validate() != nil ||
			(evidence.Artifact.Kind != domain.ArenaArtifactKindGameResult &&
				evidence.Artifact.Kind != domain.ArenaArtifactKindSeriesResult) {
			return false
		}
		if _, exists := i.results[evidence.Artifact]; exists {
			return false
		}
		i.results[evidence.Artifact] = evidence
	}
	for _, evidence := range graph.Scores {
		if evidence.SeriesID == uuid.Nil {
			return false
		}
		if _, exists := i.scores[evidence.SeriesID]; exists {
			return false
		}
		i.scores[evidence.SeriesID] = evidence
	}
	for _, evidence := range graph.Pauses {
		if !validArenaRecoveryPauseKind(evidence.Kind) || evidence.EntityID == uuid.Nil {
			return false
		}
		key := arenaRecoveryPauseKey(evidence.Kind, evidence.EntityID)
		if _, exists := i.pauses[key]; exists {
			return false
		}
		i.pauses[key] = evidence
	}
	return true
}

//nolint:gocyclo // Result evidence is checked exhaustively for terminal Games and Series in one pass.
func (i arenaRecoveryIndex) hasResults() bool {
	for _, child := range i.games {
		game := child.game
		if !game.State.IsTerminal() {
			continue
		}
		if game.ResultReason == "" || (game.State == domain.ArenaGameStateCompleted && game.WinnerID == nil) {
			return false
		}
		artifact := domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindGameResult, EntityID: game.ID}
		evidence, exists := i.results[artifact]
		if !exists || evidence.Reason != game.ResultReason || !sameArenaRecoveryWinner(evidence.WinnerID, game.WinnerID) {
			return false
		}
	}
	for seriesID, child := range i.series {
		series := child.Series
		if !series.State.IsTerminal() {
			continue
		}
		if series.State == domain.ArenaSeriesStateCompleted && series.WinnerID == nil {
			return false
		}
		artifact := domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesResult, EntityID: seriesID}
		evidence, exists := i.results[artifact]
		if !exists || evidence.Reason != "" || !sameArenaRecoveryWinner(evidence.WinnerID, series.WinnerID) {
			return false
		}
	}
	return len(i.results) == arenaRecoveryTerminalCount(i)
}

func (i arenaRecoveryIndex) hasScores() bool {
	if len(i.scores) != len(i.series) {
		return false
	}
	for seriesID, child := range i.series {
		evidence, exists := i.scores[seriesID]
		if !exists || evidence.Score != child.Series.Score {
			return false
		}
	}
	return true
}

func (i arenaRecoveryIndex) hasPauses(graph ArenaRecoveryGraph) bool {
	expected := 0
	if graph.Tournament.State == domain.ArenaTournamentStateTechnicalPause {
		expected++
		if graph.Tournament.PausedFromState == nil ||
			!i.validPause(ArenaRecoveryPauseTournament, graph.TournamentID, nil) {
			return false
		}
	}
	for _, wave := range graph.Waves {
		if wave.State != domain.ArenaWaveStatePaused {
			continue
		}
		expected++
		if wave.PausedAt == nil || !i.validPause(ArenaRecoveryPauseWave, wave.ID, wave.PausedAt) {
			return false
		}
	}
	for seriesID, child := range i.series {
		if child.Series.State != domain.ArenaSeriesStateTechnicalPause {
			continue
		}
		expected++
		if !i.validPause(ArenaRecoveryPauseSeries, seriesID, nil) {
			return false
		}
	}
	for gameID, child := range i.games {
		if child.game.State != domain.ArenaGameStatePaused {
			continue
		}
		expected++
		if !i.validPause(ArenaRecoveryPauseGame, gameID, nil) {
			return false
		}
	}
	return len(i.pauses) == expected
}

func (i arenaRecoveryIndex) validPause(
	kind ArenaRecoveryPauseKind,
	entityID uuid.UUID,
	want *time.Time,
) bool {
	evidence, exists := i.pauses[arenaRecoveryPauseKey(kind, entityID)]
	if !exists || evidence.PausedAt.IsZero() || evidence.PausedAt.Location() != time.UTC {
		return false
	}
	return want == nil || evidence.PausedAt.Equal(*want)
}

//nolint:gocyclo // Revision validation binds every persisted child and derived artifact before rearming.
func (i arenaRecoveryIndex) hasRevisions(graph ArenaRecoveryGraph) bool {
	for _, wave := range graph.Waves {
		if wave.RevisionID.IsZero() ||
			(wave.ReadyWindow != nil && wave.ReadyWindow.RevisionID.IsZero()) {
			return false
		}
	}
	for seriesID, child := range i.series {
		series := child.Series
		score := i.scores[seriesID]
		if series.CurrentScoreRevisionID == nil || series.CurrentScoreRevisionID.IsZero() ||
			score.RevisionID.IsZero() || score.DerivedRevisionID.IsZero() ||
			*series.CurrentScoreRevisionID != score.RevisionID {
			return false
		}
		if series.State.IsTerminal() &&
			(series.CurrentResultRevisionID == nil || series.CurrentResultRevisionID.IsZero()) {
			return false
		}
	}
	for _, child := range i.games {
		game := child.game
		if game.State.IsTerminal() && (game.ResultRevisionID == nil || game.ResultRevisionID.IsZero()) {
			return false
		}
	}
	for artifact, result := range i.results {
		if result.RevisionID.IsZero() || result.DerivedRevisionID.IsZero() {
			return false
		}
		switch artifact.Kind {
		case domain.ArenaArtifactKindGameResult:
			game := i.games[artifact.EntityID].game
			if game.ResultRevisionID == nil || *game.ResultRevisionID != result.RevisionID {
				return false
			}
		case domain.ArenaArtifactKindSeriesResult:
			series := i.series[artifact.EntityID].Series
			if series.CurrentResultRevisionID == nil || *series.CurrentResultRevisionID != result.RevisionID {
				return false
			}
		case domain.ArenaArtifactKindSeriesScore,
			domain.ArenaArtifactKindStandings,
			domain.ArenaArtifactKindGoldenGroup,
			domain.ArenaArtifactKindTopFour,
			domain.ArenaArtifactKindBracket,
			domain.ArenaArtifactKindChampion:
			return false
		}
	}
	return !graph.Cursor.DerivedRevisionID.IsZero()
}

func (i arenaRecoveryIndex) hasReservations(graph ArenaRecoveryGraph) bool {
	requiredPlayers := make(map[uuid.UUID]struct{}, len(i.participants))
	for _, participant := range i.participants {
		if participant.Attendance == domain.ArenaAttendanceStateCheckedIn {
			requiredPlayers[participant.PlayerID] = struct{}{}
		}
	}
	if len(graph.Reservations) != len(requiredPlayers) {
		return false
	}
	seenPlayers := make(map[uuid.UUID]struct{}, len(graph.Reservations))
	for _, reservation := range graph.Reservations {
		if !reservation.IsValid() || reservation.OwnerKind != domain.ParticipantReservationOwnerArena ||
			reservation.OwnerID != graph.TournamentID {
			return false
		}
		if _, exists := seenPlayers[reservation.PlayerID]; exists {
			return false
		}
		seenPlayers[reservation.PlayerID] = struct{}{}
	}
	for playerID := range requiredPlayers {
		if _, exists := seenPlayers[playerID]; !exists {
			return false
		}
	}
	return true
}

func (i arenaRecoveryIndex) expectedWork(graph ArenaRecoveryGraph) []ArenaRecoveryWork {
	work := make([]ArenaRecoveryWork, 0, len(i.games)+len(i.waves))
	for _, wave := range graph.Waves {
		if (wave.State != domain.ArenaWaveStateReadyWindowOpen &&
			wave.State != domain.ArenaWaveStateReady) || wave.ReadyWindow == nil {
			continue
		}
		work = append(work, ArenaRecoveryWork{
			Kind: ArenaRecoveryWorkReadyWindow, TournamentID: graph.TournamentID,
			WaveID: wave.ID, ReadyWindowID: wave.ReadyWindow.ID,
		})
	}
	for _, child := range i.games {
		if child.game.State != domain.ArenaGameStateActive {
			continue
		}
		work = append(work, ArenaRecoveryWork{
			Kind: ArenaRecoveryWorkGame, TournamentID: graph.TournamentID,
			WaveID: child.waveID, SeriesID: child.seriesID, SlotID: child.slotID,
			GameID: child.game.ID,
		})
	}
	sortArenaRecoveryWork(work)
	return work
}

type arenaRecoveryEvidenceStatus int

const (
	arenaRecoveryEvidenceValid arenaRecoveryEvidenceStatus = iota
	arenaRecoveryEvidenceMissing
	arenaRecoveryEvidenceInvalid
)

func matchArenaRecoveryDeadlines(
	evidence []ArenaRecoveryDeadlineEvidence,
	expected []ArenaRecoveryWork,
) ([]ArenaRecoveryDeadlineEvidence, arenaRecoveryEvidenceStatus) {
	byWork := make(map[ArenaRecoveryWork]ArenaRecoveryDeadlineEvidence, len(evidence))
	for _, item := range evidence {
		if !validArenaRecoveryWork(item.Work) || item.Deadline.IsZero() ||
			item.Deadline.Location() != time.UTC {
			return nil, arenaRecoveryEvidenceInvalid
		}
		if _, exists := byWork[item.Work]; exists {
			return nil, arenaRecoveryEvidenceInvalid
		}
		byWork[item.Work] = item
	}
	matched := make([]ArenaRecoveryDeadlineEvidence, 0, len(expected))
	for _, work := range expected {
		item, exists := byWork[work]
		if !exists {
			return nil, arenaRecoveryEvidenceMissing
		}
		matched = append(matched, item)
		delete(byWork, work)
	}
	if len(byWork) != 0 {
		return nil, arenaRecoveryEvidenceInvalid
	}
	return matched, arenaRecoveryEvidenceValid
}

func (i arenaRecoveryIndex) validDomainGraph(graph ArenaRecoveryGraph) bool {
	if graph.Tournament.Validate() != nil || graph.Roster.Validate() != nil {
		return false
	}
	for _, wave := range graph.Waves {
		if wave.Validate() != nil {
			return false
		}
	}
	for _, child := range graph.Series {
		if child.Series.Validate() != nil {
			return false
		}
	}
	return true
}

func (i arenaRecoveryIndex) validRevisionDAG(graph ArenaRecoveryGraph) bool {
	projections := graph.Revisions.Projections()
	dependencies := graph.Revisions.Dependencies()
	rebuilt, err := domain.NewArenaRevisionGraph(projections, dependencies)
	if err != nil {
		return false
	}
	for _, projection := range projections {
		if projection.Revision().TournamentID() != graph.TournamentID {
			return false
		}
	}
	for seriesID, evidence := range i.scores {
		artifact := domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindSeriesScore, EntityID: seriesID}
		current, exists := rebuilt.CurrentRevision(artifact)
		if !exists || current.Revision().ID() != evidence.DerivedRevisionID {
			return false
		}
	}
	for artifact, evidence := range i.results {
		current, exists := rebuilt.CurrentRevision(artifact)
		if !exists || current.Revision().ID() != evidence.DerivedRevisionID {
			return false
		}
	}
	return true
}

func validArenaRecoveryCursor(graph ArenaRecoveryGraph) bool {
	cursor := graph.Cursor
	if cursor.SchemaVersion != ArenaRecoveryCursorSchemaVersion ||
		cursor.TournamentID != graph.TournamentID || cursor.LastSequence < 1 ||
		cursor.LastSequence != graph.LastSequence || cursor.ProjectionRevision < 1 ||
		cursor.ProjectionRevision != cursor.LastSequence || cursor.DerivedRevisionID.IsZero() {
		return false
	}
	projections := graph.Revisions.Projections()
	if len(projections) == 0 {
		return false
	}
	sort.Slice(projections, func(left, right int) bool {
		leftRevision := projections[left].Revision()
		rightRevision := projections[right].Revision()
		if !leftRevision.CreatedAt().Equal(rightRevision.CreatedAt()) {
			return leftRevision.CreatedAt().Before(rightRevision.CreatedAt())
		}
		return leftRevision.ID().UUID().String() < rightRevision.ID().UUID().String()
	})
	return projections[len(projections)-1].Revision().ID() == cursor.DerivedRevisionID
}

func validArenaRecoveryLease(lease *ArenaRecoveryLease, tournamentID uuid.UUID, now time.Time) bool {
	return lease != nil && lease.TournamentID == tournamentID && lease.LeaseID != uuid.Nil &&
		lease.HolderID != uuid.Nil && lease.Revision >= 1 && !lease.AcquiredAt.IsZero() &&
		!lease.RenewedAt.Before(lease.AcquiredAt) && lease.ExpiresAt.After(lease.RenewedAt) &&
		lease.AcquiredAt.Location() == time.UTC && lease.RenewedAt.Location() == time.UTC &&
		lease.ExpiresAt.Location() == time.UTC && lease.ExpiresAt.After(now)
}

func validArenaRecoveryWork(work ArenaRecoveryWork) bool {
	if work.TournamentID == uuid.Nil || work.WaveID == uuid.Nil {
		return false
	}
	switch work.Kind {
	case ArenaRecoveryWorkGame:
		return work.SeriesID != uuid.Nil && work.SlotID != uuid.Nil && work.GameID != uuid.Nil &&
			work.ReadyWindowID == uuid.Nil
	case ArenaRecoveryWorkReadyWindow:
		return work.SeriesID == uuid.Nil && work.SlotID == uuid.Nil && work.GameID == uuid.Nil &&
			work.ReadyWindowID != uuid.Nil
	default:
		return false
	}
}

func validArenaRecoveryPauseKind(kind ArenaRecoveryPauseKind) bool {
	switch kind {
	case ArenaRecoveryPauseTournament,
		ArenaRecoveryPauseWave,
		ArenaRecoveryPauseSeries,
		ArenaRecoveryPauseGame:
		return true
	default:
		return false
	}
}

func sortArenaRecoveryWork(work []ArenaRecoveryWork) {
	sort.Slice(work, func(left, right int) bool {
		return arenaRecoveryWorkKey(work[left]) < arenaRecoveryWorkKey(work[right])
	})
}

func arenaRecoveryWorkKey(work ArenaRecoveryWork) string {
	return string(work.Kind) + "/" + work.WaveID.String() + "/" + work.SeriesID.String() +
		"/" + work.SlotID.String() + "/" + work.GameID.String() + "/" + work.ReadyWindowID.String()
}

func arenaRecoveryWaveHasParticipant(wave domain.ArenaWave, participantID uuid.UUID) bool {
	for _, member := range wave.Members {
		if member.ParticipantID == participantID {
			return true
		}
	}
	return false
}

func arenaRecoveryTerminalCount(indexed arenaRecoveryIndex) int {
	count := 0
	for _, child := range indexed.games {
		if child.game.State.IsTerminal() {
			count++
		}
	}
	for _, child := range indexed.series {
		if child.Series.State.IsTerminal() {
			count++
		}
	}
	return count
}

func arenaRecoveryPauseKey(kind ArenaRecoveryPauseKind, entityID uuid.UUID) [2]string {
	return [2]string{string(kind), entityID.String()}
}

func sameArenaRecoveryWinner(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
