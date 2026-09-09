package recovery

import (
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateRecoveryGraph(
	graph RecoveryGraph,
	now time.Time,
) (recoveryIndex, RecoveryFailReason) {
	indexed, ok := indexRecoveryChildren(graph)
	if !ok {
		return recoveryIndex{}, RecoveryFailReasonInvalidGraph
	}
	if !indexed.hasResults() {
		return recoveryIndex{}, RecoveryFailReasonMissingResult
	}
	if !indexed.hasScores() {
		return recoveryIndex{}, RecoveryFailReasonMissingScore
	}
	if !indexed.hasPauses(graph) {
		return recoveryIndex{}, RecoveryFailReasonMissingPause
	}
	if !indexed.hasRevisions(graph) {
		return recoveryIndex{}, RecoveryFailReasonMissingRevision
	}
	if !indexed.hasReservations(graph) {
		return recoveryIndex{}, RecoveryFailReasonMissingReservation
	}
	expectedWork := indexed.expectedWork(graph)
	if len(expectedWork) > 0 && !validRecoveryLease(graph.Lease, graph.TournamentID, now) {
		return recoveryIndex{}, RecoveryFailReasonMissingLease
	}
	deadlines, deadlineStatus := matchRecoveryDeadlines(graph.Deadlines, expectedWork)
	if deadlineStatus == recoveryEvidenceMissing {
		return recoveryIndex{}, RecoveryFailReasonMissingDeadline
	}
	if deadlineStatus == recoveryEvidenceInvalid {
		return recoveryIndex{}, RecoveryFailReasonInvalidGraph
	}
	indexed.deadlines = deadlines

	if !indexed.validDomainGraph(graph) {
		return recoveryIndex{}, RecoveryFailReasonInvalidGraph
	}
	if !indexed.validRevisionDAG(graph) {
		return recoveryIndex{}, RecoveryFailReasonInvalidDAG
	}
	if !validRecoveryCursor(graph) {
		return recoveryIndex{}, RecoveryFailReasonInvalidCursor
	}
	return indexed, ""
}

//nolint:gocyclo // Result evidence is checked exhaustively for terminal Games and Series in one pass.
func (i recoveryIndex) hasResults() bool {
	for _, child := range i.games {
		game := child.game
		if !game.State.IsTerminal() {
			continue
		}
		if game.ResultReason == "" || (game.State == domain.GameStateCompleted && game.WinnerID == nil) {
			return false
		}
		artifact := domain.ArtifactRef{Kind: domain.ArtifactKindGameResult, EntityID: game.ID}
		evidence, exists := i.results[artifact]
		if !exists || evidence.Reason != game.ResultReason || !sameRecoveryWinner(evidence.WinnerID, game.WinnerID) {
			return false
		}
	}
	for seriesID, child := range i.series {
		series := child.Series
		if !series.State.IsTerminal() {
			continue
		}
		if series.State == domain.SeriesStateCompleted && series.WinnerID == nil {
			return false
		}
		artifact := domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: seriesID}
		evidence, exists := i.results[artifact]
		if !exists || evidence.Reason != "" || !sameRecoveryWinner(evidence.WinnerID, series.WinnerID) {
			return false
		}
	}
	return len(i.results) == recoveryTerminalCount(i)
}

func (i recoveryIndex) hasScores() bool {
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

func (i recoveryIndex) hasPauses(graph RecoveryGraph) bool {
	expected := 0
	if graph.Tournament.State == domain.TournamentStateTechnicalPause {
		expected++
		if graph.Tournament.PausedFromState == nil ||
			!i.validPause(RecoveryPauseTournament, graph.TournamentID, nil) {
			return false
		}
	}
	for _, wave := range graph.Waves {
		if wave.State != domain.WaveStatePaused {
			continue
		}
		expected++
		if wave.PausedAt == nil || !i.validPause(RecoveryPauseWave, wave.ID, wave.PausedAt) {
			return false
		}
	}
	for seriesID, child := range i.series {
		if child.Series.State != domain.SeriesStateTechnicalPause {
			continue
		}
		expected++
		if !i.validPause(RecoveryPauseSeries, seriesID, nil) {
			return false
		}
	}
	for gameID, child := range i.games {
		if child.game.State != domain.GameStatePaused {
			continue
		}
		expected++
		if !i.validPause(RecoveryPauseGame, gameID, nil) {
			return false
		}
	}
	return len(i.pauses) == expected
}

func (i recoveryIndex) validPause(
	kind RecoveryPauseKind,
	entityID uuid.UUID,
	want *time.Time,
) bool {
	evidence, exists := i.pauses[recoveryPauseKey(kind, entityID)]
	if !exists || evidence.PausedAt.IsZero() || evidence.PausedAt.Location() != time.UTC {
		return false
	}
	return want == nil || evidence.PausedAt.Equal(*want)
}

//nolint:gocyclo // Revision validation binds every persisted child and derived artifact before rearming.
func (i recoveryIndex) hasRevisions(graph RecoveryGraph) bool {
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
		case domain.ArtifactKindGameResult:
			game := i.games[artifact.EntityID].game
			if game.ResultRevisionID == nil || *game.ResultRevisionID != result.RevisionID {
				return false
			}
		case domain.ArtifactKindSeriesResult:
			series := i.series[artifact.EntityID].Series
			if series.CurrentResultRevisionID == nil || *series.CurrentResultRevisionID != result.RevisionID {
				return false
			}
		case domain.ArtifactKindSeriesScore,
			domain.ArtifactKindStandings,
			domain.ArtifactKindGoldenGroup,
			domain.ArtifactKindTopFour,
			domain.ArtifactKindBracket,
			domain.ArtifactKindChampion:
			return false
		}
	}
	return !graph.Cursor.DerivedRevisionID.IsZero()
}

func (i recoveryIndex) hasReservations(graph RecoveryGraph) bool {
	requiredPlayers := make(map[uuid.UUID]struct{}, len(i.participants))
	for _, participant := range i.participants {
		if participant.Attendance == domain.AttendanceStateCheckedIn {
			requiredPlayers[participant.PlayerID] = struct{}{}
		}
	}
	if len(graph.Reservations) != len(requiredPlayers) {
		return false
	}
	seenPlayers := make(map[uuid.UUID]struct{}, len(graph.Reservations))
	for _, reservation := range graph.Reservations {
		if !reservation.IsValid() || reservation.TournamentID != graph.TournamentID {
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

func (i recoveryIndex) validDomainGraph(graph RecoveryGraph) bool {
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

func (i recoveryIndex) validRevisionDAG(graph RecoveryGraph) bool {
	projections := graph.Revisions.Projections()
	dependencies := graph.Revisions.Dependencies()
	rebuilt, err := domain.NewRevisionGraph(projections, dependencies)
	if err != nil {
		return false
	}
	for _, projection := range projections {
		if projection.Revision().TournamentID() != graph.TournamentID {
			return false
		}
	}
	for seriesID, evidence := range i.scores {
		artifact := domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: seriesID}
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

func validRecoveryCursor(graph RecoveryGraph) bool {
	cursor := graph.Cursor
	if cursor.SchemaVersion != RecoveryCursorSchemaVersion ||
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

func validRecoveryLease(lease *RecoveryLease, tournamentID uuid.UUID, now time.Time) bool {
	return lease != nil && lease.TournamentID == tournamentID && lease.LeaseID != uuid.Nil &&
		lease.HolderID != uuid.Nil && lease.Revision >= 1 && !lease.AcquiredAt.IsZero() &&
		!lease.RenewedAt.Before(lease.AcquiredAt) && lease.ExpiresAt.After(lease.RenewedAt) &&
		lease.AcquiredAt.Location() == time.UTC && lease.RenewedAt.Location() == time.UTC &&
		lease.ExpiresAt.Location() == time.UTC && lease.ExpiresAt.After(now)
}

func validRecoveryWork(work RecoveryWork) bool {
	if work.TournamentID == uuid.Nil || work.WaveID == uuid.Nil {
		return false
	}
	switch work.Kind {
	case RecoveryWorkGame:
		return work.SeriesID != uuid.Nil && work.SlotID != uuid.Nil && work.GameID != uuid.Nil &&
			work.ReadyWindowID == uuid.Nil
	case RecoveryWorkReadyWindow:
		return work.SeriesID == uuid.Nil && work.SlotID == uuid.Nil && work.GameID == uuid.Nil &&
			work.ReadyWindowID != uuid.Nil
	default:
		return false
	}
}

func validRecoveryPauseKind(kind RecoveryPauseKind) bool {
	switch kind {
	case RecoveryPauseTournament,
		RecoveryPauseWave,
		RecoveryPauseSeries,
		RecoveryPauseGame:
		return true
	default:
		return false
	}
}
