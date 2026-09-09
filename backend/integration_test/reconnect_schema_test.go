//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReconnectMigration(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createReconnectMigrationFixture(ctx, t)
	insertResumeDecision(ctx, t, fixture, fixture.rootPauseID, 1, nil, nil, "resume", fixture.pausedAt)
	insertResumeDecision(ctx, t, fixture, fixture.gamePauseID, 1, nil, nil, "resume", fixture.pausedAt)
	assertParentPauseWaitsForChild(ctx, t, fixture)

	fixture.firstIntervalID, fixture.firstDeadline = disconnectParticipant(
		ctx, t,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(time.Second),
		2*time.Minute,
	)
	insertResumeDecision(
		ctx, t,
		fixture,
		fixture.gamePauseID,
		2,
		&fixture.firstIntervalID,
		nil,
		"wait_first",
		fixture.pausedAt.Add(time.Second),
	)

	fixture.secondIntervalID, fixture.secondDeadline = disconnectParticipant(
		ctx, t,
		fixture,
		fixture.draft.participantIDs[1],
		fixture.pausedAt.Add(2*time.Second),
		3*time.Minute,
	)
	insertResumeDecision(
		ctx, t,
		fixture,
		fixture.gamePauseID,
		3,
		&fixture.firstIntervalID,
		&fixture.secondIntervalID,
		"wait_both",
		fixture.pausedAt.Add(2*time.Second),
	)
	require.NotEqual(t, fixture.firstDeadline, fixture.secondDeadline)

	reconnectParticipant(
		ctx, t,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.firstIntervalID,
		fixture.pausedAt.Add(3*time.Second),
	)
	insertResumeDecision(
		ctx, t,
		fixture,
		fixture.gamePauseID,
		4,
		nil,
		&fixture.secondIntervalID,
		"wait_second",
		fixture.pausedAt.Add(3*time.Second),
	)

	reconnectParticipant(
		ctx, t,
		fixture,
		fixture.draft.participantIDs[1],
		fixture.secondIntervalID,
		fixture.pausedAt.Add(4*time.Second),
	)
	insertResumeDecision(
		ctx, t,
		fixture,
		fixture.gamePauseID,
		5,
		nil,
		nil,
		"resume",
		fixture.pausedAt.Add(4*time.Second),
	)
	resumeMigrationPause(ctx, t, fixture, true, fixture.pausedAt.Add(5*time.Second))

	insertResumeDecision(
		ctx, t,
		fixture,
		fixture.rootPauseID,
		2,
		nil,
		nil,
		"resume",
		fixture.pausedAt.Add(5*time.Second),
	)
	resumeMigrationPause(ctx, t, fixture, false, fixture.pausedAt.Add(6*time.Second))

	assertReconnectPersistence(ctx, t, fixture)
	assertReconnectCAS(ctx, t, fixture)
}

func TestReconnectMigrationResumeCAS(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createReconnectMigrationFixture(ctx, t)
	assertPauseResumeRejected(
		ctx, t,
		fixture.gamePauseID,
		fixture.gamePauseRevisionID,
		fixture.pausedAt.Add(time.Second),
		"current resume decision",
	)

	insertResumeDecision(
		ctx, t,
		fixture,
		fixture.gamePauseID,
		1,
		nil,
		nil,
		"resume",
		fixture.pausedAt.Add(time.Second),
	)
	intervalID, _ := disconnectParticipant(
		ctx, t,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(2*time.Second),
		2*time.Minute,
	)
	assertPauseResumeRejected(
		ctx, t,
		fixture.gamePauseID,
		fixture.gamePauseRevisionID,
		fixture.pausedAt.Add(3*time.Second),
		"current reconnect evidence",
	)

	reconnectedAt := fixture.pausedAt.Add(4 * time.Second)
	reconnectParticipant(
		ctx, t,
		fixture,
		fixture.draft.participantIDs[0],
		intervalID,
		reconnectedAt,
	)
	insertResumeDecision(
		ctx, t,
		fixture,
		fixture.gamePauseID,
		2,
		nil,
		nil,
		"resume",
		reconnectedAt,
	)
	resumeMigrationPause(ctx, t, fixture, true, fixture.pausedAt.Add(5*time.Second))
}
