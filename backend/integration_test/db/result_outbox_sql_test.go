//go:build integration

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestResultOutboxSQLAcceptsUUIDIdentity(t *testing.T) {
	_, err := sqlc.New(postgresPool).CreateResultOutboxEvent(context.Background(), sqlc.CreateResultOutboxEventParams{
		TournamentID: uuid.New(), RosterID: uuid.New(), ProjectionRevisionID: uuid.New(), ProjectionRevision: 1,
		ID: uuid.New(), IdempotencyKey: uuid.New(), SeriesID: uuid.New(), ResultEventID: uuid.New(), ProjectionEvidenceID: uuid.New(),
		Topic: "tournament.result.committed", Payload: []byte(`{}`), CreatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	// A valid UUID for an absent tournament yields no allocation, not a SQL
	// parameter-inference error before the scope guard can run.
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestChampionOutboxSQLAcceptsUUIDIdentity(t *testing.T) {
	_, err := sqlc.New(postgresPool).CreateFinalChampionOutboxEvent(context.Background(), sqlc.CreateFinalChampionOutboxEventParams{
		TournamentID: uuid.New(), RosterID: uuid.New(), ProjectionRevisionID: uuid.New(), ProjectionRevision: 1,
		ID: uuid.New(), IdempotencyKey: uuid.New(), FinalSeriesID: uuid.New(), FinalResultRevisionID: uuid.New(), ChampionArtifactID: uuid.New(),
		Payload: []byte(`{}`), CreatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}
