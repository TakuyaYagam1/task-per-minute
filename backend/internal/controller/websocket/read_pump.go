package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

type flagSubmitPayload struct {
	DuelID *uuid.UUID `json:"duel_id,omitempty"`
	Flag   string     `json:"flag"`
}

type surrenderPayload struct {
	DuelID *uuid.UUID `json:"duel_id,omitempty"`
}

func (s *Server) readPump(ctx context.Context, c *client) {
	for {
		msgType, reader, err := c.conn.Reader(ctx)
		if err != nil {
			return
		}
		if msgType != coderws.MessageText {
			_, _ = io.Copy(io.Discard, reader)
			_ = c.sendError(ErrorInvalidPayload, "message must be text")
			continue
		}

		var event IncomingEvent
		if err := json.NewDecoder(reader).Decode(&event); err != nil {
			_ = c.sendError(ErrorInvalidJSON, "invalid json")
			continue
		}
		s.routeEvent(ctx, c, event)
	}
}

func (s *Server) routeEvent(ctx context.Context, c *client, event IncomingEvent) {
	if !s.ensureSessionFresh(ctx, c) {
		return
	}

	switch event.Type {
	case EventJoinQueue:
		s.handleJoinQueue(ctx, c)
	case EventLeaveQueue:
		s.handleLeaveQueue(ctx, c)
	case EventFlagSubmit:
		s.handleFlagSubmit(ctx, c, event.Payload)
	case EventSurrender:
		s.handleSurrender(ctx, c, event.Payload)
	case EventPing:
		_ = c.sendEvent(EventPong, nil)
	default:
		_ = c.sendError(ErrorUnknownEvent, "unknown event type")
	}
}

func (s *Server) ensureSessionFresh(ctx context.Context, c *client) bool {
	if c.sessionToken == uuid.Nil {
		_ = c.sendError(string(apperr.CodeInvalidSession), apperr.ErrInvalidSession.Message)
		time.AfterFunc(10*time.Millisecond, c.Close)
		return false
	}
	player, err := s.players.GetBySessionToken(ctx, c.sessionToken)
	if err != nil || player == nil || player.ID != c.player.ID {
		_ = c.sendError(string(apperr.CodeInvalidSession), apperr.ErrInvalidSession.Message)
		time.AfterFunc(10*time.Millisecond, c.Close)
		return false
	}
	return true
}

func (s *Server) handleJoinQueue(ctx context.Context, c *client) {
	if s.matchmaking == nil {
		_ = c.sendError(ErrorInternal, "matchmaking is not configured")
		return
	}

	result, err := s.matchmaking.JoinQueue(ctx, c.player.ID)
	if err != nil {
		s.sendAppError(c, err)
		return
	}
	if result == nil {
		c.setQueued(true)
		_ = c.sendEvent(EventQueueJoined, nil)
		return
	}

	_ = c.sendEvent(EventQueueJoined, nil)
	s.publishMatch(ctx, result)
}

func (s *Server) handleLeaveQueue(ctx context.Context, c *client) {
	if s.matchmaking == nil {
		_ = c.sendError(ErrorInternal, "matchmaking is not configured")
		return
	}
	if err := s.matchmaking.LeaveQueue(ctx, c.player.ID); err != nil {
		s.sendAppError(c, err)
		return
	}
	c.setQueued(false)
	_ = c.sendEvent(EventQueueLeft, nil)
}

func (s *Server) handleFlagSubmit(ctx context.Context, c *client, raw json.RawMessage) {
	if s.flags == nil {
		_ = c.sendError(ErrorInternal, "flag submit is not configured")
		return
	}

	var payload flagSubmitPayload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			_ = c.sendError(ErrorInvalidPayload, "invalid flag_submit payload")
			return
		}
	}
	duelID, ok := c.currentDuel()
	if payload.DuelID != nil {
		duelID, ok = *payload.DuelID, true
	}
	if !ok || payload.Flag == "" {
		_ = c.sendError(ErrorInvalidPayload, "duel_id and flag are required")
		return
	}
	if s.reconnect != nil && s.reconnect.DuelPaused(duelID) {
		_ = c.sendError(ErrorDuelPaused, "duel is paused while a player reconnects")
		return
	}

	result, err := s.flags.SubmitFlag(ctx, duelID, c.player.ID, payload.Flag)
	if err != nil {
		if errors.Is(err, apperr.ErrFlagIncorrect) {
			_ = c.sendEvent(EventFlagResult, FlagResultPayload{
				DuelID:  duelID,
				Correct: false,
				Message: "incorrect flag",
			})
			return
		}
		s.sendAppError(c, err)
		return
	}
	if result.AlreadyFinished {
		return
	}
	if !result.Correct || result.FinishedDuel == nil {
		_ = c.sendEvent(EventFlagResult, FlagResultPayload{
			DuelID:  duelID,
			Correct: false,
			Message: "incorrect flag",
		})
		return
	}

	_ = c.sendEvent(EventFlagResult, FlagResultPayload{
		DuelID:  result.FinishedDuel.ID,
		Correct: true,
		Message: "correct flag",
	})
	s.publishOpponentSolved(result.FinishedDuel, c.player.ID)
	s.publishDuelFinished(ctx, result.FinishedDuel, &c.player.ID)
}

func (s *Server) handleSurrender(ctx context.Context, c *client, raw json.RawMessage) {
	if s.reconnect == nil {
		_ = c.sendError(ErrorInternal, "surrender is not configured")
		return
	}

	var payload surrenderPayload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			_ = c.sendError(ErrorInvalidPayload, "invalid surrender payload")
			return
		}
	}
	duelID, ok := c.currentDuel()
	if payload.DuelID != nil {
		duelID, ok = *payload.DuelID, true
	}
	if !ok {
		_ = c.sendError(ErrorInvalidPayload, "duel_id is required")
		return
	}

	if _, err := s.reconnect.FinalizePlayerForfeit(ctx, duelID, c.player.ID); err != nil {
		s.sendAppError(c, err)
		return
	}
}

func (s *Server) publishMatch(ctx context.Context, result *duelusecase.MatchResult) {
	duel := result.Duel
	if duel == nil {
		return
	}

	//nolint:contextcheck // Duel hubs outlive the single request context that created the match.
	s.hubs.Create(s.ctx, duel.ID)
	assignments := map[uuid.UUID]TaskAssignedPayload{
		duel.Player1ID: taskAssignedPayload(duel, result.Player1Task),
		duel.Player2ID: taskAssignedPayload(duel, result.Player2Task),
	}

	present := make(map[uuid.UUID]struct{}, len(assignments))
	for playerID := range assignments {
		participant, ok := s.clientByPlayer(playerID)
		if !ok {
			continue
		}
		participant.setQueued(false)
		participant.setDuel(duel.ID)
		if err := s.hubs.Register(ctx, duel.ID, participant); err != nil {
			participant.clearDuel()
			s.sendAppError(participant, err)
			continue
		}
		present[playerID] = struct{}{}
	}

	for playerID, assignment := range assignments {
		participant, ok := s.clientByPlayer(playerID)
		if !ok {
			continue
		}
		_ = participant.sendEvent(EventMatchFound, s.matchFoundPayload(ctx, duel, playerID))
		_ = participant.sendEvent(EventTaskAssigned, assignment)
	}
	if s.hints != nil {
		s.hints.StartDuel(duel, map[uuid.UUID]*domain.Task{
			duel.Player1ID: result.Player1Task,
			duel.Player2ID: result.Player2Task,
		})
	}
	if s.reconnect != nil {
		s.reconnect.StartDuelTimer(duel)
		for playerID := range assignments {
			if _, ok := present[playerID]; ok {
				continue
			}
			s.reconnect.HandleDisconnect(ctx, duel.ID, playerID)
		}
	}
}

func taskAssignedPayload(duel *domain.Duel, task *domain.Task) TaskAssignedPayload {
	return TaskAssignedPayload{
		DuelID:           duel.ID,
		Deadline:         duel.Deadline,
		TimeLimitSeconds: task.TimeLimit,
		Task: taskPayload(task, duelusecase.HintSnapshot{
			Schedule: domain.BuildHintSchedule(duel.StartedAt, task.TimeLimit),
		}),
	}
}

func (s *Server) matchFoundPayload(ctx context.Context, duel *domain.Duel, playerID uuid.UUID) MatchFoundPayload {
	opponentID, _ := duelOpponentID(duel, playerID)
	return MatchFoundPayload{
		DuelID:           duel.ID,
		OpponentUsername: s.playerUsername(ctx, opponentID),
		Duel:             duelPayload(duel),
	}
}

func (s *Server) publishOpponentSolved(duel *domain.Duel, solverID uuid.UUID) {
	opponentID, ok := duelOpponentID(duel, solverID)
	if !ok {
		return
	}
	if opponent, exists := s.clientByPlayer(opponentID); exists {
		_ = opponent.sendEvent(EventOpponentSolved, OpponentSolvedPayload{
			DuelID:   duel.ID,
			PlayerID: solverID,
		})
	}
}

func (s *Server) publishDuelFinished(ctx context.Context, duel *domain.Duel, solvedPlayerID *uuid.UUID) {
	if s.hints != nil {
		s.hints.StopDuel(duel.ID)
	}
	if s.reconnect != nil {
		s.reconnect.CloseDuel(duel.ID)
	}

	if c, ok := s.clientByPlayer(duel.Player1ID); ok {
		payload := duelFinishedPayload(duel, duel.Player1ID, solvedPlayerID, s.winnerUsername(ctx, duel))
		_ = c.sendEvent(EventDuelFinished, payload)
		c.clearDuel()
		c.setQueued(false)
	}
	if c, ok := s.clientByPlayer(duel.Player2ID); ok {
		payload := duelFinishedPayload(duel, duel.Player2ID, solvedPlayerID, s.winnerUsername(ctx, duel))
		_ = c.sendEvent(EventDuelFinished, payload)
		c.clearDuel()
		c.setQueued(false)
	}

	delay := s.closeDelay
	if delay <= 0 {
		s.hubs.Close(duel.ID)
		return
	}
	timer := time.AfterFunc(delay, func() {
		s.hubs.Close(duel.ID)
	})
	//nolint:contextcheck // server lifecycle ctx by design.
	context.AfterFunc(s.ctx, func() {
		timer.Stop()
	})
}

func (s *Server) winnerUsername(ctx context.Context, duel *domain.Duel) *string {
	if duel == nil || duel.WinnerID == nil {
		return nil
	}
	username := s.playerUsername(ctx, *duel.WinnerID)
	if username == "" {
		return nil
	}
	return &username
}

func (s *Server) playerUsername(ctx context.Context, playerID uuid.UUID) string {
	if playerID == uuid.Nil {
		return ""
	}
	if c, ok := s.clientByPlayer(playerID); ok && c.player != nil {
		return c.player.Username
	}
	player, err := s.players.GetByID(ctx, playerID)
	if err != nil || player == nil {
		return ""
	}
	return player.Username
}
