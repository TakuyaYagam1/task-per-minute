//go:build integration

package player_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestPlayerAvatarVideoMigrationPreservesImagesAndAcceptsMP4(t *testing.T) {
	ctx := context.Background()
	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "avatar_video_migration")
	migrationsDir := migrationsDirAbs()
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 38))

	imagePlayerID := uuid.New()
	imageObjectKey := avatarMigrationObjectKey(imagePlayerID, "png")
	insertAvatarMigrationPlayer(t, ctx, pool, imagePlayerID)
	require.NoError(t, insertAvatarMigrationObject(ctx, pool, imagePlayerID, imageObjectKey, "active", nil))
	require.NoError(t, insertAvatarMigrationMetadata(ctx, pool, imagePlayerID, imageObjectKey, "image/png"))

	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 39))
	var preservedKey, preservedContentType string
	var preservedSize int64
	var preservedDigest []byte
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT object_key, content_type, size_bytes, sha256
		FROM player_avatars
		WHERE player_id = $1`, imagePlayerID,
	).Scan(&preservedKey, &preservedContentType, &preservedSize, &preservedDigest))
	require.Equal(t, imageObjectKey, preservedKey)
	require.Equal(t, "image/png", preservedContentType)
	require.EqualValues(t, 1, preservedSize)
	require.Len(t, preservedDigest, sha256.Size)

	videoPlayerID := uuid.New()
	videoObjectKey := avatarMigrationObjectKey(videoPlayerID, "mp4")
	insertAvatarMigrationPlayer(t, ctx, pool, videoPlayerID)
	require.NoError(t, insertAvatarMigrationObject(ctx, pool, videoPlayerID, videoObjectKey, "active", nil))
	require.NoError(t, insertAvatarMigrationMetadata(ctx, pool, videoPlayerID, videoObjectKey, "video/mp4"))

	badExtensionKey := avatarMigrationObjectKey(uuid.New(), "mov")
	err := insertAvatarMigrationObject(ctx, pool, uuid.New(), badExtensionKey, "deleting", ptrTime(time.Now().UTC()))
	require.ErrorContains(t, err, "player_avatar_objects_key_check")

	wrongContentPlayerID := uuid.New()
	wrongContentObjectKey := avatarMigrationObjectKey(wrongContentPlayerID, "mp4")
	insertAvatarMigrationPlayer(t, ctx, pool, wrongContentPlayerID)
	require.NoError(t, insertAvatarMigrationObject(ctx, pool, wrongContentPlayerID, wrongContentObjectKey, "active", nil))
	err = insertAvatarMigrationMetadata(ctx, pool, wrongContentPlayerID, wrongContentObjectKey, "video/quicktime")
	require.ErrorContains(t, err, "player_avatars_content_type_check")
}

func TestPlayerAvatarVideoMigrationRollbackGuardsCleanupAndRestoresChecks(t *testing.T) {
	ctx := context.Background()
	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "avatar_video_rollback")
	migrationsDir := migrationsDirAbs()
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 38))

	imagePlayerID := uuid.New()
	imageObjectKey := avatarMigrationObjectKey(imagePlayerID, "jpg")
	insertAvatarMigrationPlayer(t, ctx, pool, imagePlayerID)
	require.NoError(t, insertAvatarMigrationObject(ctx, pool, imagePlayerID, imageObjectKey, "active", nil))
	require.NoError(t, insertAvatarMigrationMetadata(ctx, pool, imagePlayerID, imageObjectKey, "image/jpeg"))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 39))

	cleanupPlayerID := uuid.New()
	cleanupObjectKey := avatarMigrationObjectKey(cleanupPlayerID, "mp4")
	require.NoError(t, insertAvatarMigrationObject(
		ctx, pool, cleanupPlayerID, cleanupObjectKey, "deleting", ptrTime(time.Now().UTC()),
	))
	var activeAvatarCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM player_avatars WHERE player_id = $1`, cleanupPlayerID,
	).Scan(&activeAvatarCount))
	require.Zero(t, activeAvatarCount, "cleanup-only MP4 object must have no active avatar metadata")

	err := goose.DownToContext(ctx, database, migrationsDir, 38)
	require.ErrorContains(t, err, "cannot remove MP4 avatar constraints while MP4 objects or cleanup work remain")
	var appliedVersion int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&appliedVersion))
	require.EqualValues(t, 39, appliedVersion, "a rejected rollback must keep migration 39 applied")
	var cleanupRows int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM player_avatar_objects WHERE object_key = $1 AND lifecycle_state = 'deleting'`, cleanupObjectKey,
	).Scan(&cleanupRows))
	require.Equal(t, 1, cleanupRows, "a rejected rollback must retain cleanup work")

	stillAcceptedPlayerID := uuid.New()
	stillAcceptedKey := avatarMigrationObjectKey(stillAcceptedPlayerID, "mp4")
	require.NoError(t, insertAvatarMigrationObject(
		ctx, pool, stillAcceptedPlayerID, stillAcceptedKey, "deleting", ptrTime(time.Now().UTC()),
	))
	_, err = pool.Exec(ctx, `
		DELETE FROM player_avatar_objects
		WHERE object_key IN ($1, $2)`, cleanupObjectKey, stillAcceptedKey)
	require.NoError(t, err)

	require.NoError(t, goose.DownToContext(ctx, database, migrationsDir, 38))
	var imageRows int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM player_avatars
		WHERE player_id = $1 AND object_key = $2 AND content_type = 'image/jpeg'`, imagePlayerID, imageObjectKey,
	).Scan(&imageRows))
	require.Equal(t, 1, imageRows, "a safe rollback must preserve existing image metadata")

	oldKeyRejected := avatarMigrationObjectKey(uuid.New(), "mp4")
	err = insertAvatarMigrationObject(ctx, pool, uuid.New(), oldKeyRejected, "deleting", ptrTime(time.Now().UTC()))
	require.ErrorContains(t, err, "player_avatar_objects_key_check")

	wrongTypePlayerID := uuid.New()
	wrongTypeObjectKey := avatarMigrationObjectKey(wrongTypePlayerID, "png")
	insertAvatarMigrationPlayer(t, ctx, pool, wrongTypePlayerID)
	require.NoError(t, insertAvatarMigrationObject(ctx, pool, wrongTypePlayerID, wrongTypeObjectKey, "active", nil))
	err = insertAvatarMigrationMetadata(ctx, pool, wrongTypePlayerID, wrongTypeObjectKey, "video/mp4")
	require.ErrorContains(t, err, "player_avatars_content_type_check")

	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 39))
	reenabledPlayerID := uuid.New()
	reenabledObjectKey := avatarMigrationObjectKey(reenabledPlayerID, "mp4")
	insertAvatarMigrationPlayer(t, ctx, pool, reenabledPlayerID)
	require.NoError(t, insertAvatarMigrationObject(ctx, pool, reenabledPlayerID, reenabledObjectKey, "active", nil))
	require.NoError(t, insertAvatarMigrationMetadata(ctx, pool, reenabledPlayerID, reenabledObjectKey, "video/mp4"))
}

func insertAvatarMigrationPlayer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, playerID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO players (id, username)
		VALUES ($1, $2)`, playerID, uniq("avatar_migration"))
	require.NoError(t, err)
}

func insertAvatarMigrationObject(
	ctx context.Context,
	pool *pgxpool.Pool,
	playerID uuid.UUID,
	objectKey string,
	lifecycleState string,
	cleanupAfter *time.Time,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO player_avatar_objects (
			object_key, player_id, generation, lifecycle_state, cleanup_after
		)
		VALUES ($1, $2, 1, $3, $4)`, objectKey, playerID, lifecycleState, cleanupAfter)
	return err
}

func insertAvatarMigrationMetadata(
	ctx context.Context,
	pool *pgxpool.Pool,
	playerID uuid.UUID,
	objectKey string,
	contentType string,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO player_avatars (
			player_id, object_key, content_type, size_bytes, sha256
		)
		VALUES ($1, $2, $3, 1, $4)`, playerID, objectKey, contentType, make([]byte, sha256.Size))
	return err
}

func avatarMigrationObjectKey(playerID uuid.UUID, extension string) string {
	return fmt.Sprintf("avatars/%s/%s.%s", playerID, uuid.New(), extension)
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
