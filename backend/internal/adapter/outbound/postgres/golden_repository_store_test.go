package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestGoldenRepositoryRevisionFromEnvelopeRejectsTamperedTopologySnapshot(t *testing.T) {
	t.Parallel()

	revision := newGoldenRepositoryTopologyRevision(t)
	payload, err := canonicalGoldenRepositoryEnvelope(revision)
	require.NoError(t, err)

	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "payload",
			mutate: func(snapshot map[string]any) {
				snapshot["payload"] = "dGFtcGVyZWQ="
			},
		},
		{
			name: "digest",
			mutate: func(snapshot map[string]any) {
				digest := snapshot["payload_digest"].([]any)
				digest[0] = float64(0)
			},
		},
		{
			name: "proof",
			mutate: func(snapshot map[string]any) {
				snapshot["proof_hash"] = "tampered"
			},
		},
		{
			name: "lineage",
			mutate: func(snapshot map[string]any) {
				snapshot["revision_no"] = float64(2)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var envelope map[string]any
			require.NoError(t, json.Unmarshal(payload, &envelope))
			test.mutate(envelope["document"].(map[string]any))
			tampered, err := json.Marshal(envelope)
			require.NoError(t, err)

			_, err = goldenRepositoryRevisionFromEnvelope(
				revision.ID,
				revision.Number,
				revision.PreviousID,
				tampered,
				revision.Digest[:],
				revision.CreatedAt,
			)
			require.ErrorIs(t, err, ErrGoldenRepositoryInvalid)
		})
	}
}

func TestGoldenRepositoryEnvelopeUsesTypedDocument(t *testing.T) {
	t.Parallel()

	revision := newGoldenRepositoryTopologyRevision(t)
	payload, err := canonicalGoldenRepositoryEnvelope(revision)
	require.NoError(t, err)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(payload, &envelope))
	document, ok := envelope["document"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, goldenRepositoryAggregateKind, envelope["kind"])
	require.Contains(t, document, "payload_digest")
	require.NotContains(t, document, "PayloadDigest")
}

func TestGoldenRepositoryRevisionFromEnvelopeRejectsNonStateKind(t *testing.T) {
	t.Parallel()

	revision := newGoldenRepositoryTopologyRevision(t)
	payload, err := canonicalGoldenRepositoryEnvelope(revision)
	require.NoError(t, err)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(payload, &envelope))
	envelope["kind"] = "wave"
	payload, err = json.Marshal(envelope)
	require.NoError(t, err)

	_, err = goldenRepositoryRevisionFromEnvelope(
		revision.ID,
		revision.Number,
		revision.PreviousID,
		payload,
		revision.Digest[:],
		revision.CreatedAt,
	)
	require.ErrorIs(t, err, ErrGoldenRepositoryInvalid)
}

func TestGoldenRepositoryRevisionFollowsHeadRejectsTamperedCurrentHead(t *testing.T) {
	t.Parallel()

	current := newGoldenRepositoryTopologyRevision(t)
	payload, err := canonicalGoldenRepositoryEnvelope(current)
	require.NoError(t, err)

	nextID := uuid.New()
	next := current
	next.ID = nextID
	next.Number = 2
	next.PreviousID = &current.ID
	next.CreatedAt = current.CreatedAt.Add(time.Second)

	head := sqlcLockGoldenRepositoryHead(current, payload)
	head.PayloadDigest[0] ^= 0xff

	require.False(t, goldenRepositoryRevisionFollowsHead(next, head))
}

func TestGoldenRepositoryScopeContainsOnlyItsTopologyRevision(t *testing.T) {
	t.Parallel()

	revision := newGoldenRepositoryTopologyRevision(t)
	groupID := revision.Snapshot.GroupID
	groupRevisionID := revision.Snapshot.RevisionID.UUID()
	scope := GoldenRepositoryScope{
		TournamentID: revision.Snapshot.TournamentID, RosterID: uuid.New(),
		GroupID: groupID, GroupRevisionID: groupRevisionID,
	}
	require.True(t, goldenRepositoryScopeContainsRevision(scope, revision))

	otherGroupID := uuid.New()
	scope.GroupID = otherGroupID
	require.False(t, goldenRepositoryScopeContainsRevision(scope, revision))
}

func TestGoldenRepositoryScopeMatchesEquivalentValues(t *testing.T) {
	t.Parallel()

	revision := newGoldenRepositoryTopologyRevision(t)
	groupID := revision.Snapshot.GroupID
	groupRevisionID := revision.Snapshot.RevisionID.UUID()
	scope := GoldenRepositoryScope{
		TournamentID: revision.Snapshot.TournamentID, RosterID: uuid.New(),
		GroupID: groupID, GroupRevisionID: groupRevisionID,
	}
	row := sqlc.GoldenRepositoryScope{
		AggregateKind: goldenRepositoryAggregateKind, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		GroupID:         uuid.NullUUID{UUID: groupID, Valid: true},
		GroupRevisionID: uuid.NullUUID{UUID: groupRevisionID, Valid: true},
	}
	require.True(t, goldenRepositoryScopeMatches(row, scope))

	row.RosterID = uuid.New()
	require.False(t, goldenRepositoryScopeMatches(row, scope))
}

func TestGoldenRepositoryScopeFromRowRejectsNonStateKinds(t *testing.T) {
	t.Parallel()

	revision := newGoldenRepositoryTopologyRevision(t)
	row := sqlc.GoldenRepositoryScope{
		AggregateKind: goldenRepositoryAggregateKind, TournamentID: revision.Snapshot.TournamentID, RosterID: uuid.New(),
		GroupID:         uuid.NullUUID{UUID: revision.Snapshot.GroupID, Valid: true},
		GroupRevisionID: uuid.NullUUID{UUID: revision.Snapshot.RevisionID.UUID(), Valid: true},
	}
	for _, kind := range []string{"plan", "wave", "submission", "attempt", "connection", "continuation", "failure", "prestart"} {
		t.Run(kind, func(t *testing.T) {
			current := row
			current.AggregateKind = kind
			_, err := goldenRepositoryScopeFromRow(current)
			require.ErrorIs(t, err, ErrGoldenRepositoryInvalid)
		})
	}
}

func TestCloneGoldenRepositoryRevisionIsolated(t *testing.T) {
	t.Parallel()

	revision := newGoldenRepositoryTopologyRevision(t)
	clone := cloneGoldenRepositoryRevision(revision)
	require.NotNil(t, clone)

	clone.Snapshot.Payload[0] ^= 0xff
	clone.Snapshot.Members[0].Points++
	require.NotEqual(t, clone.Snapshot.Payload, revision.Snapshot.Payload)
	require.NotEqual(t, clone.Snapshot.Members, revision.Snapshot.Members)
}

func TestGoldenRepositoryReplayRequiresTheSameScope(t *testing.T) {
	t.Parallel()

	revision := newGoldenRepositoryTopologyRevision(t)
	groupID := revision.Snapshot.GroupID
	groupRevisionID := revision.Snapshot.RevisionID.UUID()
	scope := GoldenRepositoryScope{
		TournamentID: revision.Snapshot.TournamentID, RosterID: uuid.New(),
		GroupID: groupID, GroupRevisionID: groupRevisionID,
	}
	digest := revision.Digest
	command := GoldenRepositoryCommand{
		ID: uuid.New(), Kind: GoldenRepositoryStateReady, Digest: digest, OccurredAt: revision.CreatedAt,
	}
	replay := GoldenRepositoryCommit{Scope: scope, Revision: revision, Command: &command}
	require.True(t, goldenRepositoryReplayMatches(replay, replay))

	other := replay
	other.Scope.RosterID = uuid.New()
	require.False(t, goldenRepositoryReplayMatches(replay, other))
}

func newGoldenRepositoryTopologyRevision(tb testing.TB) GoldenRepositoryRevision {
	tb.Helper()

	tournamentID := uuid.New()
	source, err := goldenusecase.NewStandingsProjection(
		tournamentID,
		uuid.New(),
		domain.DerivedRevisionID(uuid.New()),
		1,
		nil,
		true,
		[]swissusecase.NormalStanding{
			{ParticipantID: uuid.New(), Position: 1, Points: 9, PointsLabel: swissusecase.PointsFinal, Seed: 1},
			{ParticipantID: uuid.New(), Position: 2, Points: 9, PointsLabel: swissusecase.PointsFinal, Seed: 2},
			{ParticipantID: uuid.New(), Position: 3, Points: 9, PointsLabel: swissusecase.PointsFinal, Seed: 3},
			{ParticipantID: uuid.New(), Position: 4, Points: 7, PointsLabel: swissusecase.PointsFinal, Seed: 4},
		},
	)
	require.NoError(tb, err)
	seed, err := goldenusecase.PartitionTies(source)
	require.NoError(tb, err)
	groups := seed.Groups()
	require.Len(tb, groups, 1)

	group, err := goldenusecase.BuildGroupRevision(goldenusecase.GroupRevisionCommand{
		TournamentID:                tournamentID,
		GroupID:                     uuid.New(),
		RevisionID:                  domain.DerivedRevisionID(uuid.New()),
		RevisionNo:                  1,
		ExpectedSourceRevisionID:    source.RevisionID,
		ExpectedSourcePayloadDigest: source.PayloadDigest,
		PositionFrom:                groups[0].PositionFrom,
		PositionTo:                  groups[0].PositionTo,
	}, source, groups[0], nil, nil)
	require.NoError(tb, err)

	snapshot := group.PersistenceSnapshot()
	return GoldenRepositoryRevision{
		ID:        snapshot.RevisionID.UUID(),
		Number:    int64(snapshot.RevisionNo),
		Snapshot:  snapshot,
		Digest:    snapshot.PayloadDigest,
		CreatedAt: time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC),
	}
}

func sqlcLockGoldenRepositoryHead(revision GoldenRepositoryRevision, payload []byte) sqlc.LockGoldenRepositoryHeadRow {
	return sqlc.LockGoldenRepositoryHeadRow{
		ScopeID: uuid.New(), RevisionID: revision.ID, RevisionNumber: revision.Number,
		PayloadDigest: append([]byte(nil), revision.Digest[:]...), AggregateKind: goldenRepositoryAggregateKind,
		Payload: payload, CreatedAt: pgtype.Timestamptz{Time: revision.CreatedAt, Valid: true},
	}
}
