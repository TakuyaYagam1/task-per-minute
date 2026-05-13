package response

import (
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

const CookieAdminSessionToken = "__cookie_admin_session__"

func TokenPair(pair *adminusecase.TokenPair, now time.Time) openapi.AdminTokenResponse {
	return tokenPair(pair.AccessToken, pair.RefreshToken, pair.AccessExpiresAt, now)
}

func CookieSessionTokenPair(pair *adminusecase.TokenPair, now time.Time) openapi.AdminTokenResponse {
	return tokenPair(CookieAdminSessionToken, CookieAdminSessionToken, pair.AccessExpiresAt, now)
}

func tokenPair(accessToken, refreshToken string, accessExpiresAt time.Time, now time.Time) openapi.AdminTokenResponse {
	expiresIn := accessExpiresAt.Sub(now) / time.Second
	if expiresIn < 0 {
		expiresIn = 0
	}
	if expiresIn > math.MaxInt32 {
		expiresIn = math.MaxInt32
	}

	return openapi.AdminTokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    openapi.Bearer,
		ExpiresIn:    Int64ToInt32(int64(expiresIn)),
	}
}

func AdminPlayer(player usecase.AdminPlayerRecord) openapi.AdminPlayerResponse {
	return openapi.AdminPlayerResponse{
		Id:                 player.PlayerID,
		Username:           player.Username,
		Status:             openapi.PlayerStatus(player.Status),
		CreatedAt:          player.CreatedAt,
		DeletedAt:          player.DeletedAt,
		Wins:               IntToInt32(player.Wins),
		AverageSolveTimeMs: player.AverageSolveTimeMs,
		StatsOverridden:    player.StatsOverridden,
	}
}

func AdminPlayers(players []usecase.AdminPlayerRecord) []openapi.AdminPlayerResponse {
	out := make([]openapi.AdminPlayerResponse, 0, len(players))
	for _, player := range players {
		out = append(out, AdminPlayer(player))
	}
	return out
}

func AdminPlayerAuditEvent(event usecase.AdminPlayerAuditEvent) openapi.AdminPlayerAuditEventResponse {
	return openapi.AdminPlayerAuditEventResponse{
		Id:           event.ID,
		ActorSubject: event.Actor.Subject,
		ActorJti:     event.Actor.JTI,
		Action:       openapi.AdminPlayerAuditAction(event.Action),
		PlayerId:     event.PlayerID,
		BeforeState:  adminPlayerAuditState(event.BeforeState),
		AfterState:   adminPlayerAuditState(event.AfterState),
		CreatedAt:    event.CreatedAt,
	}
}

func AdminPlayerAuditEvents(events []usecase.AdminPlayerAuditEvent) []openapi.AdminPlayerAuditEventResponse {
	out := make([]openapi.AdminPlayerAuditEventResponse, 0, len(events))
	for _, event := range events {
		out = append(out, AdminPlayerAuditEvent(event))
	}
	return out
}

func adminPlayerAuditState(state usecase.AdminPlayerAuditState) openapi.AdminPlayerAuditState {
	return openapi.AdminPlayerAuditState{
		Username:           state.Username,
		Status:             openapi.PlayerStatus(state.Status),
		Wins:               IntToInt32(state.Wins),
		AverageSolveTimeMs: state.AverageSolveTimeMs,
		StatsOverridden:    state.StatsOverridden,
		Deleted:            state.Deleted,
	}
}
