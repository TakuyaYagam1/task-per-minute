//go:build integration

// Package correctionseed builds the prepared result and wave scope shared by
// correction repository integration tests. It accepts an explicit pool so
// capability-specific test packages can own their lifecycle and cleanup.
package correctionseed

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// Input identifies the already prepared result scope. Draft, assignment and
// initial result-audit construction remain owned by their respective testkit
// capabilities; this builder only adds the correction-specific graph.
type Input struct {
	ResultScope    resultrepo.ResultScope
	AssignmentID   uuid.UUID
	ParticipantIDs []uuid.UUID
	LockedAt       time.Time
}

// Scope contains the durable identities and records needed by correction
// repository flow tests and their future child package.
type Scope struct {
	ResultScope        resultrepo.ResultScope
	AssignmentID       uuid.UUID
	Participants       []uuid.UUID
	Result             *resultrepo.ResultCommitRecord
	Projection         *projectionrepo.ProjectionRecord
	WaveID             uuid.UUID
	WindowID           uuid.UUID
	WaveRevision       int64
	ReadinessRevisions map[uuid.UUID]int64
	Deadline           time.Time
	NextTime           time.Time
}

// Build extends an already locked result-audit scope with the additional
// participants, settled result, published projection, and ready wave used by
// correction repository tests. Each production adapter keeps its existing
// transaction and CAS boundaries.
func Build(ctx context.Context, pool *pgxpool.Pool, input Input) (Scope, error) {
	if pool == nil {
		return Scope{}, fmt.Errorf("correction seed: nil pool")
	}
	if len(input.ParticipantIDs) != 2 {
		return Scope{}, fmt.Errorf("correction seed: expected two base participants, got %d", len(input.ParticipantIDs))
	}
	if input.ResultScope.TournamentID == uuid.Nil || input.ResultScope.RosterID == uuid.Nil ||
		input.ResultScope.SeriesID == uuid.Nil || input.ResultScope.AttemptID == uuid.Nil ||
		input.AssignmentID == uuid.Nil || input.LockedAt.IsZero() {
		return Scope{}, fmt.Errorf("correction seed: invalid result scope")
	}

	participants := append([]uuid.UUID(nil), input.ParticipantIDs...)
	playerIDs, err := tournamentseed.CreatePlayers(ctx, pool, "tournament_migration", 2)
	if err != nil {
		return Scope{}, fmt.Errorf("correction seed: create players: %w", err)
	}
	for _, playerID := range playerIDs {
		var participantID uuid.UUID
		err = pool.QueryRow(ctx, `
			INSERT INTO participants (roster_id, player_id, seed, attendance)
			VALUES (
				$1,
				$2,
				(SELECT COALESCE(MAX(seed), 0) + 1 FROM participants WHERE roster_id = $1),
				'checked_in'
			)
			RETURNING id`, input.ResultScope.RosterID, playerID).Scan(&participantID)
		if err != nil {
			return Scope{}, fmt.Errorf("correction seed: create participant: %w", err)
		}
		participants = append(participants, participantID)
	}

	resultRepository := resultauthority.NewResultPostgres(postgres.NewTxManager(pool))
	winnerID := participants[0]
	createdAt := input.LockedAt.Add(time.Second)
	digest := sha256.Sum256([]byte("accepted correction fixture submission"))
	submission, changed, err := resultRepository.RecordSubmission(ctx, resultrepo.SubmissionInput{
		ID:           uuid.New(),
		Scope:        input.ResultScope,
		AssignmentID: input.AssignmentID, ParticipantID: winnerID,
		IdempotencyKey: uuid.New(), Status: "accepted", PayloadDigest: digest,
		IntentDigest: digest,
		SubmittedAt:  createdAt, ReceivedAt: createdAt, CreatedAt: createdAt,
		ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.GameStateActive,
	})
	if err != nil {
		return Scope{}, fmt.Errorf("correction seed: record submission: %w", err)
	}
	if !changed {
		return Scope{}, fmt.Errorf("correction seed: record submission did not change")
	}

	var seriesRevision int64
	err = pool.QueryRow(ctx, `SELECT revision FROM series WHERE id = $1`, input.ResultScope.SeriesID).
		Scan(&seriesRevision)
	if err != nil {
		return Scope{}, fmt.Errorf("correction seed: load series revision: %w", err)
	}

	settledAt := createdAt.Add(time.Second)
	projectionDigest := sha256.Sum256([]byte("initial correction fixture projection"))
	result, changed, err := resultRepository.Settle(ctx, resultrepo.ResultSettlementInput{
		IDs: resultrepo.ResultSettlementIDs{
			CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
			GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
			SeriesResultRevisionID: uuid.New(), AuditEventID: uuid.New(), OutboxEventID: uuid.New(),
			OutboxIdempotencyKey: uuid.New(), ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
		},
		Scope:             input.ResultScope,
		SubmissionEventID: submission.ID,
		GameState:         domain.GameStateCompleted, GameReason: domain.GameResultReasonSolved,
		GameWinnerID: &winnerID, Score: domain.SeriesScore{FirstParticipantWins: 1},
		NextSeriesState: domain.SeriesStateCompleted, SeriesResultReason: "score_complete",
		SeriesWinnerID: &winnerID, ActorKind: "server",
		ProjectionArtifactKinds: []domain.ArtifactKind{
			domain.ArtifactKindStandings, domain.ArtifactKindBracket,
			domain.ArtifactKindTopFour,
		},
		ProjectionPayloadDigest: projectionDigest, SettledAt: settledAt,
		ExpectedAttemptRevision: 1, ExpectedAttemptState: domain.GameStateActive,
		ExpectedSeriesRevision: seriesRevision, ExpectedSeriesState: domain.SeriesStateLocked,
	})
	if err != nil {
		return Scope{}, fmt.Errorf("correction seed: settle result: %w", err)
	}
	if !changed {
		return Scope{}, fmt.Errorf("correction seed: settle result did not change")
	}

	projectionRepository := projectionrepo.NewProjectionPostgres(postgres.NewTxManager(pool))
	projectionAt := settledAt.Add(time.Second)
	projection, err := projectionRepository.Publish(ctx, projectionrepo.ProjectionPublishInput{
		IDs: projectionrepo.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: projectionrepo.ProjectionScope{
			TournamentID: input.ResultScope.TournamentID, RosterID: input.ResultScope.RosterID,
		},
		Source: projectionrepo.ProjectionSource{
			Kind: "official_result", OfficialResultRevisionID: &result.GameRevision.ID,
			Reason: "initial result projection",
		},
		Artifacts: ProjectionArtifacts(
			participants, result.GameRevision.ID, input.ResultScope.SeriesID, "initial",
		),
		SupersessionReason: "replace initial projection", CutoffAt: projectionAt,
		CreatedAt: projectionAt, PublishedAt: projectionAt,
	})
	if err != nil {
		return Scope{}, fmt.Errorf("correction seed: publish projection: %w", err)
	}

	waveRepository := waverepo.NewWavePostgres(postgres.NewTxManager(pool))
	waveID := uuid.New()
	waveAt := projectionAt.Add(time.Second)
	_, err = waveRepository.Create(ctx, waverepo.WaveCreateInput{
		ID: waveID, TournamentID: input.ResultScope.TournamentID, RosterID: input.ResultScope.RosterID,
		RevisionID: domain.WaveRevisionID(uuid.New()), ParticipantIDs: participants,
		CommandID: uuid.New(), SourceProjectionRevisionID: projection.Revision.ID,
		SourceProjectionRevision: projection.Revision.RevisionNumber,
		Series: []waverepo.WaveSeriesInput{
			{
				ID: uuid.New(), FirstParticipantID: participants[0], SecondParticipantID: participants[1],
				Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
			},
			{
				ID: uuid.New(), FirstParticipantID: participants[2], SecondParticipantID: participants[3],
				Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
			},
		},
		CreatedAt: waveAt,
	})
	if err != nil {
		return Scope{}, fmt.Errorf("correction seed: create wave: %w", err)
	}

	windowID := uuid.New()
	openedAt := waveAt.Add(time.Second)
	deadline := openedAt.Add(30 * time.Second)
	wave, changed, err := waveRepository.OpenReadyWindow(
		ctx, input.ResultScope.TournamentID, waveID, 1,
		waverepo.ReadyWindowInput{
			ID: windowID, RevisionID: domain.ReadyWindowRevisionID(uuid.New()),
			OpenedAt: openedAt, Deadline: deadline,
		},
	)
	if err != nil {
		return Scope{}, fmt.Errorf("correction seed: open ready window: %w", err)
	}
	if !changed {
		return Scope{}, fmt.Errorf("correction seed: open ready window did not change")
	}
	wave, changed, err = waveRepository.MarkReady(
		ctx, input.ResultScope.TournamentID, waveID, windowID, participants[0],
		wave.Revision, wave.ReadinessRevisions[participants[0]], openedAt.Add(time.Second),
	)
	if err != nil {
		return Scope{}, fmt.Errorf("correction seed: mark ready: %w", err)
	}
	if !changed {
		return Scope{}, fmt.Errorf("correction seed: mark ready did not change")
	}

	return Scope{
		ResultScope: input.ResultScope, AssignmentID: input.AssignmentID,
		Participants: participants, Result: result, Projection: projection,
		WaveID: waveID, WindowID: windowID, WaveRevision: wave.Revision,
		ReadinessRevisions: wave.ReadinessRevisions,
		Deadline:           deadline, NextTime: openedAt.Add(2 * time.Second),
	}, nil
}
