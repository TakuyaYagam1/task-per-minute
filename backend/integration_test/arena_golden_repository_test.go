//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
)

func TestArenaGoldenRepositoryRestoresRetainedExecutionEvidence(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaGoldenMigrationFixture(t, ctx, 5)
	repository := postgres.NewArenaGoldenPostgres(postgres.NewTxManager(sharedPool))
	attemptID := uuid.New()
	scope := postgres.ArenaGoldenScope{
		TournamentID: fixture.tournamentID, RosterID: fixture.rosterID, AttemptID: attemptID,
	}
	membershipIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	reservePosition := int16(1)
	excludedAt := fixture.createdAt
	created, err := repository.CreateAttempt(ctx, postgres.ArenaGoldenCreateAttemptInput{
		Scope: scope, AttemptNumber: 1, CreatedAt: fixture.createdAt,
		Memberships: []postgres.ArenaGoldenMembershipInput{
			{ID: membershipIDs[0], ParticipantID: fixture.participantIDs[0], SelectionKind: "direct", SelectedAt: fixture.createdAt},
			{ID: membershipIDs[1], ParticipantID: fixture.participantIDs[1], SelectionKind: "direct", SelectedAt: fixture.createdAt},
			{ID: membershipIDs[2], ParticipantID: fixture.participantIDs[2], SelectionKind: "direct", SelectedAt: fixture.createdAt},
			{
				ID: membershipIDs[3], ParticipantID: fixture.participantIDs[3], SelectionKind: "reserve",
				ReservePosition: &reservePosition, SelectedAt: fixture.createdAt,
			},
			{
				ID: membershipIDs[4], ParticipantID: fixture.participantIDs[4], SelectionKind: "excluded",
				SelectedAt: fixture.createdAt, ExcludedAt: &excludedAt, ExclusionReason: "preflight exclusion",
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, created.Memberships, 5)

	readyAt := fixture.createdAt.Add(time.Second)
	for _, membershipID := range []uuid.UUID{membershipIDs[0], membershipIDs[1], membershipIDs[3]} {
		_, err = repository.MarkReady(ctx, postgres.ArenaGoldenMemberEventInput{
			Scope: scope, MembershipID: membershipID, OccurredAt: readyAt,
		})
		require.NoError(t, err)
	}
	_, err = repository.MarkNoShow(ctx, postgres.ArenaGoldenMemberEventInput{
		Scope: scope, MembershipID: membershipIDs[2], OccurredAt: readyAt,
	})
	require.NoError(t, err)
	promotedAt := fixture.createdAt.Add(2 * time.Second)
	_, err = repository.PromoteReserve(ctx, postgres.ArenaGoldenPromotionInput{
		ID: uuid.New(), Scope: scope, ReserveMembershipID: membershipIDs[3],
		ReserveParticipantID: fixture.participantIDs[3], ReplacedMembershipID: membershipIDs[2],
		ReplacedParticipantID: fixture.participantIDs[2], Reason: "replace direct no-show",
		PromotedAt: promotedAt, CreatedAt: promotedAt,
	})
	require.NoError(t, err)

	disclosedAt := fixture.createdAt.Add(3 * time.Second)
	readyStateAt := fixture.createdAt.Add(4 * time.Second)
	_, changed, err := repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
		Scope: scope, ExpectedState: "prepared", NextState: "ready",
		DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt,
	})
	require.NoError(t, err)
	require.True(t, changed)

	disconnectedAt := fixture.createdAt.Add(5 * time.Second)
	disconnectID := uuid.New()
	_, err = repository.OpenDisconnect(ctx, postgres.ArenaGoldenDisconnectInput{
		ID: disconnectID, Scope: scope, MembershipID: membershipIDs[1],
		ParticipantID: fixture.participantIDs[1], SequenceNumber: 1,
		DisconnectedAt: disconnectedAt, CreatedAt: disconnectedAt,
	})
	require.NoError(t, err)
	reconnectedAt := fixture.createdAt.Add(6 * time.Second)
	_, err = repository.CloseDisconnect(ctx, postgres.ArenaGoldenDisconnectCloseInput{
		Scope: scope, DisconnectID: disconnectID, NextState: "reconnected", ReconnectedAt: &reconnectedAt,
	})
	require.NoError(t, err)

	participationAt := fixture.createdAt.Add(7 * time.Second)
	for _, membershipID := range []uuid.UUID{membershipIDs[0], membershipIDs[1], membershipIDs[3]} {
		_, err = repository.EstablishParticipation(ctx, postgres.ArenaGoldenMemberEventInput{
			Scope: scope, MembershipID: membershipID, OccurredAt: participationAt,
		})
		require.NoError(t, err)
	}
	startedAt := fixture.createdAt.Add(8 * time.Second)
	_, changed, err = repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
		Scope: scope, ExpectedState: "ready", NextState: "active",
		DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt, StartedAt: &startedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)

	recoveryAt := fixture.createdAt.Add(9 * time.Second)
	readyDeadline := fixture.createdAt.Add(30 * time.Second).Format(time.RFC3339Nano)
	_, err = repository.RecordRecovery(ctx, postgres.ArenaGoldenRecoveryInput{
		ID: uuid.New(), Scope: scope, State: "stable",
		RecoveryEvidence: json.RawMessage(`{"ready_deadline":"` + readyDeadline + `","source":"coordinator"}`),
		RecordedAt:       recoveryAt, CreatedAt: recoveryAt,
	})
	require.NoError(t, err)
	pausedAt := fixture.createdAt.Add(10 * time.Second)
	_, err = repository.RecordRecovery(ctx, postgres.ArenaGoldenRecoveryInput{
		ID: uuid.New(), Scope: scope, State: "technical_pause",
		RecoveryEvidence: json.RawMessage(`{"reason":"worker_disconnect"}`),
		RecordedAt:       pausedAt, CreatedAt: pausedAt,
	})
	require.NoError(t, err)
	_, changed, err = repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
		Scope: scope, ExpectedState: "active", NextState: "technical_pause",
		DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt, StartedAt: &startedAt, PausedAt: &pausedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	recoveringAt := fixture.createdAt.Add(11 * time.Second)
	_, err = repository.RecordRecovery(ctx, postgres.ArenaGoldenRecoveryInput{
		ID: uuid.New(), Scope: scope, State: "recovering",
		RecoveryEvidence: json.RawMessage(`{"restored_sequence":3}`),
		RecordedAt:       recoveringAt, CreatedAt: recoveringAt,
	})
	require.NoError(t, err)
	resumedAt := fixture.createdAt.Add(12 * time.Second)
	_, err = repository.RecordRecovery(ctx, postgres.ArenaGoldenRecoveryInput{
		ID: uuid.New(), Scope: scope, State: "resumed",
		RecoveryEvidence: json.RawMessage(`{"resume_epoch":2}`),
		RecordedAt:       resumedAt, CreatedAt: resumedAt,
	})
	require.NoError(t, err)
	_, changed, err = repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
		Scope: scope, ExpectedState: "technical_pause", NextState: "active",
		DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt, StartedAt: &startedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)

	firstSubmission := arenaGoldenRepositorySubmission(
		scope, membershipIDs[0], fixture.participantIDs[0], 1, 1, resumedAt.Add(time.Second),
	)
	type submissionResult struct {
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan submissionResult, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, won, submitErr := repository.RecordSubmission(ctx, firstSubmission)
			results <- submissionResult{changed: won, err: submitErr}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	changedCount := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.changed {
			changedCount++
		}
	}
	require.Equal(t, 1, changedCount)

	secondSubmission := arenaGoldenRepositorySubmission(
		scope, membershipIDs[1], fixture.participantIDs[1], 2, 2, resumedAt.Add(2*time.Second),
	)
	thirdSubmission := arenaGoldenRepositorySubmission(
		scope, membershipIDs[3], fixture.participantIDs[3], 3, 3, resumedAt.Add(3*time.Second),
	)
	for _, submission := range []postgres.ArenaGoldenSubmissionInput{secondSubmission, thirdSubmission} {
		_, changed, err = repository.RecordSubmission(ctx, submission)
		require.NoError(t, err)
		require.True(t, changed)
	}
	positionAt := resumedAt.Add(4 * time.Second)
	for index, item := range []struct {
		membershipID  uuid.UUID
		participantID uuid.UUID
		submissionID  uuid.UUID
	}{
		{membershipIDs[0], fixture.participantIDs[0], firstSubmission.ID},
		{membershipIDs[1], fixture.participantIDs[1], secondSubmission.ID},
		{membershipIDs[3], fixture.participantIDs[3], thirdSubmission.ID},
	} {
		_, err = repository.CommitPosition(ctx, postgres.ArenaGoldenPositionInput{
			ID: uuid.New(), Scope: scope, MembershipID: item.membershipID,
			ParticipantID: item.participantID, ProvisionalSubmissionID: item.submissionID,
			Position: int16(index + 1), CommittedAt: positionAt, CreatedAt: positionAt,
		})
		require.NoError(t, err)
	}
	completedAt := positionAt.Add(time.Second)
	_, changed, err = repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
		Scope: scope, ExpectedState: "active", NextState: "completed",
		DisclosedAt: &disclosedAt, ReadyAt: &readyStateAt, StartedAt: &startedAt, CompletedAt: &completedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)

	restored, err := repository.Get(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "completed", restored.Attempt.State)
	require.Len(t, restored.Memberships, 5)
	require.Len(t, restored.Disconnects, 1)
	require.Equal(t, "reconnected", restored.Disconnects[0].State)
	require.Len(t, restored.Submissions, 3)
	require.EqualValues(t, []int64{1, 2, 3}, []int64{
		restored.Submissions[0].ServerSequence,
		restored.Submissions[1].ServerSequence,
		restored.Submissions[2].ServerSequence,
	})
	require.Len(t, restored.Positions, 3)
	require.Len(t, restored.Promotions, 1)
	require.Len(t, restored.Recoveries, 4)
	require.Contains(t, string(restored.Recoveries[0].RecoveryEvidence), "ready_deadline")

	secondAttemptID := uuid.New()
	secondScope := scope
	secondScope.AttemptID = secondAttemptID
	secondCreatedAt := completedAt.Add(time.Minute)
	_, err = repository.CreateAttempt(ctx, postgres.ArenaGoldenCreateAttemptInput{
		Scope: secondScope, AttemptNumber: 2, PreviousAttemptID: &attemptID, CreatedAt: secondCreatedAt,
		Memberships: []postgres.ArenaGoldenMembershipInput{
			{ID: uuid.New(), ParticipantID: fixture.participantIDs[0], SelectionKind: "direct", SelectedAt: secondCreatedAt},
			{ID: uuid.New(), ParticipantID: fixture.participantIDs[1], SelectionKind: "direct", SelectedAt: secondCreatedAt},
		},
	})
	require.NoError(t, err)
	supersededAt := secondCreatedAt.Add(time.Second)
	_, changed, err = repository.TransitionAttempt(ctx, postgres.ArenaGoldenAttemptTransitionInput{
		Scope: secondScope, ExpectedState: "prepared", NextState: "superseded",
		SupersededAt: &supersededAt, SupersessionReason: "retain unused retry after recovery",
	})
	require.NoError(t, err)
	require.True(t, changed)
	history, err := repository.History(ctx, fixture.tournamentID, fixture.rosterID)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, "completed", history[0].State)
	require.Equal(t, "superseded", history[1].State)
}

func arenaGoldenRepositorySubmission(
	scope postgres.ArenaGoldenScope,
	membershipID uuid.UUID,
	participantID uuid.UUID,
	serverSequence int64,
	position int16,
	createdAt time.Time,
) postgres.ArenaGoldenSubmissionInput {
	digest := [32]byte{}
	copy(digest[:], bytes.Repeat([]byte{byte(40 + serverSequence)}, 32))
	return postgres.ArenaGoldenSubmissionInput{
		ID: uuid.New(), Scope: scope, MembershipID: membershipID, ParticipantID: participantID,
		ServerSequence: serverSequence, IdempotencyKey: uuid.New(), ProvisionalPosition: position,
		ElapsedMilliseconds: serverSequence * 1000, Status: "accepted", PayloadDigest: digest,
		SubmittedAt: createdAt, ReceivedAt: createdAt, CreatedAt: createdAt,
	}
}
