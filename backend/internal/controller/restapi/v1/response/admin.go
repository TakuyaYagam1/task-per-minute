package response

import (
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

func TokenPair(pair *adminusecase.TokenPair, now time.Time) openapi.AdminTokenResponse {
	expiresIn := pair.AccessExpiresAt.Sub(now) / time.Second
	if expiresIn < 0 {
		expiresIn = 0
	}
	if expiresIn > math.MaxInt32 {
		expiresIn = math.MaxInt32
	}

	return openapi.AdminTokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		TokenType:    openapi.Bearer,
		ExpiresIn:    Int64ToInt32(int64(expiresIn)),
	}
}
