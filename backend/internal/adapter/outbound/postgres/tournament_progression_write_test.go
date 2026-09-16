package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

type progressionRow struct {
	values []any
	err    error
}

func (r progressionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("unexpected scan width")
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(r.values[i]))
	}
	return nil
}

type progressionWriteTx struct {
	pgx.Tx

	t       *testing.T
	queries []string
	rows    []progressionRow
}

type progressionRows struct {
	pgx.Rows

	rows  []progressionRow
	index int
}

func (r *progressionRows) Next() bool { return r.index < len(r.rows) }
func (r *progressionRows) Scan(dest ...any) error {
	row := r.rows[r.index]
	r.index++
	return row.Scan(dest...)
}
func (r *progressionRows) Close()     {}
func (r *progressionRows) Err() error { return nil }

type progressionReadTx struct {
	pgx.Tx

	t       *testing.T
	queries []string
	results [][]progressionRow
}

func (tx *progressionReadTx) Query(_ context.Context, query string, _ ...any) (pgx.Rows, error) {
	tx.t.Helper()
	require.NotEmpty(tx.t, tx.queries, "unexpected query: %s", query)
	require.Contains(tx.t, strings.Split(query, "\n")[0], tx.queries[0])
	rows := &progressionRows{rows: tx.results[0]}
	tx.queries, tx.results = tx.queries[1:], tx.results[1:]
	return rows, nil
}

func (tx *progressionReadTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	tx.t.Helper()
	require.NotEmpty(tx.t, tx.queries, "unexpected query: %s", query)
	require.Contains(tx.t, strings.Split(query, "\n")[0], tx.queries[0])
	require.NotEmpty(tx.t, tx.results[0])
	row := tx.results[0][0]
	tx.queries, tx.results = tx.queries[1:], tx.results[1:]
	return row
}

func progressionStructRow(value any) progressionRow {
	v := reflect.ValueOf(value)
	row := progressionRow{}
	for i := 0; i < v.NumField(); i++ {
		row.values = append(row.values, v.Field(i).Interface())
	}
	return row
}

func TestProgressionLoadGoldenSettlementsUsesLockedSealedRows(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		fixture := newProgressionGoldenEvidenceFixture()
		if malformed {
			fixture.commitRows[0].ParticipantID = uuid.New()
		}
		tx := &progressionReadTx{t: t, queries: []string{"ResolveTournamentProgressionGoldenSource", "LockTournamentProgressionGoldenSettlements", "LockTournamentProgressionGoldenAttempts", "LockTournamentProgressionGoldenPositionCommits", "LockTournamentProgressionGoldenPositionLedger", "LockTournamentProgressionGoldenPositionLedgerRevisionSeals"}}
		tx.results = append(tx.results, []progressionRow{{values: []any{
			fixture.authority.ProjectionRevisionID,
			fixture.authority.ProjectionRevision,
		}}})
		for _, values := range []any{fixture.settlementRows, fixture.attemptRows, fixture.commitRows, fixture.ledgerRows, fixture.seals} {
			v := reflect.ValueOf(values)
			rows := make([]progressionRow, v.Len())
			for i := 0; i < v.Len(); i++ {
				rows[i] = progressionStructRow(v.Index(i).Interface())
			}
			tx.results = append(tx.results, rows)
		}
		ctx := db.WithTransaction(context.Background(), tx)
		got, err := NewTournamentProgressionPostgres(&TxManager{}).loadGoldenSettlements(ctx, fixture.authority)
		if malformed {
			require.ErrorIs(t, err, domain.ErrConflict)
			require.Nil(t, got)
		} else {
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, fixture.commitID, got[0].Positions.Positions()[0].CommitID)
		}
		require.Empty(t, tx.queries)
	}
}

func TestProgressionProofReadbackRejectsChangedAuthority(t *testing.T) {
	now := time.Date(2026, 9, 7, 3, 0, 0, 123456789, time.UTC)
	want := sqlc.CreateTournamentStageProgressionCASParams{CommandID: uuid.New(), ActorID: uuid.New(), Action: "start_golden", SourceTournamentRevision: 5, SourceTournamentState: "swiss", SourceProjectionRevisionID: uuid.New(), SourceProjectionRevision: 4, ResultingTournamentState: "golden", Proof: []byte(`{"command":"advance","count":3}`), ProofDigest: []byte{1}, ExecutedAt: tstz(now), TournamentID: uuid.New(), RosterID: uuid.New()}
	row := sqlc.TournamentStageProgression{CommandID: want.CommandID, TournamentID: want.TournamentID, RosterID: want.RosterID, ActorID: want.ActorID, Action: want.Action, SourceTournamentRevision: want.SourceTournamentRevision, SourceTournamentState: want.SourceTournamentState, SourceProjectionRevisionID: want.SourceProjectionRevisionID, SourceProjectionRevision: want.SourceProjectionRevision, ResultingTournamentRevision: 6, ResultingTournamentState: "golden", Proof: []byte(`{"count": 3, "command": "advance"}`), ProofDigest: want.ProofDigest, ExecutedAt: tstz(now), CreatedAt: tstz(now)}
	row.ExecutedAt = tstz(now.Truncate(time.Microsecond))
	row.CreatedAt = tstz(now.Truncate(time.Microsecond))
	require.NoError(t, progressionProofMatches(want, row))
	for _, mutate := range []func(*sqlc.TournamentStageProgression){
		func(r *sqlc.TournamentStageProgression) { r.CommandID = uuid.New() },
		func(r *sqlc.TournamentStageProgression) { r.SourceProjectionRevision++ },
		func(r *sqlc.TournamentStageProgression) { r.ResultingTournamentRevision++ },
		func(r *sqlc.TournamentStageProgression) { r.Proof = []byte(`{"command":"advance","count":4}`) },
		func(r *sqlc.TournamentStageProgression) { r.ActorID = uuid.New() },
		func(r *sqlc.TournamentStageProgression) { r.ExecutedAt.Time = r.ExecutedAt.Time.Add(time.Microsecond) },
		func(r *sqlc.TournamentStageProgression) { r.CreatedAt.Time = r.CreatedAt.Time.Add(-time.Microsecond) },
		func(r *sqlc.TournamentStageProgression) { r.CreatedAt.Valid = false },
	} {
		changed := row
		mutate(&changed)
		require.ErrorIs(t, progressionProofMatches(want, changed), domain.ErrConflict)
	}
}

func TestProgressionPublishedRecordRejectsPartialOrRedirectedPublication(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ProjectionRecord)
	}{
		{"complete", func(*ProjectionRecord) {}},
		{"missing artifact", func(r *ProjectionRecord) { r.Artifacts = r.Artifacts[:2] }},
		{"wrong parent", func(r *ProjectionRecord) { r.Revision.PreviousRevisionID = nullableUUIDValue(uuid.New()) }},
		{"wrong revision", func(r *ProjectionRecord) { r.Revision.RevisionNumber++ }},
		{"wrong command", func(r *ProjectionRecord) { r.Cutoff.StageProgressionCommandID = nullableUUIDValue(uuid.New()) }},
		{"wrong ordering", func(r *ProjectionRecord) { r.Artifacts[0].Members[0].Position = 2 }},
		{"wrong participant", func(r *ProjectionRecord) { r.Artifacts[0].Members[0].ParticipantID = uuid.New() }},
		{"changed payload", func(r *ProjectionRecord) { r.Artifacts[0].Artifact.Payload = []byte(`{}`) }},
		{"missing dependency", func(r *ProjectionRecord) { r.Artifacts[1].Dependencies = nil }},
		{"wrong dependency", func(r *ProjectionRecord) {
			r.Artifacts[1].Dependencies[0].DependsOnArtifactID = nullableUUIDValue(uuid.New())
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, input, record := progressionPublicationFixture()
			test.mutate(record)
			err := progressionPublishedRecord(plan, input, record)
			if test.name == "complete" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, domain.ErrConflict)
			}
		})
	}
}

func progressionPublicationFixture() (tournamentprogression.Plan, ProjectionPublishInput, *ProjectionRecord) {
	commandID, sourceID := uuid.New(), uuid.New()
	ids, _ := tournamentprogression.PlayoffPublicationIdentity(commandID)
	plan := tournamentprogression.Plan{Record: tournamentprogression.Record{Command: tournamentprogression.Command{CommandID: commandID}, Source: tournamentprogression.ProjectionReference{RevisionID: sourceID, Revision: 4}}}
	input := ProjectionPublishInput{IDs: ProjectionIDs{RevisionID: ids.ProjectionRevisionID, CutoffID: ids.CutoffID}, Scope: ProjectionScope{TournamentID: uuid.New(), RosterID: uuid.New()}}
	record := &ProjectionRecord{Revision: sqlc.ProjectionRevision{ID: ids.ProjectionRevisionID, TournamentID: input.Scope.TournamentID, RosterID: input.Scope.RosterID, RevisionNumber: 5, State: "published", PreviousRevisionID: nullableUUIDValue(sourceID), CutoffID: ids.CutoffID}, Cutoff: sqlc.ProjectionCutoff{ID: ids.CutoffID, StageProgressionCommandID: nullableUUIDValue(commandID)}}
	for _, kind := range []domain.ArtifactKind{domain.ArtifactKindStandings, domain.ArtifactKindTopFour, domain.ArtifactKindBracket} {
		artifactID, participantID, dependencyID, parentID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		artifact := ProjectionArtifactInput{ID: artifactID, Kind: kind, Key: string(kind), Payload: []byte(`{"entries":[1]}`), PayloadDigest: [32]byte{1}, Members: []ProjectionMemberInput{{ParticipantID: participantID, Position: 1}}, Dependencies: []ProjectionDependencyInput{{ID: dependencyID, Kind: projectionDependencyArtifact, DependsOnArtifactID: &parentID}}}
		input.Artifacts = append(input.Artifacts, artifact)
		record.Artifacts = append(record.Artifacts, ProjectionArtifactRecord{Artifact: sqlc.ProjectionArtifact{ID: artifactID, TournamentID: input.Scope.TournamentID, RosterID: input.Scope.RosterID, ProducedByRevisionID: ids.ProjectionRevisionID, ArtifactKind: string(kind), ArtifactKey: string(kind), Payload: artifact.Payload, PayloadDigest: artifact.PayloadDigest[:]}, Members: []sqlc.ProjectionArtifactMember{{ArtifactID: artifactID, ParticipantID: participantID, Position: 1}}, Dependencies: []sqlc.ProjectionDependency{{ID: dependencyID, ArtifactID: artifactID, TournamentID: input.Scope.TournamentID, RosterID: input.Scope.RosterID, DependencyKind: projectionDependencyArtifact, DependsOnArtifactID: nullableUUIDValue(parentID)}}})
	}
	return plan, input, record
}

func (tx *progressionWriteTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	tx.t.Helper()
	require.NotEmpty(tx.t, tx.queries, "unexpected query: %s", query)
	require.Contains(tx.t, strings.Split(query, "\n")[0], tx.queries[0])
	row := tx.rows[0]
	tx.queries, tx.rows = tx.queries[1:], tx.rows[1:]
	return row
}

func TestProgressionWriteIdentityRejectsMissingAndRedirectedWrites(t *testing.T) {
	id := uuid.New()
	for _, row := range []progressionRow{{err: pgx.ErrNoRows}, {values: []any{uuid.New()}}, {values: []any{id}}} {
		tx := &progressionWriteTx{t: t, queries: []string{"CreateTournamentStageProgressionCAS"}, rows: []progressionRow{row}}
		err := createProgressionProof(context.Background(), sqlc.New(tx), sqlc.CreateTournamentStageProgressionCASParams{CommandID: id})
		if row.err == nil && row.values[0] == id {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, domain.ErrConflict)
		}
		require.Empty(t, tx.queries)
	}
}

func TestProgressionWriteMethodsRejectInvalidInput(t *testing.T) {
	repository := NewTournamentProgressionPostgres(&TxManager{})
	_, err := repository.PublishPlayoffStage(context.Background(), tournamentprogression.Plan{})
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = repository.PersistStageProgression(context.Background(), tournamentprogression.Plan{}, nil)
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = repository.LoadGoldenEvidence(context.Background(), tournamentprogression.Authority{})
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestProgressionGoldenDependenciesCoverExactlySealedCommits(t *testing.T) {
	commandID := uuid.New()
	ids, err := tournamentprogression.PlayoffPublicationIdentity(commandID)
	require.NoError(t, err)
	commits := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	dependencies, err := progressionGoldenDependencies(ids, commandID, commits)
	require.NoError(t, err)
	require.Len(t, dependencies, len(commits))
	for i, dependency := range dependencies {
		want, err := ids.GoldenDependencyID(commandID, commits[i])
		require.NoError(t, err)
		require.Equal(t, want, dependency.ID)
		require.Equal(t, projectionDependencyGoldenPosition, dependency.Kind)
		require.Equal(t, commits[i], *dependency.GoldenPositionCommitID)
		require.Nil(t, dependency.DependsOnArtifactID)
	}
	empty, err := progressionGoldenDependencies(ids, commandID, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
	_, err = progressionGoldenDependencies(ids, commandID, []uuid.UUID{commits[0], commits[0]})
	require.ErrorIs(t, err, domain.ErrConflict)
	_, err = progressionGoldenDependencies(ids, commandID, []uuid.UUID{uuid.Nil})
	require.ErrorIs(t, err, domain.ErrValidation)
}
