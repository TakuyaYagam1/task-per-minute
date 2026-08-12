package middleware

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

type contextKey string

const (
	adminClaimsKey contextKey = "admin_claims"
	playerKey      contextKey = "player"
)

func GetAdminClaimsFromCtx(ctx context.Context) (*admin.Claims, bool) {
	claims, ok := ctx.Value(adminClaimsKey).(*admin.Claims)
	return claims, ok && claims != nil
}

func GetPlayerFromCtx(ctx context.Context) (*domain.Player, bool) {
	player, ok := ctx.Value(playerKey).(*domain.Player)
	return player, ok && player != nil
}

func withAdminClaims(ctx context.Context, claims *admin.Claims) context.Context {
	return context.WithValue(ctx, adminClaimsKey, claims)
}

func withPlayer(ctx context.Context, player *domain.Player) context.Context {
	return context.WithValue(ctx, playerKey, player)
}
