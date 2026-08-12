package response

import (
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

const CookieAdminSessionToken = "__cookie_admin_session__"

func TokenPair(pair *adminusecase.TokenPair, now time.Time) api.AdminTokenResponse {
	return tokenPair(pair.AccessToken, pair.RefreshToken, pair.AccessExpiresAt, now)
}

func CookieSessionTokenPair(pair *adminusecase.TokenPair, now time.Time) api.AdminTokenResponse {
	return tokenPair(CookieAdminSessionToken, CookieAdminSessionToken, pair.AccessExpiresAt, now)
}

func tokenPair(accessToken, refreshToken string, accessExpiresAt time.Time, now time.Time) api.AdminTokenResponse {
	expiresIn := accessExpiresAt.Sub(now) / time.Second
	if expiresIn < 0 {
		expiresIn = 0
	}
	if expiresIn > math.MaxInt32 {
		expiresIn = math.MaxInt32
	}

	return api.AdminTokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    api.Bearer,
		ExpiresIn:    Int64ToInt32(int64(expiresIn)),
	}
}

func AdminPlayer(player adminusecase.PlayerRecord) api.AdminPlayerResponse {
	return api.AdminPlayerResponse{
		Id:                 player.PlayerID,
		Username:           player.Username,
		Status:             api.PlayerStatus(player.Status),
		CreatedAt:          player.CreatedAt,
		DeletedAt:          player.DeletedAt,
		Wins:               IntToInt32(player.Wins),
		AverageSolveTimeMs: player.AverageSolveTimeMs,
		StatsOverridden:    player.StatsOverridden,
	}
}

func AdminPlayers(players []adminusecase.PlayerRecord) []api.AdminPlayerResponse {
	out := make([]api.AdminPlayerResponse, 0, len(players))
	for _, player := range players {
		out = append(out, AdminPlayer(player))
	}
	return out
}

func AdminPlayerAuditEvent(event adminusecase.PlayerAuditEvent) api.AdminPlayerAuditEventResponse {
	return api.AdminPlayerAuditEventResponse{
		Id:           event.ID,
		ActorSubject: event.Actor.Subject,
		ActorJti:     event.Actor.JTI,
		Action:       api.AdminPlayerAuditAction(event.Action),
		PlayerId:     event.PlayerID,
		BeforeState:  adminPlayerAuditState(event.BeforeState),
		AfterState:   adminPlayerAuditState(event.AfterState),
		CreatedAt:    event.CreatedAt,
	}
}

func AdminPlayerAuditEvents(events []adminusecase.PlayerAuditEvent) []api.AdminPlayerAuditEventResponse {
	out := make([]api.AdminPlayerAuditEventResponse, 0, len(events))
	for _, event := range events {
		out = append(out, AdminPlayerAuditEvent(event))
	}
	return out
}

func adminPlayerAuditState(state adminusecase.PlayerAuditState) api.AdminPlayerAuditState {
	return api.AdminPlayerAuditState{
		Username:           state.Username,
		Status:             api.PlayerStatus(state.Status),
		Wins:               IntToInt32(state.Wins),
		AverageSolveTimeMs: state.AverageSolveTimeMs,
		StatsOverridden:    state.StatsOverridden,
		Deleted:            state.Deleted,
	}
}
