//go:build integration

package execution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// RunExecutionRepositorySerializesReadinessAndStart runs the wave repository
// concurrency coverage against a caller-owned integration pool.
func RunExecutionRepositorySerializesReadinessAndStart(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	withExecutionRepositoryPool(t, pool, runExecutionRepositorySerializesReadinessAndStart)
}

func withExecutionRepositoryPool(t *testing.T, pool *pgxpool.Pool, run func(*testing.T)) {
	t.Helper()
	require.NotNil(t, pool)
	migrationPoolMu.Lock()
	previousPool := migrationPool
	migrationPool = pool
	defer func() {
		migrationPool = previousPool
		migrationPoolMu.Unlock()
	}()

	ctx := context.Background()
	resetMigrationTables(ctx, t)
	defer resetMigrationTables(ctx, t)
	run(t)
}

func runExecutionRepositorySerializesReadinessAndStart(t *testing.T) {
	ctx := context.Background()

	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	players := createMigrationPlayers(ctx, t, 2)
	participants := createSwissMigrationParticipants(ctx, t, rosterID, players)
	baseTime := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	sourceProjectionID := uuid.New()
	sourceCutoffID := uuid.New()
	_, err := migrationPool.Exec(ctx, `
		INSERT INTO projection_cutoffs (
			id, tournament_id, roster_id, sequence_number, source_kind,
			reason, cutoff_at, created_at
		)
		VALUES ($1, $2, $3, 1, 'initial', 'execution genesis source', $4, $4)`,
		sourceCutoffID, tournamentID, rosterID, baseTime,
	)
	require.NoError(t, err)
	_, err = migrationPool.Exec(ctx, `
		INSERT INTO projection_revisions (
			id, tournament_id, roster_id, revision_number, cutoff_id, created_at
		)
		VALUES ($1, $2, $3, 1, $4, $5)`,
		sourceProjectionID, tournamentID, rosterID, sourceCutoffID, baseTime,
	)
	require.NoError(t, err)
	repository := waverepo.NewWavePostgres(postgres.NewTxManager(migrationPool))
	waveID := uuid.New()

	wave, err := repository.Create(ctx, waverepo.WaveCreateInput{
		ID: waveID, TournamentID: tournamentID, RosterID: rosterID,
		RevisionID: domain.WaveRevisionID(uuid.New()), ParticipantIDs: participants,
		CommandID: uuid.New(), SourceProjectionRevisionID: sourceProjectionID, SourceProjectionRevision: 1,
		Series: []waverepo.WaveSeriesInput{{
			ID: uuid.New(), FirstParticipantID: participants[0], SecondParticipantID: participants[1],
			Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
		}},
		CreatedAt: baseTime,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, wave.Revision)
	require.Len(t, wave.Series, 1)
	require.EqualValues(t, 1, wave.ReadinessRevisions[participants[0]])

	seriesID := wave.Series[0].ID
	var seriesState, operation string
	var scoreRevisionID uuid.UUID
	var scoreRevision, firstWins, secondWins int64
	require.NoError(t, migrationPool.QueryRow(ctx, `
		SELECT series.state,
			series.current_score_revision_id,
			score.revision_number,
			score.operation,
			score.first_participant_wins,
			score.second_participant_wins
		FROM series
		INNER JOIN series_score_revisions AS score
			ON score.id = series.current_score_revision_id
		WHERE series.id = $1`, seriesID,
	).Scan(&seriesState, &scoreRevisionID, &scoreRevision, &operation, &firstWins, &secondWins))
	require.Equal(t, string(domain.SeriesStatePlanned), seriesState)
	require.NotEqual(t, uuid.Nil, scoreRevisionID)
	require.EqualValues(t, 1, scoreRevision)
	require.Equal(t, "initialize", operation)
	require.Zero(t, firstWins)
	require.Zero(t, secondWins)

	var resultHeads, attemptEvidence, adjudications int
	require.NoError(t, migrationPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM official_result_heads WHERE entity_kind = 'series' AND entity_id = $1),
			(SELECT COUNT(*) FROM series_score_revision_attempts WHERE score_revision_id = $2),
			(SELECT COUNT(*) FROM series_score_revision_adjudications WHERE score_revision_id = $2)`,
		seriesID, scoreRevisionID,
	).Scan(&resultHeads, &attemptEvidence, &adjudications))
	require.Zero(t, resultHeads)
	require.Zero(t, attemptEvidence)
	require.Zero(t, adjudications)

	_, err = migrationPool.Exec(ctx, `
		INSERT INTO series (
			id, tournament_id, roster_id, first_participant_id, second_participant_id,
			format, state, revision, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, 'bo1', 'planned', 1, $6, $6)`,
		uuid.New(), tournamentID, rosterID, participants[0], participants[1], baseTime.Add(time.Second),
	)
	require.ErrorContains(t, err, "planned Series requires exactly one genesis score head")

	windowID := uuid.New()
	openedAt := baseTime.Add(time.Second)
	deadline := openedAt.Add(30 * time.Second)
	wave, changed, err := repository.OpenReadyWindow(ctx, tournamentID, waveID, wave.Revision, waverepo.ReadyWindowInput{
		ID: windowID, RevisionID: domain.ReadyWindowRevisionID(uuid.New()), OpenedAt: openedAt, Deadline: deadline,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.EqualValues(t, 2, wave.Revision)
	require.EqualValues(t, 2, wave.ReadinessRevisions[participants[0]])

	type readyResult struct {
		participantID uuid.UUID
		changed       bool
		err           error
	}
	readyStart := make(chan struct{})
	readyResults := make(chan readyResult, 2)
	var workers sync.WaitGroup
	for _, participantID := range participants {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-readyStart
			_, changed, markErr := repository.MarkReady(
				ctx, tournamentID, waveID, windowID, participantID, 2, 2, openedAt.Add(time.Second),
			)
			readyResults <- readyResult{participantID: participantID, changed: changed, err: markErr}
		}()
	}
	close(readyStart)
	workers.Wait()
	close(readyResults)
	var staleParticipant uuid.UUID
	readyWinners := 0
	for item := range readyResults {
		require.NoError(t, item.err)
		if item.changed {
			readyWinners++
		} else {
			staleParticipant = item.participantID
		}
	}
	require.Equal(t, 1, readyWinners)
	require.NotEqual(t, uuid.Nil, staleParticipant)

	wave, changed, err = repository.MarkReady(
		ctx, tournamentID, waveID, windowID, staleParticipant, 3, 2, openedAt.Add(2*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WaveStateReady, wave.Wave.State)
	require.EqualValues(t, 4, wave.Revision)

	start := make(chan struct{})
	startResults := make(chan readyResult, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, started, startErr := repository.Start(
				ctx, tournamentID, waveID, windowID, 4, openedAt.Add(3*time.Second),
			)
			startResults <- readyResult{changed: started, err: startErr}
		}()
	}
	close(start)
	workers.Wait()
	close(startResults)
	startWinners := 0
	for item := range startResults {
		require.NoError(t, item.err)
		if item.changed {
			startWinners++
		}
	}
	require.Equal(t, 1, startWinners)

	loaded, err := repository.Get(ctx, tournamentID, waveID)
	require.NoError(t, err)
	require.Equal(t, domain.WaveStateActive, loaded.Wave.State)
	require.Equal(t, domain.ReadyWindowStateConsumed, loaded.Wave.ReadyWindow.State)
	require.EqualValues(t, 5, loaded.Revision)
	loaded, changed, err = repository.Close(ctx, tournamentID, waveID, loaded.Revision, openedAt.Add(4*time.Second))
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WaveStateCompleted, loaded.Wave.State)
}
