//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamenoshow "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/noshow"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func TestOperatorNoShowAndWaveStartUseSameLockOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	fixture := prepareFinalSwissBeforeStart(ctx, t, true, true)
	repository, _ := swissOperatorResultWorkflow(t, fixture)
	binding := fixture.binding[1]
	authority, err := repository.LockOperatorResultAuthority(ctx, fixture.tournamentID, binding.SeriesID)
	require.NoError(t, err)
	var waveRevisionID, windowRevisionID uuid.UUID
	var deadline time.Time
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT wave.revision_id, ready_window.revision_id, ready_window.deadline
		FROM waves AS wave JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id WHERE wave.id = $1`, fixture.waveID).
		Scan(&waveRevisionID, &windowRevisionID, &deadline))
	command := tournamentadmin.NoShowCommand{
		CommandScope: tournamentadmin.CommandScope{Operator: tournamentadmin.OperatorIdentity{ActorID: uuid.New()}, TournamentID: fixture.tournamentID, CommandID: uuid.New()},
		WaveID:       fixture.waveID, WindowID: fixture.windowID, SeriesID: binding.SeriesID,
		Confirmed: true, Reason: "participant absent at ready deadline", ExpectedAuthorityRevision: authority.AuthorityRevision,
		ExpectedWaveRevisionID: waveRevisionID, ExpectedWindowRevisionID: windowRevisionID, ExpectedSeriesState: domain.SeriesStateReady,
		GameResultRevisionIDs: []uuid.UUID{uuid.New()}, ScoreRevisionID: uuid.New(), SeriesResultRevisionID: uuid.New(),
	}
	loaded, err := repository.LoadOperatorNoShowAuthority(ctx, command)
	require.NoError(t, err)
	if delay := time.Until(deadline); delay > 0 {
		time.Sleep(delay + time.Millisecond)
	}
	resolution, _, err := gamenoshow.NoShowNewUseCase(noShowPlanRepository{authority: loaded}, playoffPublicationClock{}).Resolve(ctx, gamenoshow.NoShowCommand{
		Scope: loaded.Scope, CommandID: command.CommandID, ExpectedWaveRevisionID: domain.WaveRevisionID(waveRevisionID),
		ExpectedWindowRevisionID: domain.ReadyWindowRevisionID(windowRevisionID), ExpectedSeriesState: command.ExpectedSeriesState,
		GameResultRevisionIDs: []domain.OfficialResultRevisionID{domain.OfficialResultRevisionID(command.GameResultRevisionIDs[0])},
		ScoreRevisionID:       domain.SeriesScoreRevisionID(command.ScoreRevisionID), SeriesResultRevisionID: domain.OfficialResultRevisionID(command.SeriesResultRevisionID),
	})
	require.NoError(t, err)
	header, err := fixture.tx.Querier(ctx).LockWaveStartAuthority(ctx, sqlc.LockWaveStartAuthorityParams{TournamentID: fixture.tournamentID, WaveID: fixture.waveID})
	require.NoError(t, err)
	startCommand := gamestart.StartCommand{Scope: gamestart.StartScope{TournamentID: fixture.tournamentID, WaveID: fixture.waveID, WindowID: fixture.windowID},
		CommandID: uuid.New(), ActorID: uuid.New(), ExecutionAuthority: fixture.executionAuthority,
		ExpectedProjectionRevision: header.ProjectionRevision, RequestDigest: sha256.Sum256([]byte("contended Wave start")),
		ExpectedRevisions: domain.ReadyWindowSourceRevisions{WaveRevisionID: domain.WaveRevisionID(header.RevisionID), WaveRevision: header.Revision,
			ProjectionRevisionID: header.ProjectionRevisionID, ProjectionRevision: header.ProjectionRevision,
			ArtifactRevisionID: header.ArtifactRevisionID, ArtifactRevision: header.ArtifactRevision}}
	document, err := json.Marshal(struct {
		Action  tournamentadmin.OperatorResultAction `json:"action"`
		Command any                                  `json:"command"`
	}{Action: tournamentadmin.OperatorResultActionNoShow, Command: command})
	require.NoError(t, err)
	digest := sha256.Sum256(document)
	startLocked, continueStart := make(chan error, 1), make(chan struct{})
	startDone, noShowDone := make(chan error, 1), make(chan error, 1)
	noShowPID := make(chan int32, 1)
	go func() {
		startDone <- fixture.tx.Do(ctx, func(txCtx context.Context) error {
			_, err := fixture.tx.Querier(txCtx).LockWaveStartAuthority(txCtx, sqlc.LockWaveStartAuthorityParams{TournamentID: fixture.tournamentID, WaveID: fixture.waveID})
			startLocked <- err
			if err != nil {
				return err
			}
			select {
			case <-continueStart:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, _, err = fixture.start.Start(txCtx, startCommand)
			return err
		})
	}()
	require.NoError(t, <-startLocked)
	go func() {
		noShowDone <- fixture.tx.Do(ctx, func(txCtx context.Context) error {
			var pid int32
			if err := fixture.tx.Conn(txCtx).QueryRow(txCtx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				return err
			}
			noShowPID <- pid
			_, _, err := repository.CommitOperatorNoShow(txCtx, command, digest, *resolution)
			return err
		})
	}()
	pid := <-noShowPID
	waiting := false
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		err := sharedPool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1)) > 0", pid).Scan(&waiting)
		if err != nil || waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A writer waiting for the Wave/projection boundary must not already own
	// the later Game lock. This detects the inversion without relying on the
	// database choosing a particular deadlock victim.
	probe, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	_, attemptLockErr := probe.Exec(ctx, "SELECT id FROM game_attempts WHERE id = $1 FOR UPDATE NOWAIT", resolution.GameRevisions[0].GameID)
	require.NoError(t, probe.Rollback(ctx))
	close(continueStart)
	startErr, noShowErr := <-startDone, <-noShowDone
	require.True(t, waiting, "no-show must contend with the exact WaveStart header locks")
	require.NoError(t, attemptLockErr, "no-show cannot lock the Game before the Wave/projection authority")
	for _, outcome := range []error{startErr, noShowErr} {
		var detail *pgconn.PgError
		if errors.As(outcome, &detail) {
			require.NotEqual(t, "40P01", detail.Code, "opposite lock acquisition order")
		}
	}
	require.ErrorIs(t, startErr, gamestart.ErrWaveStartAuthorityConflict, "an incomplete ready window cannot start")
	require.NoError(t, noShowErr)
	var starts, noShows int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM wave_control_commands WHERE wave_id = $1 AND action = 'start'),
		(SELECT count(*) FROM normal_no_show_commits WHERE wave_id = $1)`, fixture.waveID).Scan(&starts, &noShows))
	require.Zero(t, starts)
	require.Equal(t, 1, noShows)
}

// Planning uses the production use case; persistence below is the real
// repository. This captures a valid resolution before introducing contention.
type noShowPlanRepository struct{ authority gamenoshow.NoShowAuthority }

func (r noShowPlanRepository) LoadNormalNoShowAuthority(context.Context, domain.NormalNoShowScope) (gamenoshow.NoShowAuthority, error) {
	return r.authority, nil
}
func (noShowPlanRepository) CommitNormalNoShow(_ context.Context, resolution gamenoshow.NoShowResolution) (*gamenoshow.NoShowResolution, bool, error) {
	return &resolution, true, nil
}
