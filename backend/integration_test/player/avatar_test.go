//go:build integration

package player_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	avatarrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/avatar"
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	avatarusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/avatar"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestPlayerAvatarUploadReadReplaceDeleteAndCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	players := playerrepo.NewPlayerPostgres(mgr)
	avatarRepository := avatarrepo.NewRepository(mgr)
	player, token := createVerifiedAccountSession(
		ctx, t, pool, mgr, players, uniq("avatar_owner"), time.Now().UTC().Add(time.Hour),
	)
	storage := newMemoryAvatarStorage()
	service, err := avatarusecase.NewService(avatarRepository, storage, avatarTestClock{}, nil)
	require.NoError(t, err)

	wrongToken := uuid.New()
	err = service.ReplaceAvatar(ctx, player.ID, wrongToken, "image/png", avatarPNG(t, color.NRGBA{R: 30, G: 60, B: 90, A: 255}))
	require.ErrorIs(t, err, domain.ErrInvalidSession)
	require.Empty(t, storage.keys())

	firstPNG := avatarPNG(t, color.NRGBA{R: 30, G: 60, B: 90, A: 255})
	require.NoError(t, service.ReplaceAvatar(ctx, player.ID, token, "image/png", firstPNG))
	first, err := avatarRepository.GetAvatar(ctx, player.ID, token)
	require.NoError(t, err)
	require.Contains(t, first.ObjectKey, "avatars/"+player.ID.String()+"/")
	require.Equal(t, "image/png", first.ContentType)
	firstContent, err := service.GetAvatar(ctx, player.ID, token)
	require.NoError(t, err)
	require.Equal(t, "image/png", firstContent.ContentType)
	require.Equal(t, first.SizeBytes, int64(len(firstContent.Data)))

	secondPNG := avatarPNG(t, color.NRGBA{R: 200, G: 40, B: 10, A: 255})
	putErr := errors.New("synthetic object storage failure")
	storage.setPutError(putErr)
	require.ErrorIs(t, service.ReplaceAvatar(ctx, player.ID, token, "image/png", secondPNG), putErr)
	activeAfterFailure, err := avatarRepository.GetAvatar(ctx, player.ID, token)
	require.NoError(t, err)
	require.Equal(t, first.ObjectKey, activeAfterFailure.ObjectKey)
	require.Contains(t, storage.keys(), first.ObjectKey, "a failed replacement must leave the active object untouched")
	storage.setPutError(nil)
	require.NoError(t, service.ReplaceAvatar(ctx, player.ID, token, "image/png", secondPNG))
	second, err := avatarRepository.GetAvatar(ctx, player.ID, token)
	require.NoError(t, err)
	require.NotEqual(t, first.ObjectKey, second.ObjectKey)
	var oldState string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT lifecycle_state FROM player_avatar_objects WHERE object_key = $1`, first.ObjectKey,
	).Scan(&oldState))
	require.Equal(t, "deleting", oldState)
	var activeCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM player_avatar_objects WHERE player_id = $1 AND lifecycle_state = 'active'`, player.ID,
	).Scan(&activeCount))
	require.Equal(t, 1, activeCount)
	require.Contains(t, storage.keys(), first.ObjectKey, "replacement must retain the old object until cleanup claims it")

	cleanupAvatarObjectWithRetry(ctx, t, avatarRepository, storage, first.ObjectKey)
	require.NotContains(t, storage.keys(), first.ObjectKey)
	secondContent, err := service.GetAvatar(ctx, player.ID, token)
	require.NoError(t, err)
	require.Equal(t, "image/png", secondContent.ContentType)

	require.NoError(t, service.DeleteAvatar(ctx, player.ID, token))
	_, err = service.GetAvatar(ctx, player.ID, token)
	require.ErrorIs(t, err, domain.ErrAvatarNotFound)
	var metadataCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM player_avatars WHERE player_id = $1`, player.ID,
	).Scan(&metadataCount))
	require.Zero(t, metadataCount)
	cleanupAvatarObject(ctx, t, avatarRepository, storage, second.ObjectKey, time.Now().UTC().Add(time.Second))
	require.NotContains(t, storage.keys(), second.ObjectKey)
}

func TestPlayerAvatarDeleteInvalidatesInflightUploadAndKeepsCleanupLease(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	players := playerrepo.NewPlayerPostgres(mgr)
	avatarRepository := avatarrepo.NewRepository(mgr)
	player, token := createVerifiedAccountSession(
		ctx, t, pool, mgr, players, uniq("avatar_race"), time.Now().UTC().Add(time.Hour),
	)
	storage := &blockingAvatarStorage{memoryAvatarStorage: newMemoryAvatarStorage(), started: make(chan struct{}), release: make(chan struct{})}
	service, err := avatarusecase.NewService(avatarRepository, storage, avatarTestClock{}, nil)
	require.NoError(t, err)

	replaceDone := make(chan error, 1)
	go func() {
		replaceDone <- service.ReplaceAvatar(ctx, player.ID, token, "image/png", avatarPNG(t, color.NRGBA{R: 80, G: 100, B: 120, A: 255}))
	}()
	select {
	case <-storage.started:
	case <-ctx.Done():
		t.Fatal("avatar upload did not reach object storage")
	}
	// The request has durably staged its object key, but storage has not finished.
	require.NoError(t, service.DeleteAvatar(ctx, player.ID, token))
	close(storage.release)
	require.ErrorIs(t, <-replaceDone, domain.ErrConflict)
	_, err = service.GetAvatar(ctx, player.ID, token)
	require.ErrorIs(t, err, domain.ErrAvatarNotFound)

	var lifecycleState string
	var cleanupAfter time.Time
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT lifecycle_state, cleanup_after
		FROM player_avatar_objects
		WHERE player_id = $1`, player.ID,
	).Scan(&lifecycleState, &cleanupAfter))
	require.Equal(t, "uploading", lifecycleState)
	require.True(t, cleanupAfter.After(time.Now().UTC()), "cleanup must wait beyond the bounded object upload")

	claimToken := uuid.New()
	items, err := avatarRepository.ClaimCleanup(ctx, time.Now().UTC(), time.Now().UTC().Add(time.Minute), claimToken, 10)
	require.NoError(t, err)
	require.Empty(t, items, "the upload intent remains leased until the request deadline")
	cleanupAvatarObject(ctx, t, avatarRepository, storage, storage.lastKey(), time.Now().UTC().Add(6*time.Minute))
	require.Empty(t, storage.keys())
}

func TestPlayerAvatarAdminSoftDeleteQueuesObjectAndRevokesOwnerSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	players := playerrepo.NewPlayerPostgres(mgr)
	avatarRepository := avatarrepo.NewRepository(mgr)
	player, token := createVerifiedAccountSession(
		ctx, t, pool, mgr, players, uniq("avatar_deleted"), time.Now().UTC().Add(time.Hour),
	)
	storage := newMemoryAvatarStorage()
	service, err := avatarusecase.NewService(avatarRepository, storage, avatarTestClock{}, nil)
	require.NoError(t, err)
	require.NoError(t, service.ReplaceAvatar(ctx, player.ID, token, "image/png", avatarPNG(t, color.NRGBA{R: 10, G: 20, B: 30, A: 255})))
	avatar, err := avatarRepository.GetAvatar(ctx, player.ID, token)
	require.NoError(t, err)

	management := playerusecase.ManagementNewUseCase(mgr, players, avatarTestLeaderboard{}, avatarTestClock{})
	require.NoError(t, management.DeletePlayer(ctx, player.ID, playerusecase.Actor{
		Subject: "integration-admin",
		JTI:     uuid.NewString(),
	}))
	_, err = service.GetAvatar(ctx, player.ID, token)
	require.ErrorIs(t, err, domain.ErrAccountDeleted)
	var metadataCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM player_avatars WHERE player_id = $1`, player.ID,
	).Scan(&metadataCount))
	require.Zero(t, metadataCount)
	var state string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT lifecycle_state FROM player_avatar_objects WHERE object_key = $1`, avatar.ObjectKey,
	).Scan(&state))
	require.Equal(t, "deleting", state)
	require.Contains(t, storage.keys(), avatar.ObjectKey, "soft delete must leave object cleanup durable")
	cleanupAvatarObject(ctx, t, avatarRepository, storage, avatar.ObjectKey, time.Now().UTC().Add(time.Second))
	require.NotContains(t, storage.keys(), avatar.ObjectKey)
}

func TestPlayerAvatarHardDeleteRetainsObjectCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := newParallelTestDB(t)
	mgr := postgres.NewTxManager(pool)
	players := playerrepo.NewPlayerPostgres(mgr)
	avatarRepository := avatarrepo.NewRepository(mgr)
	player, token := createVerifiedAccountSession(
		ctx, t, pool, mgr, players, uniq("avatar_hard_deleted"), time.Now().UTC().Add(time.Hour),
	)
	storage := newMemoryAvatarStorage()
	service, err := avatarusecase.NewService(avatarRepository, storage, avatarTestClock{}, nil)
	require.NoError(t, err)
	require.NoError(t, service.ReplaceAvatar(ctx, player.ID, token, "image/png", avatarPNG(t, color.NRGBA{R: 40, G: 80, B: 120, A: 255})))
	avatar, err := avatarRepository.GetAvatar(ctx, player.ID, token)
	require.NoError(t, err)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hard-delete setup transaction: %v", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("rollback hard-delete setup transaction: %v", rollbackErr)
		}
	}()
	reservationTag, err := tx.Exec(ctx, `
		DELETE FROM player_username_reservations
		WHERE account_id = (SELECT id FROM player_accounts WHERE player_id = $1)`, player.ID,
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, reservationTag.RowsAffected())
	accountTag, err := tx.Exec(ctx, `DELETE FROM player_accounts WHERE player_id = $1`, player.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, accountTag.RowsAffected())
	playerTag, err := tx.Exec(ctx, `DELETE FROM players WHERE id = $1`, player.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, playerTag.RowsAffected())
	require.NoError(t, tx.Commit(ctx))
	var metadataCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM player_avatars WHERE player_id = $1`, player.ID).Scan(&metadataCount))
	require.Zero(t, metadataCount)
	var state string
	require.NoError(t, pool.QueryRow(ctx, `SELECT lifecycle_state FROM player_avatar_objects WHERE object_key = $1`, avatar.ObjectKey).Scan(&state))
	require.Equal(t, "deleting", state)
	require.Contains(t, storage.keys(), avatar.ObjectKey, "hard deletion must preserve its object cleanup job")
	cleanupAvatarObject(ctx, t, avatarRepository, storage, avatar.ObjectKey, time.Now().UTC().Add(time.Second))
	require.NotContains(t, storage.keys(), avatar.ObjectKey)
}

func cleanupAvatarObject(
	ctx context.Context,
	t *testing.T,
	repository *avatarrepo.Repository,
	storage interface {
		DeleteAvatar(ctx context.Context, objectKey string) error
	},
	objectKey string,
	now time.Time,
) {
	t.Helper()
	claimToken := uuid.New()
	items, err := repository.ClaimCleanup(ctx, now, now.Add(time.Minute), claimToken, 10)
	require.NoError(t, err)
	var found bool
	for _, item := range items {
		if item.ObjectKey != objectKey {
			continue
		}
		found = true
		require.NoError(t, storage.DeleteAvatar(ctx, item.ObjectKey))
		require.NoError(t, repository.CompleteCleanup(ctx, item.ObjectKey, claimToken))
	}
	require.True(t, found, "expected a durable cleanup claim for %q", objectKey)
}

func cleanupAvatarObjectWithRetry(
	ctx context.Context,
	t *testing.T,
	repository *avatarrepo.Repository,
	storage *memoryAvatarStorage,
	objectKey string,
) {
	t.Helper()
	now := time.Now().UTC().Add(time.Second)
	claimToken := uuid.New()
	items, err := repository.ClaimCleanup(ctx, now, now.Add(time.Minute), claimToken, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, objectKey, items[0].ObjectKey)

	deleteErr := errors.New("synthetic transient delete failure")
	storage.setDeleteError(deleteErr)
	require.ErrorIs(t, storage.DeleteAvatar(ctx, objectKey), deleteErr)
	retryAt := now.Add(2 * time.Minute)
	require.NoError(t, repository.RetryCleanup(ctx, objectKey, claimToken, retryAt))
	storage.setDeleteError(nil)

	items, err = repository.ClaimCleanup(ctx, retryAt.Add(-time.Second), retryAt, uuid.New(), 10)
	require.NoError(t, err)
	require.Empty(t, items, "cleanup retry must not be claimable before its retry time")

	claimToken = uuid.New()
	items, err = repository.ClaimCleanup(ctx, retryAt.Add(time.Nanosecond), retryAt.Add(time.Minute), claimToken, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, objectKey, items[0].ObjectKey)
	require.NoError(t, storage.DeleteAvatar(ctx, objectKey))
	require.NoError(t, repository.CompleteCleanup(ctx, objectKey, claimToken))
}

func avatarPNG(t *testing.T, fill color.NRGBA) []byte {
	t.Helper()
	imageData := image.NewNRGBA(image.Rect(0, 0, 10, 8))
	for y := 0; y < imageData.Bounds().Dy(); y++ {
		for x := 0; x < imageData.Bounds().Dx(); x++ {
			imageData.SetNRGBA(x, y, fill)
		}
	}
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, imageData))
	return encoded.Bytes()
}

type avatarTestClock struct{}

func (avatarTestClock) Now() time.Time {
	return time.Now().UTC()
}

type avatarTestLeaderboard struct{}

func (avatarTestLeaderboard) Invalidate() {}

type memoryAvatarStorage struct {
	mu          sync.Mutex
	objects     map[string][]byte
	putError    error
	deleteError error
}

func newMemoryAvatarStorage() *memoryAvatarStorage {
	return &memoryAvatarStorage{objects: make(map[string][]byte)}
}

func (s *memoryAvatarStorage) PutAvatar(_ context.Context, key string, data []byte, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putError != nil {
		return s.putError
	}
	s.objects[key] = append([]byte(nil), data...)
	return nil
}

func (s *memoryAvatarStorage) GetAvatar(_ context.Context, key string, maxBytes int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, domain.ErrAvatarNotFound
	}
	if int64(len(data)) > maxBytes {
		return nil, domain.ErrAvatarTooLarge
	}
	return append([]byte(nil), data...), nil
}

func (s *memoryAvatarStorage) DeleteAvatar(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteError != nil {
		return s.deleteError
	}
	delete(s.objects, key)
	return nil
}

func (s *memoryAvatarStorage) setPutError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putError = err
}

func (s *memoryAvatarStorage) setDeleteError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteError = err
}

func (s *memoryAvatarStorage) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		keys = append(keys, key)
	}
	return keys
}

type blockingAvatarStorage struct {
	*memoryAvatarStorage

	started chan struct{}
	release chan struct{}
	last    string
}

func (s *blockingAvatarStorage) PutAvatar(ctx context.Context, key string, data []byte, contentType string) error {
	s.last = key
	close(s.started)
	select {
	case <-s.release:
		return s.memoryAvatarStorage.PutAvatar(ctx, key, data, contentType)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *blockingAvatarStorage) lastKey() string {
	return s.last
}
