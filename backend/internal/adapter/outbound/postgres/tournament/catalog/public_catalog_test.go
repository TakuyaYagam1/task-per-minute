package catalog

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestMapPublicTournamentCatalogValuesNormalizesDatabaseTimezone(t *testing.T) {
	t.Parallel()

	offset := time.FixedZone("catalog-offset", 3*60*60)
	createdAt := time.Date(2026, 9, 20, 15, 0, 0, 0, offset)
	record, err := mapPublicTournamentCatalogValues(
		uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		"alpha", "Alpha", "alpha", "tournament_v1", "technical_pause", "live", "swiss",
		8, 4,
		pgtype.Timestamptz{Time: createdAt, Valid: true},
		pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{},
	)

	require.NoError(t, err)
	require.Equal(t, createdAt.UTC(), record.CreatedAt)
	require.Equal(t, time.UTC, record.CreatedAt.Location())
	require.Equal(t, domain.TournamentStateTechnicalPause, record.State)
	require.Equal(t, domain.TournamentStateSwiss, record.Stage)
	require.Equal(t, inbound.PublicTournamentCatalogGroupLive, record.Group)
	require.Nil(t, record.ScheduledAt)
}

func TestPublicCatalogSQLKeepsVisibilityAndKeysetRulesInDatabase(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../../../../../db/queries/tournament_public_catalog.sql")
	require.NoError(t, err)
	sql := string(data)
	for _, fragment := range []string{
		"WHERE tournament.state <> 'draft'",
		"strpos(classified.order_name",
		"strpos(LOWER(classified.public_id)",
		"LIMIT sqlc.arg(page_limit)::INTEGER",
		"NULL::TIMESTAMPTZ AS scheduled_at",
		"visible.tournament_id > sqlc.arg(cursor_id)::UUID",
	} {
		require.Contains(t, sql, fragment, "missing SQL contract fragment %q", fragment)
	}
}
