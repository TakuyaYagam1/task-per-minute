//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	taskrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/task"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

func TestSourceFiles_UploadSourceFile_HappyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newParallelTestDB(t)
	tasks := taskrepo.NewTaskPostgres(postgres.NewTxManager(pool))
	st := newSeaweedStorage(t)
	uc := taskusecase.NewSourceFiles(taskusecase.NewUseCase(tasks), st, nil)
	task := mustCreateTask(t, tasks, uniq("forensics"), domain.DifficultyEasy)
	payload := []byte{'P', 'K', 0x03, 0x04, 'z', 'i', 'p'}

	presignedURL, err := uc.UploadSourceFile(
		ctx, task.ID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)
	require.NoError(t, err)
	require.Contains(t, presignedURL, "X-Amz-Signature")
	require.Contains(t, presignedURL, "tasks/"+task.ID.String()+"/sources/")

	got, err := tasks.GetByID(ctx, task.ID)
	require.NoError(t, err)
	require.NotNil(t, got.SourceFileURL)
	require.Contains(t, *got.SourceFileURL, "tasks/"+task.ID.String()+"/sources/")
	require.NotContains(t, *got.SourceFileURL, "X-Amz-Signature")

	resp := httpGetWithTimeout(t, presignedURL)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, payload, body)
}

func TestSourceFiles_RetainsArchivesBeforeAssignmentAndAfterTaskDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newParallelTestDB(t)
	repository := taskrepo.NewTaskPostgres(postgres.NewTxManager(pool))
	storage := newSeaweedStorage(t)
	service := taskusecase.NewSourceFiles(taskusecase.NewUseCase(repository), storage, nil)
	task := mustCreateTask(t, repository, uniq("retention-before-assignment"), domain.DifficultyEasy)

	firstPayload := sourceArchivePayload("before-assignment-first")
	firstURL := uploadSourceArchive(ctx, t, service, repository, task.ID, firstPayload)
	secondPayload := sourceArchivePayload("before-assignment-second")
	secondURL := uploadSourceArchive(ctx, t, service, repository, task.ID, secondPayload)

	current, err := repository.GetByID(ctx, task.ID)
	require.NoError(t, err)
	cleared, err := service.ClearSourceFile(ctx, task.ID, sourceFileUpdateInput(current))
	require.NoError(t, err)
	require.Nil(t, cleared.SourceFileURL)
	require.ElementsMatch(t, []string{firstURL, secondURL}, sourceArchiveURLs(ctx, t, pool, task.ID))
	assertStoredSourceArchive(t, storage, firstURL, firstPayload)
	assertStoredSourceArchive(t, storage, secondURL, secondPayload)

	require.NoError(t, taskusecase.NewUseCase(repository).DeleteTask(ctx, task.ID))
	assertStoredSourceArchive(t, storage, firstURL, firstPayload)
	assertStoredSourceArchive(t, storage, secondURL, secondPayload)
}

func TestSourceFiles_RetainsArchivesDuringAndAfterActivePlay(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	repository := newTaskRepo()
	storage := newSeaweedStorage(t)
	service := taskusecase.NewSourceFiles(taskusecase.NewUseCase(repository), storage, nil)
	originalPayload := sourceArchivePayload("assigned-original")
	var originalTaskID uuid.UUID
	var originalURL string
	draft := createDraftMigrationFixtureWithContentHook(ctx, t, func(normalTaskIDs []uuid.UUID) {
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT id
			FROM tasks
			WHERE id = ANY($1::UUID[]) AND category = 'web'
			ORDER BY id
			LIMIT 1`, normalTaskIDs).Scan(&originalTaskID))
		originalURL = uploadSourceArchive(ctx, t, service, repository, originalTaskID, originalPayload)
	})
	fixture := createResultAuditMigrationFixtureFromDraft(ctx, t, draft)

	var taskID, snapshotID uuid.UUID
	var assignmentTaskVersion int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT task_id, task_version, snapshot_id
		FROM assignments
		WHERE id = $1`, fixture.assignmentID).Scan(&taskID, &assignmentTaskVersion, &snapshotID))
	var snapshotSourceURL *string
	var snapshotDigest []byte
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT source_file_url, content_digest
		FROM task_snapshots
		WHERE id = $1`, snapshotID).Scan(&snapshotSourceURL, &snapshotDigest))
	require.Equal(t, originalTaskID, taskID)
	require.NotNil(t, snapshotSourceURL)
	require.Equal(t, originalURL, *snapshotSourceURL)
	assertStoredSourceArchive(t, storage, *snapshotSourceURL, originalPayload)

	var tournamentCreatedAt time.Time
	var initialTournamentRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT created_at, revision
		FROM tournaments
		WHERE id = $1`, fixture.draft.tournamentID).Scan(
		&tournamentCreatedAt,
		&initialTournamentRevision,
	))
	activeAt := fixture.lockedAt.Add(time.Second)
	require.False(t, activeAt.Before(tournamentCreatedAt))
	_, err := sharedPool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'swiss', revision = revision + 1,
			started_at = $2, updated_at = $2
		WHERE id = $1`, fixture.draft.tournamentID, activeAt)
	require.NoError(t, err)
	var tournamentState string
	var tournamentRevision int64
	var tournamentStartedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, revision, started_at
		FROM tournaments
		WHERE id = $1`, fixture.draft.tournamentID).Scan(
		&tournamentState,
		&tournamentRevision,
		&tournamentStartedAt,
	))
	require.Equal(t, "swiss", tournamentState)
	require.Equal(t, initialTournamentRevision+1, tournamentRevision)
	require.True(t, tournamentStartedAt.Equal(activeAt))

	activePayload := sourceArchivePayload("active-replacement")
	activeURL := uploadSourceArchive(ctx, t, service, repository, taskID, activePayload)
	clearCurrentSource(ctx, t, service, repository, taskID)
	assertStoredSourceArchive(t, storage, originalURL, originalPayload)
	assertStoredSourceArchive(t, storage, activeURL, activePayload)
	assertAssignmentSnapshotUnchanged(
		ctx, t, fixture.assignmentID, taskID, assignmentTaskVersion, snapshotID, snapshotSourceURL, snapshotDigest, "active",
	)

	completedAt := activeAt.Add(time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignments
		SET state = 'completed', revision = revision + 1,
			updated_at = $2, completed_at = $2
		WHERE id = $1`, fixture.assignmentID, completedAt)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'completed', revision = revision + 1,
			started_at = $2, updated_at = $3, finished_at = $3
		WHERE id = $1`, fixture.draft.tournamentID, activeAt, completedAt)
	require.NoError(t, err)
	var tournamentFinishedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, revision, started_at, finished_at
		FROM tournaments
		WHERE id = $1`, fixture.draft.tournamentID).Scan(
		&tournamentState,
		&tournamentRevision,
		&tournamentStartedAt,
		&tournamentFinishedAt,
	))
	require.Equal(t, "completed", tournamentState)
	require.Equal(t, initialTournamentRevision+2, tournamentRevision)
	require.True(t, tournamentStartedAt.Equal(activeAt))
	require.True(t, tournamentFinishedAt.Equal(completedAt))

	completedFirstPayload := sourceArchivePayload("completed-first")
	completedFirstURL := uploadSourceArchive(ctx, t, service, repository, taskID, completedFirstPayload)
	completedSecondPayload := sourceArchivePayload("completed-second")
	completedSecondURL := uploadSourceArchive(ctx, t, service, repository, taskID, completedSecondPayload)
	clearCurrentSource(ctx, t, service, repository, taskID)
	assertStoredSourceArchive(t, storage, originalURL, originalPayload)
	assertStoredSourceArchive(t, storage, completedFirstURL, completedFirstPayload)
	assertStoredSourceArchive(t, storage, completedSecondURL, completedSecondPayload)
	assertAssignmentSnapshotUnchanged(
		ctx, t, fixture.assignmentID, taskID, assignmentTaskVersion, snapshotID, snapshotSourceURL, snapshotDigest, "completed",
	)
	require.ElementsMatch(
		t,
		[]string{originalURL, activeURL, completedFirstURL, completedSecondURL},
		sourceArchiveURLs(ctx, t, sharedPool, taskID),
	)
}

func TestSourceFiles_ConcurrentPublicationRetainsEveryCommittedArchive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newParallelTestDB(t)
	repository := taskrepo.NewTaskPostgres(postgres.NewTxManager(pool))
	storage := newSeaweedStorage(t)
	service := taskusecase.NewSourceFiles(taskusecase.NewUseCase(repository), storage, nil)
	task := mustCreateTask(t, repository, uniq("retention-concurrent-publication"), domain.DifficultyEasy)
	unrelatedTask := mustCreateTask(t, repository, uniq("retention-unrelated"), domain.DifficultyEasy)
	unrelatedPayload := sourceArchivePayload("unrelated")
	unrelatedURL := uploadSourceArchive(ctx, t, service, repository, unrelatedTask.ID, unrelatedPayload)

	initialPayload := sourceArchivePayload("publication-initial")
	initialURL := uploadSourceArchive(ctx, t, service, repository, task.ID, initialPayload)
	payloads := [][]byte{
		sourceArchivePayload("publication-one"),
		sourceArchivePayload("publication-two"),
	}

	type uploadResult struct {
		err error
	}
	start := make(chan struct{})
	results := make(chan uploadResult, len(payloads))
	var workers sync.WaitGroup
	for _, payload := range payloads {
		payload := append([]byte(nil), payload...)
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, uploadErr := service.UploadSourceFile(
				ctx, task.ID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
			)
			if uploadErr != nil {
				results <- uploadResult{err: uploadErr}
				return
			}
			results <- uploadResult{}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	assertStoredSourceArchive(t, storage, initialURL, initialPayload)
	for result := range results {
		require.NoError(t, result.err)
	}
	archives := sourceArchiveURLs(ctx, t, pool, task.ID)
	require.Len(t, archives, 3)
	storedPayloads := make(map[string]struct{}, len(archives))
	for _, archiveURL := range archives {
		storedPayloads[string(readStoredSourceArchive(t, storage, archiveURL))] = struct{}{}
	}
	for _, payload := range append(payloads, initialPayload) {
		_, ok := storedPayloads[string(payload)]
		require.True(t, ok, "every concurrently committed archive must remain retrievable")
	}
	require.Equal(t, []string{unrelatedURL}, sourceArchiveURLs(ctx, t, pool, unrelatedTask.ID))
	assertStoredSourceArchive(t, storage, unrelatedURL, unrelatedPayload)
}

func TestSourceFiles_AmbiguousUpdateResultRetainsCommittedArchive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newParallelTestDB(t)
	repository := taskrepo.NewTaskPostgres(postgres.NewTxManager(pool))
	storage := newSeaweedStorage(t)
	taskCatalog := taskusecase.NewUseCase(repository)
	task := mustCreateTask(t, repository, uniq("retention-ambiguous-update"), domain.DifficultyEasy)
	ambiguousErr := errors.New("update result unavailable")
	service := taskusecase.NewSourceFiles(
		&committedErrorCatalog{Catalog: taskCatalog, err: ambiguousErr},
		storage,
		nil,
	)
	payload := sourceArchivePayload("ambiguous-update")

	_, err := service.UploadSourceFile(
		ctx, task.ID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)
	require.ErrorIs(t, err, ambiguousErr)

	archives := sourceArchiveURLs(ctx, t, pool, task.ID)
	require.Len(t, archives, 1)
	assertStoredSourceArchive(t, storage, archives[0], payload)
}

type committedErrorCatalog struct {
	taskusecase.Catalog

	err error
}

func (catalog *committedErrorCatalog) UpdateTask(
	ctx context.Context,
	taskID uuid.UUID,
	input taskusecase.UpdateInput,
) (*domain.Task, error) {
	if _, err := catalog.Catalog.UpdateTask(ctx, taskID, input); err != nil {
		return nil, err
	}
	return nil, catalog.err
}

func sourceArchivePayload(label string) []byte {
	return append([]byte{'P', 'K', 0x03, 0x04}, label...)
}

func uploadSourceArchive(
	ctx context.Context,
	t *testing.T,
	service *taskusecase.SourceFiles,
	repository *taskrepo.TaskPostgres,
	taskID uuid.UUID,
	payload []byte,
) string {
	t.Helper()
	_, err := service.UploadSourceFile(ctx, taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip")
	require.NoError(t, err)
	current, err := repository.GetByID(ctx, taskID)
	require.NoError(t, err)
	require.NotNil(t, current.SourceFileURL)
	return *current.SourceFileURL
}

func clearCurrentSource(
	ctx context.Context,
	t *testing.T,
	service *taskusecase.SourceFiles,
	repository *taskrepo.TaskPostgres,
	taskID uuid.UUID,
) {
	t.Helper()
	current, err := repository.GetByID(ctx, taskID)
	require.NoError(t, err)
	cleared, err := service.ClearSourceFile(ctx, taskID, sourceFileUpdateInput(current))
	require.NoError(t, err)
	require.Nil(t, cleared.SourceFileURL)
}

func sourceFileUpdateInput(task *domain.Task) taskusecase.UpdateInput {
	return taskusecase.UpdateInput{
		Title: task.Title, Description: task.Description, Category: task.Category,
		Difficulty: task.Difficulty, TimeLimit: task.TimeLimit, Flag: task.Flag,
		Kind: task.Kind, Enabled: task.Enabled, Hints: append([]string(nil), task.Hints...),
		TaskURL: task.TaskURL, SourceFileURL: task.SourceFileURL,
	}
}

func sourceArchiveURLs(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	taskID uuid.UUID,
) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT source_file_url
		FROM task_versions
		WHERE task_id = $1 AND source_file_url IS NOT NULL
		ORDER BY version`, taskID)
	require.NoError(t, err)
	defer rows.Close()
	urls := make([]string, 0, 4)
	for rows.Next() {
		var sourceURL string
		require.NoError(t, rows.Scan(&sourceURL))
		urls = append(urls, sourceURL)
	}
	require.NoError(t, rows.Err())
	return urls
}

func assertStoredSourceArchive(
	t *testing.T,
	storage *objectstorage.SeaweedStorage,
	storedURL string,
	want []byte,
) {
	t.Helper()
	require.Equal(t, want, readStoredSourceArchive(t, storage, storedURL))
}

func readStoredSourceArchive(
	t *testing.T,
	storage *objectstorage.SeaweedStorage,
	storedURL string,
) []byte {
	t.Helper()
	marker := "/" + sharedSeaweed(t).bucket + "/"
	_, key, ok := strings.Cut(storedURL, marker)
	require.True(t, ok, "stored source URL must contain the configured bucket")
	presignedURL, err := storage.PresignedGetURL(context.Background(), key, time.Minute)
	require.NoError(t, err)
	resp := httpGetWithTimeout(t, presignedURL)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	payload, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return payload
}

func assertAssignmentSnapshotUnchanged(
	ctx context.Context,
	t *testing.T,
	assignmentID uuid.UUID,
	taskID uuid.UUID,
	taskVersion int,
	snapshotID uuid.UUID,
	snapshotSourceURL *string,
	snapshotDigest []byte,
	wantState string,
) {
	t.Helper()
	var gotTaskID, gotSnapshotID uuid.UUID
	var gotTaskVersion int
	var gotState string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT task_id, task_version, snapshot_id, state
		FROM assignments
		WHERE id = $1`, assignmentID).Scan(&gotTaskID, &gotTaskVersion, &gotSnapshotID, &gotState))
	require.Equal(t, taskID, gotTaskID)
	require.Equal(t, taskVersion, gotTaskVersion)
	require.Equal(t, snapshotID, gotSnapshotID)
	require.Equal(t, wantState, gotState)
	var gotSourceURL *string
	var gotDigest []byte
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT source_file_url, content_digest
		FROM task_snapshots
		WHERE id = $1`, snapshotID).Scan(&gotSourceURL, &gotDigest))
	require.Equal(t, snapshotSourceURL, gotSourceURL)
	require.Equal(t, snapshotDigest, gotDigest)
}
