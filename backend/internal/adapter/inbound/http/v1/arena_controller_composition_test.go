package v1

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestArenaControllerComposition(t *testing.T) {
	t.Parallel()

	t.Run("generated nullable references remain pointers", func(t *testing.T) {
		requirePointerFields(t, api.ArenaTournament{}, "PausedFromState")
		requirePointerFields(t, api.ArenaSwissRound{}, "Bye")
		requirePointerFields(t, api.ArenaGame{}, "ResultReason")
		requirePointerFields(t, api.ArenaOfficialResultRevision{}, "GameState", "GameReason", "SeriesState", "SeriesReason")
		requirePointerFields(t, api.ArenaWave{}, "ReadyWindow")
		requirePointerFields(t, api.ArenaPauseGame{}, "ResumeState")
		requirePointerFields(t, api.ArenaPauseSeries{}, "ResumeState")
		requirePointerFields(t, api.ArenaPauseGraph{}, "Draft")
		requirePointerFields(t, api.ArenaAuditPage{}, "NextCursor")
		requirePointerFields(t, api.ArenaPublicRecoverySnapshot{}, "LiveDraft")
		requirePointerFields(t, api.ArenaParticipantRecoverySnapshot{}, "Series", "Wave", "Draft", "Assignment")
		requirePointerFields(t, api.ArenaOperatorRecoverySnapshot{}, "PauseGraph")
	})

	t.Run("generated document contains the validated Arena surface", func(t *testing.T) {
		document, err := api.GetSwagger()
		require.NoError(t, err)
		require.NoError(t, document.Validate(t.Context()))

		arenaPaths := 0
		for path := range document.Paths.Map() {
			if strings.HasPrefix(path, "/api/v1/arena/") {
				arenaPaths++
			}
		}
		require.Equal(t, 29, arenaPaths)
	})

	t.Run("each generated Arena operation belongs to one controller port", func(t *testing.T) {
		generated := reflect.TypeOf((*api.ServerInterface)(nil)).Elem()
		generatedArena := make([]string, 0, 31)
		for index := range generated.NumMethod() {
			method := generated.Method(index)
			if strings.Contains(method.Name, "Arena") {
				generatedArena = append(generatedArena, method.Name)
			}
		}

		ownedArena := make([]string, 0, 31)
		seen := make(map[string]struct{}, 31)
		for _, controller := range []reflect.Type{
			reflect.TypeOf((*ArenaOperatorController)(nil)).Elem(),
			reflect.TypeOf((*ArenaParticipantController)(nil)).Elem(),
			reflect.TypeOf((*ArenaPublicController)(nil)).Elem(),
		} {
			for index := range controller.NumMethod() {
				method := controller.Method(index)
				_, duplicate := seen[method.Name]
				require.Falsef(t, duplicate, "Arena operation %s is exposed by more than one controller port", method.Name)
				seen[method.Name] = struct{}{}
				ownedArena = append(ownedArena, method.Name)
			}
		}

		require.Len(t, generatedArena, 31)
		require.ElementsMatch(t, generatedArena, ownedArena)
	})

	operator := &arenaOperatorControllerStub{}
	participant := &arenaParticipantControllerStub{}
	public := &arenaPublicControllerStub{}
	middlewareCalls := 0
	handler := NewHandler(New(Dependencies{
		ArenaOperator:    operator,
		ArenaParticipant: participant,
		ArenaPublic:      public,
	}), HandlerOptions{
		Middlewares: []api.MiddlewareFunc{
			func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					middlewareCalls++
					next.ServeHTTP(w, r)
				})
			},
		},
	})

	tournamentID := "6fd7411a-06f4-4cbf-a19e-9b60cb3a6330"
	requests := []struct {
		name string
		path string
		port string
	}{
		{
			name: "operator",
			path: "/api/v1/arena/operator/tournaments",
			port: "operator",
		},
		{
			name: "participant",
			path: "/api/v1/arena/tournaments/" + tournamentID + "/participant/lobby",
			port: "participant",
		},
		{
			name: "public",
			path: "/api/v1/arena/public/tournaments/" + tournamentID,
			port: "public",
		},
	}

	for _, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, request.path, nil))

			require.Equal(t, http.StatusNoContent, recorder.Code)
			require.Equal(t, request.port, recorder.Header().Get("X-Arena-Port"))
		})
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, len(requests)+1, middlewareCalls)
	require.Equal(t, 1, operator.calls)
	require.Equal(t, 1, participant.calls)
	require.Equal(t, 1, public.calls)

	fallbackHandler := NewHandler(New(Dependencies{}), HandlerOptions{})
	for _, request := range requests {
		t.Run(request.name+" fallback", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			fallbackHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, request.path, nil))

			require.Equal(t, http.StatusNotImplemented, recorder.Code)
		})
	}
}

func requirePointerFields(t *testing.T, model any, names ...string) {
	t.Helper()

	modelType := reflect.TypeOf(model)
	for _, name := range names {
		field, ok := modelType.FieldByName(name)
		require.Truef(t, ok, "%s.%s is missing", modelType.Name(), name)
		require.Equalf(t, reflect.Pointer, field.Type.Kind(), "%s.%s must represent nullable OpenAPI data", modelType.Name(), name)
	}
}

type arenaOperatorControllerStub struct {
	api.Unimplemented

	calls int
}

func (s *arenaOperatorControllerStub) ListArenaOperatorTournaments(
	w http.ResponseWriter,
	_ *http.Request,
	_ api.ListArenaOperatorTournamentsParams,
) {
	s.calls++
	w.Header().Set("X-Arena-Port", "operator")
	w.WriteHeader(http.StatusNoContent)
}

type arenaParticipantControllerStub struct {
	api.Unimplemented

	calls int
}

func (s *arenaParticipantControllerStub) GetArenaParticipantLobby(
	w http.ResponseWriter,
	_ *http.Request,
	_ openapi_types.UUID,
) {
	s.calls++
	w.Header().Set("X-Arena-Port", "participant")
	w.WriteHeader(http.StatusNoContent)
}

type arenaPublicControllerStub struct {
	api.Unimplemented

	calls int
}

func (s *arenaPublicControllerStub) GetArenaPublicTournament(
	w http.ResponseWriter,
	_ *http.Request,
	_ openapi_types.UUID,
) {
	s.calls++
	w.Header().Set("X-Arena-Port", "public")
	w.WriteHeader(http.StatusNoContent)
}
