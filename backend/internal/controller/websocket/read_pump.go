package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"

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

var (
	errTrailingJSON     = errors.New("websocket message must contain a single json value")
	errMissingEventType = errors.New("websocket message type is required")
	errMalformedFrame   = errors.New("malformed websocket frame")
)

const maxMalformedFrames = 3

func (s *Server) readPump(ctx context.Context, c *client) {
	malformedFrames := 0
	for {
		// Per-iteration idle deadline: half-open clients (no FIN, no error)
		// would otherwise pin a goroutine on conn.Reader indefinitely. The
		// cancel must run AFTER the reader is fully consumed because the
		// coderws Reader returns a streaming reader bound to readCtx.
		readCtx, cancel := context.WithTimeout(ctx, defaultReadIdleTimeout)
		err := s.readOnce(readCtx, c)
		cancel()
		if errors.Is(err, errMalformedFrame) {
			malformedFrames++
			if malformedFrames >= maxMalformedFrames {
				c.Close()
				return
			}
			continue
		}
		if err != nil {
			return
		}
		malformedFrames = 0
	}
}

func (s *Server) readOnce(ctx context.Context, c *client) error {
	msgType, reader, err := c.conn.Reader(ctx)
	if err != nil {
		return err
	}
	if !s.ensureValidSession(ctx, c) {
		return apperr.ErrInvalidSession
	}
	if msgType != coderws.MessageText {
		_, _ = io.Copy(io.Discard, reader)
		_ = c.sendError(ErrorInvalidPayload, "message must be text")
		return errMalformedFrame
	}
	event, err := decodeIncomingEvent(reader)
	if err != nil {
		_ = c.sendError(ErrorInvalidJSON, "invalid json")
		return errMalformedFrame
	}
	if !s.allowInboundEvent(c, event.Type) {
		return apperr.ErrRateLimited
	}
	s.routeEvent(ctx, c, event)
	return nil
}

func (s *Server) routeEvent(ctx context.Context, c *client, event IncomingEvent) {
	if !s.canRouteEvent(ctx, c) {
		return
	}
	switch event.Type {
	case EventJoinQueue:
		s.routeCurrentNoPayloadEvent(ctx, c, event.Payload, s.handleJoinQueue)
	case EventLeaveQueue:
		s.routeCurrentNoPayloadEvent(ctx, c, event.Payload, s.handleLeaveQueue)
	case EventFlagSubmit:
		s.routeCurrentPayloadEvent(ctx, c, func(ctx context.Context, c *client) {
			s.handleFlagSubmit(ctx, c, event.Payload)
		})
	case EventSurrender:
		s.routeCurrentPayloadEvent(ctx, c, func(ctx context.Context, c *client) {
			s.handleSurrender(ctx, c, event.Payload)
		})
	case EventPing:
		if rejectUnexpectedPayload(c, event.Payload) {
			return
		}
		_ = c.sendEvent(EventPong, nil)
	default:
		_ = c.sendError(ErrorUnknownEvent, "unknown event type")
	}
}

func (s *Server) canRouteEvent(ctx context.Context, c *client) bool {
	if c.isDisplaced() || c.closed.Load() {
		return false
	}
	return s.ensureValidSession(ctx, c)
}

func (s *Server) routeCurrentNoPayloadEvent(
	ctx context.Context,
	c *client,
	payload json.RawMessage,
	handle func(context.Context, *client),
) {
	if rejectUnexpectedPayload(c, payload) || !s.requireCurrentClient(c) {
		return
	}
	handle(ctx, c)
}

func (s *Server) routeCurrentPayloadEvent(
	ctx context.Context,
	c *client,
	handle func(context.Context, *client),
) {
	if !s.requireCurrentClient(c) {
		return
	}
	handle(ctx, c)
}

func (s *Server) ensureValidSession(ctx context.Context, c *client) bool {
	if s.validateSession(ctx, c) {
		return true
	}
	s.rejectInvalidSession(c, "stale_session")
	return false
}

func (s *Server) rejectInvalidSession(c *client, reason string) {
	s.logClientSecurityEvent(c, "ws.session", wsSecurityOutcomeFailure, logkit.Fields{
		"error_code": string(apperr.CodeInvalidSession),
		"reason":     reason,
	})
	_ = c.sendError(string(apperr.CodeInvalidSession), "invalid session token")
	s.closeAfterError(c)
}

func (s *Server) allowInboundEvent(c *client, eventType string) bool {
	if !c.allowMessage() {
		s.rejectRateLimited(c, eventType, "message_rate_limit")
		return false
	}
	if isActionEvent(eventType) && !c.allowAction() {
		s.rejectRateLimited(c, eventType, "action_rate_limit")
		return false
	}
	return true
}

func (s *Server) rejectRateLimited(c *client, eventType, reason string) {
	s.logClientSecurityEvent(c, "ws.message", wsSecurityOutcomeRateLimited, logkit.Fields{
		"error_code": string(apperr.CodeRateLimited),
		"event_type": eventType,
		"reason":     reason,
	})
	_ = c.sendError(string(apperr.CodeRateLimited), "too many websocket messages")
	s.closeAfterError(c)
}

func isActionEvent(eventType string) bool {
	switch eventType {
	case EventJoinQueue, EventLeaveQueue, EventFlagSubmit, EventSurrender:
		return true
	default:
		return false
	}
}

func (s *Server) requireCurrentClient(c *client) bool {
	if s.isCurrentClient(c) {
		return true
	}
	_ = c.sendError(ErrorStaleConnection, "stale websocket connection")
	return false
}

func rejectUnexpectedPayload(c *client, raw json.RawMessage) bool {
	if !hasPayload(raw) {
		return false
	}
	_ = c.sendError(ErrorInvalidPayload, "payload is not allowed for this event")
	return true
}

func decodeIncomingEvent(reader io.Reader) (IncomingEvent, error) {
	var event IncomingEvent
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return event, err
	}
	if err := requireSingleJSONValue(decoder); err != nil {
		return event, err
	}
	if strings.TrimSpace(event.Type) == "" {
		return event, errMissingEventType
	}
	return event, nil
}

func requireSingleJSONValue(decoder *json.Decoder) error {
	var extra struct{}
	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errTrailingJSON
	default:
		return err
	}
}

func (s *Server) closeAfterError(c *client) {
	if c == nil {
		return
	}
	delay := s.closeDelay
	if delay <= 0 {
		delay = 10 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		c.Close()
	case <-s.done():
	}
}

// validateSession compares the WS connection's session token captured at
// handshake time against the player's current token in storage. If the player
// rotated their token (e.g. via /api/v1/players/join with the same username
// from another device), the old WS becomes stale and must be rejected.
func (s *Server) validateSession(ctx context.Context, c *client) bool {
	if s.players == nil || c == nil || c.player == nil {
		return true
	}
	if c.sessionToken == uuid.Nil {
		return false
	}
	current, err := s.players.GetByID(ctx, c.player.ID)
	if err != nil || current == nil || current.SessionToken == nil {
		return false
	}
	if *current.SessionToken != c.sessionToken {
		return false
	}
	return current.SessionExpiresAt != nil && current.SessionExpiresAt.After(time.Now().UTC())
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
	if err := decodeEventPayload(raw, &payload); err != nil {
		_ = c.sendError(ErrorInvalidPayload, "invalid flag_submit payload")
		return
	}
	payload.Flag = strings.TrimSpace(payload.Flag)
	duelID, ok := c.currentDuel()
	if !ok || payload.Flag == "" {
		_ = c.sendError(ErrorInvalidPayload, "active duel and flag are required")
		return
	}
	if payload.DuelID != nil && *payload.DuelID != duelID {
		_ = c.sendError(ErrorInvalidPayload, "duel_id does not match active duel")
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
		_ = c.sendError(string(apperr.CodeDuelFinished), apperr.ErrDuelFinished.Message)
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

func decodeEventPayload(raw json.RawMessage, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return requireSingleJSONValue(decoder)
}

func decodeOptionalEventPayload(raw json.RawMessage, dst any) error {
	if !hasPayload(raw) {
		return nil
	}
	return decodeEventPayload(raw, dst)
}

func hasPayload(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func (s *Server) handleSurrender(ctx context.Context, c *client, raw json.RawMessage) {
	if s.reconnect == nil {
		_ = c.sendError(ErrorInternal, "surrender is not configured")
		return
	}

	var payload surrenderPayload
	if err := decodeOptionalEventPayload(raw, &payload); err != nil {
		_ = c.sendError(ErrorInvalidPayload, "invalid surrender payload")
		return
	}
	duelID, ok := c.currentDuel()
	if !ok {
		_ = c.sendError(ErrorInvalidPayload, "no active duel for this connection")
		return
	}
	if payload.DuelID != nil && *payload.DuelID != duelID {
		_ = c.sendError(ErrorInvalidPayload, "duel_id does not match active duel")
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
		if _, ok := present[playerID]; !ok {
			continue
		}
		participant, ok := s.clientByPlayer(playerID)
		if !ok {
			continue
		}
		if !s.sendCriticalEvent(participant, EventMatchFound, s.matchFoundPayload(ctx, duel, playerID)) {
			s.hubs.Unregister(duel.ID, participant)
			continue
		}
		if !s.sendCriticalEvent(participant, EventTaskAssigned, assignment) {
			s.hubs.Unregister(duel.ID, participant)
		}
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

func (s *Server) sendCriticalEvent(c *client, eventType string, payload any) bool {
	if c == nil {
		return false
	}
	if err := c.sendEvent(eventType, payload); err != nil {
		s.closeAfterCriticalSendFailure(c, eventType, err)
		return false
	}
	return true
}

func (s *Server) closeAfterCriticalSendFailure(c *client, eventType string, err error) {
	s.logClientDeliveryFailure(c, eventType, err)
	if c != nil {
		c.CloseNow()
	}
}

func (s *Server) logClientDeliveryFailure(c *client, eventType string, err error) {
	if s == nil || s.log == nil || err == nil {
		return
	}
	fields := logkit.Fields{
		"event_type": eventType,
		"error":      err.Error(),
	}
	if c != nil && c.player != nil {
		fields["player_id"] = c.player.ID.String()
	}
	s.log.Warn("websocket event delivery failed", fields)
}

func taskAssignedPayload(duel *domain.Duel, task *domain.Task) TaskAssignedPayload {
	return TaskAssignedPayload{
		DuelID:           duel.ID,
		Deadline:         duel.Deadline,
		TimeLimitSeconds: task.TimeLimit,
		Task: taskPayload(task, duelusecase.HintSnapshot{
			Schedule: taskVisibleHintSchedule(duel.StartedAt, task),
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
		_ = c.sendDuelFinishedIfCurrent(duel.ID, payload)
	}
	if c, ok := s.clientByPlayer(duel.Player2ID); ok {
		payload := duelFinishedPayload(duel, duel.Player2ID, solvedPlayerID, s.winnerUsername(ctx, duel))
		_ = c.sendDuelFinishedIfCurrent(duel.ID, payload)
	}

	delay := s.closeDelay
	if delay <= 0 {
		s.hubs.Close(duel.ID)
		return
	}
	runAfterOrDone(s.done(), delay, func() {
		s.hubs.Close(duel.ID)
	})
}

func (s *Server) done() <-chan struct{} {
	if s == nil || s.ctx == nil {
		return nil
	}
	return s.ctx.Done()
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
