//go:build integration && capacity

package integration_test

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

const (
	arenaCapacityNominalDuration             = 60 * time.Minute
	arenaCapacityNominalParticipants         = 16
	arenaCapacityNominalGames                = 8
	arenaCapacityNominalWorkers              = 4
	arenaCapacityNominalOperationInterval    = 250 * time.Millisecond
	arenaCapacityNominalMinimumThroughput    = 12.0
	arenaCapacityNominalLatencyP99LimitMS    = 250.0
	arenaCapacityNominalSchedulingLagLimitMS = 2000.0
	arenaCapacityResourceSampleInterval      = time.Second
	arenaCapacityReconnectProbeTimeout       = 30 * time.Second
)

func TestArenaNominal60Minute(t *testing.T) {
	config := arenaCapacityConfig{
		Name:                 "arena60",
		Profile:              "nominal",
		Duration:             arenaCapacityNominalDuration,
		Participants:         arenaCapacityNominalParticipants,
		Games:                arenaCapacityNominalGames,
		Workers:              arenaCapacityNominalWorkers,
		OperationInterval:    arenaCapacityNominalOperationInterval,
		MinimumThroughput:    arenaCapacityNominalMinimumThroughput,
		LatencyP99LimitMS:    arenaCapacityNominalLatencyP99LimitMS,
		SchedulingLagLimitMS: arenaCapacityNominalSchedulingLagLimitMS,
	}

	report := runArenaCapacityWorkload(t, config)
	require.GreaterOrEqual(t, report.Window.DurationSeconds, arenaCapacityNominalDuration.Seconds())
	assertArenaCapacityReportPass(t, report)
}

func TestArenaCapacityFixture(t *testing.T) {
	config := arenaCapacityConfig{
		Participants: arenaCapacityNominalParticipants,
		Games:        arenaCapacityNominalGames,
	}
	fixture := newArenaCapacityFixture(t, config)
	exerciseArenaCapacityReconnects(t, &fixture)
	warmArenaCapacityFixture(t, fixture)

	summary := arenaCapacitySummary{}
	checkArenaCapacityFinalState(t, fixture, config, &summary)
	require.Len(t, fixture.games, arenaCapacityNominalGames)
	require.EqualValues(t, arenaCapacityNominalGames, fixture.reconnect.Attempts)
	require.EqualValues(t, arenaCapacityNominalGames, fixture.reconnect.Succeeded)
	require.Zero(t, fixture.reconnect.Failed)
	require.EqualValues(t, arenaCapacityNominalGames+1, summary.CorrectnessChecks)
	require.Equal(t, summary.CorrectnessChecks, summary.CorrectnessPassed)
	require.Zero(t, summary.CorrectnessFailed)
}

type arenaCapacityFixture struct {
	repository  *postgres.ArenaGamePostgres
	tournaments *postgres.ArenaTournamentPostgres
	games       []arenaCapacityGameFixture
	reconnect   arenaCapacityReconnect
}

type arenaCapacityGameFixture struct {
	scope            arena.GameScope
	rosterID         uuid.UUID
	participantIDs   []uuid.UUID
	reconnectFixture arenaReconnectMigrationFixture
}

type arenaCapacityWorkerResult struct {
	operations        int64
	succeeded         int64
	failed            int64
	correctnessChecks int64
	correctnessPassed int64
	correctnessFailed int64
	latencies         []time.Duration
	schedulingLags    []time.Duration
}

type arenaCapacityResourceResult struct {
	samples                int64
	maxGoroutines          int64
	maxHeapAllocBytes      int64
	maxDatabaseConnections int64
}

func runArenaCapacityWorkload(t *testing.T, config arenaCapacityConfig) arenaCapacityReport {
	t.Helper()
	require.Positive(t, config.Duration)
	require.Equal(t, 16, config.Participants)
	require.Positive(t, config.Games)
	require.Positive(t, config.Workers)
	require.LessOrEqual(t, config.Workers, config.Games)
	require.Less(t, config.Workers, arenaCapacityPoolLimit)
	require.Positive(t, config.OperationInterval)
	require.EqualValues(t, arenaCapacityPoolLimit, sharedPool.Stat().MaxConns())
	if testDeadline, ok := t.Deadline(); ok {
		require.GreaterOrEqual(
			t,
			time.Until(testDeadline),
			config.Duration+arenaCapacityDeadlineBuffer,
			"capacity gate needs an explicit go test timeout beyond the workload duration",
		)
	}

	fixture := newArenaCapacityFixture(t, config)
	warmArenaCapacityFixture(t, fixture)
	workloadCtx, cancelWorkload := context.WithCancel(t.Context())
	defer cancelWorkload()

	start := make(chan struct{})
	stopSampler := make(chan struct{})
	resourceResults := make(chan arenaCapacityResourceResult, 1)
	workerResults := make([]arenaCapacityWorkerResult, config.Workers)
	startedAt := time.Now()
	deadline := startedAt.Add(config.Duration)

	go sampleArenaCapacityResources(start, stopSampler, resourceResults)

	var workers sync.WaitGroup
	var stopSamplerOnce sync.Once
	stopResourceSampler := func() {
		stopSamplerOnce.Do(func() { close(stopSampler) })
	}
	defer func() {
		cancelWorkload()
		workers.Wait()
		stopResourceSampler()
	}()
	for workerIndex := range config.Workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			workerResults[workerIndex] = runArenaCapacityWorker(
				workloadCtx,
				start,
				deadline,
				fixture,
				config,
				workerIndex,
			)
		}()
	}
	close(start)
	exerciseArenaCapacityReconnects(t, &fixture)
	workers.Wait()
	endedAt := time.Now()
	stopResourceSampler()
	resources := <-resourceResults

	summary := arenaCapacitySummary{
		StartedAt:              startedAt,
		EndedAt:                endedAt,
		ResourceSamples:        resources.samples,
		MaxGoroutines:          resources.maxGoroutines,
		MaxHeapAllocBytes:      resources.maxHeapAllocBytes,
		MaxDatabaseConnections: resources.maxDatabaseConnections,
		ReconnectAttempts:      fixture.reconnect.Attempts,
		ReconnectSucceeded:     fixture.reconnect.Succeeded,
		ReconnectFailed:        fixture.reconnect.Failed,
	}
	for _, result := range workerResults {
		summary.Operations += result.operations
		summary.Succeeded += result.succeeded
		summary.Failed += result.failed
		summary.CorrectnessChecks += result.correctnessChecks
		summary.CorrectnessPassed += result.correctnessPassed
		summary.CorrectnessFailed += result.correctnessFailed
		summary.Latencies = append(summary.Latencies, result.latencies...)
		summary.SchedulingLags = append(summary.SchedulingLags, result.schedulingLags...)
	}
	checkArenaCapacityFinalState(t, fixture, config, &summary)

	poolStats := sharedPool.Stat()
	summary.DatabaseAcquiredEnd = int64(poolStats.AcquiredConns())
	summary.DatabaseIdleEnd = int64(poolStats.IdleConns())
	summary.DatabaseTotalEnd = int64(poolStats.TotalConns())
	if summary.DatabaseTotalEnd > summary.MaxDatabaseConnections {
		summary.MaxDatabaseConnections = summary.DatabaseTotalEnd
	}

	report := newArenaCapacityReport(t, config, summary)
	writeArenaCapacityReport(t, report)
	return report
}

func newArenaCapacityFixture(t testing.TB, config arenaCapacityConfig) arenaCapacityFixture {
	t.Helper()
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	var databaseVersionText string
	require.NoError(t, sharedPool.QueryRow(ctx, `SHOW server_version_num`).Scan(&databaseVersionText))
	databaseVersion, err := strconv.Atoi(databaseVersionText)
	require.NoError(t, err)
	require.Equal(t, 18, databaseVersion/10000)
	require.Equal(t, config.Games*2, config.Participants)

	tx := postgres.NewTxManager(sharedPool)
	repository := postgres.NewArenaGamePostgres(tx)
	games := make([]arenaCapacityGameFixture, config.Games)

	for gameIndex := range config.Games {
		migrationFixture := createArenaReconnectMigrationFixture(t, ctx)
		require.Len(t, migrationFixture.draft.participantIDs, 2)

		var slotID uuid.UUID
		err = sharedPool.QueryRow(
			ctx,
			`SELECT slot_id FROM arena_game_attempts WHERE id = $1 AND series_id = $2`,
			migrationFixture.attemptID,
			migrationFixture.draft.seriesID,
		).Scan(&slotID)
		require.NoError(t, err)

		games[gameIndex] = arenaCapacityGameFixture{
			scope: arena.GameScope{
				TournamentID: migrationFixture.draft.tournamentID,
				SeriesID:     migrationFixture.draft.seriesID,
				SlotID:       slotID,
				GameID:       migrationFixture.attemptID,
			},
			rosterID:         migrationFixture.draft.rosterID,
			participantIDs:   append([]uuid.UUID(nil), migrationFixture.draft.participantIDs...),
			reconnectFixture: migrationFixture,
		}
	}

	return arenaCapacityFixture{
		repository:  repository,
		tournaments: postgres.NewArenaTournamentPostgres(tx),
		games:       games,
	}
}

func exerciseArenaCapacityReconnects(t testing.TB, fixture *arenaCapacityFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), arenaCapacityReconnectProbeTimeout)
	defer cancel()

	for gameIndex, game := range fixture.games {
		fixture.reconnect.Attempts++
		participantID := game.participantIDs[0]
		disconnectedAt := game.reconnectFixture.pausedAt.Add(time.Duration(gameIndex+1) * time.Second)
		intervalID, err := runArenaCapacityDisconnect(
			ctx,
			game.reconnectFixture,
			participantID,
			disconnectedAt,
		)
		if err == nil {
			err = runArenaCapacityReconnect(
				ctx,
				game.reconnectFixture,
				participantID,
				intervalID,
				disconnectedAt.Add(time.Second),
			)
		}
		if err == nil {
			var intervalState string
			var presenceState string
			err = sharedPool.QueryRow(ctx, `
				SELECT reconnect.state, presence.state
				FROM arena_reconnect_intervals reconnect
				JOIN arena_presence_states presence
					ON presence.series_id = reconnect.series_id
					AND presence.participant_id = reconnect.participant_id
				WHERE reconnect.id = $1`, intervalID).Scan(&intervalState, &presenceState)
			if err == nil && (intervalState != "reconnected" || presenceState != "connected") {
				err = fmt.Errorf("unexpected reconnect state %q/%q", intervalState, presenceState)
			}
		}
		if err == nil {
			fixture.reconnect.Succeeded++
		} else {
			fixture.reconnect.Failed++
		}
	}
}

func runArenaCapacityDisconnect(
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	participantID uuid.UUID,
	disconnectedAt time.Time,
) (uuid.UUID, error) {
	intervalID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin disconnect: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `
		SELECT 1
		FROM arena_pauses
		WHERE id = $1
		FOR UPDATE`, fixture.gamePauseID); err != nil {
		return uuid.Nil, fmt.Errorf("lock pause: %w", err)
	}
	presenceResult, err := tx.Exec(ctx, `
		UPDATE arena_presence_states
		SET state = 'disconnected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			disconnected_at = $3,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		participantID,
		disconnectedAt,
	)
	if err != nil || presenceResult.RowsAffected() != 1 {
		if err != nil {
			return uuid.Nil, fmt.Errorf("update disconnect presence: %w", err)
		}
		return uuid.Nil, fmt.Errorf("update disconnect presence: affected=%d", presenceResult.RowsAffected())
	}
	var presenceEpoch int64
	if err = tx.QueryRow(ctx, `
		SELECT presence_epoch
		FROM arena_presence_states
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		participantID,
	).Scan(&presenceEpoch); err != nil {
		return uuid.Nil, fmt.Errorf("load disconnect epoch: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO arena_reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			opened_at, deadline_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, 1,
			$8, $9, $8, $8
		)`,
		intervalID,
		fixture.gamePauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		presenceEpoch,
		disconnectedAt,
		disconnectedAt.Add(time.Minute),
	); err != nil {
		return uuid.Nil, fmt.Errorf("insert reconnect interval: %w", err)
	}
	counterResult, err := tx.Exec(ctx, `
		UPDATE arena_reconnect_slot_counters
		SET slots_used = slots_used + 1,
			revision = revision + 1,
			updated_at = $3
		WHERE pause_id = $1 AND participant_id = $2`,
		fixture.gamePauseID,
		participantID,
		disconnectedAt,
	)
	if err != nil || counterResult.RowsAffected() != 1 {
		if err != nil {
			return uuid.Nil, fmt.Errorf("update reconnect counter: %w", err)
		}
		return uuid.Nil, fmt.Errorf("update reconnect counter: affected=%d", counterResult.RowsAffected())
	}
	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit disconnect: %w", err)
	}
	return intervalID, nil
}

func runArenaCapacityReconnect(
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	participantID uuid.UUID,
	intervalID uuid.UUID,
	reconnectedAt time.Time,
) error {
	tx, err := sharedPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reconnect: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	intervalResult, err := tx.Exec(ctx, `
		UPDATE arena_reconnect_intervals
		SET state = 'reconnected',
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, reconnectedAt)
	if err != nil || intervalResult.RowsAffected() != 1 {
		if err != nil {
			return fmt.Errorf("close reconnect interval: %w", err)
		}
		return fmt.Errorf("close reconnect interval: affected=%d", intervalResult.RowsAffected())
	}
	presenceResult, err := tx.Exec(ctx, `
		UPDATE arena_presence_states
		SET state = 'connected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			connected_at = $3,
			disconnected_at = NULL,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		participantID,
		reconnectedAt,
	)
	if err != nil || presenceResult.RowsAffected() != 1 {
		if err != nil {
			return fmt.Errorf("update reconnect presence: %w", err)
		}
		return fmt.Errorf("update reconnect presence: affected=%d", presenceResult.RowsAffected())
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reconnect: %w", err)
	}
	return nil
}

func warmArenaCapacityFixture(t testing.TB, fixture arenaCapacityFixture) {
	t.Helper()
	for _, game := range fixture.games {
		record, err := fixture.repository.GetAttemptRecord(context.Background(), game.scope)
		require.NoError(t, err)
		require.True(t, arenaCapacityRecordMatches(record, game))
	}
}

func runArenaCapacityWorker(
	ctx context.Context,
	start <-chan struct{},
	deadline time.Time,
	fixture arenaCapacityFixture,
	config arenaCapacityConfig,
	workerIndex int,
) arenaCapacityWorkerResult {
	<-start
	result := arenaCapacityWorkerResult{}
	ticker := time.NewTicker(config.OperationInterval)
	defer ticker.Stop()
	deadlineTimer := time.NewTimer(time.Until(deadline))
	defer deadlineTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return result
		case <-deadlineTimer.C:
			return result
		case scheduledAt := <-ticker.C:
			if !scheduledAt.Before(deadline) {
				return result
			}
			startedAt := time.Now()
			lag := startedAt.Sub(scheduledAt)
			if lag < 0 {
				lag = 0
			}
			result.schedulingLags = append(result.schedulingLags, lag)

			operationIndex := int(result.operations)
			gameIndex := (workerIndex + operationIndex*config.Workers) % len(fixture.games)
			game := fixture.games[gameIndex]
			operationCtx, cancel := context.WithTimeout(ctx, arenaCapacityOperationTimeout)
			record, err := fixture.repository.GetAttemptRecord(operationCtx, game.scope)
			cancel()
			result.latencies = append(result.latencies, time.Since(startedAt))
			result.operations++
			if err != nil {
				result.failed++
				continue
			}
			result.succeeded++
			result.correctnessChecks++
			if arenaCapacityRecordMatches(record, game) {
				result.correctnessPassed++
			} else {
				result.correctnessFailed++
			}
		}
	}
}

func arenaCapacityRecordMatches(
	record *postgres.ArenaGameAttemptRecord,
	game arenaCapacityGameFixture,
) bool {
	return record != nil &&
		record.Game.ID == game.scope.GameID &&
		record.Game.SlotID == game.scope.SlotID &&
		record.SeriesID == game.scope.SeriesID &&
		record.RosterID == game.rosterID &&
		record.Revision == 2 &&
		record.Game.State == domain.ArenaGameStatePaused
}

func checkArenaCapacityFinalState(
	t testing.TB,
	fixture arenaCapacityFixture,
	config arenaCapacityConfig,
	summary *arenaCapacitySummary,
) {
	t.Helper()
	ctx := context.Background()
	participantCount := 0
	participantsValid := true
	for _, game := range fixture.games {
		participants, err := fixture.tournaments.ListParticipants(ctx, game.rosterID)
		if err != nil || len(participants) != len(game.participantIDs) {
			participantsValid = false
			continue
		}
		participantCount += len(participants)
	}
	summary.CorrectnessChecks++
	if participantsValid && participantCount == config.Participants {
		summary.CorrectnessPassed++
	} else {
		summary.CorrectnessFailed++
	}

	for _, game := range fixture.games {
		record, loadErr := fixture.repository.GetAttemptRecord(ctx, game.scope)
		summary.CorrectnessChecks++
		if loadErr == nil && arenaCapacityRecordMatches(record, game) {
			summary.CorrectnessPassed++
		} else {
			summary.CorrectnessFailed++
		}
	}
}

func sampleArenaCapacityResources(
	start <-chan struct{},
	stop <-chan struct{},
	result chan<- arenaCapacityResourceResult,
) {
	<-start
	current := arenaCapacityResourceResult{}
	sample := func() {
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		goroutines := int64(runtime.NumGoroutine())
		heapAlloc := int64(memory.HeapAlloc)
		databaseConnections := int64(sharedPool.Stat().TotalConns())
		current.samples++
		if goroutines > current.maxGoroutines {
			current.maxGoroutines = goroutines
		}
		if heapAlloc > current.maxHeapAllocBytes {
			current.maxHeapAllocBytes = heapAlloc
		}
		if databaseConnections > current.maxDatabaseConnections {
			current.maxDatabaseConnections = databaseConnections
		}
	}

	sample()
	ticker := time.NewTicker(arenaCapacityResourceSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			sample()
			result <- current
			return
		case <-ticker.C:
			sample()
		}
	}
}
