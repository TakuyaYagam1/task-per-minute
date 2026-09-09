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
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

const (
	tournamentCapacityNominalDuration             = 60 * time.Minute
	tournamentCapacityNominalParticipants         = 16
	tournamentCapacityNominalGames                = 8
	tournamentCapacityNominalWorkers              = 4
	tournamentCapacityNominalOperationInterval    = 250 * time.Millisecond
	tournamentCapacityNominalMinimumThroughput    = 12.0
	tournamentCapacityNominalLatencyP99LimitMS    = 250.0
	tournamentCapacityNominalSchedulingLagLimitMS = 2000.0
	tournamentCapacityResourceSampleInterval      = time.Second
	tournamentCapacityReconnectProbeTimeout       = 30 * time.Second
)

func TestTournamentNominal60Minute(t *testing.T) {
	config := tournamentCapacityConfig{
		Name:                 "tournament60",
		Profile:              "nominal",
		Duration:             tournamentCapacityNominalDuration,
		Participants:         tournamentCapacityNominalParticipants,
		Games:                tournamentCapacityNominalGames,
		Workers:              tournamentCapacityNominalWorkers,
		OperationInterval:    tournamentCapacityNominalOperationInterval,
		MinimumThroughput:    tournamentCapacityNominalMinimumThroughput,
		LatencyP99LimitMS:    tournamentCapacityNominalLatencyP99LimitMS,
		SchedulingLagLimitMS: tournamentCapacityNominalSchedulingLagLimitMS,
	}

	report := runTournamentCapacityWorkload(t, config)
	require.GreaterOrEqual(t, report.Window.DurationSeconds, tournamentCapacityNominalDuration.Seconds())
	assertTournamentCapacityReportPass(t, report)
}

func TestTournamentCapacityFixture(t *testing.T) {
	config := tournamentCapacityConfig{
		Participants: tournamentCapacityNominalParticipants,
		Games:        tournamentCapacityNominalGames,
	}
	fixture := newTournamentCapacityFixture(t, config)
	exerciseTournamentCapacityReconnects(t, &fixture)
	warmTournamentCapacityFixture(t, fixture)

	summary := tournamentCapacitySummary{}
	checkTournamentCapacityFinalState(t, fixture, config, &summary)
	require.Len(t, fixture.games, tournamentCapacityNominalGames)
	require.EqualValues(t, tournamentCapacityNominalGames, fixture.reconnect.Attempts)
	require.EqualValues(t, tournamentCapacityNominalGames, fixture.reconnect.Succeeded)
	require.Zero(t, fixture.reconnect.Failed)
	require.EqualValues(t, tournamentCapacityNominalGames+1, summary.CorrectnessChecks)
	require.Equal(t, summary.CorrectnessChecks, summary.CorrectnessPassed)
	require.Zero(t, summary.CorrectnessFailed)
}

type tournamentCapacityFixture struct {
	repository  *postgres.GamePostgres
	tournaments *postgres.TournamentPostgres
	games       []tournamentCapacityGameFixture
	reconnect   tournamentCapacityReconnect
}

type tournamentCapacityGameFixture struct {
	scope            gamedomain.Scope
	rosterID         uuid.UUID
	participantIDs   []uuid.UUID
	reconnectFixture reconnectMigrationFixture
}

type tournamentCapacityWorkerResult struct {
	operations        int64
	succeeded         int64
	failed            int64
	correctnessChecks int64
	correctnessPassed int64
	correctnessFailed int64
	latencies         []time.Duration
	schedulingLags    []time.Duration
}

type tournamentCapacityResourceResult struct {
	samples                int64
	maxGoroutines          int64
	maxHeapAllocBytes      int64
	maxDatabaseConnections int64
}

func runTournamentCapacityWorkload(t *testing.T, config tournamentCapacityConfig) tournamentCapacityReport {
	t.Helper()
	require.Positive(t, config.Duration)
	require.Equal(t, 16, config.Participants)
	require.Positive(t, config.Games)
	require.Positive(t, config.Workers)
	require.LessOrEqual(t, config.Workers, config.Games)
	require.Less(t, config.Workers, tournamentCapacityPoolLimit)
	require.Positive(t, config.OperationInterval)
	require.EqualValues(t, tournamentCapacityPoolLimit, sharedPool.Stat().MaxConns())
	if testDeadline, ok := t.Deadline(); ok {
		require.GreaterOrEqual(
			t,
			time.Until(testDeadline),
			config.Duration+tournamentCapacityDeadlineBuffer,
			"capacity gate needs an explicit go test timeout beyond the workload duration",
		)
	}

	fixture := newTournamentCapacityFixture(t, config)
	warmTournamentCapacityFixture(t, fixture)
	workloadCtx, cancelWorkload := context.WithCancel(t.Context())
	defer cancelWorkload()

	start := make(chan struct{})
	stopSampler := make(chan struct{})
	resourceResults := make(chan tournamentCapacityResourceResult, 1)
	workerResults := make([]tournamentCapacityWorkerResult, config.Workers)
	startedAt := time.Now()
	deadline := startedAt.Add(config.Duration)

	go sampleTournamentCapacityResources(start, stopSampler, resourceResults)

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
			workerResults[workerIndex] = runTournamentCapacityWorker(
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
	exerciseTournamentCapacityReconnects(t, &fixture)
	workers.Wait()
	endedAt := time.Now()
	stopResourceSampler()
	resources := <-resourceResults

	summary := tournamentCapacitySummary{
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
	checkTournamentCapacityFinalState(t, fixture, config, &summary)

	poolStats := sharedPool.Stat()
	summary.DatabaseAcquiredEnd = int64(poolStats.AcquiredConns())
	summary.DatabaseIdleEnd = int64(poolStats.IdleConns())
	summary.DatabaseTotalEnd = int64(poolStats.TotalConns())
	if summary.DatabaseTotalEnd > summary.MaxDatabaseConnections {
		summary.MaxDatabaseConnections = summary.DatabaseTotalEnd
	}

	report := newTournamentCapacityReport(t, config, summary)
	writeTournamentCapacityReport(t, report)
	return report
}

func newTournamentCapacityFixture(tb testing.TB, config tournamentCapacityConfig) tournamentCapacityFixture {
	tb.Helper()
	ctx := context.Background()
	resetMigrationTables(ctx, tb)
	tb.Cleanup(func() { resetMigrationTables(ctx, tb) })

	var databaseVersionText string
	require.NoError(tb, sharedPool.QueryRow(ctx, `SHOW server_version_num`).Scan(&databaseVersionText))
	databaseVersion, err := strconv.Atoi(databaseVersionText)
	require.NoError(tb, err)
	require.Equal(tb, 18, databaseVersion/10000)
	require.Equal(tb, config.Games*2, config.Participants)

	tx := postgres.NewTxManager(sharedPool)
	repository := postgres.NewGamePostgres(tx)
	games := make([]tournamentCapacityGameFixture, config.Games)

	for gameIndex := range config.Games {
		migrationFixture := createReconnectMigrationFixture(ctx, tb)
		require.Len(tb, migrationFixture.draft.participantIDs, 2)

		var slotID uuid.UUID
		err = sharedPool.QueryRow(
			ctx,
			`SELECT slot_id FROM game_attempts WHERE id = $1 AND series_id = $2`,
			migrationFixture.attemptID,
			migrationFixture.draft.seriesID,
		).Scan(&slotID)
		require.NoError(tb, err)

		games[gameIndex] = tournamentCapacityGameFixture{
			scope: gamedomain.Scope{
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

	return tournamentCapacityFixture{
		repository:  repository,
		tournaments: postgres.NewTournamentPostgres(tx),
		games:       games,
	}
}

func exerciseTournamentCapacityReconnects(tb testing.TB, fixture *tournamentCapacityFixture) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), tournamentCapacityReconnectProbeTimeout)
	defer cancel()

	for gameIndex, game := range fixture.games {
		fixture.reconnect.Attempts++
		participantID := game.participantIDs[0]
		disconnectedAt := game.reconnectFixture.pausedAt.Add(time.Duration(gameIndex+1) * time.Second)
		intervalID, err := runTournamentCapacityDisconnect(
			ctx,
			game.reconnectFixture,
			participantID,
			disconnectedAt,
		)
		if err == nil {
			err = runTournamentCapacityReconnect(
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
				FROM reconnect_intervals reconnect
				JOIN presence_states presence
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

func warmTournamentCapacityFixture(tb testing.TB, fixture tournamentCapacityFixture) {
	tb.Helper()
	for _, game := range fixture.games {
		record, err := fixture.repository.GetAttemptRecord(context.Background(), game.scope)
		require.NoError(tb, err)
		require.True(tb, tournamentCapacityRecordMatches(record, game))
	}
}

func runTournamentCapacityWorker(
	ctx context.Context,
	start <-chan struct{},
	deadline time.Time,
	fixture tournamentCapacityFixture,
	config tournamentCapacityConfig,
	workerIndex int,
) tournamentCapacityWorkerResult {
	<-start
	result := tournamentCapacityWorkerResult{}
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
			operationCtx, cancel := context.WithTimeout(ctx, tournamentCapacityOperationTimeout)
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
			if tournamentCapacityRecordMatches(record, game) {
				result.correctnessPassed++
			} else {
				result.correctnessFailed++
			}
		}
	}
}

func tournamentCapacityRecordMatches(
	record *postgres.GameAttemptRecord,
	game tournamentCapacityGameFixture,
) bool {
	return record != nil &&
		record.Game.ID == game.scope.GameID &&
		record.Game.SlotID == game.scope.SlotID &&
		record.SeriesID == game.scope.SeriesID &&
		record.RosterID == game.rosterID &&
		record.Revision == 2 &&
		record.Game.State == domain.GameStatePaused
}

func checkTournamentCapacityFinalState(
	tb testing.TB,
	fixture tournamentCapacityFixture,
	config tournamentCapacityConfig,
	summary *tournamentCapacitySummary,
) {
	tb.Helper()
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
		if loadErr == nil && tournamentCapacityRecordMatches(record, game) {
			summary.CorrectnessPassed++
		} else {
			summary.CorrectnessFailed++
		}
	}
}

func sampleTournamentCapacityResources(
	start <-chan struct{},
	stop <-chan struct{},
	result chan<- tournamentCapacityResourceResult,
) {
	<-start
	current := tournamentCapacityResourceResult{}
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
	ticker := time.NewTicker(tournamentCapacityResourceSampleInterval)
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
