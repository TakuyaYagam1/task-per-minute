package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestArenaPublicHandlers(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("09f586f5-8e1f-42df-a900-51707b09c4ef")
	seriesID := uuid.MustParse("ed365411-08d9-4c1a-9b7c-2bc635e0bf1f")
	startedAt := time.Date(2026, 9, 2, 13, 0, 0, 0, time.UTC)

	t.Run("returns only the public tournament projection", func(t *testing.T) {
		service := &arenaPublicServiceStub{
			arenaAdminServiceStub: &arenaAdminServiceStub{},
			tournamentResult: api.ArenaPublicTournamentResponse{
				TournamentId: tournamentID, Preset: api.ArenaV1, State: api.ArenaTournamentStateSwiss,
				RosterSize: 8, StartedAt: &startedAt, ProjectionRevision: 14,
			},
		}
		controller := newArenaAdminController(service)
		recorder := httptest.NewRecorder()

		controller.GetArenaPublicTournament(
			recorder,
			httptest.NewRequest(http.MethodGet, "/api/v1/arena/public/tournaments/"+tournamentID.String(), nil),
			tournamentID,
		)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.tournamentCalls)
		require.Equal(t, tournamentID, service.tournamentCommand.TournamentID)
		var body map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		require.ElementsMatch(t,
			[]string{"tournament_id", "preset", "state", "roster_size", "started_at", "finished_at", "projection_revision"},
			mapKeys(body),
		)
		require.NotContains(t, recorder.Body.String(), "player_id")
		require.NotContains(t, recorder.Body.String(), "task")
	})

	t.Run("returns allowlisted scoreboard and bracket projections", func(t *testing.T) {
		service := &arenaPublicServiceStub{arenaAdminServiceStub: &arenaAdminServiceStub{
			standingsResult: api.ArenaPublicScoreboardResponse{
				TournamentId: tournamentID, ProjectionRevision: 15,
				Entries: []api.ArenaPublicScoreboardEntry{{Rank: 1, DisplayName: "red", Points: 3, Buchholz: 2, EffectiveTimeMs: 4100}},
			},
			bracketResult: api.ArenaPublicBracketResponse{
				TournamentId: tournamentID, ProjectionRevision: 16,
				Matches: []api.ArenaPublicBracketMatch{{
					Stage: api.ArenaPublicBracketMatchStageSemifinal, Position: 1, FirstDisplayName: "red", SecondDisplayName: "blue",
					Score: api.ArenaSeriesScore{FirstParticipantWins: 1, SecondParticipantWins: 0}, State: api.ArenaSeriesStateActive,
				}},
			},
		}}
		controller := newArenaAdminController(service)

		scoreboard := httptest.NewRecorder()
		controller.GetArenaPublicScoreboard(scoreboard, httptest.NewRequest(http.MethodGet, "/scoreboard", nil), tournamentID)
		bracket := httptest.NewRecorder()
		controller.GetArenaPublicBracket(bracket, httptest.NewRequest(http.MethodGet, "/bracket", nil), tournamentID)

		require.Equal(t, http.StatusOK, scoreboard.Code)
		require.Equal(t, http.StatusOK, bracket.Code)
		require.NotContains(t, scoreboard.Body.String(), "player_id")
		require.NotContains(t, bracket.Body.String(), "participant_id")
		require.Equal(t, 1, service.standingsCalls)
		require.Equal(t, 1, service.bracketCalls)
	})

	t.Run("returns the public live draft without private assignment data", func(t *testing.T) {
		service := &arenaPublicServiceStub{
			arenaAdminServiceStub: &arenaAdminServiceStub{},
			liveDraftResult: api.ArenaPublicLiveDraftResponse{
				TournamentId: tournamentID, SeriesId: seriesID, ProjectionRevision: 17,
				Format: api.Bo1, State: api.ArenaDraftStateActive,
				Pool: []api.ArenaCategory{api.ArenaCategoryWeb, api.ArenaCategoryCrypto},
				Actions: []api.ArenaPublicDraftAction{{
					Turn: 1, ActorDisplayName: "red", Action: api.Ban, Category: api.ArenaCategoryCrypto, OccurredAt: startedAt,
				}},
				SelectedCategories: []api.ArenaCategory{api.ArenaCategoryWeb},
			},
		}
		controller := newArenaAdminController(service)
		recorder := httptest.NewRecorder()

		controller.GetArenaPublicLiveDraft(recorder, httptest.NewRequest(http.MethodGet, "/live-draft", nil), tournamentID)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.liveDraftCalls)
		require.Equal(t, tournamentID, service.liveDraftCommand.TournamentID)
		require.NotContains(t, recorder.Body.String(), "actor_id")
		require.NotContains(t, recorder.Body.String(), "assignment")
		require.NotContains(t, recorder.Body.String(), "flag")
	})

	t.Run("returns the public recovery snapshot with the live series projection", func(t *testing.T) {
		cursor := api.ArenaPublicCursor{ProjectionRevision: 16, EventSequence: 44}
		service := &arenaPublicServiceStub{
			arenaAdminServiceStub: &arenaAdminServiceStub{},
			snapshotResult: api.ArenaPublicRecoverySnapshot{
				Tournament: api.ArenaPublicTournamentResponse{
					TournamentId: tournamentID, Preset: api.ArenaV1, State: api.ArenaTournamentStateSwiss,
					RosterSize: 8, StartedAt: &startedAt, ProjectionRevision: 18,
				},
				Scoreboard: api.ArenaPublicScoreboardResponse{
					TournamentId: tournamentID, ProjectionRevision: 18,
					Entries: []api.ArenaPublicScoreboardEntry{{Rank: 1, DisplayName: "red", Points: 3}},
				},
				Bracket: api.ArenaPublicBracketResponse{
					TournamentId: tournamentID, ProjectionRevision: 18,
					Matches: []api.ArenaPublicBracketMatch{{
						Stage: api.ArenaPublicBracketMatchStageSemifinal, Position: 1,
						FirstDisplayName: "red", SecondDisplayName: "blue", State: api.ArenaSeriesStateActive,
					}},
				},
				LiveDraft: &api.ArenaPublicLiveDraftResponse{
					TournamentId: tournamentID, SeriesId: seriesID, ProjectionRevision: 18,
					Format: api.Bo1, State: api.ArenaDraftStateActive,
				},
				NextCursor: api.ArenaPublicRecoveryCursor{ProjectionRevision: 18, EventSequence: 47},
			},
		}
		controller := newArenaAdminController(service)
		recorder := httptest.NewRecorder()

		controller.GetArenaPublicSnapshot(
			recorder,
			httptest.NewRequest(http.MethodGet, "/snapshot", nil),
			tournamentID,
			api.GetArenaPublicSnapshotParams{Cursor: &cursor},
		)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.snapshotCalls)
		require.Equal(t, tournamentID, service.snapshotTournamentID)
		require.Equal(t, &cursor, service.snapshotCursor)
		var body map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		require.ElementsMatch(t, []string{"tournament", "scoreboard", "bracket", "live_draft", "next_cursor"}, mapKeys(body))
		require.NotContains(t, recorder.Body.String(), "participant_id")
		require.NotContains(t, recorder.Body.String(), "task")
		require.NotContains(t, recorder.Body.String(), "flag")
	})
}

type arenaPublicServiceStub struct {
	*arenaAdminServiceStub

	tournamentCalls   int
	tournamentCommand ArenaTournamentReadCommand
	tournamentResult  api.ArenaPublicTournamentResponse
	tournamentErr     error

	liveDraftCalls   int
	liveDraftCommand ArenaTournamentReadCommand
	liveDraftResult  api.ArenaPublicLiveDraftResponse
	liveDraftErr     error

	snapshotCalls        int
	snapshotTournamentID api.ArenaTournamentId
	snapshotCursor       *api.ArenaPublicCursor
	snapshotResult       api.ArenaPublicRecoverySnapshot
	snapshotErr          error
}

func (s *arenaPublicServiceStub) GetPublicTournament(
	_ context.Context,
	command ArenaTournamentReadCommand,
) (api.ArenaPublicTournamentResponse, error) {
	s.tournamentCalls++
	s.tournamentCommand = command
	return s.tournamentResult, s.tournamentErr
}

func (s *arenaPublicServiceStub) GetLiveDraft(
	_ context.Context,
	command ArenaTournamentReadCommand,
) (api.ArenaPublicLiveDraftResponse, error) {
	s.liveDraftCalls++
	s.liveDraftCommand = command
	return s.liveDraftResult, s.liveDraftErr
}

func (s *arenaPublicServiceStub) GetPublicSnapshot(
	_ context.Context,
	tournamentID api.ArenaTournamentId,
	cursor *api.ArenaPublicCursor,
) (api.ArenaPublicRecoverySnapshot, error) {
	s.snapshotCalls++
	s.snapshotTournamentID = tournamentID
	s.snapshotCursor = cursor
	return s.snapshotResult, s.snapshotErr
}

func mapKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	return keys
}
