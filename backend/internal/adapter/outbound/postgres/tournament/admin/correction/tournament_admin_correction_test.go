package correction

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

var _ tournamentadmin.CorrectionWorkflowRepository = (*TournamentAdminCorrectionPostgres)(nil)

func TestCorrectionSettlementIDsKeepCommandIdempotencyForCommitLedger(t *testing.T) {
	t.Parallel()

	commandID := uuid.New()
	ids := correctionSettlementIDs(commandID)
	require.Equal(t, commandID, ids.CommitIdempotencyKey)
	require.NotEqual(t, uuid.Nil, ids.CommitID)
	require.NotEqual(t, ids.CommitID, ids.ResultEventID)
	require.NotEqual(t, ids.ResultEventID, ids.GameResultRevisionID)
	require.Equal(t, ids, correctionSettlementIDs(commandID))
}

func TestTournamentAdminCorrectionCommandRecordRejectsTamperedEvidence(t *testing.T) {
	t.Parallel()

	requestedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	validationDigest := sha256.Sum256([]byte("validation"))
	evidence := tournamentadmin.CorrectionEvidence{
		CommandID: uuid.New(), TournamentID: uuid.New(), SeriesID: uuid.New(), GameID: uuid.New(),
		OperatorID: uuid.New(), Reason: "operator_ruling", Fields: []string{"winner"},
		RequestedAt: requestedAt, ValidationDigest: validationDigest,
	}
	payload, err := marshalTournamentAdminCorrectionEvidence(evidence)
	require.NoError(t, err)

	requestDigest := sha256.Sum256([]byte("request"))
	row := sqlc.ResultCorrectionCommit{
		CommandID: evidence.CommandID, TournamentID: evidence.TournamentID, RosterID: uuid.New(),
		SeriesID: evidence.SeriesID, GameAttemptID: evidence.GameID, ActorID: evidence.OperatorID,
		SourceProjectionRevisionID: uuid.New(), SourceProjectionRevision: 1,
		ResultingProjectionRevisionID: uuid.New(), ResultingProjectionRevision: 2,
		RequestDigest: requestDigest[:], ValidationDigest: validationDigest[:],
		EvidenceDocument: payload, ResultCommitID: uuid.New(),
		ExecutedAt: pgtype.Timestamptz{Time: requestedAt, Valid: true},
	}

	record, err := tournamentAdminCorrectionCommandRecord(row)
	require.NoError(t, err)
	require.Equal(t, evidence.CommandID, record.Evidence.CommandID)
	require.Equal(t, evidence.TournamentID, record.Evidence.TournamentID)
	require.Equal(t, evidence.SeriesID, record.Evidence.SeriesID)
	require.Equal(t, evidence.GameID, record.Evidence.GameID)
	require.Equal(t, evidence.OperatorID, record.Evidence.OperatorID)
	require.Equal(t, evidence.Reason, record.Evidence.Reason)
	require.Equal(t, evidence.Fields, record.Evidence.Fields)
	require.Equal(t, evidence.RequestedAt, record.Evidence.RequestedAt)
	require.Equal(t, evidence.ValidationDigest, record.Evidence.ValidationDigest)
	require.Equal(t, evidence.Supersessions, record.Evidence.Supersessions)
	require.Empty(t, record.Evidence.UnlockIntents)
	require.NotNil(t, record.Evidence.UnlockIntents)

	var document tournamentAdminCorrectionEvidenceDocument
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	require.NoError(t, json.Unmarshal(payload, &document))
	document.OperatorID = uuid.New()
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	tampered, err := json.Marshal(document)
	require.NoError(t, err)
	row.EvidenceDocument = tampered
	_, err = tournamentAdminCorrectionCommandRecord(row)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestCorrectionLogicalProjectionCurrentRejectsHistoricalNode(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	artifact := domain.ArtifactRef{Kind: domain.ArtifactKindBracket, EntityID: tournamentID}
	createdAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	base, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(uuid.New()), tournamentID, artifact, 1, nil, createdAt,
		[]byte(`{"rounds":["initial"]}`),
	)
	require.NoError(t, err)
	previous := base.Revision().ID()
	current, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(uuid.New()), tournamentID, artifact, 2, &previous, createdAt.Add(time.Second),
		[]byte(`{"rounds":["corrected"]}`),
	)
	require.NoError(t, err)

	logical := correctionLogicalGraph{
		byID: map[domain.DerivedRevisionID]domain.ProjectionRevision{
			base.Revision().ID():    base,
			current.Revision().ID(): current,
		},
		current: map[domain.ArtifactRef]domain.DerivedRevision{artifact: current.Revision()},
	}
	require.False(t, correctionLogicalProjectionIsCurrent(logical, base))
	require.True(t, correctionLogicalProjectionIsCurrent(logical, current))
}

func TestCorrectionCanonicalMaterializedStateRequestsTheFullPublishedArtifactSet(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	participants := []sqlc.ListTournamentAdminCorrectionProjectionParticipantsRow{
		{ID: uuid.New(), Seed: 1},
		{ID: uuid.New(), Seed: 2},
		{ID: uuid.New(), Seed: 3},
		{ID: uuid.New(), Seed: 4},
	}
	ledger := []sqlc.ListTournamentAdminCorrectionSwissPointLedgerRow{{
		ID: uuid.New(), TournamentID: tournamentID, RoundID: uuid.New(), RoundRevisionID: uuid.New(),
		RoundNumber: 1, SourceKind: "bye", ByeRevisionID: uuid.NullUUID{UUID: uuid.New(), Valid: true},
		ParticipantID: participants[0].ID, Points: 1, StableSeed: 1,
	}}

	state, err := correctionCanonicalMaterializedState(tournamentID, participants, ledger)
	require.NoError(t, err)
	require.Equal(t, []domain.ArtifactKind{
		domain.ArtifactKindStandings,
		domain.ArtifactKindTopFour,
		domain.ArtifactKindBracket,
	}, state.ArtifactKinds)
	require.IsType(t, correctionusecase.MaterializedProjectionState{}, state)
}
