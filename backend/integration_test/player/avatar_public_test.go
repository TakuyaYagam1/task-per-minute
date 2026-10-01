//go:build integration

package player_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	avatarrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/avatar"
	leaderboardrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/leaderboard"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestPublicAvatarLookupAndLeaderboardMetadataUseCurrentActiveAvatar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newParallelTestDB(t)
	fixture := newDatabaseFixture(pool)
	current, err := fixture.players.Create(ctx, uniq("avatar_public_current"))
	require.NoError(t, err)
	missing, err := fixture.players.Create(ctx, uniq("avatar_public_missing"))
	require.NoError(t, err)
	deleted, err := fixture.players.Create(ctx, uniq("avatar_public_deleted"))
	require.NoError(t, err)

	currentData := []byte("current-avatar-bytes")
	currentDigest := sha256.Sum256(currentData)
	insertPublicAvatarMetadata(ctx, t, pool, current.ID, currentData, currentDigest[:])
	deletedData := []byte("deleted-avatar-bytes")
	deletedDigest := sha256.Sum256(deletedData)
	insertPublicAvatarMetadata(ctx, t, pool, deleted.ID, deletedData, deletedDigest[:])
	_, err = pool.Exec(ctx, `UPDATE players SET deleted_at = now() WHERE id = $1`, deleted.ID)
	require.NoError(t, err)

	avatarRepository := avatarrepo.NewRepository(postgres.NewTxManager(pool))
	metadata, err := avatarRepository.GetPublicAvatar(ctx, current.ID, currentDigest[:])
	require.NoError(t, err)
	require.Equal(t, current.ID, metadata.PlayerID)
	require.Equal(t, currentDigest[:], metadata.SHA256)
	require.Equal(t, "image/png", metadata.ContentType)
	oldDigest := sha256.Sum256([]byte("old-version"))
	assertPublicAvatarNotFound(t, ctx, avatarRepository, current.ID, oldDigest[:])
	assertPublicAvatarNotFound(t, ctx, avatarRepository, missing.ID, currentDigest[:])
	assertPublicAvatarNotFound(t, ctx, avatarRepository, deleted.ID, deletedDigest[:])

	pageReader := leaderboardusecase.NewPageUseCase(leaderboardrepo.NewLeaderboardPostgres(fixture.mgr))
	page, err := pageReader.Page(ctx, leaderboardusecase.PageQuery{
		Wins:    leaderboardusecase.WinsAll,
		Page:    1,
		PerPage: leaderboardusecase.DefaultPageSize,
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total, "deleted players must be absent from public ranking")
	entries := make(map[string]leaderboardusecase.Entry, len(page.Entries))
	for _, entry := range page.Entries {
		entries[entry.Username] = entry
	}
	currentEntry, ok := entries[current.Username]
	require.True(t, ok)
	require.NotNil(t, currentEntry.Avatar)
	require.Equal(t, current.ID, currentEntry.Avatar.PlayerID)
	require.Equal(t, hex.EncodeToString(currentDigest[:]), currentEntry.Avatar.Version)
	require.Equal(t, "image/png", currentEntry.Avatar.ContentType)

	missingEntry, ok := entries[missing.Username]
	require.True(t, ok)
	require.Nil(t, missingEntry.Avatar)
	_, ok = entries[deleted.Username]
	require.False(t, ok)
}

func insertPublicAvatarMetadata(ctx context.Context, t testing.TB, pool *pgxpool.Pool, playerID uuid.UUID, data, digest []byte) {
	t.Helper()
	objectID := uuid.New()
	objectKey := fmt.Sprintf("avatars/%s/%s.png", playerID, objectID)
	_, err := pool.Exec(ctx, `
		INSERT INTO player_avatar_objects (object_key, player_id, generation, lifecycle_state)
		VALUES ($1, $2, 1, 'active')`, objectKey, playerID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO player_avatars (player_id, object_key, content_type, size_bytes, sha256)
		VALUES ($1, $2, 'image/png', $3, $4)`, playerID, objectKey, len(data), digest)
	require.NoError(t, err)
}

func assertPublicAvatarNotFound(
	t testing.TB,
	ctx context.Context,
	repository *avatarrepo.Repository,
	playerID uuid.UUID,
	digest []byte,
) {
	t.Helper()
	_, err := repository.GetPublicAvatar(ctx, playerID, digest)
	require.ErrorIs(t, err, domain.ErrAvatarNotFound)
}
