package progression

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
)

func TestProgressionCanonicalSourceUsesOwnedPayload(t *testing.T) {
	payload := []byte(`{"entries":[1]}`)
	digest := sha256.Sum256(payload)
	current := ProjectionReference{ArtifactID: uuid.New(), RevisionID: uuid.New(), Revision: 1, Kind: domain.ArtifactKindStandings, Digest: digest, Payload: []byte(`{"untrusted":true}`)}
	canonical := resultprojection.CanonicalMaterialization{Artifacts: []resultprojection.CanonicalMaterializedArtifact{{Kind: domain.ArtifactKindStandings, Payload: payload, PayloadDigest: digest}}}
	source, err := canonicalSwissSource(current, canonical)
	require.NoError(t, err)
	require.Equal(t, digest, sha256.Sum256(source.Payload))
	clone := cloneProjectionReference(source)
	source.Payload[0] = ' '
	require.Equal(t, digest, sha256.Sum256(clone.Payload))
	require.Equal(t, digest, sha256.Sum256(canonical.Artifacts[0].Payload))
	require.JSONEq(t, `{"untrusted":true}`, string(current.Payload))
}

func TestProgressionCanonicalSourceRejectsInvalidPayload(t *testing.T) {
	payload := []byte(`{"entries":[1]}`)
	digest := sha256.Sum256(payload)
	current := ProjectionReference{ArtifactID: uuid.New(), RevisionID: uuid.New(), Revision: 1, Kind: domain.ArtifactKindStandings, Digest: digest}
	for _, invalid := range []string{`{"entries":[2]}`, `{"entries":[1]`} {
		canonical := resultprojection.CanonicalMaterialization{Artifacts: []resultprojection.CanonicalMaterializedArtifact{{Kind: domain.ArtifactKindStandings, Payload: []byte(invalid), PayloadDigest: digest}}}
		_, err := canonicalSwissSource(current, canonical)
		require.ErrorIs(t, err, domain.ErrConflict)
	}
}

func TestPrepareSwissTerminalInputRejectsReaderSuppliedGoldenIdentities(t *testing.T) {
	t.Parallel()

	terminal := playoff.ProgressionSwissInput{GoldenGroups: []playoff.FinalSwissGoldenGroupIdentity{{
		PositionFrom: 1,
		PositionTo:   2,
		GroupID:      uuid.New(),
		RevisionID:   domain.DerivedRevisionID(uuid.New()),
	}}}

	_, err := prepareSwissTerminalInput(
		Command{Action: ActionStartGolden},
		Authority{Tournament: inbound.TournamentView{State: domain.TournamentStateSwiss}},
		terminal,
	)

	require.Error(t, err)
}

func TestPlayoffGoldenDependencyIdentity(t *testing.T) {
	commandID, commitID := uuid.New(), uuid.New()
	ids, err := PlayoffPublicationIdentity(commandID)
	require.NoError(t, err)
	first, err := ids.GoldenDependencyID(commandID, commitID)
	require.NoError(t, err)
	second, err := ids.GoldenDependencyID(commandID, commitID)
	require.NoError(t, err)
	require.Equal(t, first, second)
	other, err := ids.GoldenDependencyID(commandID, uuid.New())
	require.NoError(t, err)
	require.NotEqual(t, first, other)
	require.NotEqual(t, first, ids.Top4DependencyID)
	_, err = ids.GoldenDependencyID(uuid.New(), commitID)
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = ids.GoldenDependencyID(commandID, uuid.Nil)
	require.ErrorIs(t, err, domain.ErrValidation)
	ids.Top4ArtifactID = uuid.New()
	_, err = ids.GoldenDependencyID(commandID, commitID)
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestServerGoldenGroupIdentitiesUseCommandNamespace(t *testing.T) {
	t.Parallel()

	commandID := uuid.New()
	identities := serverGoldenGroupIdentities(commandID, []playoff.ImpactfulGoldenTieRange{{
		PositionFrom: 1,
		PositionTo:   2,
	}, {
		PositionFrom: 4,
		PositionTo:   5,
	}})

	require.Equal(t, []playoff.FinalSwissGoldenGroupIdentity{{
		PositionFrom: 1,
		PositionTo:   2,
		GroupID:      uuid.NewSHA1(commandID, []byte("tournament-stage-progression:golden-group:1:2:id")),
		RevisionID: domain.DerivedRevisionID(uuid.NewSHA1(
			commandID,
			[]byte("tournament-stage-progression:golden-group:1:2:revision"),
		)),
	}, {
		PositionFrom: 4,
		PositionTo:   5,
		GroupID:      uuid.NewSHA1(commandID, []byte("tournament-stage-progression:golden-group:4:5:id")),
		RevisionID: domain.DerivedRevisionID(uuid.NewSHA1(
			commandID,
			[]byte("tournament-stage-progression:golden-group:4:5:revision"),
		)),
	}}, identities)
}

func TestPlayoffPublicationIdentityReservesEveryPhysicalWriteID(t *testing.T) {
	t.Parallel()

	commandID := uuid.New()
	ids, err := PlayoffPublicationIdentity(commandID)

	require.NoError(t, err)
	require.Equal(t, uuid.NewSHA1(commandID, []byte(
		"tournament-stage-progression:playoff-projection-revision",
	)), ids.ProjectionRevisionID)
	require.Equal(t, uuid.NewSHA1(commandID, []byte(
		"tournament-stage-progression:playoff-top-four-artifact",
	)), ids.Top4ArtifactID)
	require.Equal(t, domain.SeriesScoreRevisionID(uuid.NewSHA1(commandID, []byte(
		"tournament-stage-progression:playoff-semifinal-1-initial-score-revision",
	))), ids.FirstSemifinalScoreRevisionID)
	require.True(t, ids.Valid())
}

func TestPlayoffPublicationIdentityRejectsEmptyCommandID(t *testing.T) {
	t.Parallel()

	_, err := PlayoffPublicationIdentity(uuid.Nil)

	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestPrepareSwissTerminalIdentityRangesKeepsDirectSwissPlayoffsIdentityFree(t *testing.T) {
	t.Parallel()

	prepared, err := prepareSwissTerminalIdentityRanges(
		Command{Action: ActionStartPlayoffs},
		domain.TournamentStateSwiss,
		playoff.ProgressionSwissInput{},
		[]playoff.ImpactfulGoldenTieRange{{PositionFrom: 1, PositionTo: 2}},
	)

	require.NoError(t, err)
	require.Empty(t, prepared.GoldenGroups)
}

func TestPrepareSwissTerminalIdentityRangesPreservesExactPersistedGoldenIdentities(t *testing.T) {
	t.Parallel()

	ranges := []playoff.ImpactfulGoldenTieRange{{PositionFrom: 1, PositionTo: 2}}
	persisted := []playoff.FinalSwissGoldenGroupIdentity{{
		PositionFrom: 1,
		PositionTo:   2,
		GroupID:      uuid.New(),
		RevisionID:   domain.DerivedRevisionID(uuid.New()),
	}}
	terminal := playoff.ProgressionSwissInput{GoldenGroups: persisted}

	prepared, err := prepareSwissTerminalIdentityRanges(
		Command{Action: ActionStartPlayoffs},
		domain.TournamentStateGolden,
		terminal,
		ranges,
	)

	require.NoError(t, err)
	require.Equal(t, persisted, prepared.GoldenGroups)
}

func TestPrepareSwissTerminalIdentityRangesRejectsNonExactPersistedGoldenIdentities(t *testing.T) {
	t.Parallel()

	ranges := []playoff.ImpactfulGoldenTieRange{
		{PositionFrom: 1, PositionTo: 2},
		{PositionFrom: 4, PositionTo: 5},
	}
	valid := func(from, to int) playoff.FinalSwissGoldenGroupIdentity {
		return playoff.FinalSwissGoldenGroupIdentity{
			PositionFrom: from,
			PositionTo:   to,
			GroupID:      uuid.New(),
			RevisionID:   domain.DerivedRevisionID(uuid.New()),
		}
	}
	first := valid(1, 2)
	second := valid(4, 5)
	tests := []struct {
		name       string
		identities []playoff.FinalSwissGoldenGroupIdentity
	}{
		{name: "missing", identities: []playoff.FinalSwissGoldenGroupIdentity{first}},
		{name: "extra", identities: append([]playoff.FinalSwissGoldenGroupIdentity{first, second}, valid(6, 7))},
		{name: "duplicate range", identities: []playoff.FinalSwissGoldenGroupIdentity{first, first}},
		{name: "tampered range", identities: []playoff.FinalSwissGoldenGroupIdentity{first, valid(3, 4)}},
		{name: "nil group", identities: []playoff.FinalSwissGoldenGroupIdentity{{
			PositionFrom: 1, PositionTo: 2, RevisionID: domain.DerivedRevisionID(uuid.New()),
		}, second}},
		{name: "nil revision", identities: []playoff.FinalSwissGoldenGroupIdentity{{
			PositionFrom: 1, PositionTo: 2, GroupID: uuid.New(),
		}, second}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := prepareSwissTerminalIdentityRanges(
				Command{Action: ActionStartPlayoffs},
				domain.TournamentStateGolden,
				playoff.ProgressionSwissInput{GoldenGroups: test.identities},
				ranges,
			)

			require.Error(t, err)
		})
	}
}

func TestExactPublishedSupersessionRequiresResultingRevision(t *testing.T) {
	t.Parallel()

	publishedID := uuid.New()
	publication := PlayoffPublication{PublishedProjectionID: publishedID}
	receipt := PersistenceReceipt{
		SourceProjectionState:        "superseded",
		SourceSupersededByRevisionID: publishedID,
	}

	require.True(t, isExactPublishedSupersession(receipt, publication))
}

func TestExactPublishedSupersessionRejectsOtherResultingRevision(t *testing.T) {
	t.Parallel()

	receipt := PersistenceReceipt{
		SourceProjectionState:        "superseded",
		SourceSupersededByRevisionID: uuid.New(),
	}

	require.False(t, isExactPublishedSupersession(receipt, PlayoffPublication{
		PublishedProjectionID: uuid.New(),
	}))
}

func TestPlayoffPersistenceMatchesReturnedArtifactsToPlanRevisions(t *testing.T) {
	t.Parallel()

	top4, bracket := persistedArtifactPlan(t)
	publishedID := uuid.New()
	publication := PlayoffPublication{
		PublishedProjectionID: publishedID,
		PublishedRevision:     8,
		Top4ArtifactID:        uuid.New(),
		BracketArtifactID:     uuid.New(),
	}
	receipt := PersistenceReceipt{
		SourceProjectionState:        "superseded",
		SourceSupersededByRevisionID: publishedID,
		PublishedProjectionID:        publishedID,
		PublishedRevision:            8,
		Top4: PersistedArtifact{
			ID: publication.Top4ArtifactID, Kind: domain.ArtifactKindTopFour,
			Digest: top4.Revision().PayloadDigest(),
		},
		Bracket: PersistedArtifact{
			ID: publication.BracketArtifactID, Kind: domain.ArtifactKindBracket,
			Digest: bracket.Revision().PayloadDigest(),
		},
	}

	require.True(t, matchesPlayoffPersistence(publication, receipt, top4, bracket))
	receipt.Bracket.Digest[0] ^= 0xff
	require.False(t, matchesPlayoffPersistence(publication, receipt, top4, bracket))
}

func persistedArtifactPlan(t *testing.T) (domain.ProjectionRevision, domain.ProjectionRevision) {
	t.Helper()
	tournamentID := uuid.New()
	createdAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	top4, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(uuid.New()), tournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindTopFour, EntityID: tournamentID},
		1, nil, createdAt, []byte(`{"positions":[1,2,3,4]}`),
	)
	require.NoError(t, err)
	bracket, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(uuid.New()), tournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindBracket, EntityID: tournamentID},
		1, nil, createdAt, []byte(`{"semifinals":[1,2]}`),
	)
	require.NoError(t, err)
	return top4, bracket
}
