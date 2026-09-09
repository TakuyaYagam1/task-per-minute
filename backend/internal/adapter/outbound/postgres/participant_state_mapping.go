package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

var ErrParticipantStateInvalid = errors.New("participant state: persisted read model is invalid")

type participantStateRoot struct {
	tournamentID            uuid.UUID
	tournamentState         domain.TournamentState
	rosterID                uuid.UUID
	rosterLocked            bool
	participantID           uuid.UUID
	playerID                uuid.UUID
	projectionRevision      int64
	participantViewRevision int64
	eventSequence           int64
	observedAt              time.Time
}

func participantStateRootFromRow(
	row sqlc.GetParticipantStateRootRow,
	query tournamentparticipant.StateQuery,
) (participantStateRoot, error) {
	state := domain.TournamentState(row.TournamentState)
	observedAt, ok := participantStateRequiredTime(row.ObservedAt)
	if row.TournamentID != query.TournamentID || row.PlayerID != query.PlayerID ||
		row.RosterID == uuid.Nil || row.ParticipantID == uuid.Nil || row.ProjectionRevisionID == uuid.Nil ||
		!state.IsValid() || row.ProjectionRevision < 1 || row.ParticipantViewRevision < 1 ||
		row.EventSequence < 0 || !ok {
		return participantStateRoot{}, participantStateInvalid("root")
	}
	return participantStateRoot{
		tournamentID:            row.TournamentID,
		tournamentState:         state,
		rosterID:                row.RosterID,
		rosterLocked:            row.RosterLocked,
		participantID:           row.ParticipantID,
		playerID:                row.PlayerID,
		projectionRevision:      row.ProjectionRevision,
		participantViewRevision: row.ParticipantViewRevision,
		eventSequence:           row.EventSequence,
		observedAt:              observedAt,
	}, nil
}

func participantLobbySeries(
	rows []sqlc.ListParticipantLobbySeriesRow,
	participantID uuid.UUID,
) ([]usecase.LobbySeriesView, error) {
	result := make([]usecase.LobbySeriesView, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		waveID, err := requiredParticipantStateUUID(row.WaveID)
		format := domain.SeriesFormat(row.Format)
		state := domain.SeriesState(row.State)
		if err != nil || row.SeriesID == uuid.Nil || row.ParticipantID != participantID ||
			row.OpponentID == uuid.Nil || row.OpponentID == participantID ||
			strings.TrimSpace(row.OpponentDisplayName) == "" || !format.IsValid() || !state.IsValid() {
			return nil, participantStateInvalid("lobby series")
		}
		if _, duplicate := seen[row.SeriesID]; duplicate {
			return nil, participantStateInvalid("duplicate lobby series")
		}
		seen[row.SeriesID] = struct{}{}
		result[index] = usecase.LobbySeriesView{
			SeriesID: row.SeriesID, WaveID: waveID, ParticipantID: row.ParticipantID,
			OpponentID: row.OpponentID, OpponentDisplayName: row.OpponentDisplayName,
			Format: format, State: state,
		}
	}
	return result, nil
}

func participantAssignmentFromRow(
	row sqlc.GetParticipantStateAssignmentRow,
) (usecase.TournamentParticipantAssignmentView, error) {
	waveID, err := requiredParticipantStateUUID(row.WaveID)
	if err != nil || row.TaskVersion < 1 || row.TimeLimit < 1 ||
		row.UndisclosedReserveCount < 0 || row.UndisclosedReserveCount > domain.AssignmentReserveCount ||
		row.UndisclosedReserveCount > math.MaxInt {
		return usecase.TournamentParticipantAssignmentView{}, participantStateInvalid("assignment identity")
	}
	hints := []string{}
	if err := json.Unmarshal(row.Hints, &hints); err != nil || !domain.IsValidTaskHints(hints) {
		return usecase.TournamentParticipantAssignmentView{}, participantStateInvalid("assignment hints")
	}
	deliveredAt, ok := participantStateRequiredTime(row.DeliveredAt)
	if !ok {
		return usecase.TournamentParticipantAssignmentView{}, participantStateInvalid("assignment receipt time")
	}
	assignment := usecase.TournamentParticipantAssignmentView{
		AssignmentID:  row.AssignmentID,
		AttemptID:     row.AttemptID,
		ParticipantID: row.ParticipantID,
		SeriesID:      row.SeriesID,
		GameID:        row.GameID,
		WaveID:        waveID,
		ActiveSnapshot: usecase.TaskSnapshotView{
			SnapshotID: row.SnapshotID, TaskID: row.TaskID, Version: int(row.TaskVersion),
			Kind: domain.AssignmentTaskKind(row.Kind), Title: row.Title, Description: row.Description,
			Category: domain.Category(row.Category), Difficulty: domain.Difficulty(row.Difficulty),
			TimeLimit: int(row.TimeLimit), Hints: append([]string(nil), hints...),
			TaskURL: cloneParticipantStateString(row.TaskUrl), SourceFileURL: cloneParticipantStateString(row.SourceFileUrl),
		},
		Receipt: domain.TaskDeliveryReceipt{
			ID: row.ReceiptID, AssignmentID: row.AssignmentID, AttemptID: row.AttemptID,
			ParticipantID: row.ParticipantID, InstanceID: row.InstanceID,
			SnapshotID: row.SnapshotID, TaskID: row.TaskID, DeliveredAt: deliveredAt,
		},
		UndisclosedReserveCount: int(row.UndisclosedReserveCount),
	}
	if err := validateParticipantStateAssignment(assignment); err != nil {
		return usecase.TournamentParticipantAssignmentView{}, err
	}
	return assignment, nil
}

func validateParticipantStateAssignment(assignment usecase.TournamentParticipantAssignmentView) error {
	snapshot := assignment.ActiveSnapshot
	if !validParticipantStateAssignmentIdentity(assignment) || assignment.Receipt.Validate() != nil ||
		!validParticipantStateTaskSnapshot(snapshot) {
		return participantStateInvalid("assignment")
	}
	return nil
}

func validParticipantStateAssignmentIdentity(
	assignment usecase.TournamentParticipantAssignmentView,
) bool {
	return assignment.AssignmentID != uuid.Nil && assignment.AttemptID != uuid.Nil &&
		assignment.ParticipantID != uuid.Nil && assignment.SeriesID != uuid.Nil &&
		assignment.GameID != uuid.Nil && assignment.WaveID != uuid.Nil
}

func validParticipantStateTaskSnapshot(snapshot usecase.TaskSnapshotView) bool {
	return snapshot.SnapshotID != uuid.Nil && snapshot.TaskID != uuid.Nil && snapshot.Version >= 1 &&
		snapshot.Kind.IsValid() && domain.IsValidTaskTitle(snapshot.Title) &&
		domain.IsValidTaskDescription(snapshot.Description) && snapshot.Category.IsValid() &&
		snapshot.Difficulty.IsValid() && domain.IsValidTaskTimeLimit(snapshot.TimeLimit) &&
		domain.IsValidTaskURLShape(snapshot.Category, snapshot.TaskURL, snapshot.SourceFileURL)
}

func participantSeriesFromRows(
	row sqlc.Series,
	graph []sqlc.ListParticipantStateSeriesGraphRow,
	root participantStateRoot,
) (domain.Series, error) {
	series := domain.Series{
		ID: row.ID, TournamentID: row.TournamentID,
		FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
		Format: domain.SeriesFormat(row.Format), State: domain.SeriesState(row.State),
		Score: domain.SeriesScore{
			FirstParticipantWins:  int(row.FirstParticipantWins),
			SecondParticipantWins: int(row.SecondParticipantWins),
		},
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		series.WinnerID = &winnerID
	}
	if row.CurrentScoreRevisionID.Valid {
		revisionID := domain.SeriesScoreRevisionID(row.CurrentScoreRevisionID.UUID)
		series.CurrentScoreRevisionID = &revisionID
	}
	if row.CurrentResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(row.CurrentResultRevisionID.UUID)
		series.CurrentResultRevisionID = &revisionID
	}
	if row.TournamentID != root.tournamentID || row.RosterID != root.rosterID ||
		(root.participantID != row.FirstParticipantID && root.participantID != row.SecondParticipantID) {
		return domain.Series{}, participantStateInvalid("series scope")
	}
	slots, err := participantSeriesSlots(row, graph)
	if err != nil {
		return domain.Series{}, err
	}
	series.Slots = slots
	if err := series.Validate(); err != nil {
		return domain.Series{}, participantStateInvalid("series")
	}
	return series, nil
}

func participantSeriesSlots(
	series sqlc.Series,
	graph []sqlc.ListParticipantStateSeriesGraphRow,
) ([]domain.GameSlot, error) {
	slots := make([]domain.GameSlot, 0, len(graph))
	for index := 0; index < len(graph); {
		slotRow := graph[index].GameSlot
		if slotRow.SeriesID != series.ID || slotRow.RosterID != series.RosterID {
			return nil, participantStateInvalid("series slot scope")
		}
		slot := domain.GameSlot{
			ID: slotRow.ID, SeriesID: slotRow.SeriesID, Position: int(slotRow.SlotNumber),
			Category: domain.Category(slotRow.Category),
			ScoreBefore: domain.SeriesScore{
				FirstParticipantWins:  int(slotRow.FirstParticipantWinsBefore),
				SecondParticipantWins: int(slotRow.SecondParticipantWinsBefore),
			},
		}
		for index < len(graph) && graph[index].GameSlot.ID == slotRow.ID {
			attemptRow := graph[index].GameAttempt
			if attemptRow.SlotID != slotRow.ID || attemptRow.SeriesID != series.ID ||
				attemptRow.RosterID != series.RosterID {
				return nil, participantStateInvalid("series attempt scope")
			}
			game, err := participantGameFromRow(attemptRow)
			if err != nil {
				return nil, err
			}
			slot.Attempts = append(slot.Attempts, game)
			index++
		}
		if err := slot.Validate(); err != nil {
			return nil, participantStateInvalid("series slot")
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

func participantGameFromRow(row sqlc.GameAttempt) (domain.Game, error) {
	game := domain.Game{
		ID: row.ID, SlotID: row.SlotID, AttemptNo: int(row.AttemptNumber),
		State: domain.GameState(row.State), ResultReason: domain.GameResultReason(stringValue(row.ResultReason)),
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		game.WinnerID = &winnerID
	}
	if row.ResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(row.ResultRevisionID.UUID)
		game.ResultRevisionID = &revisionID
	}
	if err := game.Validate(); err != nil {
		return domain.Game{}, participantStateInvalid("series game")
	}
	return game, nil
}

func participantWaveFromRows(
	row sqlc.GetParticipantStateWaveRow,
	rows []sqlc.ListParticipantStateWaveMembersRow,
	root participantStateRoot,
) (usecase.WaveView, error) {
	wave := domain.Wave{
		ID: row.WaveID, TournamentID: row.TournamentID,
		RevisionID: domain.WaveRevisionID(row.WaveRevisionID),
		State:      domain.WaveState(row.WaveState),
		StartedAt:  participantStateOptionalTime(row.StartedAt),
		PausedAt:   participantStateOptionalTime(row.PausedAt),
	}
	readyWindow, err := participantReadyWindowFromRow(row)
	if err != nil {
		return usecase.WaveView{}, err
	}
	wave.ReadyWindow = readyWindow
	readiness := make(map[uuid.UUID]int64, len(rows))
	seriesIDs := make(map[uuid.UUID]uuid.UUID, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	foundParticipant := false
	for _, member := range rows {
		seriesID, parseErr := requiredParticipantStateUUID(member.SeriesID)
		if parseErr != nil || member.ParticipantID == uuid.Nil || member.ReadinessRevision < 1 ||
			member.SeriesCount != 1 {
			return usecase.WaveView{}, participantStateInvalid("wave member")
		}
		if _, duplicate := seen[member.ParticipantID]; duplicate {
			return usecase.WaveView{}, participantStateInvalid("duplicate wave member")
		}
		seen[member.ParticipantID] = struct{}{}
		wave.Members = append(wave.Members, domain.WaveMember{
			ParticipantID: member.ParticipantID, Ready: member.Ready,
		})
		readiness[member.ParticipantID] = member.ReadinessRevision
		seriesIDs[member.ParticipantID] = seriesID
		foundParticipant = foundParticipant || member.ParticipantID == root.participantID
	}
	if row.TournamentID != root.tournamentID || row.WaveRevision < 1 || !foundParticipant || wave.Validate() != nil {
		return usecase.WaveView{}, participantStateInvalid("wave")
	}
	return usecase.WaveView{
		Wave: wave, Revision: row.WaveRevision,
		ReadinessRevisions: readiness, SeriesIDs: seriesIDs,
	}, nil
}

func participantReadyWindowFromRow(
	row sqlc.GetParticipantStateWaveRow,
) (*domain.ReadyWindow, error) {
	if !row.ReadyWindowID.Valid {
		if row.ReadyWindowRevisionID.Valid || row.ReadyWindowState != nil || row.OpenedAt.Valid ||
			row.Deadline.Valid || row.ConsumedAt.Valid {
			return nil, participantStateInvalid("ready window shape")
		}
		return nil, nil
	}
	openedAt, opened := participantStateRequiredTime(row.OpenedAt)
	deadline, hasDeadline := participantStateRequiredTime(row.Deadline)
	if !row.ReadyWindowRevisionID.Valid || row.ReadyWindowState == nil || !opened || !hasDeadline {
		return nil, participantStateInvalid("ready window evidence")
	}
	window := domain.ReadyWindow{
		ID: row.ReadyWindowID.UUID, WaveID: row.WaveID,
		RevisionID: domain.ReadyWindowRevisionID(row.ReadyWindowRevisionID.UUID),
		State:      domain.ReadyWindowState(*row.ReadyWindowState), OpenedAt: openedAt, Deadline: deadline,
		ConsumedAt: participantStateOptionalTime(row.ConsumedAt),
	}
	if window.Validate() != nil {
		return nil, participantStateInvalid("ready window")
	}
	return &window, nil
}

func validateParticipantStateAlignment(
	root participantStateRoot,
	view usecase.RecoveryView,
) error {
	if view.Assignment != nil {
		if view.Series == nil || view.Wave == nil || view.Assignment.SeriesID != view.Series.ID ||
			view.Assignment.WaveID != view.Wave.Wave.ID {
			return participantStateInvalid("assignment graph")
		}
	}
	if view.Draft != nil {
		if view.Series == nil || view.Draft.Execution.SeriesID != view.Series.ID ||
			(view.Draft.Execution.FirstParticipantID != root.participantID &&
				view.Draft.Execution.SecondParticipantID != root.participantID) {
			return participantStateInvalid("draft graph")
		}
	}
	if view.Series != nil && view.Wave != nil &&
		view.Wave.SeriesIDs[root.participantID] != view.Series.ID {
		return participantStateInvalid("wave series")
	}
	return nil
}

func participantStateRequiredTime(value pgtype.Timestamptz) (time.Time, bool) {
	if !value.Valid {
		return time.Time{}, false
	}
	result := value.Time.UTC()
	return result, domain.IsValidServerTime(result)
}

func participantStateOptionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func requiredParticipantStateUUID(value string) (uuid.UUID, error) {
	result, err := uuid.Parse(value)
	if err != nil || result == uuid.Nil {
		return uuid.Nil, ErrParticipantStateInvalid
	}
	return result, nil
}

func optionalParticipantStateUUID(value string) (*uuid.UUID, error) {
	if value == "" {
		return nil, nil
	}
	result, err := requiredParticipantStateUUID(value)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func cloneParticipantStateString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func participantStateInvalid(field string) error {
	return fmt.Errorf("%w: %s: %w", ErrParticipantStateInvalid, field, domain.ErrInternal)
}
