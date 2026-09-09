package middleware

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

type contextKey string

const (
	adminClaimsKey contextKey = "admin_claims"
	playerKey      contextKey = "player"
)

func GetAdminClaimsFromCtx(ctx context.Context) (*auth.Claims, bool) {
	claims, ok := ctx.Value(adminClaimsKey).(*auth.Claims)
	return claims, ok && claims != nil
}

func GetPlayerFromCtx(ctx context.Context) (*domain.Player, bool) {
	player, ok := ctx.Value(playerKey).(*domain.Player)
	return player, ok && player != nil
}

func withAdminClaims(ctx context.Context, claims *auth.Claims) context.Context {
	return context.WithValue(ctx, adminClaimsKey, claims)
}

func withPlayer(ctx context.Context, player *domain.Player) context.Context {
	return context.WithValue(ctx, playerKey, player)
}
