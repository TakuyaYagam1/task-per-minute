//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// TestConfigurableReserveCountPersistsThroughProductionHTTPAndPostgres keeps
// the operator-facing configuration contract explicit. New tournaments start
// with no reserves, while a pre-start operator may select each supported value
// and read the same value back from both the HTTP projection and PostgreSQL.
func TestConfigurableReserveCountPersistsThroughProductionHTTPAndPostgres(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	prepareCreateToChampionContent(ctx, t)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	content := getTournamentContentThroughREST(t, fixture, adminToken)
	created := createTournamentThroughREST(t, fixture, adminToken, content.ContentRevision, "configurable-reserves")
	path := "/api/v1/admin/tournaments/" + created.Id.String() + "/configuration"

	read := func() api.TournamentConfiguration {
		t.Helper()
		req, resp := doTournamentFlowJSON(
			t, fixture, http.MethodGet, path, "", adminSession(adminToken), uuid.New(), "",
		)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		fixture.validateResponse(t, req, resp)
		return decodeJSON[api.TournamentConfiguration](t, resp)
	}

	configuration := read()
	require.Zero(t, configuration.ReserveCount)

	for _, reserveCount := range []int32{0, 1, 2} {
		body, err := json.Marshal(api.UpdateTournamentConfigurationRequest{
			ExpectedProjectionRevision:    configuration.ProjectionRevision,
			ExpectedConfigurationRevision: configuration.ConfigurationRevision,
			ReserveCount:                  reserveCount,
			Confirmed:                     api.UpdateTournamentConfigurationRequestConfirmed(true),
			Reason:                        "verify configurable assignment reserves",
			SwissDefault: api.TournamentConfigurationStageDefaultInput{
				Mode: configuration.SwissDefault.Mode, Categories: configuration.SwissDefault.Categories,
			},
			SemifinalDefault: api.TournamentConfigurationStageDefaultInput{
				Mode: configuration.SemifinalDefault.Mode, Categories: configuration.SemifinalDefault.Categories,
			},
			UnlockIntents: []api.ConfigurationUnlockIntent{},
		})
		require.NoError(t, err)
		req, resp := doTournamentFlowJSON(
			t, fixture, http.MethodPatch, path, string(body), adminSession(adminToken), uuid.New(), "",
		)
		require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
		fixture.validateResponse(t, req, resp)

		configuration = read()
		require.Equal(t, reserveCount, configuration.ReserveCount)

		var persisted int16
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT reserve_count
			FROM tournament_content_configurations
			WHERE tournament_id = $1 AND state = 'published'`, created.Id).Scan(&persisted))
		require.Equal(t, reserveCount, int32(persisted))
	}
}

// TestConfigurableReserveCountBuildsVariableNormalChains exercises the
// production PostgreSQL assignment adapter. Every branch receives exactly one
// primary plus the configured number of immutable reserve edges, and the
// persisted aggregate retains that configuration after a fresh read.
func TestConfigurableReserveCountBuildsVariableNormalChains(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(context.Background(), t) })

	draft := createDraftMigrationFixture(ctx, t)
	repository := assignmentrepo.NewAssignmentPostgres(postgres.NewTxManager(sharedPool))
	createdAt := draft.createdAt.Add(5 * time.Second)

	for _, reserveCount := range []int{0, 1, 2} {
		t.Run("reserve_count_"+string(rune('0'+reserveCount)), func(t *testing.T) {
			planID := uuid.New()
			_, err := repository.CreateConservativePlan(ctx, assignmentrepo.ConservativePlanInput{
				ID: planID, TournamentID: draft.tournamentID, RosterID: draft.rosterID,
				ReserveCount: reserveCount, RevisionID: uuid.New(), SourceRosterRevision: 1,
				SourcePoolRevisionID: draft.normalPoolRevisionID,
				ConstraintGraph:      map[string]any{"reserve_count": reserveCount},
				ProofEvidence:        map[string]any{"certified": true}, CreatedAt: createdAt,
			})
			require.NoError(t, err)

			branch := configurableReserveBranch(ctx, t, draft, reserveCount, "normal-reserve")
			exactID := uuid.New()
			decision, err := domain.NewDecisionEvidence(
				uuid.New(), domain.DecisionPurposeTask, domain.DecisionAlgorithmV1,
				[]string{branch.Key}, exactID, createdAt,
			)
			require.NoError(t, err)
			exact, err := repository.CreateExactPlan(ctx, assignmentrepo.ExactPlanInput{
				ID: exactID, TournamentID: draft.tournamentID, RosterID: draft.rosterID,
				ReserveCount: reserveCount, ParentPlanID: planID, RevisionID: uuid.New(),
				SourceRosterRevision: 1, SourcePoolRevisionID: draft.normalPoolRevisionID,
				SourceDraftRevision: draft.initialRevisionID,
				ConstraintGraph:     map[string]any{"reserve_count": reserveCount},
				ProofEvidence:       map[string]any{"certified": true}, DecisionEvidence: decision,
				Branches: []assignmentrepo.AssignmentBranchInput{branch}, CreatedAt: createdAt,
			})
			require.NoError(t, err)
			require.Equal(t, reserveCount, exact.Plan.ReserveCount)
			require.Len(t, exact.Edges, reserveCount+1)
			require.Len(t, exact.Reservations, reserveCount+1)
			require.Len(t, exact.Snapshots, reserveCount+1)

			reloaded, err := repository.GetPlan(ctx, exactID)
			require.NoError(t, err)
			require.Equal(t, reserveCount, reloaded.Plan.ReserveCount)
			require.Len(t, reloaded.Edges, reserveCount+1)
			require.Len(t, reloaded.Reservations, reserveCount+1)
			require.Len(t, reloaded.Snapshots, reserveCount+1)
		})
	}
}

func configurableReserveBranch(
	ctx context.Context,
	t testing.TB,
	draft draftMigrationFixture,
	reserveCount int,
	key string,
) assignmentrepo.AssignmentBranchInput {
	t.Helper()
	branch := assignmentrepo.AssignmentBranchInput{
		ID: uuid.New(), DraftID: draft.draftID, DraftRevisionID: draft.initialRevisionID,
		Key: key, Categories: []domain.Category{domain.CategoryWeb},
	}
	for position := 1; position <= reserveCount+1; position++ {
		var taskID uuid.UUID
		var taskVersion int
		err := sharedPool.QueryRow(ctx, `
			SELECT membership.task_id, membership.task_version
			FROM task_pool_version_memberships AS membership
			INNER JOIN tasks AS task ON task.id = membership.task_id
			INNER JOIN LATERAL (
				SELECT attestation.healthy
				FROM task_version_health_attestations AS attestation
				WHERE attestation.task_id = membership.task_id
					AND attestation.task_version = membership.task_version
				ORDER BY attestation.revision DESC
				LIMIT 1
			) AS health ON health.healthy
			WHERE membership.task_pool_revision_id = $1
				AND task.category = 'web' AND task.enabled AND task.deleted_at IS NULL
				AND NOT EXISTS (
					SELECT 1
					FROM task_version_reservations AS reservation
					WHERE reservation.tournament_id = $2
						AND reservation.task_id = membership.task_id
						AND reservation.task_version = membership.task_version
						AND reservation.state IN ('reserved', 'committed')
				)
			ORDER BY membership.task_id
			OFFSET $3 LIMIT 1`, draft.normalPoolRevisionID, draft.tournamentID, position-1).Scan(&taskID, &taskVersion)
		require.NoError(t, err)
		snapshot := domain.AssignmentTaskSnapshot{
			SnapshotID: uuid.New(), TaskID: taskID, Version: taskVersion,
			Kind: domain.AssignmentTaskKindNormal, Title: key + " task",
			Description: "configurable reserve integration snapshot", Category: domain.CategoryWeb,
			Difficulty: domain.DifficultyMedium, TimeLimit: 60,
			Flag: "FLAG{configurable-reserve}", Hints: []string{"hint"},
		}
		require.NoError(t, snapshot.Validate())
		branch.Edges = append(branch.Edges, assignmentrepo.AssignmentEdgeInput{
			ID: uuid.New(), ReservationID: uuid.New(), Position: position, Snapshot: snapshot,
			ContentDigest:     sha256.Sum256([]byte(snapshot.SnapshotID.String())),
			SelectionEvidence: map[string]any{"position": position, "eligible": true},
		})
	}
	return branch
}
