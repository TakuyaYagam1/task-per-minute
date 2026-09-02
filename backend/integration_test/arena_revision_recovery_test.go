//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func TestArenaRevisionRecovery(t *testing.T) {
	t.Run("restart restores Golden attempt and recovery history", func(t *testing.T) {
		ctx := context.Background()
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaRevisionGoldenFixture(t, ctx, realIntegrationClock())
		restarted := postgres.NewArenaGoldenPostgres(postgres.NewTxManager(sharedPool))

		history, err := restarted.History(ctx, fixture.migration.tournamentID, fixture.migration.rosterID)
		require.NoError(t, err)
		require.Len(t, history, 2)
		require.Equal(t, fixture.completedScope.AttemptID, history[0].ID)
		require.Equal(t, fixture.waitingScope.AttemptID, history[1].ID)
		require.True(t, history[1].PreviousAttemptID.Valid)
		require.Equal(t, fixture.completedScope.AttemptID, history[1].PreviousAttemptID.UUID)

		completed, err := restarted.Get(ctx, fixture.completedScope)
		require.NoError(t, err)
		waiting, err := restarted.Get(ctx, fixture.waitingScope)
		require.NoError(t, err)
		require.Len(t, completed.Submissions, 4)
		require.Len(t, completed.Positions, 4)
		require.Len(t, completed.Recoveries, 4)
		recoveryStates := make([]string, len(completed.Recoveries))
		for index, revision := range completed.Recoveries {
			recoveryStates[index] = revision.State
			require.EqualValues(t, index+1, revision.RevisionNumber)
			if index == 0 {
				require.False(t, revision.PreviousRevisionID.Valid)
				continue
			}
			require.True(t, revision.PreviousRevisionID.Valid)
			require.Equal(t, completed.Recoveries[index-1].ID, revision.PreviousRevisionID.UUID)
		}
		require.Equal(t, []string{"stable", "technical_pause", "recovering", "resumed"}, recoveryStates)
		require.Len(t, waiting.Recoveries, 1)
		readyDeadline := arenaRevisionReadyDeadline(t, waiting)

		groupID := uuid.New()
		groupRevisionID := domain.ArenaDerivedRevisionID(uuid.New())
		recovered, err := recovery.RecoverGoldenGroup(recovery.GoldenRecoveryInput{
			Group: recovery.GoldenRecoveryGroup{
				ID: groupID, TournamentID: fixture.migration.tournamentID,
				RevisionID: groupRevisionID, SourceProjectionRevisionID: domain.ArenaDerivedRevisionID(uuid.New()),
				PositionFrom: 1, PositionTo: 4,
				ParticipantIDs: append([]uuid.UUID(nil), fixture.migration.participantIDs...),
			},
			Attempts: []recovery.GoldenRecoveryAttempt{
				arenaRevisionGoldenAttempt(t, groupID, groupRevisionID, completed, nil),
				arenaRevisionGoldenAttempt(t, groupID, groupRevisionID, waiting, &readyDeadline),
			},
		})
		require.NoError(t, err)
		require.Len(t, recovered.Group.Snapshot().Attempts, 2)
		require.Len(t, recovered.CommittedPriorAttempts, 1)
		require.Equal(t, fixture.completedScope.AttemptID, recovered.CommittedPriorAttempts[0].AttemptID)
		require.Len(t, recovered.CommittedPriorAttempts[0].Positions, 4)
		require.NotNil(t, recovered.LiveAttempt)
		require.Equal(t, fixture.waitingScope.AttemptID, recovered.LiveAttempt.AttemptID)
		require.Equal(t, readyDeadline, recovered.LiveAttempt.Deadlines.Ready)
	})

	t.Run("late correction failure leaves the committed revision graph intact", func(t *testing.T) {
		ctx := context.Background()
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaCorrectionRepositoryFixture(t, ctx)
		corrections := postgres.NewArenaCorrectionPostgres(postgres.NewTxManager(sharedPool))
		input := newArenaCorrectionInput(t, ctx, fixture, fixture.result, fixture.projection, 1, fixture.nextTime)
		corrected, err := corrections.Rebuild(ctx, input)
		require.NoError(t, err)
		require.EqualValues(t, 2, corrected.ResultCommit.GameRevision.RevisionNumber)
		require.True(t, corrected.ResultCommit.GameRevision.PreviousRevisionID.Valid)
		require.Equal(t, fixture.result.GameRevision.ID, corrected.ResultCommit.GameRevision.PreviousRevisionID.UUID)
		require.EqualValues(t, 3, corrected.ResultCommit.ScoreRevision.RevisionNumber)
		require.True(t, corrected.ResultCommit.ScoreRevision.PreviousRevisionID.Valid)
		require.Equal(t, fixture.result.ScoreRevision.ID, corrected.ResultCommit.ScoreRevision.PreviousRevisionID.UUID)
		require.NotNil(t, corrected.ResultCommit.SeriesRevision)
		require.EqualValues(t, 2, corrected.ResultCommit.SeriesRevision.RevisionNumber)
		require.True(t, corrected.Projection.Revision.PreviousRevisionID.Valid)
		require.Equal(t, fixture.projection.Revision.ID, corrected.Projection.Revision.PreviousRevisionID.UUID)

		before := loadArenaRevisionHeads(t, ctx, fixture)
		failedInput := newArenaCorrectionInput(
			t,
			ctx,
			fixture,
			corrected.ResultCommit,
			corrected.Projection,
			0,
			fixture.nextTime.Add(time.Second),
		)
		failedInput.ProjectionArtifacts[0].Dependencies = append(
			failedInput.ProjectionArtifacts[0].Dependencies,
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact",
				DependsOnArtifactID: &failedInput.ProjectionArtifacts[0].ID,
			},
		)
		_, err = corrections.Rebuild(ctx, failedInput)
		require.ErrorIs(t, err, domain.ErrConflict)

		after := loadArenaRevisionHeads(t, ctx, fixture)
		require.Equal(t, before, after)

		restartedResults := postgres.NewArenaResultPostgres(postgres.NewTxManager(sharedPool))
		restartedProjections := postgres.NewArenaProjectionPostgres(postgres.NewTxManager(sharedPool))
		scope := arenaRevisionResultScope(fixture)
		currentResult, err := restartedResults.Current(ctx, scope)
		require.NoError(t, err)
		require.Equal(t, corrected.ResultCommit.GameRevision.ID, currentResult.GameRevision.ID)
		resultHistory, err := restartedResults.History(ctx, scope)
		require.NoError(t, err)
		require.Len(t, resultHistory, 2)
		require.EqualValues(t, []int64{1, 2}, []int64{
			resultHistory[0].GameRevisionNumber,
			resultHistory[1].GameRevisionNumber,
		})
		require.EqualValues(t, []int64{2, 3}, []int64{
			resultHistory[0].ScoreRevisionNumber,
			resultHistory[1].ScoreRevisionNumber,
		})
		currentProjection, err := restartedProjections.Current(ctx, arenaRevisionProjectionScope(fixture))
		require.NoError(t, err)
		require.Equal(t, corrected.Projection.Revision.ID, currentProjection.Revision.ID)
		projectionHistory, err := restartedProjections.History(ctx, arenaRevisionProjectionScope(fixture))
		require.NoError(t, err)
		require.Len(t, projectionHistory, 2)
		require.Equal(t, "superseded", projectionHistory[0].State)
		require.Equal(t, "published", projectionHistory[1].State)
	})

	t.Run("outbox retry converges without another terminal outcome", func(t *testing.T) {
		ctx := context.Background()
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaCorrectionRepositoryFixture(t, ctx)
		corrections := postgres.NewArenaCorrectionPostgres(postgres.NewTxManager(sharedPool))
		input := newArenaCorrectionInput(t, ctx, fixture, fixture.result, fixture.projection, 1, fixture.nextTime)
		corrected, err := corrections.Rebuild(ctx, input)
		require.NoError(t, err)
		before := loadArenaRevisionHeads(t, ctx, fixture)

		firstManager := postgres.NewTxManager(sharedPool)
		sink := &arenaRevisionProjectionSink{
			targetProjectionID: corrected.Projection.Revision.ID,
			failures:           1,
		}
		firstPublisher := postgres.NewArenaOutboxPublisher(firstManager, sink)
		published, err := firstPublisher.PublishNext(ctx)
		require.ErrorIs(t, err, errArenaRevisionSinkUnavailable)
		require.False(t, published)
		require.Equal(t, 2, countArenaRevisionUnpublishedOutbox(t, ctx, fixture))

		restartedManager := postgres.NewTxManager(sharedPool)
		restartedPublisher := postgres.NewArenaOutboxPublisher(restartedManager, sink)
		for range 2 {
			published, err = restartedPublisher.PublishNext(ctx)
			require.NoError(t, err)
			require.True(t, published)
		}
		published, err = restartedPublisher.PublishNext(ctx)
		require.NoError(t, err)
		require.False(t, published)

		events, projectionIDs := sink.snapshot()
		require.Len(t, events, 3)
		require.Equal(t, events[0].ID, events[1].ID)
		require.Equal(t, events[0].ResultEventID, events[1].ResultEventID)
		require.Equal(t, events[0].Sequence, events[1].Sequence)
		require.NotEqual(t, events[1].ID, events[2].ID)
		require.EqualValues(t, []int64{1, 1, 2}, []int64{
			events[0].ProjectionRevision,
			events[1].ProjectionRevision,
			events[2].ProjectionRevision,
		})
		require.Len(t, projectionIDs, 3)
		for _, projectionID := range projectionIDs {
			require.Equal(t, corrected.Projection.Revision.ID, projectionID)
		}
		require.Zero(t, countArenaRevisionUnpublishedOutbox(t, ctx, fixture))
		require.Equal(t, before, loadArenaRevisionHeads(t, ctx, fixture))
		restartedProjection, err := postgres.NewArenaProjectionPostgres(restartedManager).Current(
			ctx,
			arenaRevisionProjectionScope(fixture),
		)
		require.NoError(t, err)
		require.Equal(t, corrected.Projection.Revision.ID, restartedProjection.Revision.ID)
	})
}

type arenaRevisionGoldenFixture struct {
	migration      arenaGoldenMigrationFixture
	completedScope postgres.ArenaGoldenScope
	waitingScope   postgres.ArenaGoldenScope
}

func createArenaRevisionGoldenFixture(
	t testing.TB,
	ctx context.Context,
	clock integrationClockFunc,
) arenaRevisionGoldenFixture {
	t.Helper()

	migration := createArenaGoldenMigrationFixture(t, ctx, 4)
	repository := postgres.NewArenaGoldenPostgres(postgres.NewTxManager(sharedPool))
	createdAt := clock.Now().Add(-5 * time.Minute).Truncate(time.Microsecond)
	completedScope := postgres.ArenaGoldenScope{
		TournamentID: migration.tournamentID,
		RosterID:     migration.rosterID,
		AttemptID:    uuid.New(),
	}
	membershipIDs := make([]uuid.UUID, len(migration.participantIDs))
	memberships := make([]postgres.ArenaGoldenMembershipInput, len(migration.participantIDs))
	for index, participantID := range migration.participantIDs {
		membershipIDs[index] = uuid.New()
		memberships[index] = postgres.ArenaGoldenMembershipInput{
			ID: membershipIDs[index], ParticipantID: participantID,
			SelectionKind: "direct", SelectedAt: createdAt,
		}
	}
	_, err := repository.CreateAttempt(ctx, postgres.ArenaGoldenCreateAttemptInput{
		Scope: completedScope, AttemptNumber: 1, Memberships: memberships, CreatedAt: createdAt,
	})
	require.NoError(t, err)

	readyAt := createdAt.Add(time.Second)
	for _, membershipID := range membershipIDs {
		_, err = repository.MarkReady(ctx, postgres.ArenaGoldenMemberEventInput{
			Scope: completedScope, MembershipID: membershipID, OccurredAt: readyAt,
		})
		require.NoError(t, err)
	}
	disclosedAt := createdAt.Add(2 * time.Second)
	readyStateAt := createdAt.Add(3 * time.Second)
	_, changed, err := repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
		Scope: completedScope, ExpectedState: "prepared", NextState: "ready",
		DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	participationAt := createdAt.Add(4 * time.Second)
	for _, membershipID := range membershipIDs {
		_, err = repository.EstablishParticipation(ctx, postgres.ArenaGoldenMemberEventInput{
			Scope: completedScope, MembershipID: membershipID, OccurredAt: participationAt,
		})
		require.NoError(t, err)
	}
	startedAt := createdAt.Add(5 * time.Second)
	_, changed, err = repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
		Scope: completedScope, ExpectedState: "ready", NextState: "active",
		DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt, StartedAt: &startedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)

	recoveryStates := []string{"stable", "technical_pause", "recovering", "resumed"}
	for index, state := range recoveryStates {
		recordedAt := createdAt.Add(time.Duration(6+index) * time.Second)
		_, err = repository.RecordRecovery(ctx, postgres.ArenaGoldenRecoveryInput{
			ID: uuid.New(), Scope: completedScope, State: state,
			RecoveryEvidence: json.RawMessage(`{"source":"restart integration"}`),
			RecordedAt:       recordedAt, CreatedAt: recordedAt,
		})
		require.NoError(t, err)
		if state == "technical_pause" {
			_, changed, err = repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
				Scope: completedScope, ExpectedState: "active", NextState: "technical_pause",
				DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt,
				StartedAt: &startedAt, PausedAt: &recordedAt,
			})
			require.NoError(t, err)
			require.True(t, changed)
		}
		if state == "resumed" {
			_, changed, err = repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
				Scope: completedScope, ExpectedState: "technical_pause", NextState: "active",
				DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt, StartedAt: &startedAt,
			})
			require.NoError(t, err)
			require.True(t, changed)
		}
	}

	for index, participantID := range migration.participantIDs {
		submittedAt := createdAt.Add(time.Duration(11+index) * time.Second)
		submission := arenaGoldenRepositorySubmission(
			completedScope,
			membershipIDs[index],
			participantID,
			int64(index+1),
			int16(index+1),
			submittedAt,
		)
		stored, changed, err := repository.RecordSubmission(ctx, submission)
		require.NoError(t, err)
		require.True(t, changed)
		_, err = repository.CommitPosition(ctx, postgres.ArenaGoldenPositionInput{
			ID: uuid.New(), Scope: completedScope, MembershipID: membershipIDs[index],
			ParticipantID: participantID, ProvisionalSubmissionID: stored.ID,
			Position:    int16(index + 1),
			CommittedAt: submittedAt.Add(time.Second), CreatedAt: submittedAt.Add(time.Second),
		})
		require.NoError(t, err)
	}
	completedAt := createdAt.Add(20 * time.Second)
	_, changed, err = repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
		Scope: completedScope, ExpectedState: "active", NextState: "completed",
		DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt,
		StartedAt: &startedAt, CompletedAt: &completedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)

	waitingCreatedAt := createdAt.Add(time.Minute)
	waitingScope := completedScope
	waitingScope.AttemptID = uuid.New()
	waitingMemberships := make([]postgres.ArenaGoldenMembershipInput, len(migration.participantIDs))
	for index, participantID := range migration.participantIDs {
		waitingMemberships[index] = postgres.ArenaGoldenMembershipInput{
			ID: uuid.New(), ParticipantID: participantID,
			SelectionKind: "direct", SelectedAt: waitingCreatedAt,
		}
	}
	_, err = repository.CreateAttempt(ctx, postgres.ArenaGoldenCreateAttemptInput{
		Scope: waitingScope, AttemptNumber: 2, PreviousAttemptID: &completedScope.AttemptID,
		Memberships: waitingMemberships, CreatedAt: waitingCreatedAt,
	})
	require.NoError(t, err)
	readyDeadline := waitingCreatedAt.Add(30 * time.Second)
	evidence, err := json.Marshal(struct {
		ReadyDeadline time.Time `json:"ready_deadline"`
	}{ReadyDeadline: readyDeadline})
	require.NoError(t, err)
	_, err = repository.RecordRecovery(ctx, postgres.ArenaGoldenRecoveryInput{
		ID: uuid.New(), Scope: waitingScope, State: "stable",
		RecoveryEvidence: evidence,
		RecordedAt:       waitingCreatedAt.Add(time.Second),
		CreatedAt:        waitingCreatedAt.Add(time.Second),
	})
	require.NoError(t, err)

	return arenaRevisionGoldenFixture{
		migration: migration, completedScope: completedScope, waitingScope: waitingScope,
	}
}

func arenaRevisionReadyDeadline(t testing.TB, record *postgres.ArenaGoldenRecord) time.Time {
	t.Helper()
	require.NotEmpty(t, record.Recoveries)
	var evidence struct {
		ReadyDeadline time.Time `json:"ready_deadline"`
	}
	require.NoError(t, json.Unmarshal(record.Recoveries[len(record.Recoveries)-1].RecoveryEvidence, &evidence))
	require.False(t, evidence.ReadyDeadline.IsZero())
	return evidence.ReadyDeadline.UTC()
}

func arenaRevisionGoldenAttempt(
	t testing.TB,
	groupID uuid.UUID,
	groupRevisionID domain.ArenaDerivedRevisionID,
	record *postgres.ArenaGoldenRecord,
	readyDeadline *time.Time,
) recovery.GoldenRecoveryAttempt {
	t.Helper()

	state := domain.ArenaGoldenAttemptStatePlanned
	finishedAt := (*time.Time)(nil)
	switch record.Attempt.State {
	case "completed":
		state = domain.ArenaGoldenAttemptStateCompleted
		finishedAt = arenaRevisionTime(record.Attempt.CompletedAt)
	case "prepared":
		state = domain.ArenaGoldenAttemptStatePlanned
	default:
		t.Fatalf("unsupported persisted Golden state %q", record.Attempt.State)
	}
	participants := make([]uuid.UUID, 0, len(record.Memberships))
	memberships := make([]recovery.GoldenRecoveryMembership, 0, len(record.Memberships))
	for _, membership := range record.Memberships {
		if membership.SelectionKind != "excluded" {
			participants = append(participants, membership.ParticipantID)
		}
		memberships = append(memberships, recovery.GoldenRecoveryMembership{
			ID: membership.ID, ParticipantID: membership.ParticipantID,
			Selection:                  recovery.GoldenRecoverySelection(membership.SelectionKind),
			SelectedAt:                 membership.SelectedAt.Time.UTC(),
			ReadyAt:                    arenaRevisionTime(membership.ReadyAt),
			ParticipationEstablishedAt: arenaRevisionTime(membership.ParticipationEstablishedAt),
			ExcludedAt:                 arenaRevisionTime(membership.ExcludedAt),
		})
	}
	submissions := make([]recovery.GoldenRecoverySubmission, 0, len(record.Submissions))
	for _, submission := range record.Submissions {
		if submission.Status != "accepted" {
			continue
		}
		submissions = append(submissions, recovery.GoldenRecoverySubmission{
			ID: submission.ID, MembershipID: submission.MembershipID,
			ParticipantID: submission.ParticipantID, ServerSequence: submission.ServerSequence,
			Position: int(submission.ProvisionalPosition), AcceptedAt: submission.ReceivedAt.Time.UTC(),
		})
	}
	positions := make([]recovery.GoldenRecoveryPositionCommit, 0, len(record.Positions))
	for _, position := range record.Positions {
		positions = append(positions, recovery.GoldenRecoveryPositionCommit{
			ID: position.ID, AttemptID: position.AttemptID,
			SubmissionID: position.ProvisionalSubmissionID, ParticipantID: position.ParticipantID,
			Position: int(position.Position), CommittedAt: position.CommittedAt.Time.UTC(),
		})
	}
	var previousAttemptID *uuid.UUID
	if record.Attempt.PreviousAttemptID.Valid {
		previous := record.Attempt.PreviousAttemptID.UUID
		previousAttemptID = &previous
	}
	return recovery.GoldenRecoveryAttempt{
		Attempt: domain.ArenaGoldenAttempt{
			ID: record.Attempt.ID, GroupID: groupID, GroupRevisionID: groupRevisionID,
			AttemptNo: int(record.Attempt.AttemptNumber), PreviousAttemptID: previousAttemptID,
			State: state, ParticipantIDs: participants,
			StartedAt: arenaRevisionTime(record.Attempt.StartedAt), FinishedAt: finishedAt,
		},
		Memberships: memberships, Submissions: submissions, PositionCommits: positions,
		ReadyDeadline: readyDeadline,
	}
}

func arenaRevisionTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}

type arenaRevisionHeadSnapshot struct {
	resultEvents        int
	resultCommits       int
	gameRevisions       int
	scoreRevisions      int
	seriesRevisions     int
	projectionRevisions int
	projectionCutoffs   int
	projectionArtifacts int
	projectionLinks     int
	projectionMembers   int
	projectionEdges     int
	auditEvents         int
	projectionEvidence  int
	outboxEvents        int
	waveState           string
	readyWindowState    string
	readinessRecords    int
	gameHead            uuid.UUID
	scoreHead           uuid.UUID
	seriesHead          uuid.UUID
	projectionHead      uuid.UUID
}

func loadArenaRevisionHeads(
	t testing.TB,
	ctx context.Context,
	fixture arenaCorrectionRepositoryFixture,
) arenaRevisionHeadSnapshot {
	t.Helper()

	var snapshot arenaRevisionHeadSnapshot
	err := sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM arena_result_events WHERE attempt_id = $1),
			(SELECT COUNT(*) FROM arena_result_commits WHERE attempt_id = $1),
			(SELECT COUNT(*) FROM arena_official_result_revisions WHERE entity_kind = 'game_attempt' AND entity_id = $1),
			(SELECT COUNT(*) FROM arena_series_score_revisions WHERE series_id = $2),
			(SELECT COUNT(*) FROM arena_official_result_revisions WHERE entity_kind = 'series' AND entity_id = $2),
			(SELECT COUNT(*) FROM arena_projection_revisions WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM arena_projection_cutoffs WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM arena_projection_artifacts WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM arena_projection_revision_artifacts WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM arena_projection_artifact_members WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM arena_projection_dependencies WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM arena_audit_events WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM arena_result_projection_evidence WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT COUNT(*) FROM arena_outbox_events WHERE tournament_id = $3 AND roster_id = $4),
			(SELECT state FROM arena_waves WHERE id = $5),
			(SELECT state FROM arena_ready_windows WHERE id = $6),
			(SELECT COUNT(*) FROM arena_wave_readiness WHERE ready_window_id = $6),
			(SELECT current_revision_id FROM arena_official_result_heads WHERE entity_kind = 'game_attempt' AND entity_id = $1),
			(SELECT current_revision_id FROM arena_series_score_heads WHERE series_id = $2),
			(SELECT current_revision_id FROM arena_official_result_heads WHERE entity_kind = 'series' AND entity_id = $2),
			(SELECT id FROM arena_projection_revisions WHERE tournament_id = $3 AND roster_id = $4 AND state = 'published')`,
		fixture.resultFixture.attemptID,
		fixture.resultFixture.draft.seriesID,
		fixture.resultFixture.draft.tournamentID,
		fixture.resultFixture.draft.rosterID,
		fixture.waveID,
		fixture.windowID,
	).Scan(
		&snapshot.resultEvents,
		&snapshot.resultCommits,
		&snapshot.gameRevisions,
		&snapshot.scoreRevisions,
		&snapshot.seriesRevisions,
		&snapshot.projectionRevisions,
		&snapshot.projectionCutoffs,
		&snapshot.projectionArtifacts,
		&snapshot.projectionLinks,
		&snapshot.projectionMembers,
		&snapshot.projectionEdges,
		&snapshot.auditEvents,
		&snapshot.projectionEvidence,
		&snapshot.outboxEvents,
		&snapshot.waveState,
		&snapshot.readyWindowState,
		&snapshot.readinessRecords,
		&snapshot.gameHead,
		&snapshot.scoreHead,
		&snapshot.seriesHead,
		&snapshot.projectionHead,
	)
	require.NoError(t, err)
	return snapshot
}

func arenaRevisionResultScope(fixture arenaCorrectionRepositoryFixture) postgres.ArenaResultScope {
	return postgres.ArenaResultScope{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		RosterID:     fixture.resultFixture.draft.rosterID,
		SeriesID:     fixture.resultFixture.draft.seriesID,
		AttemptID:    fixture.resultFixture.attemptID,
	}
}

func arenaRevisionProjectionScope(fixture arenaCorrectionRepositoryFixture) postgres.ArenaProjectionScope {
	return postgres.ArenaProjectionScope{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		RosterID:     fixture.resultFixture.draft.rosterID,
	}
}

func countArenaRevisionUnpublishedOutbox(
	t testing.TB,
	ctx context.Context,
	fixture arenaCorrectionRepositoryFixture,
) int {
	t.Helper()
	var count int
	err := sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_outbox_events
		WHERE tournament_id = $1 AND roster_id = $2 AND published_at IS NULL`,
		fixture.resultFixture.draft.tournamentID,
		fixture.resultFixture.draft.rosterID,
	).Scan(&count)
	require.NoError(t, err)
	return count
}

var errArenaRevisionSinkUnavailable = errors.New("projection sink unavailable")

type arenaRevisionProjectionSink struct {
	mu                 sync.Mutex
	targetProjectionID uuid.UUID
	failures           int
	events             []postgres.ArenaOutboxEvent
	projection         []uuid.UUID
}

func (s *arenaRevisionProjectionSink) Publish(
	ctx context.Context,
	event postgres.ArenaOutboxEvent,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	s.events = append(s.events, event)
	s.projection = append(s.projection, s.targetProjectionID)
	if s.failures > 0 {
		s.failures--
		return errArenaRevisionSinkUnavailable
	}
	return nil
}

func (s *arenaRevisionProjectionSink) snapshot() ([]postgres.ArenaOutboxEvent, []uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := append([]postgres.ArenaOutboxEvent(nil), s.events...)
	for index := range events {
		events[index].Payload = append(json.RawMessage(nil), events[index].Payload...)
	}
	return events, append([]uuid.UUID(nil), s.projection...)
}
