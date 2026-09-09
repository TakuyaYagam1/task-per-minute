package response

import (
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

func AdminSession(pair *authusecase.TokenPair, now time.Time) api.AdminSessionResponse {
	expiresIn := pair.AccessExpiresAt.Sub(now) / time.Second
	if expiresIn < 0 {
		expiresIn = 0
	}
	if expiresIn > math.MaxInt32 {
		expiresIn = math.MaxInt32
	}

	return api.AdminSessionResponse{
		ExpiresIn: Int64ToInt32(int64(expiresIn)),
	}
}

func PlayerManagement(player playerusecase.PlayerRecord) api.PlayerManagementView {
	return api.PlayerManagementView{
		Id:                 player.PlayerID,
		Username:           player.Username,
		CreatedAt:          player.CreatedAt,
		DeletedAt:          player.DeletedAt,
		Wins:               IntToInt32(player.Wins),
		AverageSolveTimeMs: player.AverageSolveTimeMs,
		StatsOverridden:    player.StatsOverridden,
	}
}

func PlayerManagementList(players []playerusecase.PlayerRecord) []api.PlayerManagementView {
	out := make([]api.PlayerManagementView, 0, len(players))
	for _, player := range players {
		out = append(out, PlayerManagement(player))
	}
	return out
}

func PlayerAuditEvent(event playerusecase.AuditEvent) api.PlayerAuditEvent {
	return api.PlayerAuditEvent{
		Id:           event.ID,
		ActorSubject: event.Actor.Subject,
		ActorJti:     event.Actor.JTI,
		Action:       api.PlayerAuditAction(event.Action),
		PlayerId:     event.PlayerID,
		BeforeState:  playerAuditState(event.BeforeState),
		AfterState:   playerAuditState(event.AfterState),
		CreatedAt:    event.CreatedAt,
	}
}

func PlayerAuditEvents(events []playerusecase.AuditEvent) []api.PlayerAuditEvent {
	out := make([]api.PlayerAuditEvent, 0, len(events))
	for _, event := range events {
		out = append(out, PlayerAuditEvent(event))
	}
	return out
}

func playerAuditState(state playerusecase.AuditState) api.PlayerAuditState {
	return api.PlayerAuditState{
		Username:           state.Username,
		Wins:               IntToInt32(state.Wins),
		AverageSolveTimeMs: state.AverageSolveTimeMs,
		StatsOverridden:    state.StatsOverridden,
		Deleted:            state.Deleted,
	}
}
