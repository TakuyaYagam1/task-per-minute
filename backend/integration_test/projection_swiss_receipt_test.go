//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
	settlementrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/settlement"
	progressionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/progression"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gamesettlement "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/settlement"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	progression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestFinalSwissSettlementPublishesReceipt(t *testing.T) {
	ctx := context.Background()
	fixture := prepareFinalSwissSettlement(ctx, t)
	final := settleSwissReceiptSeries(ctx, t, fixture, 1)
	var projectionID, outboxID uuid.UUID
	var digest []byte
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT receipt.projection_revision_id, outbox.id, receipt.canonical_payload_digest
		FROM final_swiss_projection_receipts AS receipt
		JOIN outbox_events AS outbox ON outbox.projection_revision_id = receipt.projection_revision_id
		WHERE receipt.tournament_id = $1`, fixture.tournamentID).Scan(&projectionID, &outboxID, &digest))
	require.Equal(t, final.Evidence.ProjectionRevisionID, projectionID)
	require.Equal(t, final.Evidence.OutboxEventID, outboxID)
	before := readFinalSwissReceipt(ctx, t, fixture)
	readDigest := before.Projection().Revision().PayloadDigest()
	require.Equal(t, digest, readDigest[:])
	closeSwissReceiptWave(ctx, t, fixture)
	after := readFinalSwissReceipt(ctx, t, fixture)
	require.Equal(t, before.Projection().Payload(), after.Projection().Payload(), "closing the mutable Wave cannot alter immutable receipt evidence")
}

func prepareFinalSwissSettlement(ctx context.Context, t *testing.T) tournamentAdminSwissProofFixture {
	t.Helper()
	return prepareFinalSwissBeforeStart(ctx, t, false)
}

func prepareFinalSwissBeforeStart(ctx context.Context, t *testing.T, stopBeforeStart bool, noShow ...bool) tournamentAdminSwissProofFixture {
	t.Helper()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })
	for range 12 {
		_, err := sharedPool.Exec(ctx, `INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
			VALUES ($1, 'Swiss receipt task', 'web', 'easy', 60, 'receipt-fixture', 'normal')`, "swiss_receipt_"+uuid.NewString()[:8])
		require.NoError(t, err)
	}
	for _, category := range []string{"web", "crypto", "forensics", "reverse", "pwn"} {
		for range 6 {
			_, err := sharedPool.Exec(ctx, `INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'Playoff task', $2, 'easy', 60, 'playoff-fixture', 'normal')`, "playoff_"+uuid.NewString()[:8], category)
			require.NoError(t, err)
		}
	}
	fixture := createTournamentAdminSwissProofFixture(ctx, t)
	for round := 1; round <= 3; round++ {
		if round > 1 {
			fixture = nextSwissReceiptWave(ctx, t, fixture, round,
				round == 3 && len(noShow) > 0 && noShow[0], round == 3 && len(noShow) > 1 && noShow[1])
		}
		if round == 3 && stopBeforeStart {
			return fixture
		}
		_, changed, err := fixture.start.Start(ctx, fixture.startCommand(ctx, t))
		require.NoError(t, err)
		require.True(t, changed)
		for index := range fixture.binding {
			if round == 3 && index == 1 {
				return fixture
			}
			settleSwissReceiptSeries(ctx, t, fixture, index)
			var receiptCount int
			require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM final_swiss_projection_receipts WHERE tournament_id = $1`, fixture.tournamentID).Scan(&receiptCount))
			require.Zero(t, receiptCount)
		}
		closeSwissReceiptWave(ctx, t, fixture)
	}
	t.Fatal("missing final Swiss settlement")
	return fixture
}

func closeSwissReceiptWave(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) {
	t.Helper()
	repository := waverepo.NewWavePostgres(fixture.tx)
	wave, err := repository.Get(ctx, fixture.tournamentID, fixture.waveID)
	require.NoError(t, err)
	_, changed, err := repository.Close(ctx, fixture.tournamentID, fixture.waveID, wave.Revision, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, changed)
}

func swissReceiptAuthority(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) progression.Authority {
	t.Helper()
	record, err := catalogrepo.NewTournamentCatalogPostgres(fixture.tx).GetTournament(ctx, fixture.tournamentID)
	require.NoError(t, err)
	projectionID, revision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	return progression.Authority{ProjectionRevisionID: projectionID, ProjectionRevision: revision, Tournament: inbound.TournamentView{
		ID: record.ID, RosterID: fixture.rosterID, Preset: record.Preset, State: record.State,
		Revision: record.Revision, RosterSize: len(fixture.participants), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, StartedAt: record.StartedAt,
	}}
}

func readFinalSwissReceipt(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) playoff.FinalSwissProjection {
	t.Helper()
	authority := swissReceiptAuthority(ctx, t, fixture)
	input, err := progressionrepo.NewTournamentProgressionPostgres(fixture.tx).LoadLockedSwissTerminalEvidence(ctx, progression.Command{
		CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, ActorID: uuid.New(),
		Action: progression.ActionStartPlayoffs, ExpectedProjectionRevision: authority.ProjectionRevision,
	}, authority)
	require.NoError(t, err)
	plan, err := playoff.PlanFinalSwissReceipt(input)
	require.NoError(t, err)
	require.Equal(t, authority.ProjectionRevisionID, plan.Projection().Revision().ID().UUID())
	return plan
}

func TestFinalSwissPublicationRollsBackWithSettlement(t *testing.T) {
	ctx := context.Background()
	fixture := prepareFinalSwissSettlement(ctx, t)
	beforeID, beforeRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	before := swissPublicationCounts(ctx, t, fixture)
	abort := errors.New("stop after receipt readback")
	err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
		settleSwissReceiptSeries(txCtx, t, fixture, 1)
		chain, err := fixture.tx.Querier(txCtx).LockTournamentProgressionFinalSwissReceiptChain(txCtx, sqlc.LockTournamentProgressionFinalSwissReceiptChainParams{
			TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
			ProjectionRevisionID: currentSwissTransactionProjection(txCtx, t, fixture),
		})
		require.NoError(t, err)
		require.Equal(t, 1, len(chain))
		return abort
	})
	require.ErrorIs(t, err, abort)
	require.Equal(t, before, swissPublicationCounts(ctx, t, fixture))
	afterID, afterRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	require.Equal(t, beforeID, afterID)
	require.Equal(t, beforeRevision, afterRevision)
	settleSwissReceiptSeries(ctx, t, fixture, 1)
	readFinalSwissReceipt(ctx, t, fixture)
}

func currentSwissTransactionProjection(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) uuid.UUID {
	t.Helper()
	revision, err := fixture.tx.Querier(ctx).GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{TournamentID: fixture.tournamentID, RosterID: fixture.rosterID})
	require.NoError(t, err)
	return revision.ID
}

func swissPublicationCounts(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture) [9]int64 {
	t.Helper()
	var counts [9]int64
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM result_commits WHERE tournament_id = $1)
		    + (SELECT count(*) FROM operator_forfeit_commits WHERE tournament_id = $1)
		    + (SELECT count(*) FROM normal_no_show_commits WHERE tournament_id = $1),
		(SELECT count(*) FROM result_projection_nodes WHERE tournament_id = $1),
		(SELECT count(*) FROM projection_revisions WHERE tournament_id = $1),
		(SELECT count(*) FROM projection_dependencies WHERE tournament_id = $1),
		(SELECT count(*) FROM final_swiss_projection_receipts WHERE tournament_id = $1),
		(SELECT count(*) FROM final_swiss_projection_receipt_series WHERE tournament_id = $1),
		(SELECT count(*) FROM final_swiss_projection_receipt_games WHERE tournament_id = $1),
		(SELECT count(*) FROM final_swiss_projection_receipt_ledger_entries WHERE tournament_id = $1),
		(SELECT count(*) FROM outbox_events WHERE tournament_id = $1)`, fixture.tournamentID).
		Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &counts[5], &counts[6], &counts[7], &counts[8]))
	return counts
}

func TestSwissWaveCloseAdvancesRevisionIdentity(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(ctx, t) })
	fixture := createTournamentAdminSwissProofFixture(ctx, t)
	_, changed, err := fixture.start.Start(ctx, fixture.startCommand(ctx, t))
	require.NoError(t, err)
	require.True(t, changed)
	for index := range fixture.binding {
		settleSwissReceiptSeries(ctx, t, fixture, index)
	}
	repository := waverepo.NewWavePostgres(fixture.tx)
	current, err := repository.Get(ctx, fixture.tournamentID, fixture.waveID)
	require.NoError(t, err)
	closed, changed, err := repository.Close(ctx, fixture.tournamentID, fixture.waveID, current.Revision, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WaveStateCompleted, closed.Wave.State)
	require.Equal(t, current.Revision+1, closed.Revision)
	require.NotEqual(t, current.Wave.RevisionID, closed.Wave.RevisionID)
	_, changed, err = repository.Close(ctx, fixture.tournamentID, fixture.waveID, current.Revision, time.Now().UTC())
	require.NoError(t, err)
	require.False(t, changed)
}

func TestFinalSwissPublicationLocksOnlySelectedHeads(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(ctx, t) })
	fixture := createTournamentAdminSwissProofFixture(ctx, t)
	_, changed, err := fixture.start.Start(ctx, fixture.startCommand(ctx, t))
	require.NoError(t, err)
	require.True(t, changed)
	for index := range fixture.binding {
		settleSwissReceiptSeries(ctx, t, fixture, index)
	}
	projectionID, _ := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	q := sqlc.New(sharedPool)
	params := sqlc.LockFinalSwissPublicationNodesParams{ProjectionRevisionID: projectionID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID}
	rows, err := q.LockFinalSwissPublicationNodes(ctx, params)
	require.NoError(t, err)
	require.Equal(t, 6, len(rows), "only two current game, result, and score nodes are receipt authority")
	lock, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Rollback(ctx) })
	_, err = lock.Exec(ctx, `SELECT node.id FROM result_projection_nodes AS node
		JOIN result_projection_node_authorities AS authority ON authority.id = node.authority_id
		WHERE node.tournament_id = $1 AND authority.source_kind = 'wave_initialization' FOR UPDATE OF node`, fixture.tournamentID)
	require.NoError(t, err)
	queryCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = q.LockFinalSwissPublicationNodes(queryCtx, params)
	require.NoError(t, err, "an unrelated historical score node must not block the selected-head snapshot")
}

func settleSwissReceiptSeries(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture, index int) *gamesettlement.SettlementRecord {
	t.Helper()
	repository, scope := submitSwissReceiptSeries(ctx, t, fixture, index)
	record, changed, err := gamesettlement.SettlementNewUseCase(receiptSettlementRepository{ParticipantSettlementRepository: repository, t: t}).Settle(ctx, gamesettlement.SettlementCommand{Scope: scope, CommandID: uuid.New()})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.SeriesStateCompleted, record.Series.State)
	return record
}

func submitSwissReceiptSeries(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture, index int) (*settlementrepo.ParticipantSettlementRepository, gamedomain.SubmissionScope) {
	t.Helper()
	binding := fixture.binding[index]
	scope := gamedomain.SubmissionScope{Game: gamedomain.Scope{TournamentID: fixture.tournamentID, SeriesID: binding.SeriesID}, WaveID: fixture.waveID, AssignmentID: binding.AssignmentID}
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT attempt.id, attempt.slot_id FROM assignments AS assignment JOIN game_attempts AS attempt ON attempt.id = assignment.attempt_id WHERE assignment.id = $1`, binding.AssignmentID).Scan(&scope.Game.GameID, &scope.Game.SlotID))
	results := resultauthority.NewResultPostgres(fixture.tx)
	repository := settlementrepo.NewParticipantSettlementRepositoryWithFinalizer(fixture.tx, results, resultauthority.FinalizeProjection)
	authority, err := repository.LoadConcurrentWinnerAuthority(ctx, scope)
	require.NoError(t, err)
	commandID := uuid.New()
	submittedAt := time.Now().UTC().Truncate(time.Microsecond)
	_, changed, err := results.RecordSubmission(ctx, resultrepo.SubmissionInput{
		ID:           uuid.NewSHA1(commandID, []byte("participant-command:submission-event")),
		Scope:        resultrepo.ResultScope{TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, SeriesID: binding.SeriesID, AttemptID: scope.Game.GameID},
		AssignmentID: scope.AssignmentID, ParticipantID: binding.FirstParticipantID, IdempotencyKey: commandID,
		Status: "accepted", PayloadDigest: authority.StartedGame.ContentDigest, IntentDigest: sha256.Sum256([]byte("Swiss receipt submission")),
		SubmittedAt: submittedAt, ReceivedAt: submittedAt, CreatedAt: submittedAt,
		ExpectedAttemptRevision: authority.Revision, ExpectedAttemptState: domain.GameStateActive,
	})
	require.NoError(t, err)
	require.True(t, changed)
	return repository, scope
}

func TestFinalSwissPublicationConcurrentWriters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := prepareFinalSwissSettlement(ctx, t)
	repository, scope := submitSwissReceiptSeries(ctx, t, fixture, 1)
	before := swissPublicationCounts(ctx, t, fixture)
	ready, release := make(chan struct{}, 2), make(chan struct{})
	type outcome struct {
		record  *gamesettlement.SettlementRecord
		changed bool
		err     error
	}
	results := make(chan outcome, 2)
	for range 2 {
		gate := &receiptSettlementBarrier{ParticipantSettlementRepository: repository, ready: ready, release: release}
		go func() {
			record, changed, err := gamesettlement.SettlementNewUseCase(gate).Settle(ctx, gamesettlement.SettlementCommand{Scope: scope, CommandID: uuid.New()})
			results <- outcome{record, changed, err}
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	winners := 0
	var committed []*gamesettlement.SettlementRecord
	for range 2 {
		select {
		case result := <-results:
			if result.err == nil {
				require.NotNil(t, result.record)
				committed = append(committed, result.record)
				if result.changed {
					winners++
				}
			} else {
				require.False(t, result.changed)
				require.True(t, errors.Is(result.err, gamesettlement.ErrConcurrentWinnerConflict) || errors.Is(result.err, gamesettlement.ErrConcurrentWinnerUnavailable) || errors.Is(result.err, domain.ErrConflict), "unexpected loser error: %v", result.err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	require.Equal(t, 1, winners)
	for _, record := range committed {
		require.Equal(t, committed[0].Evidence, record.Evidence, "a concurrent loser may replay only the committed authority")
	}
	after := swissPublicationCounts(ctx, t, fixture)
	require.Equal(t, before[0]+1, after[0], "one result commit")
	require.Equal(t, before[2]+1, after[2], "one projection revision")
	require.Equal(t, int64(1), after[4], "one receipt")
	require.Equal(t, before[8]+1, after[8], "one result outbox event")
	readFinalSwissReceipt(ctx, t, fixture)
}

type receiptSettlementBarrier struct {
	*settlementrepo.ParticipantSettlementRepository
	ready   chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *receiptSettlementBarrier) CommitConcurrentWinnerSettlement(ctx context.Context, proposed gamesettlement.SettlementRecord) (*gamesettlement.SettlementRecord, bool, error) {
	r.once.Do(func() {
		r.ready <- struct{}{}
		select {
		case <-r.release:
		case <-ctx.Done():
		}
	})
	return r.ParticipantSettlementRepository.CommitConcurrentWinnerSettlement(ctx, proposed)
}

type receiptSettlementRepository struct {
	*settlementrepo.ParticipantSettlementRepository
	t *testing.T
}

func (r receiptSettlementRepository) CommitConcurrentWinnerSettlement(ctx context.Context, proposed gamesettlement.SettlementRecord) (*gamesettlement.SettlementRecord, bool, error) {
	record, changed, err := r.ParticipantSettlementRepository.CommitConcurrentWinnerSettlement(ctx, proposed)
	if err != nil {
		r.t.Logf("settlement commit: %v", err)
	}
	return record, changed, err
}

func nextSwissReceiptWave(ctx context.Context, t *testing.T, previous tournamentAdminSwissProofFixture, round int, noShow ...bool) tournamentAdminSwissProofFixture {
	t.Helper()
	fixture := previous
	fixture.roundID, fixture.waveID, fixture.windowID = uuid.New(), uuid.New(), uuid.New()
	fixture.projectionRevisionID, fixture.sourceProjectionRevision = currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	fixture.waveRevisionID = domain.WaveRevisionID(uuid.New())
	at := time.Now().UTC().Truncate(time.Microsecond)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO swiss_rounds (id, tournament_id, roster_id, round_number, source_roster_revision, source_history_revision,
		generation_kind, pairing_inputs, decision_evidence_id, decision_algorithm_version, decision_seed,
		decision_result, decision_replay_digest, decision_owner_id, generated_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 2, 0, 'automatic', '["receipt"]'::jsonb, $5, 'hmac-sha256-order-v1', $6,
		'["receipt"]'::jsonb, $7, $1, $8, $8, $8)`, fixture.roundID, fixture.tournamentID, fixture.rosterID, round, uuid.New(), bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), at)
	require.NoError(t, err)
	pairs := [][2]int{{0, 2}, {1, 3}}
	if round == 3 {
		pairs = [][2]int{{0, 3}, {1, 2}}
	}
	series := make([]waverepo.WaveSeriesInput, len(pairs))
	for i, pair := range pairs {
		series[i] = waverepo.WaveSeriesInput{ID: uuid.New(), FirstParticipantID: fixture.participants[pair[0]], SecondParticipantID: fixture.participants[pair[1]], Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New())}
	}
	waves := waverepo.NewWavePostgres(fixture.tx)
	wave, err := waves.Create(ctx, waverepo.WaveCreateInput{ID: fixture.waveID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, RevisionID: fixture.waveRevisionID,
		ParticipantIDs: fixture.participants, Series: series, CommandID: uuid.New(), SourceProjectionRevisionID: fixture.projectionRevisionID, SourceProjectionRevision: fixture.sourceProjectionRevision, CreatedAt: at})
	require.NoError(t, err)
	fixture.binding = make([]swissusecase.LockedSeries, len(series))
	for i, item := range series {
		pairingID := createSwissMigrationPairing(ctx, t, fixture.roundID, fixture.rosterID, i+1, uuid.Nil)
		addSwissMigrationPairingMember(ctx, t, pairingID, fixture.roundID, fixture.rosterID, 1, item.FirstParticipantID)
		addSwissMigrationPairingMember(ctx, t, pairingID, fixture.roundID, fixture.rosterID, 2, item.SecondParticipantID)
		fixture.binding[i] = createRoundProofSeriesBinding(ctx, t, fixture.tournamentID, fixture.rosterID, fixture.normalPoolRevisionID, item, pairingID, at)
	}
	_, err = sharedPool.Exec(ctx, `INSERT INTO swiss_wave_links (wave_id, tournament_id, roster_id, round_id, created_at) VALUES ($1, $2, $3, $4, $5)`, fixture.waveID, fixture.tournamentID, fixture.rosterID, fixture.roundID, at)
	require.NoError(t, err)
	openedAt := time.Now().UTC().Truncate(time.Microsecond)
	wave, changed, err := waves.OpenReadyWindow(ctx, fixture.tournamentID, fixture.waveID, wave.Revision, waverepo.ReadyWindowInput{ID: fixture.windowID, RevisionID: domain.ReadyWindowRevisionID(uuid.New()), OpenedAt: openedAt, Deadline: openedAt.Add(domain.ReadyWindowDuration)})
	require.NoError(t, err)
	require.True(t, changed)
	for _, participantID := range fixture.participants {
		if len(noShow) > 0 && noShow[0] && participantID == fixture.participants[2] ||
			len(noShow) > 1 && noShow[1] && participantID == fixture.participants[3] {
			continue
		}
		wave, changed, err = waves.MarkReady(ctx, fixture.tournamentID, fixture.waveID, fixture.windowID, participantID, wave.Revision, wave.ReadinessRevisions[participantID], openedAt.Add(time.Millisecond))
		require.NoError(t, err)
		require.True(t, changed)
	}
	fixture.waveRevision = wave.Revision
	return fixture
}
