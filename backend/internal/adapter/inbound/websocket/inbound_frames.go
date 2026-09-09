package websocket

import (
	"context"
	"time"

	coderws "github.com/coder/websocket"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/wirelimits"
)

const (
	maximumMalformedFrames = 3
	maximumInboundCommands = 8
	inboundCommandWindow   = 5 * time.Second
)

type inboundFrameResult struct {
	cause error
}

type inboundFrameBudget struct {
	malformed int
	commands  []time.Time
}

func (server *Server) readTournamentFrames(
	ctx context.Context,
	connection *coderws.Conn,
	scope tournamentWriteScope,
	principal tournamentConnectionPrincipal,
) <-chan inboundFrameResult {
	results := make(chan inboundFrameResult, 1)
	go func() {
		defer close(results)
		budget := inboundFrameBudget{}
		for {
			messageType, data, err := connection.Read(ctx)
			if err != nil {
				results <- inboundFrameResult{}
				return
			}
			now := time.Now().UTC()
			if messageType != coderws.MessageText || wirelimits.ValidateJSON(data) != nil {
				if budget.recordMalformed() {
					results <- inboundFrameResult{cause: ErrTournamentInboundFrame}
					return
				}
				continue
			}
			if !budget.allowCommand(now) {
				results <- inboundFrameResult{cause: ErrTournamentInboundRate}
				return
			}
			if err := server.validateInboundFrame(ctx, scope, principal); err != nil {
				results <- inboundFrameResult{cause: err}
				return
			}
		}
	}()
	return results
}

func (server *Server) validateInboundFrame(
	ctx context.Context,
	scope tournamentWriteScope,
	principal tournamentConnectionPrincipal,
) error {
	if scope.validate() != nil {
		return ErrTournamentInboundFrame
	}
	if scope.Role != TournamentRolePublic && !server.connectionSessionValid(ctx, principal) {
		return connectionAuthenticationError(principal)
	}
	// Tournament realtime is read-only. No client frame has authority to alter
	// a tournament, so valid but unsupported commands consume only the bounded
	// command budget above.
	return nil
}

func (budget *inboundFrameBudget) recordMalformed() bool {
	if budget == nil {
		return true
	}
	budget.malformed++
	return budget.malformed >= maximumMalformedFrames
}

func (budget *inboundFrameBudget) allowCommand(now time.Time) bool {
	if budget == nil || now.IsZero() || now.Location() != time.UTC {
		return false
	}
	cutoff := now.Add(-inboundCommandWindow)
	start := 0
	for start < len(budget.commands) && !budget.commands[start].After(cutoff) {
		start++
	}
	budget.commands = append(budget.commands[:0], budget.commands[start:]...)
	if len(budget.commands) >= maximumInboundCommands {
		return false
	}
	budget.commands = append(budget.commands, now)
	return true
}
