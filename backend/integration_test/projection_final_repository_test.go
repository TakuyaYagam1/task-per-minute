//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestFinalProjectionRepositoryCommitsAuthoritativeResult(t *testing.T) {
	ctx := context.Background()
	publication, final, opponentID := createFinalPublicationFixture(t)
	tournamentRevision := publication.Expected.TournamentRevision
	repository := projectionrepo.NewProjectionPostgres(postgres.NewTxManager(sharedPool))

	stale := publication.Snapshot()
	stale.Expected.TournamentRevision--
	_, err := repository.PublishFinal(ctx, stale)
	var conflict *projection.FinalRevisionConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, tournamentRevision, conflict.CurrentTournamentRevision)
	require.Equal(t, publication.Expected.ProjectionRevision, conflict.CurrentProjectionRevision)
	assertFinalProjectionNotPersisted(ctx, t, stale, tournamentRevision)

	wrongWinner := publication.Snapshot()
	setFinalPublicationChampion(t, &wrongWinner, opponentID)
	require.NoError(t, wrongWinner.Validate())
	_, err = repository.PublishFinal(ctx, wrongWinner)
	require.ErrorIs(t, err, domain.ErrConflict)
	assertFinalProjectionNotPersisted(ctx, t, wrongWinner, tournamentRevision)

	staleGameHead := publication.Snapshot()
	staleGameHead.Expected.GameResultRevisionID = domain.OfficialResultRevisionID(uuid.New())
	require.NoError(t, staleGameHead.Validate())
	_, err = repository.PublishFinal(ctx, staleGameHead)
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, tournamentRevision, conflict.CurrentTournamentRevision)
	require.Equal(t, publication.Expected.ProjectionRevision, conflict.CurrentProjectionRevision)
	require.Equal(t, domain.OfficialResultRevisionID(final.GameRevision.ID), conflict.CurrentGameResultRevisionID)
	require.Equal(t, domain.SeriesScoreRevisionID(final.ScoreRevision.ID), conflict.CurrentScoreRevisionID)
	assertFinalProjectionNotPersisted(ctx, t, staleGameHead, tournamentRevision)

	staleScoreHead := publication.Snapshot()
	staleScoreHead.Expected.ScoreRevisionID = domain.SeriesScoreRevisionID(uuid.New())
	require.NoError(t, staleScoreHead.Validate())
	_, err = repository.PublishFinal(ctx, staleScoreHead)
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, tournamentRevision, conflict.CurrentTournamentRevision)
	require.Equal(t, publication.Expected.ProjectionRevision, conflict.CurrentProjectionRevision)
	require.Equal(t, domain.OfficialResultRevisionID(final.GameRevision.ID), conflict.CurrentGameResultRevisionID)
	require.Equal(t, domain.SeriesScoreRevisionID(final.ScoreRevision.ID), conflict.CurrentScoreRevisionID)
	assertFinalProjectionNotPersisted(ctx, t, staleScoreHead, tournamentRevision)

	abort := errors.New("stop after champion publication")
	tx := postgres.NewTxManager(sharedPool)
	err = tx.Do(ctx, func(txCtx context.Context) error {
		if _, err := projectionrepo.NewProjectionPostgres(tx).PublishFinal(txCtx, publication); err != nil {
			return err
		}
		return abort
	})
	require.ErrorIs(t, err, abort)
	assertFinalProjectionNotPersisted(ctx, t, publication, tournamentRevision)

	receipt, err := repository.PublishFinal(ctx, publication)
	require.NoError(t, err)
	require.True(t, receipt.Changed)
	require.Equal(t, publication.IDs.RevisionID, receipt.ProjectionRevisionID)
	require.EqualValues(t, final.Outbox.ProjectionRevision+1, receipt.ProjectionRevision)
	require.NotEqual(t, final.Outbox.ID, receipt.OutboxEventID)
	require.EqualValues(t, 1, receipt.OutboxOrdinal)
	require.Equal(t, tournamentRevision+1, receipt.TournamentRevision)

	assertFinalProjectionCommit(ctx, t, publication, final, receipt)

	replayed, err := repository.PublishFinal(ctx, publication)
	require.NoError(t, err)
	require.False(t, replayed.Changed)
	require.Equal(t, receipt.ProjectionRevisionID, replayed.ProjectionRevisionID)
	require.Equal(t, receipt.ProjectionRevision, replayed.ProjectionRevision)
	require.Equal(t, receipt.TournamentRevision, replayed.TournamentRevision)
	require.Equal(t, receipt.OutboxEventID, replayed.OutboxEventID)
	require.Equal(t, receipt.OutboxOrdinal, replayed.OutboxOrdinal)
}

func TestFinalProjectionConcurrentPublication(t *testing.T) {
	publication, final, _ := createFinalPublicationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	publications := [2]projection.FinalPublication{publication.Snapshot(), publication.Snapshot()}
	publications[1].IDs.RevisionID, publications[1].IDs.CutoffID = uuid.New(), uuid.New()
	var receipts [2]projection.FinalPublicationReceipt
	var failures [2]error
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	start := make(chan struct{})
	for i := range publications {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			receipts[i], failures[i] = projectionrepo.NewProjectionPostgres(postgres.NewTxManager(sharedPool)).PublishFinal(ctx, publications[i])
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	winner := -1
	for i, err := range failures {
		if err == nil {
			require.Equal(t, -1, winner)
			winner = i
			require.True(t, receipts[i].Changed)
		} else {
			var conflict *projection.FinalRevisionConflictError
			require.ErrorAs(t, err, &conflict)
			require.Equal(t, publication.Expected.ProjectionRevision+1, conflict.CurrentProjectionRevision)
			require.Equal(t, publication.Expected.GameResultRevisionID, conflict.CurrentGameResultRevisionID)
			require.Equal(t, publication.Expected.ScoreRevisionID, conflict.CurrentScoreRevisionID)
		}
	}
	require.NotEqual(t, -1, winner)
	assertFinalProjectionCommit(ctx, t, publications[winner], final, receipts[winner])
	var loserRows int
	require.NoError(t, sharedPool.QueryRow(ctx, "SELECT count(*) FROM projection_revisions WHERE id = $1", publications[1-winner].IDs.RevisionID).Scan(&loserRows))
	require.Zero(t, loserRows)
}

func createFinalPublicationFixture(t *testing.T) (projection.FinalPublication, *postgres.ResultCommitRecord, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	fixture, ids, coordinator := prepareActiveFinal(ctx, t)
	firstInput := activeFinalSettlementInput(ctx, t, fixture, ids, 1)
	err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
		if _, _, err := postgres.NewResultPostgres(fixture.tx).Settle(txCtx, firstInput); err != nil {
			return err
		}
		_, err := coordinator.AdvanceAfterSeriesSettlement(txCtx, playoff.TerminalSeriesCommand{TournamentID: fixture.tournamentID, SeriesID: ids.FinalSeriesID})
		return err
	})
	require.NoError(t, err)
	lastInput := activeFinalSettlementInput(ctx, t, fixture, ids, 2)
	final, changed, err := postgres.NewResultPostgres(fixture.tx).Settle(ctx, lastInput)
	require.NoError(t, err)
	require.True(t, changed)
	terminal := postgres.NewPlayoffTerminalPostgres(fixture.tx, postgres.NewDraftPostgres(fixture.tx), postgres.NewAssignmentPostgres(fixture.tx))
	authority, err := terminal.LoadFinalSettlement(ctx, playoff.TerminalSeriesCommand{TournamentID: fixture.tournamentID, SeriesID: ids.FinalSeriesID})
	require.NoError(t, err)
	require.NotNil(t, authority)
	require.NotNil(t, authority.Publication)
	var opponentID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, "SELECT second_participant_id FROM series WHERE id = $1", ids.FinalSeriesID).Scan(&opponentID))
	return authority.Publication.Snapshot(), final, opponentID
}

func setFinalPublicationChampion(
	t *testing.T,
	publication *projection.FinalPublication,
	winnerID uuid.UUID,
) {
	t.Helper()
	publication.Expected.WinnerID = winnerID
	for index := range publication.Artifacts {
		artifact := &publication.Artifacts[index]
		if artifact.Kind != domain.ArtifactKindChampion {
			continue
		}
		payload, err := json.Marshal(map[string]uuid.UUID{"participant_id": winnerID})
		require.NoError(t, err)
		artifact.Payload = payload
		artifact.PayloadDigest = sha256.Sum256(payload)
		artifact.Members = []projection.PublicationMember{{ParticipantID: winnerID, Position: 1}}
		return
	}
	require.Fail(t, "final publication must have a champion artifact")
}

func assertFinalProjectionNotPersisted(
	ctx context.Context,
	t *testing.T,
	publication projection.FinalPublication,
	expectedTournamentRevision int64,
) {
	t.Helper()

	var (
		partialWriteCounts [7]int
		tournamentState    string
		tournamentRev      int64
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM projection_cutoffs WHERE id = $2),
			(SELECT COUNT(*) FROM projection_revisions WHERE id = $1),
			(SELECT COUNT(*) FROM projection_artifacts WHERE produced_by_revision_id = $1),
			(SELECT COUNT(*) FROM projection_artifact_members AS member
				JOIN projection_artifacts AS artifact ON artifact.id = member.artifact_id
				WHERE artifact.produced_by_revision_id = $1),
			(SELECT COUNT(*) FROM projection_dependencies AS dependency
				JOIN projection_artifacts AS artifact ON artifact.id = dependency.artifact_id
				WHERE artifact.produced_by_revision_id = $1),
			(SELECT COUNT(*) FROM projection_revision_artifacts WHERE revision_id = $1),
			(SELECT COUNT(*) FROM outbox_events WHERE projection_revision_id = $1)
		`, publication.IDs.RevisionID, publication.IDs.CutoffID).Scan(
		&partialWriteCounts[0],
		&partialWriteCounts[1],
		&partialWriteCounts[2],
		&partialWriteCounts[3],
		&partialWriteCounts[4],
		&partialWriteCounts[5],
		&partialWriteCounts[6],
	))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, revision
		FROM tournaments
		WHERE id = $1`, publication.Scope.TournamentID).Scan(&tournamentState, &tournamentRev))
	require.Equal(t, [7]int{}, partialWriteCounts)
	require.Equal(t, string(domain.TournamentStatePlayoffs), tournamentState)
	require.Equal(t, expectedTournamentRevision, tournamentRev)
}

func initializeFinalProjectionSeries(
	ctx context.Context,
	tb testing.TB,
	fixture goldenMigrationFixture,
	seriesID uuid.UUID,
	startedAt time.Time,
) uuid.UUID {
	tb.Helper()

	initialScoreRevisionID := lockMigrationSeries(ctx, tb, draftMigrationFixture{
		tournamentID: fixture.tournamentID,
		rosterID:     fixture.rosterID,
		seriesID:     seriesID,
	}, startedAt)
	_, err := sharedPool.Exec(
		ctx, `
		UPDATE tournaments
		SET state = 'playoffs',
			revision = revision + 1,
			started_at = $2,
			updated_at = $2
		WHERE id = $1`,
		fixture.tournamentID,
		startedAt,
	)
	require.NoError(tb, err)
	return initialScoreRevisionID
}

func finalProjectionSettlementInput(
	fixture goldenMigrationFixture,
	seriesID uuid.UUID,
	attemptID uuid.UUID,
	winnerID uuid.UUID,
	operatorID uuid.UUID,
	score domain.SeriesScore,
	expectedSeriesState domain.SeriesState,
	nextSeriesState domain.SeriesState,
	expectedSeriesRevision int64,
	digest [sha256.Size]byte,
	settledAt time.Time,
) postgres.ResultSettlementInput {
	seriesResultRevisionID := uuid.Nil
	seriesResultReason := ""
	var seriesWinnerID *uuid.UUID
	artifactKinds := []domain.ArtifactKind{
		domain.ArtifactKindGameResult,
		domain.ArtifactKindSeriesScore,
	}
	if nextSeriesState == domain.SeriesStateCompleted {
		seriesResultRevisionID = uuid.New()
		seriesResultReason = string(domain.SeriesResultReasonScoreComplete)
		seriesWinnerID = &winnerID
		artifactKinds = []domain.ArtifactKind{
			domain.ArtifactKindGameResult,
			domain.ArtifactKindSeriesScore,
			domain.ArtifactKindStandings,
			domain.ArtifactKindSeriesResult,
		}
	}
	return postgres.ResultSettlementInput{
		IDs: postgres.ResultSettlementIDs{
			CommitID:                  uuid.New(),
			ResultEventID:             uuid.New(),
			ResultEventIdempotencyKey: uuid.New(),
			GameResultRevisionID:      uuid.New(),
			SeriesScoreRevisionID:     uuid.New(),
			SeriesResultRevisionID:    seriesResultRevisionID,
			AuditEventID:              uuid.New(),
			OutboxEventID:             uuid.New(),
			OutboxIdempotencyKey:      uuid.New(),
			ProjectionEvidenceID:      uuid.New(),
			CommitIdempotencyKey:      uuid.New(),
		},
		Scope: postgres.ResultScope{
			TournamentID: fixture.tournamentID,
			RosterID:     fixture.rosterID,
			SeriesID:     seriesID,
			AttemptID:    attemptID,
		},
		GameState:               domain.GameStateCompleted,
		GameReason:              domain.GameResultReasonOperatorForfeit,
		GameWinnerID:            &winnerID,
		Score:                   score,
		NextSeriesState:         nextSeriesState,
		SeriesResultReason:      seriesResultReason,
		SeriesWinnerID:          seriesWinnerID,
		ActorKind:               "operator",
		ActorID:                 &operatorID,
		ProjectionArtifactKinds: artifactKinds,
		ProjectionPayloadDigest: digest,
		SettledAt:               settledAt,
		ExpectedAttemptRevision: 1,
		ExpectedAttemptState:    domain.GameStateActive,
		ExpectedSeriesRevision:  expectedSeriesRevision,
		ExpectedSeriesState:     expectedSeriesState,
	}
}

func finalProjectionPublication(
	t *testing.T,
	fixture goldenMigrationFixture,
	seriesID uuid.UUID,
	attemptID uuid.UUID,
	winnerID uuid.UUID,
	result *postgres.ResultCommitRecord,
	digest [sha256.Size]byte,
	tournamentRevision int64,
	seriesRevision int64,
	attemptRevision int64,
	projectionRevision int64,
	createdAt time.Time,
) projection.FinalPublication {
	t.Helper()
	require.NotNil(t, result.SeriesRevision)

	standingsID := uuid.New()
	bracketID := uuid.New()
	topFourID := uuid.New()
	standingsMembers := make([]projection.PublicationMember, len(fixture.participantIDs))
	positionMembers := make([]projection.PublicationMember, len(fixture.participantIDs))
	entries := make([]map[string]any, len(fixture.participantIDs))
	for index, participantID := range fixture.participantIDs {
		score := int64((len(fixture.participantIDs) - index) * 1000)
		position := int32(index + 1)
		standingsMembers[index] = projection.PublicationMember{
			ParticipantID: participantID,
			Position:      position,
			ScoreMilli:    &score,
		}
		positionMembers[index] = projection.PublicationMember{
			ParticipantID: participantID,
			Position:      position,
		}
		entries[index] = map[string]any{
			"participant_id": participantID,
			"position":       position,
		}
	}
	resultRevisionID := result.SeriesRevision.ID
	officialResultDependency := func() projection.PublicationDependency {
		return projection.PublicationDependency{
			ID:                       uuid.New(),
			Kind:                     projection.DependencyOfficialResult,
			OfficialResultRevisionID: &resultRevisionID,
			OfficialResultSeriesID:   &seriesID,
		}
	}
	standings := finalPublicationArtifact(
		t,
		standingsID,
		domain.ArtifactKindStandings,
		map[string]any{"entries": entries},
		standingsMembers,
		[]projection.PublicationDependency{officialResultDependency()},
	)
	bracket := finalPublicationArtifact(
		t,
		bracketID,
		domain.ArtifactKindBracket,
		map[string]any{"rounds": []map[string]any{{"series_id": seriesID}}},
		positionMembers,
		[]projection.PublicationDependency{officialResultDependency()},
	)
	topFour := finalPublicationArtifact(
		t,
		topFourID,
		domain.ArtifactKindTopFour,
		map[string]any{"participants": fixture.participantIDs},
		positionMembers,
		[]projection.PublicationDependency{{
			ID:                  uuid.New(),
			Kind:                projection.DependencyArtifact,
			DependsOnArtifactID: &bracketID,
		}},
	)
	champion := finalPublicationArtifact(
		t,
		uuid.New(),
		domain.ArtifactKindChampion,
		map[string]any{"participant_id": winnerID},
		[]projection.PublicationMember{{ParticipantID: winnerID, Position: 1}},
		[]projection.PublicationDependency{
			{
				ID:                  uuid.New(),
				Kind:                projection.DependencyArtifact,
				DependsOnArtifactID: &bracketID,
			},
			officialResultDependency(),
		},
	)
	return projection.FinalPublication{
		IDs: projection.PublicationIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: projection.FinalScope{
			TournamentID:  fixture.tournamentID,
			RosterID:      fixture.rosterID,
			SeriesID:      seriesID,
			GameAttemptID: attemptID,
		},
		Expected: projection.FinalHeadExpectation{
			TournamentRevision:     tournamentRevision,
			ProjectionRevision:     projectionRevision,
			GameAttemptRevision:    attemptRevision,
			GameResultRevisionID:   domain.OfficialResultRevisionID(result.GameRevision.ID),
			SeriesRevision:         seriesRevision,
			ScoreHeadRevision:      result.ScoreRevision.RevisionNumber,
			ScoreRevisionID:        domain.SeriesScoreRevisionID(result.ScoreRevision.ID),
			SeriesResultRevisionID: domain.OfficialResultRevisionID(result.SeriesRevision.ID),
			WinnerID:               winnerID,
		},
		Artifacts:              []projection.PublicationArtifact{standings, bracket, topFour, champion},
		ResultProjectionDigest: digest,
		Reason:                 "final result changed the tournament projection",
		SupersessionReason:     "replaced by the terminal tournament projection",
		CutoffAt:               createdAt,
		CreatedAt:              createdAt,
		PublishedAt:            createdAt.Add(time.Second),
	}
}

func finalPublicationArtifact(
	t *testing.T,
	id uuid.UUID,
	kind domain.ArtifactKind,
	payload any,
	members []projection.PublicationMember,
	dependencies []projection.PublicationDependency,
) projection.PublicationArtifact {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return projection.PublicationArtifact{
		ID:            id,
		Kind:          kind,
		Key:           string(kind) + "-final",
		Payload:       raw,
		PayloadDigest: sha256.Sum256(raw),
		Members:       members,
		Dependencies:  dependencies,
	}
}

func assertFinalProjectionCommit(
	ctx context.Context,
	t *testing.T,
	publication projection.FinalPublication,
	result *postgres.ResultCommitRecord,
	receipt projection.FinalPublicationReceipt,
) {
	t.Helper()

	var (
		tournamentState          string
		projectionState          string
		resultProjectionID       uuid.UUID
		resultProjectionRevision int64
		resultProjectionOrdinal  int16
		resultSourceID           uuid.UUID
		resultSourceRevision     int64
		resultSourceOrdinal      int16
		championProjectionID     uuid.UUID
		championProjectionRev    int64
		championOrdinal          int16
		championSourceID         uuid.UUID
		championSourceRevision   int64
		championSourceOrdinal    int16
		championResultRevisionID uuid.UUID
		championArtifactID       uuid.UUID
		championTerminal         bool
		championTopic            string
		artifactCount            int
		resultDependencyCount    int
		boundOutboxCount         int
		outboxOrdinals           []int16
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state
		FROM tournaments
		WHERE id = $1`, publication.Scope.TournamentID).Scan(&tournamentState))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state
		FROM projection_revisions
		WHERE id = $1`, publication.IDs.RevisionID).Scan(&projectionState))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT event.projection_revision_id,
			event.projection_revision,
			event.projection_ordinal,
			source.projection_revision_id,
			source.projection_revision,
			source.projection_ordinal
		FROM outbox_events AS event
		INNER JOIN outbox_result_sources AS source
			ON source.outbox_event_id = event.id
		WHERE event.id = $1`, result.Outbox.ID).Scan(
		&resultProjectionID,
		&resultProjectionRevision,
		&resultProjectionOrdinal,
		&resultSourceID,
		&resultSourceRevision,
		&resultSourceOrdinal,
	))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT event.projection_revision_id,
			event.projection_revision,
			event.projection_ordinal,
			source.projection_revision_id,
			source.projection_revision,
			source.projection_ordinal,
			source.final_result_revision_id,
			source.champion_artifact_id,
			event.terminal,
			event.topic
		FROM outbox_events AS event
		INNER JOIN outbox_champion_sources AS source
			ON source.outbox_event_id = event.id
		WHERE event.id = $1`, receipt.OutboxEventID).Scan(
		&championProjectionID,
		&championProjectionRev,
		&championOrdinal,
		&championSourceID,
		&championSourceRevision,
		&championSourceOrdinal,
		&championResultRevisionID,
		&championArtifactID,
		&championTerminal,
		&championTopic,
	))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM projection_revision_artifacts
		WHERE revision_id = $1`, publication.IDs.RevisionID).Scan(&artifactCount))
	require.NoError(t, sharedPool.QueryRow(
		ctx, `
		SELECT COUNT(*)
		FROM projection_dependencies
		WHERE tournament_id = $1
			AND roster_id = $2
			AND official_result_revision_id = $3`,
		publication.Scope.TournamentID,
		publication.Scope.RosterID,
		publication.Expected.SeriesResultRevisionID.UUID(),
	).Scan(&resultDependencyCount))
	require.NoError(t, sharedPool.QueryRow(
		ctx, `
		SELECT COUNT(*) FILTER (
			WHERE projection_revision_id = $2
				AND projection_ordinal IS NOT NULL
		), ARRAY_AGG(projection_ordinal ORDER BY sequence) FILTER (
			WHERE projection_revision_id = $2
				AND projection_ordinal IS NOT NULL
		)
		FROM outbox_events
		WHERE tournament_id = $1
			AND roster_id = $3`,
		publication.Scope.TournamentID,
		publication.IDs.RevisionID,
		publication.Scope.RosterID,
	).Scan(&boundOutboxCount, &outboxOrdinals))

	require.Equal(t, string(domain.TournamentStateCompleted), tournamentState)
	require.Equal(t, "published", projectionState)
	require.Equal(t, result.ProjectionEvidence.ID, resultProjectionID)
	require.Equal(t, result.Outbox.ProjectionRevision, resultProjectionRevision)
	require.Equal(t, result.Outbox.ProjectionOrdinal, resultProjectionOrdinal)
	require.Equal(t, resultProjectionID, resultSourceID)
	require.Equal(t, resultProjectionRevision, resultSourceRevision)
	require.Equal(t, resultProjectionOrdinal, resultSourceOrdinal)
	require.Equal(t, receipt.ProjectionRevisionID, championProjectionID)
	require.Equal(t, receipt.ProjectionRevision, championProjectionRev)
	require.Equal(t, receipt.OutboxOrdinal, championOrdinal)
	require.Equal(t, championProjectionID, championSourceID)
	require.Equal(t, championProjectionRev, championSourceRevision)
	require.Equal(t, championOrdinal, championSourceOrdinal)
	require.Equal(t, publication.Expected.SeriesResultRevisionID.UUID(), championResultRevisionID)
	require.Equal(t, finalChampionArtifact(t, publication), championArtifactID)
	require.True(t, championTerminal)
	require.Equal(t, "tournament.champion.published", championTopic)
	require.Equal(t, 4, artifactCount)
	require.Equal(t, 2, resultDependencyCount)
	require.Equal(t, 1, boundOutboxCount)
	require.Equal(t, []int16{receipt.OutboxOrdinal}, outboxOrdinals)
}

func finalChampionArtifact(t testing.TB, publication projection.FinalPublication) uuid.UUID {
	t.Helper()
	for _, artifact := range publication.Artifacts {
		if artifact.Kind == domain.ArtifactKindChampion {
			return artifact.ID
		}
	}
	require.Fail(t, "final publication must contain a champion artifact")
	return uuid.Nil
}
