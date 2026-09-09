package websocket

import (
	"context"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/requestmeta"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	appobservability "github.com/TakuyaYagam1/task-per-minute/internal/observability"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

type tournamentConnectionObserver func(action, outcome, reason string, revision int64)

func (server *Server) serveTournamentConnection(
	ctx context.Context,
	connection *coderws.Conn,
	scope tournamentConnectionScope,
	principal tournamentConnectionPrincipal,
	resume tournamentConnectionResume,
) {
	writeScope, err := newTournamentWriteScope(scope, principal)
	if err != nil {
		return
	}
	startedAt := time.Now()
	observe := func(action, outcome, reason string, revision int64) {
		requestID := requestmeta.RequestIDFromContext(ctx)
		_ = tournamentws.ObserveTransportEvent(ctx, server.tournamentObserver, tournamentws.TournamentTransportEvent{
			CorrelationID: tournamentws.TransportCorrelationID(requestID, scope.TournamentID),
			TournamentID:  scope.TournamentID,
			Role:          string(scope.Role),
			Action:        action,
			Outcome:       outcome,
			ReasonCode:    reason,
			Duration:      time.Since(startedAt),
			Revision:      revision,
		})
	}
	defer observe(
		tournamentws.TournamentTransportDisconnect,
		appobservability.TournamentOutcomeSuccess,
		"closed",
		0,
	)

	initialSequence := int64(0)
	var deliverySession *realtimeDeliverySession
	if server.realtimeDelivery != nil {
		cursor, cursorErr := server.realtimeDelivery.Cursor(ctx, scope.TournamentID)
		if cursorErr != nil {
			server.writeTournamentRejection(ctx, connection, writeScope, cursorErr)
			observe(tournamentws.TournamentTransportReject, appobservability.TournamentOutcomeRejected, "cursor_unavailable", 0)
			return
		}
		initialSequence = cursor
		audience, principalID, deliveryErr := tournamentDeliveryAudience(scope, principal)
		if deliveryErr != nil {
			observe(tournamentws.TournamentTransportDelivery, appobservability.TournamentOutcomeFailure, "subscriber_scope", 0)
			return
		}
		deliverySession, deliveryErr = server.realtimeDelivery.openSession(
			ctx,
			scope.TournamentID,
			audience,
			principalID,
			initialSequence,
			resume.ID,
			connection,
			writeScope,
			func(renderCtx context.Context, event eventdelivery.Event) ([]byte, error) {
				return server.renderTournamentEvent(renderCtx, scope, principal, event, deliverySession.resumeID())
			},
		)
		if deliveryErr != nil {
			server.writeTournamentRejection(ctx, connection, writeScope, deliveryErr)
			observe(tournamentws.TournamentTransportDelivery, appobservability.TournamentOutcomeFailure, "subscriber_open", 0)
			return
		}
		defer server.closeRealtimeDeliverySession(ctx, deliverySession)
	}
	resumeID := uuid.Nil
	if deliverySession != nil {
		resumeID = deliverySession.resumeID()
		initialSequence = deliverySession.lastSequence.Load()
	}
	initial, revision, err := server.openTournamentConnection(ctx, scope, principal, initialSequence, nil, resumeID)
	if err != nil {
		server.writeTournamentRejection(ctx, connection, writeScope, err)
		observe(tournamentws.TournamentTransportReject, appobservability.TournamentOutcomeRejected, string(TournamentRejectionFor(err).Code), revision)
		return
	}
	if err := writeTournamentInitial(ctx, connection, writeScope, deliverySession, initial); err != nil {
		observe(tournamentws.TournamentTransportDelivery, appobservability.TournamentOutcomeFailure, "write_failed", revision)
		return
	}
	if server.realtimeDelivery != nil {
		if server.realtimeDelivery.hasWrittenTerminal(deliverySession) {
			if deliveryErr := server.realtimeDelivery.closeWrittenTerminal(ctx, deliverySession); deliveryErr != nil {
				observe(tournamentws.TournamentTransportDelivery, appobservability.TournamentOutcomeFailure, "terminal_close_failed", revision)
				return
			}
			observe(tournamentws.TournamentTransportDisconnect, appobservability.TournamentOutcomeSuccess, "terminal_written", revision)
			return
		}
		if deliveryErr := server.realtimeDelivery.deliverPendingTerminal(ctx, deliverySession); deliveryErr != nil {
			observe(tournamentws.TournamentTransportDelivery, appobservability.TournamentOutcomeFailure, "terminal_retry_failed", revision)
			return
		}
		if !server.realtimeDelivery.sessionActive(deliverySession) {
			observe(tournamentws.TournamentTransportDisconnect, appobservability.TournamentOutcomeSuccess, "terminal", revision)
			return
		}
		if deliveryErr := server.realtimeDelivery.CatchUp(ctx, deliverySession); deliveryErr != nil {
			observe(tournamentws.TournamentTransportDelivery, appobservability.TournamentOutcomeFailure, "catch_up_failed", revision)
			return
		}
	}
	observe(tournamentws.TournamentTransportConnect, appobservability.TournamentOutcomeSuccess, "opened", revision)
	server.streamTournamentConnection(ctx, connection, writeScope, principal, deliverySession, revision, observe)
}

func writeTournamentInitial(
	ctx context.Context,
	connection *coderws.Conn,
	writeScope tournamentWriteScope,
	session *realtimeDeliverySession,
	initial []byte,
) error {
	if session != nil {
		return session.write(ctx, initial)
	}
	return writeTournamentMessage(ctx, connection, writeScope, initial)
}

func (server *Server) streamTournamentConnection(
	ctx context.Context,
	connection *coderws.Conn,
	writeScope tournamentWriteScope,
	principal tournamentConnectionPrincipal,
	deliverySession *realtimeDeliverySession,
	revision int64,
	observe tournamentConnectionObserver,
) {
	inboundFrames := server.readTournamentFrames(ctx, connection, writeScope, principal)
	ping := time.NewTicker(defaultPingInterval)
	defer ping.Stop()
	sessionTicker, sessionChecks, expiryTimer, sessionExpires := server.connectionSessionSignals(principal)
	if sessionTicker != nil {
		defer sessionTicker.Stop()
	}
	if expiryTimer != nil {
		defer expiryTimer.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case result, open := <-inboundFrames:
			if !open || result.cause == nil {
				return
			}
			server.writeTournamentSessionRejection(ctx, connection, writeScope, deliverySession, result.cause)
			observe(tournamentws.TournamentTransportReject, appobservability.TournamentOutcomeRejected, string(TournamentRejectionFor(result.cause).Code), revision)
			return
		case <-sessionChecks:
			if !server.connectionSessionValid(ctx, principal) {
				server.writeTournamentSessionRejection(ctx, connection, writeScope, deliverySession, connectionAuthenticationError(principal))
				observe(tournamentws.TournamentTransportReject, appobservability.TournamentOutcomeRejected, "stale_session", revision)
				return
			}
		case <-sessionExpires:
			server.writeTournamentSessionRejection(ctx, connection, writeScope, deliverySession, connectionAuthenticationError(principal))
			observe(tournamentws.TournamentTransportReject, appobservability.TournamentOutcomeRejected, "session_expired", revision)
			return
		case <-ping.C:
			if err := writeTournamentSessionPing(ctx, connection, writeScope, deliverySession); err != nil {
				return
			}
		}
	}
}

func (server *Server) connectionSessionSignals(
	principal tournamentConnectionPrincipal,
) (*time.Ticker, <-chan time.Time, *time.Timer, <-chan time.Time) {
	expiresAt, monitored := connectionSessionExpiry(principal)
	if !monitored {
		return nil, nil, nil, nil
	}
	interval := server.sessionCheckInterval
	if interval <= 0 {
		interval = defaultSessionCheckInterval
	}
	ticker := time.NewTicker(interval)
	if expiresAt == nil {
		return ticker, ticker.C, nil, nil
	}
	delay := time.Until(*expiresAt)
	if delay <= 0 {
		delay = time.Nanosecond
	}
	timer := time.NewTimer(delay)
	return ticker, ticker.C, timer, timer.C
}

func connectionSessionExpiry(principal tournamentConnectionPrincipal) (*time.Time, bool) {
	if principal.Player != nil {
		return principal.Player.SessionExpiresAt, true
	}
	if principal.OperatorSession != nil {
		return &principal.OperatorSession.ExpiresAt, true
	}
	return nil, false
}

func connectionAuthenticationError(principal tournamentConnectionPrincipal) error {
	if principal.OperatorSession != nil {
		return tournamentws.ErrOperatorRealtimeAuthentication
	}
	return tournamentws.ErrParticipantRealtimeUnauthenticated
}

func (server *Server) connectionSessionValid(ctx context.Context, principal tournamentConnectionPrincipal) bool {
	if principal.OperatorSession != nil {
		return server.operatorSessionValid(ctx, principal.OperatorSession)
	}
	return server.participantSessionValid(ctx, principal)
}

func (server *Server) operatorSessionValid(ctx context.Context, session *TournamentOperatorSession) bool {
	if session == nil || session.Validate == nil || !session.ExpiresAt.After(time.Now().UTC()) {
		return false
	}
	timeout := server.sessionCheckTimeout
	if timeout <= 0 {
		timeout = defaultSessionCheckTimeout
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return session.Validate(checkCtx)
}

func (server *Server) participantSessionValid(
	ctx context.Context,
	principal tournamentConnectionPrincipal,
) bool {
	if server.players == nil || principal.Player == nil || principal.ParticipantSession == uuid.Nil {
		return false
	}
	timeout := server.sessionCheckTimeout
	if timeout <= 0 {
		timeout = defaultSessionCheckTimeout
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	current, err := server.players.GetBySessionToken(checkCtx, principal.ParticipantSession)
	return err == nil && current != nil && current.ID == principal.Player.ID &&
		current.SessionToken != nil && *current.SessionToken == principal.ParticipantSession &&
		current.SessionExpiresAt != nil && current.SessionExpiresAt.After(time.Now().UTC())
}

func (server *Server) openTournamentConnection(
	ctx context.Context,
	scope tournamentConnectionScope,
	principal tournamentConnectionPrincipal,
	sequence int64,
	event *eventdelivery.Event,
	resumeID uuid.UUID,
) ([]byte, int64, error) {
	switch scope.Role {
	case TournamentRoleParticipant:
		if principal.Player == nil || server.participant == nil {
			return nil, 0, ErrTournamentRoleMismatch
		}
		payload, err := server.participant.OpenTournamentParticipant(ctx, TournamentParticipantConnectionRequest{
			Principal: tournamentws.ParticipantRealtimePrincipal{
				Authenticated: true,
				TournamentID:  scope.TournamentID,
				PlayerID:      principal.Player.ID,
			},
			TournamentID: scope.TournamentID,
		})
		if err != nil {
			return nil, 0, err
		}
		applyParticipantDeliveryMetadata(&payload, sequence, event, resumeID)
		encoded, err := MarshalTournamentParticipant(payload)
		return encoded, payload.Envelope.ProjectionRevision, err
	case TournamentRolePublic:
		if server.public == nil {
			return nil, 0, ErrTournamentRoleMismatch
		}
		payload, err := server.public.OpenTournamentPublic(ctx, TournamentPublicConnectionRequest{
			TournamentID: scope.TournamentID,
		})
		if err != nil {
			return nil, 0, err
		}
		applyPublicDeliveryMetadata(&payload, sequence, event, resumeID)
		encoded, err := MarshalTournamentPublic(payload)
		return encoded, payload.Envelope.ProjectionRevision, err
	case TournamentRoleOperator:
		if principal.OperatorSession == nil || server.operator == nil {
			return nil, 0, ErrTournamentRoleMismatch
		}
		payload, err := server.operator.OpenTournamentOperator(ctx, TournamentOperatorConnectionRequest{
			Principal:    principal.OperatorSession.Principal,
			TournamentID: scope.TournamentID,
		})
		if err != nil {
			return nil, 0, err
		}
		applyOperatorDeliveryMetadata(&payload, sequence, event, resumeID)
		encoded, err := MarshalTournamentOperator(payload)
		return encoded, payload.Envelope.ProjectionRevision, err
	default:
		return nil, 0, ErrTournamentRoleMismatch
	}
}

func (server *Server) renderTournamentEvent(
	ctx context.Context,
	scope tournamentConnectionScope,
	principal tournamentConnectionPrincipal,
	event eventdelivery.Event,
	resumeID uuid.UUID,
) ([]byte, error) {
	if event.Validate() != nil || event.TournamentID != scope.TournamentID {
		return nil, ErrTournamentWriteScope
	}
	if scope.Role != TournamentRolePublic && !server.connectionSessionValid(ctx, principal) {
		return nil, connectionAuthenticationError(principal)
	}
	payload, _, err := server.openTournamentConnection(ctx, scope, principal, event.Sequence, &event, resumeID)
	return payload, err
}

func applyParticipantDeliveryMetadata(
	payload *TournamentParticipantPayload,
	sequence int64,
	event *eventdelivery.Event,
	resumeID uuid.UUID,
) {
	if payload == nil || sequence < 0 {
		return
	}
	payload.Envelope.Sequence = sequence
	payload.Envelope.Participant.LastSequence = sequence
	payload.Envelope.ResumeID = optionalTournamentResumeID(resumeID)
	if event != nil {
		payload.Envelope.EventID = event.ID
		payload.Envelope.OccurredAt = event.OccurredAt
	}
}

func applyPublicDeliveryMetadata(
	payload *TournamentPublicPayload,
	sequence int64,
	event *eventdelivery.Event,
	resumeID uuid.UUID,
) {
	if payload == nil || sequence < 0 {
		return
	}
	payload.Envelope.Sequence = sequence
	payload.Envelope.Public.LastSequence = sequence
	payload.Envelope.ResumeID = optionalTournamentResumeID(resumeID)
	if event != nil {
		payload.Envelope.EventID = event.ID
		payload.Envelope.OccurredAt = event.OccurredAt
	}
}

func applyOperatorDeliveryMetadata(
	payload *TournamentOperatorPayload,
	sequence int64,
	event *eventdelivery.Event,
	resumeID uuid.UUID,
) {
	if payload == nil || sequence < 0 {
		return
	}
	payload.Envelope.Sequence = sequence
	payload.Envelope.Operator.LastSequence = sequence
	payload.Envelope.ResumeID = optionalTournamentResumeID(resumeID)
	if event != nil {
		payload.Envelope.EventID = event.ID
		payload.Envelope.OccurredAt = event.OccurredAt
	}
}

func optionalTournamentResumeID(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	clone := value
	return &clone
}

func tournamentDeliveryAudience(
	scope tournamentConnectionScope,
	principal tournamentConnectionPrincipal,
) (eventdelivery.Audience, uuid.UUID, error) {
	switch scope.Role {
	case TournamentRolePublic:
		return eventdelivery.AudiencePublic, uuid.Nil, nil
	case TournamentRoleParticipant:
		if principal.Player == nil || principal.Player.ID == uuid.Nil {
			return "", uuid.Nil, ErrTournamentWriteScope
		}
		return eventdelivery.AudienceParticipant, principal.Player.ID, nil
	case TournamentRoleOperator:
		if principal.OperatorSession == nil || principal.OperatorSession.Principal.PrincipalID == uuid.Nil {
			return "", uuid.Nil, ErrTournamentWriteScope
		}
		return eventdelivery.AudienceOperator, principal.OperatorSession.Principal.PrincipalID, nil
	default:
		return "", uuid.Nil, ErrTournamentWriteScope
	}
}

func (server *Server) closeRealtimeDeliverySession(
	ctx context.Context,
	session *realtimeDeliverySession,
) {
	if server == nil || server.realtimeDelivery == nil || session == nil {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultSessionCheckTimeout)
	defer cancel()
	_ = server.realtimeDelivery.closeSession(closeCtx, session, "connection_closed")
}

func (server *Server) writeTournamentSessionRejection(
	ctx context.Context,
	connection *coderws.Conn,
	writeScope tournamentWriteScope,
	session *realtimeDeliverySession,
	cause error,
) {
	encoded, err := MarshalTournamentRejected(TournamentRejectionFor(cause))
	if err != nil {
		return
	}
	if session != nil {
		_ = session.write(ctx, encoded)
		return
	}
	_ = writeTournamentMessage(ctx, connection, writeScope, encoded)
}

func writeTournamentSessionPing(
	ctx context.Context,
	connection *coderws.Conn,
	writeScope tournamentWriteScope,
	session *realtimeDeliverySession,
) error {
	if session != nil {
		return session.ping(ctx)
	}
	return writeTournamentPing(ctx, connection, writeScope)
}

func (server *Server) writeTournamentRejection(
	ctx context.Context,
	connection *coderws.Conn,
	writeScope tournamentWriteScope,
	cause error,
) {
	encoded, err := MarshalTournamentRejected(TournamentRejectionFor(cause))
	if err == nil {
		_ = writeTournamentMessage(ctx, connection, writeScope, encoded)
	}
}

func writeTournamentMessage(ctx context.Context, connection realtimeSocket, scope tournamentWriteScope, data []byte) error {
	if err := validateTournamentWrite(scope, data); err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, defaultWriteWait)
	defer cancel()
	return connection.Write(writeCtx, coderws.MessageText, data)
}

func writeTournamentPing(ctx context.Context, connection realtimeSocket, scope tournamentWriteScope) error {
	if err := scope.validate(); err != nil {
		return err
	}
	pingCtx, cancel := context.WithTimeout(ctx, defaultWriteWait)
	defer cancel()
	return connection.Ping(pingCtx)
}
