package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

func TestProvideRESTServerWithClockUsesSharedClock(t *testing.T) {
	t.Parallel()

	clock := newOperatorTestClock(t)
	revocations := authmocks.NewMockRevocationStore(t)
	auth := newOperatorTestAuth(revocations, clock, "tournament-rest-clock-test-secret")
	server := provideRESTServerWithClock(
		nil,
		auth,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		restv1.HealthChecks{},
		clockFunc(clock.Now),
		loginRateLimiter{},
		adminRefreshRateLimiter{},
		joinRateLimiter{},
		leaderboardRateLimiter{},
		publicTournamentReadRateLimiter{},
		operatorTournamentReadRateLimiter{},
		operatorTournamentMutationRateLimiter{},
		participantTournamentReadRateLimiter{},
		participantTournamentMutationRateLimiter{},
		logkit.Noop(),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/login",
		strings.NewReader(`{"password":"operator-password"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.LoginAdmin(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		ExpiresIn int32 `json:"expires_in"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, int32(time.Hour/time.Second), response.ExpiresIn)
}
