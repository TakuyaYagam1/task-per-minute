package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"
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

type arenaReadResult struct {
	data  []byte
	err   error
	fatal bool
}

//nolint:gocyclo // One loop owns Arena reads, role routing, terminal ordering, writes, and connection cleanup.
func (s *Server) serveArenaConnection(
	ctx context.Context,
	r *http.Request,
	conn *coderws.Conn,
	role ArenaRole,
	player *domain.Player,
) {
	if conn == nil {
		return
	}
	if role == ArenaRolePublic {
		ctx = withArenaPublicSession(ctx)
	}
	conn.SetReadLimit(defaultReadLimit)
	readCtx, cancelRead := context.WithCancel(ctx)
	reads := make(chan arenaReadResult, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		readArenaFrames(readCtx, conn, reads)
	}()
	defer func() {
		cancelRead()
		_ = conn.CloseNow()
		<-readerDone
	}()

	first, ok := <-reads
	if !ok || first.fatal {
		return
	}
	if first.err != nil {
		_ = writeArenaRejection(ctx, conn, first.err)
		return
	}
	command, err := DecodeArenaCommand(role, first.data)
	if err != nil {
		_ = writeArenaRejection(ctx, conn, err)
		return
	}
	tournamentID, cursor, err := arenaCommandScope(command)
	if err != nil {
		_ = writeArenaRejection(ctx, conn, err)
		return
	}

	active, principalID, err := s.openArenaConnection(ctx, r, role, player, tournamentID, cursor)
	if err != nil {
		_ = writeArenaRejection(ctx, conn, err)
		return
	}

	var subscription ArenaTerminalSubscription
	var deliveries <-chan arenaws.CancellationDelivery
	if s.arenaTerminal != nil {
		subscription, err = s.arenaTerminal.SubscribeArenaTerminal(ctx, ArenaTerminalSubscriptionRequest{
			Role:          role,
			Authenticated: role != ArenaRolePublic,
			TournamentID:  tournamentID,
			ParticipantID: principalID,
		})
		if err != nil {
			_ = writeArenaRejection(ctx, conn, err)
			return
		}
		if subscription != nil {
			defer subscription.Close()
			deliveries = subscription.Deliveries()
		}
	}
	if err := writeArenaMessage(ctx, conn, active); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case delivery, open := <-deliveries:
			if !open {
				return
			}
			encoded, terminal, marshalErr := marshalArenaCancellation(role, delivery)
			if marshalErr != nil {
				_ = writeArenaRejection(ctx, conn, marshalErr)
				return
			}
			if err := writeArenaMessage(ctx, conn, encoded); err != nil || terminal {
				return
			}
		case next, open := <-reads:
			if !open || next.fatal {
				return
			}
			if next.err != nil {
				if err := writeArenaRejection(ctx, conn, next.err); err != nil {
					return
				}
				continue
			}
			command, decodeErr := DecodeArenaCommand(role, next.data)
			if decodeErr != nil {
				if err := writeArenaRejection(ctx, conn, decodeErr); err != nil {
					return
				}
				continue
			}
			nextTournamentID, nextCursor, scopeErr := arenaCommandScope(command)
			if scopeErr != nil || nextTournamentID != tournamentID {
				if err := writeArenaRejection(ctx, conn, ErrArenaInvalidPayload); err != nil {
					return
				}
				continue
			}
			encoded, _, openErr := s.openArenaConnection(ctx, r, role, player, nextTournamentID, nextCursor)
			if openErr != nil {
				if err := writeArenaRejection(ctx, conn, openErr); err != nil {
					return
				}
				continue
			}
			if err := writeArenaMessage(ctx, conn, encoded); err != nil {
				return
			}
		}
	}
}

func readArenaFrames(ctx context.Context, conn *coderws.Conn, reads chan<- arenaReadResult) {
	defer close(reads)
	for {
		messageType, data, err := conn.Read(ctx)
		if err != nil {
			select {
			case reads <- arenaReadResult{err: err, fatal: true}:
			case <-ctx.Done():
			}
			return
		}
		result := arenaReadResult{data: data}
		if messageType != coderws.MessageText {
			result = arenaReadResult{err: ErrArenaInvalidPayload}
		}
		select {
		case reads <- result:
		case <-ctx.Done():
			return
		}
	}
}

func arenaCommandScope(command ArenaCommand) (uuid.UUID, *arenaws.RealtimeCursor, error) {
	switch command.Type {
	case EventArenaConnect:
		if command.Connect == nil || command.Connect.TournamentID == uuid.Nil {
			return uuid.Nil, nil, ErrArenaInvalidPayload
		}
		return command.Connect.TournamentID, nil, nil
	case EventArenaResume:
		if command.Resume == nil {
			return uuid.Nil, nil, ErrArenaInvalidPayload
		}
		cursor := command.Resume.Cursor
		return cursor.TournamentID, &cursor, nil
	default:
		return uuid.Nil, nil, ErrArenaUnknownEvent
	}
}

//nolint:gocyclo // Role dispatch validates each trusted principal and keeps payload construction role-exclusive.
func (s *Server) openArenaConnection(
	ctx context.Context,
	r *http.Request,
	role ArenaRole,
	player *domain.Player,
	tournamentID uuid.UUID,
	cursor *arenaws.RealtimeCursor,
) ([]byte, uuid.UUID, error) {
	switch role {
	case ArenaRoleParticipant:
		if player == nil || s.arenaParticipant == nil {
			return nil, uuid.Nil, ErrArenaRoleMismatch
		}
		payload, err := s.arenaParticipant.OpenArenaParticipant(ctx, ArenaParticipantConnectionRequest{
			Principal: arenaws.ParticipantRealtimePrincipal{
				Authenticated: true,
				TournamentID:  tournamentID,
				PlayerID:      player.ID,
			},
			TournamentID: tournamentID,
			Cursor:       cloneArenaCursor(cursor),
		})
		if err != nil {
			return nil, uuid.Nil, err
		}
		encoded, err := MarshalArenaParticipant(payload)
		return encoded, player.ID, err
	case ArenaRolePublic:
		if s.arenaPublic == nil {
			return nil, uuid.Nil, ErrArenaInvalidPayload
		}
		payload, err := s.arenaPublic.OpenArenaPublic(ctx, ArenaPublicConnectionRequest{
			TournamentID: tournamentID,
			Cursor:       cloneArenaCursor(cursor),
		})
		if err != nil {
			return nil, uuid.Nil, err
		}
		encoded, err := MarshalArenaPublic(payload)
		return encoded, uuid.Nil, err
	case ArenaRoleOperator:
		if player == nil || s.arenaOperator == nil || s.arenaOperatorResolve == nil {
			return nil, uuid.Nil, ErrArenaRoleMismatch
		}
		principal, ok := s.arenaOperatorResolve(r, player, tournamentID)
		if !ok || !principal.Authenticated || principal.PrincipalID == uuid.Nil ||
			principal.Role != arenaws.OperatorRealtimeRole || principal.TournamentID != tournamentID {
			return nil, uuid.Nil, ErrArenaRoleMismatch
		}
		payload, err := s.arenaOperator.OpenArenaOperator(ctx, ArenaOperatorConnectionRequest{
			Principal:    principal,
			TournamentID: tournamentID,
			Cursor:       cloneArenaCursor(cursor),
		})
		if err != nil {
			return nil, uuid.Nil, err
		}
		encoded, err := MarshalArenaOperator(payload)
		return encoded, principal.PrincipalID, err
	default:
		return nil, uuid.Nil, ErrArenaRoleMismatch
	}
}

func cloneArenaCursor(cursor *arenaws.RealtimeCursor) *arenaws.RealtimeCursor {
	if cursor == nil {
		return nil
	}
	clone := *cursor
	return &clone
}

func writeArenaRejection(ctx context.Context, conn *coderws.Conn, cause error) error {
	encoded, err := MarshalArenaRejected(ArenaRejectionFor(cause))
	if err != nil {
		return err
	}
	return writeArenaMessage(ctx, conn, encoded)
}

func writeArenaMessage(ctx context.Context, conn *coderws.Conn, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, defaultWriteWait)
	defer cancel()
	return conn.Write(writeCtx, coderws.MessageText, data)
}

func marshalArenaCancellation(role ArenaRole, delivery arenaws.CancellationDelivery) ([]byte, bool, error) {
	switch delivery.Kind {
	case arenaws.CancellationDeliverySnapshot:
		if delivery.Snapshot == nil {
			return nil, false, ErrArenaInvalidPayload
		}
		return marshalArenaCancellationSnapshot(role, *delivery.Snapshot)
	case arenaws.CancellationDeliveryTerminal:
		return marshalArenaCancellationTerminal(role, delivery)
	default:
		return nil, false, ErrArenaInvalidPayload
	}
}

func marshalArenaCancellationSnapshot(role ArenaRole, snapshot arenaws.RealtimeEnvelope) ([]byte, bool, error) {
	switch role {
	case ArenaRoleParticipant:
		if snapshot.Participant == nil || snapshot.Public != nil || snapshot.Operator != nil {
			return nil, false, ErrArenaRoleMismatch
		}
		envelope := arenaws.ParticipantRealtimeEnvelope{
			SchemaVersion:      snapshot.SchemaVersion,
			TournamentID:       snapshot.TournamentID,
			Sequence:           snapshot.Sequence,
			EventID:            snapshot.EventID,
			OccurredAt:         snapshot.OccurredAt,
			ProjectionRevision: snapshot.ProjectionRevision,
			Participant:        *snapshot.Participant,
		}
		payload, err := NewArenaParticipantPayload(arenaws.ParticipantRealtimeResult{
			UsesSnapshot: true,
			Envelopes:    []arenaws.ParticipantRealtimeEnvelope{envelope},
		})
		if err != nil {
			return nil, false, err
		}
		encoded, err := MarshalArenaParticipant(payload)
		return encoded, false, err
	case ArenaRolePublic:
		payload, err := NewArenaPublicPayload(true, []arenaws.RealtimeEnvelope{snapshot})
		if err != nil {
			return nil, false, err
		}
		encoded, err := MarshalArenaPublic(payload)
		return encoded, false, err
	case ArenaRoleOperator:
		payload, err := NewArenaOperatorPayload(arenaws.OperatorRealtimeResult{
			UsesSnapshot: true,
			Envelopes:    []arenaws.RealtimeEnvelope{snapshot},
		})
		if err != nil {
			return nil, false, err
		}
		encoded, err := MarshalArenaOperator(payload)
		return encoded, false, err
	default:
		return nil, false, ErrArenaRoleMismatch
	}
}

func marshalArenaCancellationTerminal(role ArenaRole, delivery arenaws.CancellationDelivery) ([]byte, bool, error) {
	switch role {
	case ArenaRoleParticipant:
		if delivery.Participant == nil || delivery.Public != nil || delivery.Operator != nil {
			return nil, false, ErrArenaRoleMismatch
		}
		encoded, err := MarshalArenaParticipantTerminal(ArenaParticipantTerminalPayload(*delivery.Participant))
		return encoded, true, err
	case ArenaRolePublic:
		if delivery.Public == nil || delivery.Participant != nil || delivery.Operator != nil {
			return nil, false, ErrArenaRoleMismatch
		}
		encoded, err := MarshalArenaPublicTerminal(ArenaPublicTerminalPayload(*delivery.Public))
		return encoded, true, err
	case ArenaRoleOperator:
		if delivery.Operator == nil || delivery.Participant != nil || delivery.Public != nil {
			return nil, false, ErrArenaRoleMismatch
		}
		encoded, err := MarshalArenaOperatorTerminal(ArenaOperatorTerminalPayload(*delivery.Operator))
		return encoded, true, err
	default:
		return nil, false, ErrArenaRoleMismatch
	}
}

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
		return domain.ErrInvalidSession
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
		return domain.ErrRateLimited
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
		"error_code": string(domain.ErrorCodeInvalidSession),
		"reason":     reason,
	})
	_ = c.sendError(string(domain.ErrorCodeInvalidSession), "invalid session token")
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
		"error_code": string(domain.ErrorCodeRateLimit),
		"event_type": eventType,
		"reason":     reason,
	})
	_ = c.sendError(string(domain.ErrorCodeRateLimit), "too many websocket messages")
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
		if errors.Is(err, domain.ErrFlagIncorrect) {
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
		_ = c.sendError(string(domain.ErrorCodeDuelFinished), domain.ErrDuelFinished.Message)
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
