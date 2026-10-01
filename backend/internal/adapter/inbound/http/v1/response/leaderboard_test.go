package response

import (
	"encoding/json"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestLeaderboardMapsOptionalAvatarMetadata(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	result := Leaderboard(leaderboardusecase.PageResult{Entries: []leaderboardusecase.Entry{
		{
			Rank:     1,
			Username: "alice",
			Avatar: &leaderboardusecase.AvatarMetadata{
				PlayerID:    playerID,
				Version:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				ContentType: "image/png",
			},
		},
		{Rank: 2, Username: "bob"},
	}})

	require.NotNil(t, result.Entries[0].Avatar)
	require.Equal(t, api.LeaderboardAvatar{
		PlayerId:    playerID,
		Version:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ContentType: api.LeaderboardAvatarContentType("image/png"),
	}, *result.Entries[0].Avatar)
	require.Nil(t, result.Entries[1].Avatar)

	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"avatar":{"content_type":"image/png"`)
	require.NotContains(t, string(encoded), "object_key")
	require.NotContains(t, string(encoded), "sha256")
	require.Contains(t, string(encoded), `"username":"bob"`)
	require.NotContains(t, string(encoded), `"username":"bob","avatar"`)
}
