//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	authorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/authority"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	recoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery"
	recoveryterminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	executionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	progression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

// TestTechnicalPostseasonAdvancementThroughRecovery drives the production
// recovery boundary with a real PostgreSQL graph.  The first semifinal uses
// the existing result settlement fixture; the second semifinal is settled by
// an automatic reconnect deadline so the terminal coordinator is reached from
// recovery, not by calling it directly.
func TestTechnicalPostseasonAdvancementThroughRecovery(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	fixture, stageCommand := prepareTechnicalPlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, stageCommand)
	require.NoError(t, err)

	firstSemifinal := semifinalSettlementInput(ctx, t, fixture, stageCommand.CommandID, 1)
	_, changed, err := resultauthority.NewResultPostgres(fixture.tx).Settle(ctx, firstSemifinal)
	require.NoError(t, err)
	require.True(t, changed)

	_, secondWaveID, participants := technicalPlayoffSeries(ctx, t, fixture, stageCommand.CommandID, 2)
	technicalStartWave(ctx, t, fixture, secondWaveID, participants)
	pending := technicalDisconnectPending(ctx, t, fixture, secondWaveID, participants[0])

	deadlineHandler := technicalDeadlineHandler(fixture, pending)
	changed, err = deadlineHandler.HandleDeadline(ctx, pending)
	require.NoError(t, err)
	require.True(t, changed)

	ids, err := playoff.FinalStageIdentity(stageCommand.CommandID)
	require.NoError(t, err)
	require.Equal(t, [5]int64{1, 1, 1, 2, 1}, technicalFinalDraftCounts(ctx, t, fixture, ids, stageCommand.CommandID),
		"the terminal reconnect timeout must create one final draft and one advancement row per semifinal")

	beforeReplay := technicalFinalDraftCounts(ctx, t, fixture, ids, stageCommand.CommandID)
	changed, err = deadlineHandler.HandleDeadline(ctx, pending)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, beforeReplay, technicalFinalDraftCounts(ctx, t, fixture, ids, stageCommand.CommandID),
		"replaying the same reconnect deadline must not create another final")
	var receipts int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*)
		FROM deadline_transition_receipts
		WHERE transition_kind = 'reconnect_forfeit'
			AND deadline_id = $1 AND expected_deadline_revision = $2`,
		pending.ID, pending.ExpectedRevision).Scan(&receipts))
	require.Equal(t, int64(1), receipts)

	activateTechnicalFinal(ctx, t, fixture, ids)
	_, finalParticipants := technicalFinalParticipants(ctx, t, fixture, ids.FinalSeriesID)
	technicalStartWave(ctx, t, fixture, ids.FirstWaveID, finalParticipants)
	firstFinalGraph := technicalFinalGraphCounts(ctx, t, fixture, ids.FinalSeriesID)
	require.Equal(t, [5]int64{1, 1, 1, 1, 2}, firstFinalGraph)

	intermediatePending := technicalDisconnectPending(ctx, t, fixture, ids.FirstWaveID, finalParticipants[0])
	intermediateHandler := technicalDeadlineHandler(fixture, intermediatePending)
	changed, err = intermediateHandler.HandleDeadline(ctx, intermediatePending)
	require.NoError(t, err)
	require.True(t, changed)

	require.Equal(t, [5]int64{2, 2, 2, 2, 4}, technicalFinalGraphCounts(ctx, t, fixture, ids.FinalSeriesID),
		"an intermediate technical timeout must create the next final Game, assignment, Wave, and readiness heads")

	secondFinalGraph := technicalFinalGraphCounts(ctx, t, fixture, ids.FinalSeriesID)
	technicalReconnectPresence(ctx, t, fixture, ids.FinalSeriesID, finalParticipants[0], intermediatePending.DueAt)
	technicalStartWave(ctx, t, fixture, ids.SecondWaveID, finalParticipants)
	decidingPending := technicalDisconnectPending(ctx, t, fixture, ids.SecondWaveID, finalParticipants[0])
	decidingHandler := technicalDeadlineHandler(fixture, decidingPending)
	changed, err = decidingHandler.HandleDeadline(ctx, decidingPending)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, secondFinalGraph, technicalFinalGraphCounts(ctx, t, fixture, ids.FinalSeriesID),
		"the deciding technical timeout must publish the terminal final without extending the graph")

	var tournamentState, seriesState string
	var winnerID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT tournament.state, series.state, series.winner_id
		FROM tournaments AS tournament
		JOIN series ON series.tournament_id = tournament.id
		WHERE tournament.id = $1 AND series.id = $2`, fixture.tournamentID, ids.FinalSeriesID).
		Scan(&tournamentState, &seriesState, &winnerID))
	require.Equal(t, string(domain.TournamentStateCompleted), tournamentState)
	require.Equal(t, string(domain.SeriesStateCompleted), seriesState)
	require.Equal(t, finalParticipants[1], winnerID)
	require.Equal(t, int64(1), technicalChampionPublicationCount(ctx, t, fixture))

	beforeDecidingReplay := technicalTerminalArtifactCounts(ctx, t, fixture, ids)
	changed, err = decidingHandler.HandleDeadline(ctx, decidingPending)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, beforeDecidingReplay, technicalTerminalArtifactCounts(ctx, t, fixture, ids),
		"replaying the deciding reconnect deadline must not duplicate the champion publication")
	var decidingReceipts int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*)
		FROM deadline_transition_receipts
		WHERE transition_kind = 'reconnect_forfeit'
			AND deadline_id = $1 AND expected_deadline_revision = $2`,
		decidingPending.ID, decidingPending.ExpectedRevision).Scan(&decidingReceipts))
	require.Equal(t, int64(1), decidingReceipts)

}

func prepareTechnicalPlayoffPublication(ctx context.Context, t *testing.T) (tournamentAdminSwissProofFixture, progression.Command) {
	t.Helper()
	fixture := createTechnicalReconnectSwissProofFixture(ctx, t)
	for round := 1; round <= 3; round++ {
		if round > 1 {
			fixture = nextSwissReceiptWave(ctx, t, fixture, round)
		}
		_, changed, err := fixture.start.Start(ctx, fixture.startCommand(ctx, t))
		require.NoError(t, err)
		require.True(t, changed)
		for index := range fixture.binding {
			settleSwissReceiptSeries(ctx, t, fixture, index)
		}
		closeSwissReceiptWave(ctx, t, fixture)
	}
	_, revision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	return fixture, progression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: revision, Action: progression.ActionStartPlayoffs,
	}
}

func createTechnicalReconnectSwissProofFixture(
	ctx context.Context,
	t *testing.T,
) tournamentAdminSwissProofFixture {
	t.Helper()
	prepareRoundProofContent(ctx, t)
	retiredAt := time.Now().UTC().Truncate(time.Microsecond)
	_, err := sharedPool.Exec(ctx, `
		UPDATE tasks
		SET enabled = false, deleted_at = $1
		WHERE enabled AND time_limit = 60`, retiredAt)
	require.NoError(t, err)
	for _, category := range []string{"web", "crypto", "forensics", "reverse", "pwn"} {
		for range 30 {
			_, err = sharedPool.Exec(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'technical postseason task', $2, 'easy', 180, $3, 'normal')`,
				"technical_postseason_"+uuid.NewString()[:8], category,
				"technical-postseason-"+uuid.NewString()[:8])
			require.NoError(t, err)
		}
	}
	for range 6 {
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
			VALUES ($1, 'technical postseason golden task', 'web', 'easy', 180, $2, 'golden')`,
			"technical_postseason_golden_"+uuid.NewString()[:8],
			"technical-postseason-golden-"+uuid.NewString()[:8])
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT version.task_id, version.version, 1, true, 'content_validation'
		FROM task_versions AS version
		WHERE NOT EXISTS (
			SELECT 1
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = version.task_id
				AND attestation.task_version = version.version
				AND attestation.revision = 1
		)`)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `SELECT publish_task_pool_heads()`)
	require.NoError(t, err)

	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	playerIDs := createMigrationPlayers(ctx, t, 4)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	normalPoolRevisionID, normalPoolRevision := createParticipantReconnectContentConfiguration(
		ctx, t, tournamentID, createdAt,
	)
	fixture := createTournamentAdminSwissProofFixtureForAggregate(
		ctx, t, tournamentID, rosterID, playerIDs,
		normalPoolRevisionID, normalPoolRevision, createdAt,
	)
	for _, series := range fixture.binding {
		participantReconnectCreatePresence(ctx, t, tournamentID, rosterID, series.SeriesID,
			[]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}, createdAt)
	}
	return fixture
}

func technicalPlayoffSeries(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	commandID uuid.UUID,
	position int,
) (uuid.UUID, uuid.UUID, []uuid.UUID) {
	t.Helper()
	var seriesID, firstParticipantID, secondParticipantID, waveID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT semifinal.series_id, series.first_participant_id, series.second_participant_id
		FROM tournament_stage_playoff_semifinals AS semifinal
		JOIN series ON series.id = semifinal.series_id
		WHERE semifinal.command_id = $1 AND semifinal.position = $2`, commandID, position).
		Scan(&seriesID, &firstParticipantID, &secondParticipantID))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT wave_id
		FROM wave_series
		WHERE series_id = $1
		ORDER BY wave_id
		LIMIT 1`, seriesID).Scan(&waveID))
	require.NotEqual(t, uuid.Nil, fixture.rosterID)
	return seriesID, waveID, []uuid.UUID{firstParticipantID, secondParticipantID}
}

type technicalWaveStartRepository struct {
	*executionrepo.Repository
	startAt time.Time
}

func (r technicalWaveStartRepository) ReadWaveStartTime(context.Context) (time.Time, error) {
	return r.startAt, nil
}

func technicalStartWave(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	waveID uuid.UUID,
	participants []uuid.UUID,
) *gameusecase.StartRecord {
	t.Helper()
	waves := waverepo.NewWavePostgres(fixture.tx)
	wave, err := waves.Get(ctx, fixture.tournamentID, waveID)
	require.NoError(t, err)
	require.Equal(t, domain.WaveStatePlanned, wave.Wave.State)

	var latestReadinessCreatedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT created_at
		FROM wave_readiness
		WHERE wave_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, waveID).Scan(&latestReadinessCreatedAt))
	openedAt := time.Now().UTC().Truncate(time.Microsecond)
	if !openedAt.After(latestReadinessCreatedAt) {
		openedAt = latestReadinessCreatedAt.UTC().Add(time.Microsecond)
	}
	windowID := uuid.New()
	opened, changed, err := waves.OpenReadyWindow(
		ctx,
		fixture.tournamentID,
		waveID,
		wave.Revision,
		waverepo.ReadyWindowInput{
			ID:         windowID,
			RevisionID: domain.ReadyWindowRevisionID(uuid.New()),
			OpenedAt:   openedAt,
			Deadline:   openedAt.Add(domain.ReadyWindowDuration),
		},
	)
	require.NoError(t, err)
	require.True(t, changed)

	for _, participantID := range participants {
		readinessRevision, found := opened.ReadinessRevisions[participantID]
		require.True(t, found)
		ready, readyChanged, readyErr := waves.MarkReady(
			ctx,
			fixture.tournamentID,
			waveID,
			windowID,
			participantID,
			opened.Revision,
			readinessRevision,
			openedAt,
		)
		require.NoError(t, readyErr)
		require.True(t, readyChanged)
		opened = ready
	}

	require.NotNil(t, opened.Wave.ReadyWindow)
	startAt := opened.Wave.ReadyWindow.OpenedAt.Add(domain.ReadyWindowDuration / 2)
	start := gameusecase.NewStartUseCase(technicalWaveStartRepository{
		Repository: fixture.adapter,
		startAt:    startAt,
	}, nil)
	scope := gameusecase.StartScope{TournamentID: fixture.tournamentID, WaveID: waveID, WindowID: windowID}
	authority, err := fixture.adapter.LoadWaveStartAuthority(ctx, scope)
	require.NoError(t, err)
	command := gameusecase.StartCommand{
		Scope:                      scope,
		CommandID:                  uuid.New(),
		ActorID:                    uuid.New(),
		ExecutionAuthority:         fixture.executionAuthority,
		ExpectedProjectionRevision: authority.Revisions.ProjectionRevision,
		ExpectedRevisions:          authority.Revisions,
		RequestDigest:              sha256.Sum256([]byte("technical-postseason-wave-start:" + waveID.String())),
	}
	var record *gameusecase.StartRecord
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var startErr error
		record, changed, startErr = start.Start(txCtx, command)
		return startErr
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, record)
	require.Equal(t, domain.WaveStateActive, record.Wave.State)
	return record
}

func technicalDisconnectPending(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	waveID uuid.UUID,
	participantID uuid.UUID,
) recovery.PendingDeadline {
	t.Helper()
	scope := pausedomain.GraphScope{
		TournamentID: fixture.tournamentID,
		RosterID:     fixture.rosterID,
		WaveID:       waveID,
		Authority:    fixture.executionAuthority,
	}
	authority, err := fixture.adapter.LoadAuthority(ctx, scope, participantID)
	require.NoError(t, err)
	require.Equal(t, domain.GameStateActive, authority.Game.State)
	deadline := authority.GameClock.OriginalDeadline
	started := participantReconnectStartedFixture{
		fixture:  fixture,
		scope:    scope,
		started:  authority,
		playerID: participantID,
	}
	command := participantReconnectDisconnectCommand(started, deadline)
	_, changed, err := gameusecase.NewDisconnectUseCase(
		fixture.adapter,
		participantReconnectTestClock{at: deadline.Add(-time.Second)},
	).Disconnect(ctx, command)
	require.NoError(t, err)
	require.True(t, changed)

	deadlines, err := recoveryrepo.NewRecoveryPostgres(fixture.tx, nil).ListPendingDeadlines(
		ctx, recovery.DeadlineCursor{}, recovery.MaximumSweepBatchSize,
	)
	require.NoError(t, err)
	for _, pending := range deadlines {
		if pending.Kind == recovery.DeadlineKindReconnect && pending.ID == command.IntervalID {
			return pending
		}
	}
	require.FailNowf(t, "missing pending reconnect deadline", "wave=%s participant=%s", waveID, participantID)
	return recovery.PendingDeadline{}
}

func technicalReconnectPresence(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	seriesID, participantID uuid.UUID,
	at time.Time,
) {
	t.Helper()
	var presenceID uuid.UUID
	var presenceEpoch, presenceRevision int64
	var updatedAt time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, presence_epoch, revision, updated_at
		FROM presence_states
		WHERE tournament_id = $1 AND roster_id = $2 AND series_id = $3 AND participant_id = $4`,
		fixture.tournamentID, fixture.rosterID, seriesID, participantID).
		Scan(&presenceID, &presenceEpoch, &presenceRevision, &updatedAt))
	var state string
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state
		FROM presence_states
		WHERE id = $1`, presenceID).Scan(&state))
	require.Equal(t, string(pausedomain.PresenceStateDisconnected), state)
	if !at.After(updatedAt) {
		at = updatedAt.Add(time.Microsecond)
	}
	result, err := sharedPool.Exec(ctx, `
		UPDATE presence_states
		SET state = 'connected', connected_at = $1, disconnected_at = NULL,
			presence_epoch = presence_epoch + 1, revision = revision + 1, updated_at = $1
		WHERE id = $2 AND tournament_id = $3 AND roster_id = $4 AND series_id = $5
			AND participant_id = $6 AND state = 'disconnected'
			AND presence_epoch = $7 AND revision = $8`,
		at, presenceID, fixture.tournamentID, fixture.rosterID, seriesID, participantID,
		presenceEpoch, presenceRevision)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.RowsAffected())
}

func technicalDeadlineHandler(
	fixture tournamentAdminSwissProofFixture,
	pending recovery.PendingDeadline,
) *recovery.TerminalDeadlineHandler {
	clock := playoffPublicationClock{now: pending.DueAt}
	store := recoveryterminalrepo.NewRecoveryTerminalPostgresWithDependencies(
		fixture.tx,
		authorityrepo.NewExecutionAuthorityPostgres(fixture.tx),
		clock,
		wavestartrepo.EnsurePreStartSwissRoundProofForCommand,
		resultauthority.FinalizeProjection,
	)
	return recovery.NewTerminalDeadlineHandlerWithDependencies(
		fixture.tx,
		store,
		clock,
		newTournamentFlowTerminalCoordinator(fixture.tx),
	)
}

func technicalFinalDraftCounts(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	ids playoff.FinalStageIDs,
	commandID uuid.UUID,
) [5]int64 {
	t.Helper()
	var counts [5]int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM series WHERE id = $1),
			(SELECT count(*) FROM drafts WHERE id = $2),
			(SELECT count(*) FROM tournament_stage_playoff_finals
			 WHERE command_id = $3 AND tournament_id = $4),
			(SELECT count(*) FROM tournament_stage_playoff_final_advancements
			 WHERE command_id = $3 AND tournament_id = $4),
			(SELECT count(*) FROM series_score_revisions WHERE series_id = $1)`,
		ids.FinalSeriesID, ids.DraftID, commandID, fixture.tournamentID).
		Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4]))
	return counts
}

func activateTechnicalFinal(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	ids playoff.FinalStageIDs,
) {
	t.Helper()
	drafts := postgres.NewParticipantDraftRepository(fixture.tx, draftrepo.NewDraftPostgres(fixture.tx))
	current, err := drafts.LoadDraft(ctx, ids.DraftID)
	require.NoError(t, err)
	for current.State == draftusecase.ExecutionStateActive {
		usecase := draftusecase.NewActionUseCase(drafts, finalDraftClock{at: current.TurnDeadline.Add(-time.Second)})
		next, actionErr := usecase.Apply(ctx, draftusecase.PlayerActionCommand{
			DraftID: current.ID, CommandID: uuid.New(), ActorID: *current.CurrentActorID,
			ExpectedRevisionID: current.RevisionID, ExpectedRevision: current.Revision,
			ExpectedServiceEpoch: current.ServiceEpoch, ExpectedTurn: current.Turn,
			ResultRevisionID: uuid.New(), ActionID: uuid.New(),
			Action: *current.CurrentAction, Category: current.LegalCategories[0],
		})
		require.NoError(t, actionErr)
		current = &next.Draft
	}

	coordinator := newTournamentFlowTerminalCoordinator(fixture.tx)
	var receipt playoff.TerminalReceipt
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		receipt, err = coordinator.ActivateFinalAfterDraft(txCtx, playoff.TerminalDraftCommand{
			TournamentID: fixture.tournamentID,
			SeriesID:     ids.FinalSeriesID,
			DraftID:      ids.DraftID,
			CommandID:    current.CommandID,
		})
		return err
	})
	require.NoError(t, err)
	require.True(t, receipt.Changed)
}

func technicalFinalParticipants(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	seriesID uuid.UUID,
) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	var firstParticipantID, secondParticipantID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT first_participant_id, second_participant_id
		FROM series
		WHERE id = $1`, seriesID).Scan(&firstParticipantID, &secondParticipantID))
	return seriesID, []uuid.UUID{firstParticipantID, secondParticipantID}
}

func technicalFinalGraphCounts(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	seriesID uuid.UUID,
) [5]int64 {
	t.Helper()
	var counts [5]int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM game_slots WHERE series_id = $1),
			(SELECT count(*) FROM game_attempts WHERE series_id = $1),
			(SELECT count(*) FROM assignments WHERE series_id = $1),
			(SELECT count(*) FROM wave_series WHERE series_id = $1),
			(SELECT count(*)
			 FROM wave_readiness AS readiness
			 JOIN wave_series AS membership ON membership.wave_id = readiness.wave_id
			 WHERE membership.series_id = $1)`, seriesID).
		Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4]))
	return counts
}

func technicalChampionPublicationCount(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
) int64 {
	t.Helper()
	var count int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT count(*)
		FROM outbox_champion_sources
		WHERE tournament_id = $1`, fixture.tournamentID).Scan(&count))
	return count
}

func technicalTerminalArtifactCounts(
	ctx context.Context,
	t *testing.T,
	fixture tournamentAdminSwissProofFixture,
	ids playoff.FinalStageIDs,
) [5]int64 {
	t.Helper()
	var counts [5]int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM result_commits WHERE series_id = $1),
			(SELECT count(*) FROM official_result_revisions
			 WHERE entity_kind = 'series' AND entity_id = $1),
			(SELECT count(*) FROM outbox_champion_sources WHERE tournament_id = $2),
			(SELECT count(*) FROM projection_artifacts
			 WHERE tournament_id = $2 AND artifact_kind = 'champion'),
			(SELECT count(*) FROM outbox_events
			 WHERE tournament_id = $2 AND topic = 'tournament.champion.published')`,
		ids.FinalSeriesID, fixture.tournamentID).
		Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4]))
	return counts
}
