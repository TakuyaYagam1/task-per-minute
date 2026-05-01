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
	switch event.Type {
	case EventJoinQueue:
		s.handleJoinQueue(ctx, c)
	case EventLeaveQueue:
		s.handleLeaveQueue(ctx, c)
	case EventFlagSubmit:
		s.handleFlagSubmit(ctx, c, event.Payload)
	case EventPing:
		_ = c.sendEvent(EventPong, nil)
	default:
		_ = c.sendError(ErrorUnknownEvent, "unknown event type")
	}
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

	result, err := s.flags.SubmitFlag(ctx, duelID, c.player.ID, payload.Flag)
	if err != nil {
		if errors.Is(err, apperr.ErrFlagIncorrect) {
			_ = c.sendEvent(EventFlagResult, FlagResultPayload{Correct: false})
			return
		}
		s.sendAppError(c, err)
		return
	}
	if !result.Correct || result.FinishedDuel == nil {
		_ = c.sendEvent(EventFlagResult, FlagResultPayload{Correct: false})
		return
	}

	s.publishDuelFinished(ctx, result.FinishedDuel)
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
	}

	matchPayload := MatchFoundPayload{Duel: duelPayload(duel)}
	for playerID, assignment := range assignments {
		participant, ok := s.clientByPlayer(playerID)
		if !ok {
			continue
		}
		_ = participant.sendEvent(EventMatchFound, matchPayload)
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
	}
}

func taskAssignedPayload(duel *domain.Duel, task *domain.Task) TaskAssignedPayload {
	return TaskAssignedPayload{
		DuelID: duel.ID,
		Task: taskPayload(task, duelusecase.HintSnapshot{
			Schedule: domain.BuildHintSchedule(duel.StartedAt, task.TimeLimit),
		}),
	}
}

func (s *Server) publishDuelFinished(ctx context.Context, duel *domain.Duel) {
	if s.hints != nil {
		s.hints.StopDuel(duel.ID)
	}
	if s.reconnect != nil {
		s.reconnect.CloseDuel(duel.ID)
	}
	payload := DuelFinishedPayload{Duel: duelPayload(duel)}
	_ = s.hubs.BroadcastJSON(ctx, duel.ID, EventDuelFinished, payload)

	if c, ok := s.clientByPlayer(duel.Player1ID); ok {
		c.clearDuel()
		c.setQueued(false)
	}
	if c, ok := s.clientByPlayer(duel.Player2ID); ok {
		c.clearDuel()
		c.setQueued(false)
	}

	delay := s.closeDelay
	if delay <= 0 {
		s.hubs.Close(duel.ID)
		return
	}
	time.AfterFunc(delay, func() {
		s.hubs.Close(duel.ID)
	})
}
