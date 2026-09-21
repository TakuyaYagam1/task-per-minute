package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

const (
	tournamentBracketStageSemifinal = "semifinal"
	tournamentBracketStageFinal     = "final"
)

var (
	ErrTournamentSnapshotInvalid = errors.New("tournament snapshot: persisted read model is invalid")

	_ usecase.TournamentSnapshotUseCase = (*TournamentSnapshotPostgres)(nil)
)

type TournamentSnapshotPostgres struct {
	tx *db.TxManager
}

type standingsPayloadDocument struct {
	Entries   []standingsPayloadEntry    `json:"entries"`
	TieGroups []standingsPayloadTieGroup `json:"tie_groups"`
}

type standingsPayloadEntry struct {
	ParticipantID uuid.UUID `json:"participant_id"`
	Position      int       `json:"position"`
	Points        int       `json:"points"`
	Wins          int       `json:"wins"`
	Losses        int       `json:"losses"`
	ByeCount      int       `json:"bye_count"`
	Buchholz      int       `json:"buchholz"`
	EffectiveTime int64     `json:"effective_time"`
}

type standingsPayloadTieGroup struct {
	PositionFrom   int         `json:"position_from"`
	PositionTo     int         `json:"position_to"`
	Impactful      bool        `json:"impactful"`
	ParticipantIDs []uuid.UUID `json:"participant_ids"`
}

type topFourPayloadDocument struct {
	Participants json.RawMessage `json:"participants"`
}

type topFourPayloadParticipant struct {
	Seed          int       `json:"seed"`
	ParticipantID uuid.UUID `json:"participant_id"`
}

type bracketPayloadDocument struct {
	Rounds []bracketPayloadMatch `json:"rounds"`
}

type bracketPayloadMatch struct {
	Stage               string    `json:"stage"`
	Position            int       `json:"position"`
	FirstParticipantID  uuid.UUID `json:"first_participant_id"`
	SecondParticipantID uuid.UUID `json:"second_participant_id"`
	State               string    `json:"state"`
	FirstWins           int       `json:"first_wins"`
	SecondWins          int       `json:"second_wins"`
}

func NewTournamentSnapshotPostgres(tx *db.TxManager) *TournamentSnapshotPostgres {
	return &TournamentSnapshotPostgres{tx: tx}
}

func (r *TournamentSnapshotPostgres) ParticipantSnapshot(
	ctx context.Context,
	query usecase.ParticipantSnapshotQuery,
) (usecase.ParticipantSnapshotView, error) {
	if ctx == nil || r == nil || r.tx == nil || query.TournamentID == uuid.Nil || query.PlayerID == uuid.Nil {
		return usecase.ParticipantSnapshotView{}, domain.ErrValidation
	}

	view := usecase.ParticipantSnapshotView{
		TournamentID: query.TournamentID,
		PlayerID:     query.PlayerID,
	}
	err := r.tx.ReadSnapshot(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		cursor, err := tournamentReadCursor(txCtx, querier, query.TournamentID)
		if err != nil {
			return err
		}
		view.Cursor = cursor
		participantID, err := querier.GetParticipantReadIdentity(txCtx, sqlc.GetParticipantReadIdentityParams{
			TournamentID: query.TournamentID,
			PlayerID:     query.PlayerID,
		})
		if err != nil {
			return tournamentSnapshotLookupError("ParticipantSnapshot - identity", err)
		}
		if participantID == uuid.Nil {
			return tournamentSnapshotInvalidError("participant identity")
		}
		view.ParticipantID = participantID

		assignment, err := participantReadAssignment(txCtx, querier, query)
		if err != nil {
			return err
		}
		view.Assignment = assignment
		if assignment != nil {
			view.Game, err = participantReadGame(txCtx, querier, query.TournamentID, assignment)
			if err != nil {
				return err
			}
		}
		view.Opponent, err = participantReadOpponent(txCtx, querier, query, assignment)
		return err
	})
	if err != nil {
		return usecase.ParticipantSnapshotView{}, err
	}
	return view, nil
}

func (r *TournamentSnapshotPostgres) PublicSnapshot(
	ctx context.Context,
	query usecase.PublicSnapshotQuery,
) (usecase.PublicSnapshotView, error) {
	if ctx == nil || r == nil || r.tx == nil || query.TournamentID == uuid.Nil {
		return usecase.PublicSnapshotView{}, domain.ErrValidation
	}
	if query.Cursor != nil && (query.Cursor.ProjectionRevision < 1 || query.Cursor.EventSequence < 0) {
		return usecase.PublicSnapshotView{}, domain.ErrValidation
	}

	view := usecase.PublicSnapshotView{
		Scoreboard:      []usecase.PublicScoreboardEntryView{},
		Bracket:         []usecase.PublicBracketMatchView{},
		LiveSeries:      []usecase.PublicSeriesView{},
		OfficialResults: []usecase.PublicOfficialResultView{},
	}
	err := r.tx.ReadSnapshot(ctx, func(txCtx context.Context) error {
		return r.loadPublicSnapshot(txCtx, query.TournamentID, query.Cursor, &view)
	})
	if err != nil {
		return usecase.PublicSnapshotView{}, err
	}
	return view, nil
}

func (r *TournamentSnapshotPostgres) OperatorSnapshot(
	ctx context.Context,
	query usecase.OperatorSnapshotQuery,
) (usecase.OperatorSnapshotView, error) {
	if ctx == nil || r == nil || r.tx == nil || query.TournamentID == uuid.Nil || query.OperatorID == uuid.Nil {
		return usecase.OperatorSnapshotView{}, domain.ErrValidation
	}

	view := usecase.OperatorSnapshotView{
		TournamentID: query.TournamentID,
		Waves:        []usecase.OperatorWaveView{},
		Presence:     []usecase.OperatorPresenceView{},
		Replays:      []usecase.OperatorReplayView{},
		AuditLinks:   []usecase.OperatorAuditLinkView{},
	}
	err := r.tx.ReadSnapshot(ctx, func(txCtx context.Context) error {
		return r.loadOperatorSnapshot(txCtx, query.TournamentID, &view)
	})
	if err != nil {
		return usecase.OperatorSnapshotView{}, err
	}
	return view, nil
}

func (r *TournamentSnapshotPostgres) loadPublicSnapshot(
	ctx context.Context,
	tournamentID uuid.UUID,
	requestedCursor *usecase.SnapshotCursor,
	view *usecase.PublicSnapshotView,
) error {
	querier := r.tx.Querier(ctx)
	cursor, err := tournamentReadCursor(ctx, querier, tournamentID)
	if err != nil {
		return err
	}
	if requestedCursor != nil {
		if err := publicSnapshotCursorConflict(tournamentID, *requestedCursor, cursor); err != nil {
			return err
		}
	}
	view.Cursor = cursor

	summary, err := querier.GetPublicTournamentReadSummary(ctx, tournamentID)
	if err != nil {
		return tournamentSnapshotLookupError("PublicSnapshot - summary", err)
	}
	view.Tournament = usecase.PublicTournamentView{
		TournamentID: summary.TournamentID,
		Preset:       summary.Preset,
		State:        summary.State,
		RosterSize:   int(summary.RosterSize),
		StartedAt:    utcNullableTime(summary.StartedAt),
		FinishedAt:   utcNullableTime(summary.FinishedAt),
	}

	payloads, err := querier.GetTournamentReadProjectionPayloads(ctx, tournamentID)
	if err != nil {
		return tournamentSnapshotLookupError("PublicSnapshot - projections", err)
	}
	participants, err := querier.ListTournamentReadParticipants(ctx, tournamentID)
	if err != nil {
		return fmt.Errorf("TournamentSnapshotPostgres - PublicSnapshot - participants: %w", err)
	}
	if len(participants) != int(summary.RosterSize) {
		return tournamentSnapshotInvalidError("public roster coverage")
	}
	names := tournamentParticipantNames(participants)
	view.Scoreboard, err = tournamentScoreboard(payloads.StandingsPayload, payloads.TopFourPayload, participants)
	if err != nil {
		return err
	}
	view.Bracket, err = tournamentBracket(payloads.BracketPayload, names)
	if err != nil {
		return err
	}
	if view.LiveSeries, err = publicTournamentReadSeries(ctx, querier, tournamentID); err != nil {
		return err
	}
	if view.OfficialResults, err = publicTournamentReadResults(ctx, querier, tournamentID); err != nil {
		return err
	}
	view.Draft, err = publicTournamentReadDraft(ctx, querier, tournamentID)
	return err
}

func publicSnapshotCursorConflict(
	tournamentID uuid.UUID,
	requested usecase.SnapshotCursor,
	current usecase.SnapshotCursor,
) error {
	if requested.ProjectionRevision <= current.ProjectionRevision && requested.EventSequence <= current.EventSequence {
		return nil
	}
	return &usecase.PublicSnapshotCursorConflictError{
		TournamentID:                tournamentID,
		RequestedProjectionRevision: requested.ProjectionRevision,
		RequestedEventSequence:      requested.EventSequence,
		CurrentProjectionRevision:   current.ProjectionRevision,
		CurrentEventSequence:        current.EventSequence,
	}
}

func (r *TournamentSnapshotPostgres) loadOperatorSnapshot(
	ctx context.Context,
	tournamentID uuid.UUID,
	view *usecase.OperatorSnapshotView,
) error {
	querier := r.tx.Querier(ctx)
	cursor, err := tournamentReadCursor(ctx, querier, tournamentID)
	if err != nil {
		return err
	}
	view.Cursor = cursor

	if view.Waves, err = operatorTournamentReadWaves(ctx, querier, tournamentID); err != nil {
		return err
	}
	if view.Presence, err = operatorTournamentReadPresence(ctx, querier, tournamentID); err != nil {
		return err
	}
	if view.Replays, err = operatorTournamentReadReplays(ctx, querier, tournamentID); err != nil {
		return err
	}
	if view.Pause, err = operatorTournamentReadPause(ctx, querier, tournamentID); err != nil {
		return err
	}
	if view.Pause != nil {
		if err := operatorTournamentReadPauseGame(ctx, querier, view.Pause); err != nil {
			return err
		}
	}
	view.AuditLinks, err = operatorTournamentReadAuditLinks(ctx, querier, tournamentID)
	return err
}

//nolint:gocyclo // Snapshot hydration validates every nullable pause-clock combination fail-closed.
func participantReadGame(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	assignment *usecase.ParticipantAssignmentView,
) (*usecase.ParticipantGameView, error) {
	if assignment == nil || assignment.GameID == uuid.Nil || assignment.SeriesID == uuid.Nil {
		return nil, tournamentSnapshotInvalidError("game assignment")
	}
	row, err := querier.GetParticipantReadGame(ctx, sqlc.GetParticipantReadGameParams{
		TournamentID: tournamentID,
		SeriesID:     assignment.SeriesID,
		GameID:       assignment.GameID,
	})
	if err != nil {
		return nil, tournamentSnapshotLookupError("ParticipantSnapshot - game", err)
	}
	if row.GameID != assignment.GameID || row.Revision < 1 || !domain.GameState(row.State).IsValid() ||
		strings.TrimSpace(row.State) != row.State {
		return nil, tournamentSnapshotInvalidError("game")
	}
	game := &usecase.ParticipantGameView{
		GameID:           row.GameID,
		State:            row.State,
		Revision:         row.Revision,
		WinnerID:         participantNullableUUID(row.WinnerID),
		ResultRevisionID: participantNullableUUID(row.ResultRevisionID),
		Presence:         []usecase.ParticipantPresenceView{},
		Reconnect:        []usecase.ParticipantReconnectView{},
	}
	if row.ResultReason != nil {
		game.ResultReason = *row.ResultReason
	}
	game.Presence, err = participantReadPresence(ctx, querier, tournamentID, assignment.SeriesID)
	if err != nil {
		return nil, err
	}
	game.Reconnect, err = participantReadReconnect(ctx, querier, tournamentID, assignment.SeriesID, assignment.GameID)
	if err != nil {
		return nil, err
	}
	if row.PauseID == uuid.Nil {
		if row.PauseState != "" || row.PauseReason != "" || row.FrozenAt.Valid || row.FrozenRemainingMs != nil ||
			row.ResumedAt.Valid || row.ResumedDeadline.Valid || row.ReconnectDeadline.Valid {
			return nil, tournamentSnapshotInvalidError("game pause")
		}
		return game, nil
	}
	if row.PauseState != "active" && row.PauseState != "resumed" && row.PauseState != "cancelled" ||
		strings.TrimSpace(row.PauseState) != row.PauseState || !validParticipantPauseReason(row.PauseReason) || !row.FrozenAt.Valid ||
		!validServerTime(row.FrozenAt.Time.UTC()) || row.FrozenRemainingMs == nil || *row.FrozenRemainingMs <= 0 {
		return nil, tournamentSnapshotInvalidError("game pause")
	}
	resumedAt := utcNullableTime(row.ResumedAt)
	resumedDeadline := utcNullableTime(row.ResumedDeadline)
	if (resumedAt == nil) != (resumedDeadline == nil) ||
		(resumedAt != nil && (!validServerTime(*resumedAt) || !validServerTime(*resumedDeadline))) {
		return nil, tournamentSnapshotInvalidError("game pause resume")
	}
	reconnectDeadline := utcNullableTime(row.ReconnectDeadline)
	if reconnectDeadline != nil && !validServerTime(*reconnectDeadline) {
		return nil, tournamentSnapshotInvalidError("reconnect deadline")
	}
	game.Pause = &usecase.ParticipantGamePauseView{
		PauseID:           row.PauseID,
		State:             row.PauseState,
		Reason:            row.PauseReason,
		FrozenAt:          row.FrozenAt.Time.UTC(),
		FrozenRemainingMS: *row.FrozenRemainingMs,
		ResumedAt:         resumedAt,
		ResumedDeadline:   resumedDeadline,
		ReconnectDeadline: reconnectDeadline,
	}
	return game, nil
}

func participantReadPresence(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
) ([]usecase.ParticipantPresenceView, error) {
	rows, err := querier.ListParticipantReadPresence(ctx, sqlc.ListParticipantReadPresenceParams{
		TournamentID: tournamentID,
		SeriesID:     seriesID,
	})
	if err != nil {
		return nil, tournamentSnapshotLookupError("ParticipantSnapshot - presence", err)
	}
	result := make([]usecase.ParticipantPresenceView, len(rows))
	for index, row := range rows {
		connectedAt := utcNullableTime(row.ConnectedAt)
		updatedAt := utcNullableTime(row.UpdatedAt)
		disconnectedAt := utcNullableTime(row.DisconnectedAt)
		if row.ParticipantID == uuid.Nil || (row.State != "connected" && row.State != "disconnected") ||
			row.PresenceEpoch < 1 || row.Revision < 1 || connectedAt == nil || updatedAt == nil ||
			(row.State == "connected") != (disconnectedAt == nil) ||
			(disconnectedAt != nil && !validServerTime(*disconnectedAt)) {
			return nil, tournamentSnapshotInvalidError("participant presence")
		}
		result[index] = usecase.ParticipantPresenceView{
			ParticipantID:  row.ParticipantID,
			State:          row.State,
			PresenceEpoch:  row.PresenceEpoch,
			Revision:       row.Revision,
			ConnectedAt:    *connectedAt,
			DisconnectedAt: disconnectedAt,
			UpdatedAt:      *updatedAt,
		}
	}
	return result, nil
}

func participantReadReconnect(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
	gameID uuid.UUID,
) ([]usecase.ParticipantReconnectView, error) {
	rows, err := querier.ListParticipantReadReconnect(ctx, sqlc.ListParticipantReadReconnectParams{
		TournamentID: tournamentID,
		SeriesID:     seriesID,
		GameID:       gameID,
	})
	if err != nil {
		return nil, tournamentSnapshotLookupError("ParticipantSnapshot - reconnect", err)
	}
	result := make([]usecase.ParticipantReconnectView, len(rows))
	for index, row := range rows {
		mapped, mapErr := participantReadReconnectRow(row)
		if mapErr != nil {
			return nil, mapErr
		}
		result[index] = mapped
	}
	return result, nil
}

func participantReadReconnectRow(
	row sqlc.ListParticipantReadReconnectRow,
) (usecase.ParticipantReconnectView, error) {
	openedAt := utcNullableTime(row.OpenedAt)
	deadline := utcNullableTime(row.DeadlineAt)
	closedAt := utcNullableTime(row.ClosedAt)
	updatedAt := utcNullableTime(row.UpdatedAt)
	if row.ID == uuid.Nil || row.PauseID == uuid.Nil || row.ParticipantID == uuid.Nil ||
		row.PresenceEpoch < 1 || row.IntervalNumber < 1 || row.ContinuationNumber < 0 ||
		!validParticipantReconnectState(row.State) || openedAt == nil || deadline == nil || updatedAt == nil ||
		!deadline.After(*openedAt) || (row.State == "open") != (closedAt == nil) ||
		(closedAt != nil && !validServerTime(*closedAt)) {
		return usecase.ParticipantReconnectView{}, tournamentSnapshotInvalidError("participant reconnect")
	}
	return usecase.ParticipantReconnectView{
		ID:                 row.ID,
		PauseID:            row.PauseID,
		ParticipantID:      row.ParticipantID,
		PresenceEpoch:      row.PresenceEpoch,
		Number:             int(row.IntervalNumber),
		ContinuationNumber: int(row.ContinuationNumber),
		ContinuedFromID:    participantNullableUUID(row.ContinuedFromID),
		SuspendedByPauseID: participantNullableUUID(row.SuspendedByPauseID),
		State:              row.State,
		OpenedAt:           *openedAt,
		Deadline:           *deadline,
		ClosedAt:           closedAt,
		Revision:           row.Revision,
		UpdatedAt:          *updatedAt,
	}, nil
}

func participantNullableUUID(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	result := value.UUID
	return &result
}

func validParticipantPauseReason(value string) bool {
	switch value {
	case "operator", "disconnect", "platform", "execution_epoch":
		return true
	default:
		return false
	}
}

func validParticipantReconnectState(value string) bool {
	switch value {
	case "open", "reconnected", "expired", "cancelled":
		return true
	default:
		return false
	}
}

func operatorTournamentReadPauseGame(
	ctx context.Context,
	querier *sqlc.Queries,
	pause *usecase.OperatorPauseView,
) error {
	if pause == nil || pause.PauseID == uuid.Nil {
		return tournamentSnapshotInvalidError("operator pause")
	}
	row, err := querier.GetOperatorTournamentReadPauseGame(ctx, pause.PauseID)
	if err != nil {
		return tournamentSnapshotLookupError("OperatorSnapshot - pause game", err)
	}
	if row.GameID.Valid {
		if row.GameID.UUID == uuid.Nil || row.FrozenRemainingMs == nil || *row.FrozenRemainingMs <= 0 {
			return tournamentSnapshotInvalidError("operator pause game clock")
		}
		gameID := row.GameID.UUID
		remaining := *row.FrozenRemainingMs
		pause.GameID = &gameID
		pause.FrozenRemainingMS = &remaining
	} else if row.FrozenRemainingMs != nil || row.ReconnectDeadline.Valid {
		return tournamentSnapshotInvalidError("operator pause fields")
	}
	deadline := utcNullableTime(row.ReconnectDeadline)
	if deadline != nil && !validServerTime(*deadline) {
		return tournamentSnapshotInvalidError("operator reconnect deadline")
	}
	pause.ReconnectDeadline = deadline
	return nil
}

func tournamentReadCursor(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (usecase.SnapshotCursor, error) {
	row, err := querier.GetTournamentReadCursor(ctx, tournamentID)
	if err != nil {
		return usecase.SnapshotCursor{}, tournamentSnapshotLookupError("cursor", err)
	}
	if row.ProjectionRevision < 1 || row.EventSequence < 0 || !row.ObservedAt.Valid {
		return usecase.SnapshotCursor{}, tournamentSnapshotInvalidError("cursor")
	}
	return usecase.SnapshotCursor{
		ProjectionRevision: row.ProjectionRevision,
		EventSequence:      row.EventSequence,
		ObservedAt:         row.ObservedAt.Time.UTC(),
	}, nil
}
