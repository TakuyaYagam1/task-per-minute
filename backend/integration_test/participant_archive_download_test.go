//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/participantarchive"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

const participantArchiveTestTTL = time.Second

type participantArchiveFixture struct {
	rest         *restFixture
	storage      *objectstorage.SeaweedStorage
	sourceFiles  *taskusecase.SourceFiles
	internalHost string
	publicHost   string
}

type participantArchiveState struct {
	assignment   []byte
	reservations []byte
	receiptCount int64
}

func TestParticipantArchiveDownloadNormalAssignment(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(context.Background(), t) })

	archive := newParticipantArchiveFixture(t, nil, nil)
	payload := sourceArchivePayload("participant-normal")
	var uploadedTaskID uuid.UUID
	draft := createDraftMigrationFixtureWithContentHook(ctx, t, func(normalTaskIDs []uuid.UUID) {
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT id
			FROM tasks
			WHERE id = ANY($1::UUID[]) AND category = 'web'
			ORDER BY id
			LIMIT 1`, normalTaskIDs).Scan(&uploadedTaskID))
		_ = uploadSourceArchive(ctx, t, archive.sourceFiles, archive.rest.tasks, uploadedTaskID, payload)
	})
	fixture := createResultAuditMigrationFixtureFromDraft(ctx, t, draft)
	owner := participantArchivePlayer(t, fixture.draft.participantIDs[0])
	outsider := participantArchiveSessionForPlayer(t, createMigrationPlayers(ctx, t, 1)[0])
	archive.rest.handler = participantArchiveHandler(t, archive.rest, archive.sourceFiles, nil, nil)
	downloadPath := "/api/v1/tournaments/" + fixture.draft.tournamentID.String() +
		"/participant/assignments/" + fixture.assignmentID.String() + "/source-file"
	preStartReq, preStartResp := archive.rest.doJSON(
		t, http.MethodGet, downloadPath, "", session(owner.session),
	)
	require.Equal(t, http.StatusForbidden, preStartResp.Code, preStartResp.Body.String())
	archive.rest.validateResponse(t, preStartReq, preStartResp)

	receipt, changed, err := postgres.NewAssignmentPostgres(archive.rest.mgr).Deliver(
		ctx,
		fixture.assignmentID,
		uuid.New(),
		fixture.draft.participantIDs[0],
		time.Now().UTC(),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, fixture.draft.participantIDs[0], receipt.ParticipantID)

	before := loadParticipantArchiveNormalState(ctx, t, fixture.assignmentID)
	first := requestParticipantArchive(t, archive.rest, downloadPath, owner.session)
	assertParticipantArchiveBytes(t, archive, first.SourceFileUrl, payload)
	second := requestParticipantArchive(t, archive.rest, downloadPath, owner.session)
	assertParticipantArchiveBytes(t, archive, second.SourceFileUrl, payload)
	reconnectedOwner := participantArchiveSessionForPlayer(t, owner.id)
	reconnected := requestParticipantArchive(t, archive.rest, downloadPath, reconnectedOwner.session)
	assertParticipantArchiveBytes(t, archive, reconnected.SourceFileUrl, payload)
	require.Equal(t, before, loadParticipantArchiveNormalState(ctx, t, fixture.assignmentID))

	wrongReq, wrongResp := archive.rest.doJSON(
		t, http.MethodGet, downloadPath, "", session(outsider.session),
	)
	require.Equal(t, http.StatusForbidden, wrongResp.Code, wrongResp.Body.String())
	archive.rest.validateResponse(t, wrongReq, wrongResp)
	require.Equal(t, before, loadParticipantArchiveNormalState(ctx, t, fixture.assignmentID))

	waitForParticipantArchiveExpiry(t, first.SourceFileUrl)
	renewed := requestParticipantArchive(t, archive.rest, downloadPath, reconnectedOwner.session)
	assertParticipantArchiveBytes(t, archive, renewed.SourceFileUrl, payload)
	require.True(t, renewed.ExpiresAt.After(first.ExpiresAt))
	require.Equal(t, before, loadParticipantArchiveNormalState(ctx, t, fixture.assignmentID))
}

func TestParticipantArchiveDownloadGoldenAssignment(t *testing.T) {
	ctx := context.Background()
	fixture := prepareParticipantArchiveGoldenFinalSwiss(ctx, t)
	sourceProjectionID, sourceProjectionRevision := currentPublishedProjection(
		ctx, t, fixture.tournamentID, fixture.rosterID,
	)
	require.NoError(t, publishSwissGolden(ctx, fixture, tournamentprogression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: sourceProjectionRevision,
		Action: tournamentprogression.ActionStartGolden,
	}))

	archive := newParticipantArchiveFixture(t, nil, nil)
	payload := sourceArchivePayload("participant-golden")
	ensureGoldenRuntimeTestCapacity(ctx, t, fixture.tournamentID)
	uploadGoldenRuntimeArchives(ctx, t, archive, fixture.tournamentID, payload)
	now := time.Now().UTC().Truncate(time.Microsecond)
	createGoldenRuntimeTestPlan(
		ctx, t, fixture.tournamentID, fixture.rosterID,
		sourceProjectionID, sourceProjectionRevision, now.Add(-time.Second),
	)
	application := goldenusecase.NewRuntimeApplication(
		postgres.NewGoldenRuntimePostgres(archive.rest.mgr), goldenRuntimeClock{now: now},
	)
	operator, err := application.Open(ctx, inbound.GoldenOpenCommand{
		TournamentID: fixture.tournamentID, CommandID: uuid.New(),
		ExpectedProjectionRevision: sourceProjectionRevision,
		GoldenMutationScope:        inbound.GoldenMutationScope{ActorID: uuid.New()},
	})
	require.NoError(t, err)
	require.NotEmpty(t, operator.Groups)
	group := operator.Groups[0]
	require.NotEmpty(t, group.Members)

	players := goldenRuntimePlayers(ctx, t, group)
	ownerPlayerID := players[group.Members[0].ParticipantID]
	owner := participantArchiveSessionForPlayer(t, ownerPlayerID)
	archive.rest.handler = participantArchiveHandler(t, archive.rest, archive.sourceFiles, nil, application)

	var assignmentID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT assignment_id
		FROM golden_runtime_assignments
		WHERE attempt_id = $1`, group.AttemptID).Scan(&assignmentID))
	downloadPath := "/api/v1/tournaments/" + fixture.tournamentID.String() +
		"/participant/assignments/" + assignmentID.String() + "/source-file"
	preStartReq, preStartResp := archive.rest.doJSON(
		t, http.MethodGet, downloadPath, "", session(owner.session),
	)
	require.Equal(t, http.StatusForbidden, preStartResp.Code, preStartResp.Body.String())
	archive.rest.validateResponse(t, preStartReq, preStartResp)

	for _, member := range group.Members {
		_, readyErr := application.SetReady(ctx, goldenRuntimeReadyCommand(
			ctx, t, application, fixture.tournamentID, players[member.ParticipantID], uuid.New(),
		))
		require.NoError(t, readyErr)
	}
	_, err = application.Start(ctx, goldenRuntimeStartCommand(
		ctx, t, application, fixture.tournamentID, group.AttemptID, uuid.New(),
	))
	require.NoError(t, err)

	participantPath := "/api/v1/tournaments/" + fixture.tournamentID.String() + "/participant/golden"
	participantReq, participantResp := archive.rest.doJSON(
		t, http.MethodGet, participantPath, "", session(owner.session),
	)
	require.Equal(t, http.StatusOK, participantResp.Code, participantResp.Body.String())
	archive.rest.validateResponse(t, participantReq, participantResp)
	participant := decodeJSON[api.GoldenParticipantResponse](t, participantResp)
	require.NotNil(t, participant.Task)
	require.True(t, participant.Task.SourceFileAvailable)
	require.Equal(t, assignmentID, participant.Task.AssignmentId)

	before := loadParticipantArchiveGoldenState(ctx, t, group.AttemptID)
	download := requestParticipantArchive(t, archive.rest, downloadPath, owner.session)
	assertParticipantArchiveBytes(t, archive, download.SourceFileUrl, payload)
	reconnected := requestParticipantArchive(t, archive.rest, downloadPath, owner.session)
	assertParticipantArchiveBytes(t, archive, reconnected.SourceFileUrl, payload)
	require.Equal(t, before, loadParticipantArchiveGoldenState(ctx, t, group.AttemptID))
}

func newParticipantArchiveFixture(
	t *testing.T,
	participant inbound.TournamentParticipantUseCase,
	golden inbound.GoldenUseCase,
) *participantArchiveFixture {
	t.Helper()
	storageFixture := sharedSeaweed(t)
	host, port, err := net.SplitHostPort(storageFixture.endpoint)
	require.NoError(t, err)
	publicHostname := "localhost"
	if host == publicHostname {
		publicHostname = "127.0.0.1"
	}
	publicEndpoint := net.JoinHostPort(publicHostname, port)
	require.NotEqual(t, storageFixture.endpoint, publicEndpoint)
	storage, err := objectstorage.New(objectstorage.Config{
		Endpoint: storageFixture.endpoint, PublicEndpoint: publicEndpoint,
		AccessKey: "tpm", SecretKey: "tpm-secret", Bucket: storageFixture.bucket,
		Secure: false, PublicSecure: false,
	})
	require.NoError(t, err)
	require.NoError(t, storage.EnsureBucket(context.Background()))
	database := newDatabaseFixture()
	sourceFiles := taskusecase.NewSourceFiles(taskusecase.NewUseCase(database.tasks), storage, nil)
	rest := &restFixture{
		databaseFixture: database,
		validator:       newOpenAPIResponseValidator(t),
	}
	rest.handler = participantArchiveHandler(t, rest, sourceFiles, participant, golden)
	return &participantArchiveFixture{
		rest: rest, storage: storage, sourceFiles: sourceFiles,
		internalHost: storageFixture.endpoint, publicHost: publicEndpoint,
	}
}

func prepareParticipantArchiveGoldenFinalSwiss(
	ctx context.Context,
	t *testing.T,
) tournamentAdminSwissProofFixture {
	t.Helper()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })
	for range 12 {
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
			VALUES ($1, 'Participant archive Golden task', 'web', 'easy', 60, 'participant-archive', 'normal')`,
			"participant_archive_"+uuid.NewString()[:8])
		require.NoError(t, err)
	}
	prepareRoundProofContent(ctx, t)

	rows, err := sharedPool.Query(ctx, `
		SELECT id
		FROM tasks
		WHERE kind = 'normal' AND enabled AND deleted_at IS NULL
		ORDER BY id`)
	require.NoError(t, err)
	taskIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var taskID uuid.UUID
		require.NoError(t, rows.Scan(&taskID))
		taskIDs = append(taskIDs, taskID)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	require.NotEmpty(t, taskIDs)

	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	playerIDs := createMigrationPlayers(ctx, t, 4)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	normalPoolID := createDraftContentConfigurationFromCurrentTasks(
		ctx, t, tournamentID, createdAt, taskIDs,
	)
	var normalPoolRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision FROM task_pool_revisions WHERE id = $1`, normalPoolID).Scan(&normalPoolRevision))
	fixture := createTournamentAdminSwissProofFixtureForAggregate(
		ctx, t, tournamentID, rosterID, playerIDs, normalPoolID, normalPoolRevision, createdAt,
	)
	return finishNativeGoldenFinalSwiss(ctx, t, fixture)
}

func participantArchiveHandler(
	t *testing.T,
	fixture *restFixture,
	sourceFiles *taskusecase.SourceFiles,
	participant inbound.TournamentParticipantUseCase,
	golden inbound.GoldenUseCase,
) http.Handler {
	t.Helper()
	archive, err := participantarchive.New(
		postgres.NewParticipantArchivePostgres(fixture.mgr), sourceFiles, realIntegrationClock(),
		participantarchive.WithDownloadTTL(participantArchiveTestTTL),
	)
	require.NoError(t, err)
	server := restv1.New(restv1.Dependencies{
		TournamentParticipant: participant,
		ParticipantArchive:    archive,
		Golden:                golden,
		ParticipantTournamentReadLimiter: redisadapter.NewRateLimiter(
			sharedRedis(t).client,
			"participant-archive-"+uniq("limiter"),
			100,
			time.Minute,
		),
	})
	return middleware.NoStoreSensitiveResponses()(
		restv1.NewHandler(server, restv1.HandlerOptions{PlayerRepo: fixture.players}),
	)
}

func participantArchivePlayer(t *testing.T, participantID uuid.UUID) tournamentFlowPlayer {
	t.Helper()
	var playerID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(context.Background(), `
		SELECT player_id FROM participants WHERE id = $1`, participantID).Scan(&playerID))
	return participantArchiveSessionForPlayer(t, playerID)
}

func participantArchiveSessionForPlayer(t *testing.T, playerID uuid.UUID) tournamentFlowPlayer {
	t.Helper()
	token := uuid.New()
	expiresAt := time.Now().UTC().Add(time.Hour)
	_, err := sharedPool.Exec(context.Background(), `
		UPDATE players
		SET session_token = $2, session_expires_at = $3
		WHERE id = $1`, playerID, token, expiresAt)
	require.NoError(t, err)
	return tournamentFlowPlayer{id: playerID, session: token}
}

func requestParticipantArchive(
	t *testing.T,
	fixture *restFixture,
	path string,
	sessionToken uuid.UUID,
) api.ParticipantSourceFileResponse {
	t.Helper()
	req, resp := fixture.doJSON(t, http.MethodGet, path, "", session(sessionToken))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	download := decodeJSON[api.ParticipantSourceFileResponse](t, resp)
	require.True(t, download.ExpiresAt.After(time.Now().UTC()))
	return download
}

func assertParticipantArchiveBytes(
	t *testing.T,
	fixture *participantArchiveFixture,
	rawURL string,
	want []byte,
) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	require.Equal(t, fixture.publicHost, parsed.Host)
	require.NotEqual(t, fixture.internalHost, parsed.Host)
	require.Contains(t, parsed.Query().Get("X-Amz-Algorithm"), "AWS4")
	resp := httpGetWithTimeout(t, rawURL)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func waitForParticipantArchiveExpiry(t *testing.T, rawURL string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		resp := httpGetWithTimeout(t, rawURL)
		_, readErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		require.NoError(t, readErr)
		require.NoError(t, closeErr)
		if resp.StatusCode != http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			require.FailNow(t, "participant archive URL did not expire within the bounded poll")
		}
		<-ticker.C
	}
}

func loadParticipantArchiveNormalState(
	ctx context.Context,
	t *testing.T,
	assignmentID uuid.UUID,
) participantArchiveState {
	t.Helper()
	var state participantArchiveState
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT to_jsonb(assignment),
			COALESCE((
				SELECT jsonb_agg(to_jsonb(reservation) ORDER BY edge.branch_id, edge.position, reservation.id)
				FROM task_version_reservations AS reservation
				INNER JOIN assignment_plan_edges AS edge ON edge.id = reservation.edge_id
				WHERE reservation.branch_id = assignment.branch_id
			), '[]'::jsonb),
			(SELECT COUNT(*) FROM task_delivery_receipts WHERE assignment_id = assignment.id)
		FROM assignments AS assignment
		WHERE assignment.id = $1`, assignmentID).Scan(
		&state.assignment, &state.reservations, &state.receiptCount,
	))
	return state
}

func loadParticipantArchiveGoldenState(
	ctx context.Context,
	t *testing.T,
	attemptID uuid.UUID,
) participantArchiveState {
	t.Helper()
	var state participantArchiveState
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT to_jsonb(runtime),
			COALESCE((
				SELECT jsonb_agg(to_jsonb(reservation) ORDER BY edge.branch_id, edge.position, reservation.id)
				FROM task_version_reservations AS reservation
				INNER JOIN assignment_plan_edges AS edge ON edge.id = reservation.edge_id
				WHERE reservation.plan_id = runtime.plan_id
					AND reservation.branch_id = edge.branch_id
			), '[]'::jsonb),
			(SELECT COUNT(*) FROM task_delivery_receipts AS receipt
				WHERE receipt.roster_id = runtime.roster_id)
		FROM golden_runtime_assignments AS runtime
		WHERE runtime.attempt_id = $1`, attemptID).Scan(
		&state.assignment, &state.reservations, &state.receiptCount,
	))
	return state
}

func uploadGoldenRuntimeArchives(
	ctx context.Context,
	t *testing.T,
	fixture *participantArchiveFixture,
	tournamentID uuid.UUID,
	payload []byte,
) {
	t.Helper()
	var poolID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT golden_pool_revision_id
		FROM tournament_content_configurations
		WHERE tournament_id = $1 AND state = 'published'`, tournamentID).Scan(&poolID))
	rows, err := sharedPool.Query(ctx, `
		SELECT task.id
		FROM task_pool_version_memberships AS membership
		INNER JOIN tasks AS task ON task.id = membership.task_id
		WHERE membership.task_pool_revision_id = $1
			AND task.kind = 'golden'
			AND task.current_version = membership.task_version
		ORDER BY task.id`, poolID)
	require.NoError(t, err)
	taskIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var taskID uuid.UUID
		require.NoError(t, rows.Scan(&taskID))
		taskIDs = append(taskIDs, taskID)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	require.NotEmpty(t, taskIDs)
	for _, taskID := range taskIDs {
		_, uploadErr := fixture.sourceFiles.UploadSourceFile(
			ctx, taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
		)
		require.NoError(t, uploadErr)
		var version int
		require.NoError(t, sharedPool.QueryRow(ctx,
			`SELECT current_version FROM tasks WHERE id = $1`, taskID).Scan(&version))
		_, insertErr := sharedPool.Exec(ctx, `
			INSERT INTO task_version_health_attestations (
				task_id, task_version, revision, healthy, source
			) VALUES ($1, $2, 1, true, 'content_validation')
			ON CONFLICT (task_id, task_version, revision) DO NOTHING`, taskID, version)
		require.NoError(t, insertErr)
		_, insertErr = sharedPool.Exec(ctx, `
			INSERT INTO task_pool_version_memberships (task_pool_revision_id, task_id, task_version)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, poolID, taskID, version)
		require.NoError(t, insertErr)
	}
}
