package golden_test

import (
	"testing"

	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGroupRevisionPersistenceSnapshotRoundTrip(t *testing.T) {
	t.Parallel()

	revision := mustGoldenTopologyRevision(t)
	snapshot := revision.PersistenceSnapshot()

	restored, err := goldenusecase.RestoreGroupRevision(snapshot)
	require.NoError(t, err)
	require.NoError(t, restored.Validate())
	require.Equal(t, snapshot, restored.PersistenceSnapshot())
}

func TestGroupRevisionPersistenceSnapshotIsolatedFromCallerMutation(t *testing.T) {
	t.Parallel()

	revision := mustGoldenTopologySuccessorRevision(t)
	expected := revision.PersistenceSnapshot()
	snapshot := revision.PersistenceSnapshot()

	snapshot.Members[0].Points = -1
	snapshot.Payload[0] ^= 0xff
	*snapshot.PreviousRevisionID = topologyGoldenPlanRevisionID(709)
	*snapshot.SourceProjectionPreviousRevisionID = topologyGoldenPlanRevisionID(710)

	require.Equal(t, expected, revision.PersistenceSnapshot())
}

func TestRestoreGroupRevisionRejectsTamperedPersistenceSnapshot(t *testing.T) {
	t.Parallel()

	revision := mustGoldenTopologyRevision(t)
	tests := []struct {
		name   string
		mutate func(*goldenusecase.RevisionSnapshot)
	}{
		{
			name: "payload",
			mutate: func(snapshot *goldenusecase.RevisionSnapshot) {
				snapshot.Payload[0] ^= 0xff
			},
		},
		{
			name: "digest",
			mutate: func(snapshot *goldenusecase.RevisionSnapshot) {
				snapshot.PayloadDigest[0] ^= 0xff
			},
		},
		{
			name: "proof",
			mutate: func(snapshot *goldenusecase.RevisionSnapshot) {
				snapshot.ProofHash = "tampered"
			},
		},
		{
			name: "lineage",
			mutate: func(snapshot *goldenusecase.RevisionSnapshot) {
				snapshot.RevisionNo = 2
			},
		},
		{
			name: "scope",
			mutate: func(snapshot *goldenusecase.RevisionSnapshot) {
				snapshot.TournamentID = uuid.Nil
			},
		},
		{
			name: "identity",
			mutate: func(snapshot *goldenusecase.RevisionSnapshot) {
				snapshot.GroupID = snapshot.RevisionID.UUID()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := revision.PersistenceSnapshot()
			test.mutate(&snapshot)

			_, err := goldenusecase.RestoreGroupRevision(snapshot)
			require.ErrorIs(t, err, goldenusecase.ErrInvalidGroupRevision)
		})
	}
}

func mustGoldenTopologyRevision(tb testing.TB) goldenusecase.GroupRevision {
	tb.Helper()

	source := topologyMustGoldenPlanProjection(
		tb,
		topologyGoldenPlanID(701),
		topologyGoldenPlanID(702),
		topologyGoldenPlanRevisionID(703),
		1,
		topologyGoldenPlanStandings([]int{9, 9, 9, 7, 6, 5}),
	)
	partition, err := goldenusecase.PartitionTies(source)
	require.NoError(tb, err)
	seed := partition.Groups()[0]
	revision, err := goldenusecase.BuildGroupRevision(goldenusecase.GroupRevisionCommand{
		TournamentID:                source.TournamentID,
		GroupID:                     topologyGoldenPlanID(704),
		RevisionID:                  topologyGoldenPlanRevisionID(705),
		RevisionNo:                  1,
		ExpectedSourceRevisionID:    source.RevisionID,
		ExpectedSourcePayloadDigest: source.PayloadDigest,
		PositionFrom:                seed.PositionFrom,
		PositionTo:                  seed.PositionTo,
	}, source, seed, nil, nil)
	require.NoError(tb, err)
	return revision
}

func mustGoldenTopologySuccessorRevision(tb testing.TB) goldenusecase.GroupRevision {
	tb.Helper()

	first := mustGoldenTopologyRevision(tb)
	standings := topologyGoldenPlanStandings([]int{9, 9, 9, 7, 6, 5})
	standings[5].Buchholz++
	previousSourceRevisionID := first.SourceProjectionRevisionID()
	source, err := goldenusecase.NewStandingsProjection(
		first.TournamentID(),
		first.SourceProjectionID(),
		topologyGoldenPlanRevisionID(706),
		2,
		&previousSourceRevisionID,
		true,
		standings,
	)
	require.NoError(tb, err)
	partition, err := goldenusecase.PartitionTies(source)
	require.NoError(tb, err)
	seed := partition.Groups()[0]
	previousRevisionID := first.RevisionID()
	revision, err := goldenusecase.BuildGroupRevision(goldenusecase.GroupRevisionCommand{
		TournamentID:                first.TournamentID(),
		GroupID:                     first.GroupID(),
		RevisionID:                  topologyGoldenPlanRevisionID(707),
		RevisionNo:                  2,
		PreviousRevisionID:          &previousRevisionID,
		ExpectedSourceRevisionID:    source.RevisionID,
		ExpectedSourcePayloadDigest: source.PayloadDigest,
		PositionFrom:                seed.PositionFrom,
		PositionTo:                  seed.PositionTo,
	}, source, seed, &first, nil)
	require.NoError(tb, err)
	return revision
}
