//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

type integrationTournamentContentSelection = api.TournamentContentSelection

type integrationTournamentContentPublication struct {
	integrationTournamentContentSelection
	NormalTaskID uuid.UUID
	GoldenTaskID uuid.UUID
}

func TestTournamentContentDiscoveryThroughProductionHandlers(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	publication := prepareTournamentContentPublication(ctx, t, uniq("content_discovery"))
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)

	request, response := doTournamentFlowJSON(
		t, fixture, http.MethodGet, "/api/v1/admin/tournament-content", "",
		"", uuid.New(), "",
	)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	fixture.validateResponse(t, request, response)

	request, response = doTournamentFlowJSON(
		t, fixture, http.MethodGet, "/api/v1/admin/tournament-content", "",
		adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	discovered := decodeJSON[integrationTournamentContentSelection](t, response)
	require.Equal(t, publication.ContentRevision, discovered.ContentRevision)
	require.Equal(t, publication.PublicationId, discovered.PublicationId)
	require.True(t, publication.PublishedAt.Equal(discovered.PublishedAt))
	require.Equal(t, publication.NormalPoolRevisionId, discovered.NormalPoolRevisionId)
	require.Equal(t, publication.GoldenPoolRevisionId, discovered.GoldenPoolRevisionId)
	require.NotEqual(t, discovered.NormalPoolRevisionId, discovered.GoldenPoolRevisionId)
	assertContentPoolsSharePublication(ctx, t, discovered)
}

func TestTournamentCreateBindsExactSelectedContentAcrossPublicationRefresh(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	first := prepareTournamentContentPublication(ctx, t, uniq("content_first"))
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	selected := getTournamentContentThroughREST(t, fixture, adminToken)
	require.Equal(t, first.ContentRevision, selected.ContentRevision)

	firstTournament, firstPublicID := createTournamentWithContentThroughREST(
		t, fixture, adminToken, selected.ContentRevision, "first",
	)
	require.Equal(t, selected.ContentRevision, firstTournament.ContentRevision)
	assertTournamentContentBinding(ctx, t, firstPublicID, selected)

	second := prepareTournamentContentPublication(ctx, t, uniq("content_second"))
	refreshed := getTournamentContentThroughREST(t, fixture, adminToken)
	require.Equal(t, second.ContentRevision, refreshed.ContentRevision)
	require.NotEqual(t, selected.PublicationId, refreshed.PublicationId)
	require.NotEqual(t, selected.NormalPoolRevisionId, refreshed.NormalPoolRevisionId)
	require.NotEqual(t, selected.GoldenPoolRevisionId, refreshed.GoldenPoolRevisionId)
	assertTournamentContentBinding(ctx, t, firstPublicID, selected)

	oldTournament, oldPublicID := createTournamentWithContentThroughREST(
		t, fixture, adminToken, selected.ContentRevision, "old-selection",
	)
	require.Equal(t, selected.ContentRevision, oldTournament.ContentRevision)
	assertTournamentContentBinding(ctx, t, oldPublicID, selected)

	newTournament, newPublicID := createTournamentWithContentThroughREST(
		t, fixture, adminToken, refreshed.ContentRevision, "new-selection",
	)
	require.Equal(t, refreshed.ContentRevision, newTournament.ContentRevision)
	assertTournamentContentBinding(ctx, t, newPublicID, refreshed)

	retryCommandID := uuid.New()
	retryPublicID := tournamentContentPublicID("retry")
	firstRetry := createTournamentWithContentCommandThroughREST(
		t, fixture, adminToken, retryPublicID, refreshed.ContentRevision, retryCommandID,
	)
	prepareTournamentContentPublication(ctx, t, uniq("content_retry_refresh"))
	markTournamentContentTaskUnhealthy(ctx, t, second.NormalTaskID)
	replayedRetry := createTournamentWithContentCommandThroughREST(
		t, fixture, adminToken, retryPublicID, refreshed.ContentRevision, retryCommandID,
	)
	require.Equal(t, firstRetry, replayedRetry)
	assertTournamentContentBinding(ctx, t, retryPublicID, refreshed)
}

func TestTournamentCreateRejectsMissingOrUnusableContentAtomically(t *testing.T) {
	t.Run("empty published pools are unusable", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

		fixture := newTournamentFlowRESTFixture(t)
		adminToken := fixture.adminAccessToken(t)
		request, response := doTournamentFlowJSON(
			t, fixture, http.MethodGet, "/api/v1/admin/tournament-content", "",
			adminSession(adminToken), uuid.New(), "",
		)
		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		fixture.validateResponse(t, request, response)
		assertNoTournamentAggregate(ctx, t)
	})

	t.Run("missing content revision is malformed", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

		prepareTournamentContentPublication(ctx, t, uniq("content_missing"))
		fixture := newTournamentFlowRESTFixture(t)
		adminToken := fixture.adminAccessToken(t)
		publicID := tournamentContentPublicID("missing-content-revision")
		body := fmt.Sprintf(`{
			"preset":"tournament_v1",
			"expected_revision":0,
			"name":"Missing Content Revision",
			"public_id":%q,
			"planned_roster_size":4
		}`, publicID)

		request, response := postTournamentCreateThroughREST(t, fixture, adminToken, body, uuid.New())
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		fixture.validateResponse(t, request, response)
		assertNoTournamentAggregate(ctx, t)
	})

	t.Run("nonexistent revision is unusable and rolls back", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

		publication := prepareTournamentContentPublication(ctx, t, uniq("content_missing_publication"))
		fixture := newTournamentFlowRESTFixture(t)
		adminToken := fixture.adminAccessToken(t)
		selected := getTournamentContentThroughREST(t, fixture, adminToken)
		require.Equal(t, publication.ContentRevision, selected.ContentRevision)
		publicID := tournamentContentPublicID("unknown-content-revision")
		body := tournamentCreateBody(publicID, selected.ContentRevision+100)

		request, response := postTournamentCreateThroughREST(t, fixture, adminToken, body, uuid.New())
		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		fixture.validateResponse(t, request, response)
		assertNoTournamentAggregate(ctx, t)
	})

	t.Run("latest publication discovery fails closed when unhealthy", func(t *testing.T) {
		ctx := context.Background()
		truncateRoundProofTables(ctx, t)
		t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

		publication := prepareTournamentContentPublication(ctx, t, uniq("content_unhealthy_latest"))
		markTournamentContentTaskUnhealthy(ctx, t, publication.NormalTaskID)
		fixture := newTournamentFlowRESTFixture(t)
		adminToken := fixture.adminAccessToken(t)

		request, response := doTournamentFlowJSON(
			t, fixture, http.MethodGet, "/api/v1/admin/tournament-content", "",
			adminSession(adminToken), uuid.New(), "",
		)
		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		fixture.validateResponse(t, request, response)
		assertNoTournamentAggregate(ctx, t)
	})

	for _, testCase := range []struct {
		name       string
		unusable   func(context.Context, *testing.T, integrationTournamentContentPublication)
		publicSlug string
	}{
		{
			name: "disabled selected task",
			unusable: func(ctx context.Context, t *testing.T, publication integrationTournamentContentPublication) {
				disableTournamentContentTask(ctx, t, publication.NormalTaskID)
			},
			publicSlug: "disabled-content",
		},
		{
			name: "unhealthy selected task",
			unusable: func(ctx context.Context, t *testing.T, publication integrationTournamentContentPublication) {
				markTournamentContentTaskUnhealthy(ctx, t, publication.NormalTaskID)
			},
			publicSlug: "unhealthy-content",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			truncateRoundProofTables(ctx, t)
			t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

			publication := prepareTournamentContentPublication(ctx, t, uniq("content_selected_unusable"))
			fixture := newTournamentFlowRESTFixture(t)
			adminToken := fixture.adminAccessToken(t)
			selected := getTournamentContentThroughREST(t, fixture, adminToken)
			require.Equal(t, publication.ContentRevision, selected.ContentRevision)
			testCase.unusable(ctx, t, publication)
			publicID := tournamentContentPublicID(testCase.publicSlug)
			commandID := uuid.New()
			request, response := postTournamentCreateThroughREST(
				t, fixture, adminToken, tournamentCreateBody(publicID, selected.ContentRevision), commandID,
			)
			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			fixture.validateResponse(t, request, response)
			assertNoTournamentAggregate(ctx, t)

			if testCase.name == "unhealthy selected task" {
				markTournamentContentTaskHealthy(ctx, t, publication.NormalTaskID)
				retried := createTournamentWithContentCommandThroughREST(
					t, fixture, adminToken, publicID, selected.ContentRevision, commandID,
				)
				replayed := createTournamentWithContentCommandThroughREST(
					t, fixture, adminToken, publicID, selected.ContentRevision, commandID,
				)
				require.Equal(t, retried, replayed)
				assertTournamentContentBinding(ctx, t, publicID, selected)
			}
		})
	}
}

func getTournamentContentThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
) integrationTournamentContentSelection {
	t.Helper()
	request, response := doTournamentFlowJSON(
		t, fixture, http.MethodGet, "/api/v1/admin/tournament-content", "",
		adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	return decodeJSON[integrationTournamentContentSelection](t, response)
}

func createTournamentWithContentThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	contentRevision int64,
	name string,
) (tournamentView api.Tournament, publicID string) {
	t.Helper()
	publicID = tournamentContentPublicID(name)
	return createTournamentWithContentCommandThroughREST(
		t, fixture, adminToken, publicID, contentRevision, uuid.New(),
	), publicID
}

func createTournamentWithContentCommandThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	publicID string,
	contentRevision int64,
	commandID uuid.UUID,
) api.Tournament {
	t.Helper()
	request, response := postTournamentCreateThroughREST(
		t, fixture, adminToken, tournamentCreateBody(publicID, contentRevision), commandID,
	)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	return decodeJSON[api.Tournament](t, response)
}

func postTournamentCreateThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	body string,
	commandID uuid.UUID,
) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	return doTournamentFlowJSON(
		t, fixture, http.MethodPost, "/api/v1/admin/tournaments", body,
		adminSession(adminToken), commandID, "",
	)
}

func tournamentContentPublicID(label string) string {
	return "content-" + label + "-" + uuid.NewString()[:16]
}

func tournamentCreateBody(publicID string, contentRevision int64) string {
	return fmt.Sprintf(`{
		"preset":"tournament_v1",
		"expected_revision":0,
		"name":%q,
		"public_id":%q,
		"planned_roster_size":4,
		"content_revision":%d
	}`, "Content Selection "+publicID, publicID, contentRevision)
}

func prepareTournamentContentPublication(
	ctx context.Context,
	t testing.TB,
	prefix string,
) integrationTournamentContentPublication {
	t.Helper()
	var publication integrationTournamentContentPublication
	for _, task := range []struct {
		kind string
		name string
	}{
		{kind: "normal", name: "normal"},
		{kind: "golden", name: "golden"},
	} {
		title := prefix + "_" + task.name
		flag := prefix + "-" + task.name
		var taskID uuid.UUID
		err := sharedPool.QueryRow(ctx, `
			INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
			VALUES ($1, 'content selection fixture', 'web', 'easy', 60, $2, $3)
			RETURNING id`, title, flag, task.kind).Scan(&taskID)
		require.NoError(t, err)
		if task.kind == "normal" {
			publication.NormalTaskID = taskID
		} else {
			publication.GoldenTaskID = taskID
		}
	}
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT version.task_id, version.version, 1, true, 'content_validation'
		FROM task_versions AS version
		JOIN tasks AS task ON task.id = version.task_id
		WHERE task.id IN ($1, $2)`, publication.NormalTaskID, publication.GoldenTaskID)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `SELECT publish_task_pool_heads()`)
	require.NoError(t, err)
	err = sharedPool.QueryRow(ctx, `
		SELECT publication.id,
			publication.revision,
			publication.published_at,
			normal_pool.id,
			golden_pool.id,
			normal_membership.task_id,
			golden_membership.task_id
		FROM task_pool_publications AS publication
		JOIN task_pool_revisions AS normal_pool
			ON normal_pool.publication_id = publication.id AND normal_pool.kind = 'normal'
		JOIN task_pool_revisions AS golden_pool
			ON golden_pool.publication_id = publication.id AND golden_pool.kind = 'golden'
		JOIN LATERAL (
			SELECT membership.task_id
			FROM task_pool_version_memberships AS membership
			WHERE membership.task_pool_revision_id = normal_pool.id
			ORDER BY membership.task_id
			LIMIT 1
		) AS normal_membership ON true
		JOIN LATERAL (
			SELECT membership.task_id
			FROM task_pool_version_memberships AS membership
			WHERE membership.task_pool_revision_id = golden_pool.id
			ORDER BY membership.task_id
			LIMIT 1
		) AS golden_membership ON true
		WHERE publication.revision = (
			SELECT MAX(revision) FROM task_pool_publications
		)`).
		Scan(
			&publication.PublicationId,
			&publication.ContentRevision,
			&publication.PublishedAt,
			&publication.NormalPoolRevisionId,
			&publication.GoldenPoolRevisionId,
			&publication.NormalTaskID,
			&publication.GoldenTaskID,
		)
	require.NoError(t, err)
	return publication
}

func assertContentPoolsSharePublication(
	ctx context.Context,
	t testing.TB,
	selection integrationTournamentContentSelection,
) {
	t.Helper()
	var normalPublicationID, goldenPublicationID uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		SELECT normal_pool.publication_id, golden_pool.publication_id
		FROM task_pool_revisions AS normal_pool
		JOIN task_pool_revisions AS golden_pool ON golden_pool.id = $2
		WHERE normal_pool.id = $1`, selection.NormalPoolRevisionId, selection.GoldenPoolRevisionId).
		Scan(&normalPublicationID, &goldenPublicationID)
	require.NoError(t, err)
	require.Equal(t, selection.PublicationId, normalPublicationID)
	require.Equal(t, selection.PublicationId, goldenPublicationID)
}

func assertTournamentContentBinding(
	ctx context.Context,
	t testing.TB,
	publicID string,
	selection integrationTournamentContentSelection,
) {
	t.Helper()
	var publicationID, normalPoolID, goldenPoolID uuid.UUID
	var revision int64
	err := sharedPool.QueryRow(ctx, `
		SELECT configuration.pool_publication_id,
			configuration.normal_pool_revision_id,
			configuration.golden_pool_revision_id,
			publication.revision
		FROM tournaments AS tournament
		JOIN tournament_content_configurations AS configuration
			ON configuration.tournament_id = tournament.id AND configuration.state = 'published'
		JOIN task_pool_publications AS publication ON publication.id = configuration.pool_publication_id
		WHERE tournament.public_id = $1`, publicID).
		Scan(&publicationID, &normalPoolID, &goldenPoolID, &revision)
	require.NoError(t, err)
	require.Equal(t, selection.PublicationId, publicationID)
	require.Equal(t, selection.NormalPoolRevisionId, normalPoolID)
	require.Equal(t, selection.GoldenPoolRevisionId, goldenPoolID)
	require.Equal(t, selection.ContentRevision, revision)
	assertContentPoolsSharePublication(ctx, t, selection)
}

func markTournamentContentTaskUnhealthy(ctx context.Context, t testing.TB, taskID uuid.UUID) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT task.id, task.current_version,
			COALESCE(MAX(attestation.revision), 0) + 1,
			false, 'probe'
		FROM tasks AS task
		LEFT JOIN task_version_health_attestations AS attestation
			ON attestation.task_id = task.id AND attestation.task_version = task.current_version
		WHERE task.id = $1
		GROUP BY task.id, task.current_version`, taskID)
	require.NoError(t, err)
}

func markTournamentContentTaskHealthy(ctx context.Context, t testing.TB, taskID uuid.UUID) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT task.id, task.current_version,
			COALESCE(MAX(attestation.revision), 0) + 1,
			true, 'probe'
		FROM tasks AS task
		LEFT JOIN task_version_health_attestations AS attestation
			ON attestation.task_id = task.id AND attestation.task_version = task.current_version
		WHERE task.id = $1
		GROUP BY task.id, task.current_version`, taskID)
	require.NoError(t, err)
}

func disableTournamentContentTask(ctx context.Context, t testing.TB, taskID uuid.UUID) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `UPDATE tasks SET enabled = false WHERE id = $1`, taskID)
	require.NoError(t, err)
}

func assertNoTournamentAggregate(ctx context.Context, t testing.TB) {
	t.Helper()
	var tournamentCount, rosterCount, configurationCount, receiptCount int
	err := sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM tournaments),
			(SELECT COUNT(*) FROM rosters),
			(SELECT COUNT(*) FROM tournament_content_configurations),
			(SELECT COUNT(*) FROM tournament_create_command_receipts)`).
		Scan(&tournamentCount, &rosterCount, &configurationCount, &receiptCount)
	require.NoError(t, err)
	require.Zero(t, tournamentCount)
	require.Zero(t, rosterCount)
	require.Zero(t, configurationCount)
	require.Zero(t, receiptCount)
}
