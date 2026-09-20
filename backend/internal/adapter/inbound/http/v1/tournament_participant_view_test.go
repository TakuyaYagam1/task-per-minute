package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestParticipantLobbyResponseIncludesAuthoritativeFields(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	participantID := uuid.New()
	round := 2
	view := usecase.LobbyView{
		TournamentID:       tournamentID,
		ParticipantID:      participantID,
		Attendance:         domain.AttendanceStateCheckedIn,
		CurrentSwissRound:  &round,
		SwissPoints:        3,
		Status:             usecase.LobbyStatusAssigned,
		RequiredAction:     usecase.LobbyRequiredActionPlay,
		ProjectionRevision: 4,
		State:              domain.TournamentStateSwiss,
	}

	lobby, err := participantLobbyResponse(view)

	require.NoError(t, err)
	require.Equal(t, api.CheckedIn, lobby.Attendance)
	require.Equal(t, participantID, lobby.ParticipantId)
	require.Equal(t, int32(round), *lobby.CurrentSwissRound)
	require.Equal(t, int32(3), lobby.SwissPoints)
	require.Equal(t, api.ParticipantLobbyStatusAssigned, lobby.Status)
	require.Equal(t, api.ParticipantLobbyRequiredActionPlay, lobby.RequiredAction)

	recovery, err := participantRecoveryResponse(usecase.RecoveryView{
		TournamentID:            tournamentID,
		ParticipantID:           participantID,
		ProjectionRevision:      4,
		ParticipantViewRevision: 5,
		EventSequence:           6,
		Lobby:                   view,
	})
	require.NoError(t, err)
	require.Equal(t, lobby, recovery.Lobby)
}

func TestParticipantRuntimeResponseIncludesPauseReconnectAndOfficialOutcome(t *testing.T) {
	t.Parallel()

	participantID := uuid.New()
	opponentID := uuid.New()
	gameID := uuid.New()
	pauseID := uuid.New()
	intervalID := uuid.New()
	resultRevisionID := uuid.New()
	winnerID := opponentID
	frozenAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	deadline := frozenAt.Add(45 * time.Second)
	disconnectedAt := frozenAt
	runtime, err := participantRuntimeResponse(usecase.ParticipantGameView{
		GameID:           gameID,
		State:            string(domain.GameStateVoid),
		Revision:         7,
		ResultReason:     string(domain.GameResultReasonDisconnect),
		WinnerID:         &winnerID,
		ResultRevisionID: &resultRevisionID,
		Pause: &usecase.ParticipantGamePauseView{
			PauseID:           pauseID,
			State:             "active",
			Reason:            "disconnect",
			FrozenAt:          frozenAt,
			FrozenRemainingMS: 45_000,
			ReconnectDeadline: &deadline,
		},
		Presence: []usecase.ParticipantPresenceView{
			{ParticipantID: participantID, State: "disconnected", PresenceEpoch: 2, Revision: 3, ConnectedAt: frozenAt.Add(-time.Minute), DisconnectedAt: &disconnectedAt, UpdatedAt: frozenAt},
			{ParticipantID: opponentID, State: "connected", PresenceEpoch: 1, Revision: 1, ConnectedAt: frozenAt.Add(-time.Minute), UpdatedAt: frozenAt},
		},
		Reconnect: []usecase.ParticipantReconnectView{
			{ID: intervalID, PauseID: pauseID, ParticipantID: participantID, PresenceEpoch: 2, Number: 1, State: "open", OpenedAt: frozenAt, Deadline: deadline, Revision: 1, UpdatedAt: frozenAt},
		},
	})

	require.NoError(t, err)
	require.Equal(t, api.GameStateVoid, runtime.GameState)
	require.NotNil(t, runtime.ResultReason)
	require.Equal(t, api.GameResultReasonDisconnect, *runtime.ResultReason)
	require.Equal(t, resultRevisionID, *runtime.ResultRevisionId)
	require.Equal(t, opponentID, *runtime.WinnerId)
	require.NotNil(t, runtime.Pause)
	require.Equal(t, api.PauseReasonDisconnect, runtime.Pause.Reason)
	require.Equal(t, deadline, *runtime.Pause.ReconnectDeadline)
	require.Len(t, runtime.Presence, 2)
	require.Equal(t, api.Disconnected, runtime.Presence[0].State)
	require.Len(t, runtime.Reconnect, 1)
	require.Equal(t, api.ReconnectStateOpen, runtime.Reconnect[0].State)
	require.Equal(t, deadline, runtime.Reconnect[0].Deadline)
}

func TestParticipantAssignmentResponseIncludesAuthoritativeContext(t *testing.T) {
	t.Parallel()

	assignmentID := uuid.New()
	participantID := uuid.New()
	waveID := uuid.New()
	seriesID := uuid.New()
	gameID := uuid.New()
	startedAt := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(120 * time.Second)
	view := usecase.TournamentParticipantAssignmentView{
		AssignmentID:  assignmentID,
		AttemptID:     uuid.New(),
		ParticipantID: participantID,
		SeriesID:      seriesID,
		GameID:        gameID,
		WaveID:        waveID,
		Context: usecase.ParticipantAssignmentContextView{
			WaveID:            waveID,
			SeriesID:          seriesID,
			SlotID:            uuid.New(),
			GameID:            gameID,
			Stage:             domain.TournamentStageFinal,
			GameNumber:        2,
			SeriesScore:       domain.SeriesScore{FirstParticipantWins: 1, SecondParticipantWins: 0},
			GameState:         domain.GameStateActive,
			StartedAt:         &startedAt,
			EffectiveDeadline: &deadline,
		},
		ActiveSnapshot: usecase.TaskSnapshotView{
			SnapshotID: uuid.New(), TaskID: uuid.New(), Version: 3,
			Kind: domain.AssignmentTaskKindNormal, Title: "web task",
			Description: "solve the disclosed service", Category: domain.CategoryWeb,
			Difficulty: domain.DifficultyMedium, TimeLimit: 120,
		},
	}

	assignment, err := participantAssignment(view)

	require.NoError(t, err)
	require.Equal(t, view.Context.WaveID, assignment.Context.WaveId)
	require.Equal(t, view.Context.SeriesID, assignment.Context.SeriesId)
	require.Equal(t, view.Context.SlotID, assignment.Context.SlotId)
	require.Equal(t, view.Context.GameID, assignment.Context.GameId)
	require.Equal(t, api.ParticipantAssignmentContextStageFinal, assignment.Context.Stage)
	require.Equal(t, int32(view.Context.GameNumber), assignment.Context.GameNumber)
	require.Equal(t, int32(1), assignment.Context.SeriesScore.FirstParticipantWins)
	require.Equal(t, int32(0), assignment.Context.SeriesScore.SecondParticipantWins)
	require.Equal(t, api.GameStateActive, assignment.Context.GameState)
	require.Equal(t, startedAt, *assignment.Context.StartedAt)
	require.Equal(t, deadline, *assignment.Context.EffectiveDeadline)
	require.Nil(t, assignment.Context.SwissRound)
}

func TestParticipantWaveResponseRedactsSeriesForAuthoritativeBye(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	waveID := uuid.New()
	byeID := uuid.New()
	pairedID := uuid.New()
	seriesID := uuid.New()
	view := usecase.WaveView{
		Wave: domain.Wave{
			ID:           waveID,
			TournamentID: tournamentID,
			RevisionID:   domain.WaveRevisionID(uuid.New()),
			State:        domain.WaveStatePlanned,
			Members: []domain.WaveMember{
				{ParticipantID: byeID},
				{ParticipantID: pairedID},
			},
		},
		Revision:           1,
		ReadinessRevisions: map[uuid.UUID]int64{byeID: 1, pairedID: 1},
		SeriesIDs:          map[uuid.UUID]uuid.UUID{pairedID: seriesID},
		ByeParticipantID:   &byeID,
	}

	wave, err := participantWaveResponse(view)

	require.NoError(t, err)
	require.Len(t, wave.Members, 2)
	require.Nil(t, wave.Members[0].SeriesId)
	require.Equal(t, seriesID, *wave.Members[1].SeriesId)
}

func TestParticipantDraftResponseIncludesAuthoritativeTurnAndAutomaticEvidence(t *testing.T) {
	t.Parallel()

	draftID := uuid.New()
	firstParticipantID := uuid.New()
	secondParticipantID := uuid.New()
	deadline := time.Date(2026, 9, 6, 10, 0, 15, 0, time.UTC)
	evidence, err := domain.NewDecisionEvidence(
		uuid.New(), domain.DecisionPurposeCategory, domain.DecisionAlgorithmV1,
		[]string{string(domain.CategoryWeb), string(domain.CategoryCrypto)}, draftID, deadline,
	)
	require.NoError(t, err)
	actionCategory := domain.Category(evidence.Result[0])

	view, err := participantDraftResponse(usecase.DraftExecutionView{
		ID:                  draftID,
		SeriesID:            uuid.New(),
		Format:              domain.SeriesFormatBO1,
		FirstParticipantID:  firstParticipantID,
		SecondParticipantID: secondParticipantID,
		Pool:                []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn},
		State:               usecase.DraftExecutionState("active"),
		Revision:            2,
		Turn:                2,
		CurrentActorID:      &secondParticipantID,
		CurrentAction:       draftActionPointer(domain.DraftActionBan),
		TurnDeadline:        deadline,
		LegalCategories:     []domain.Category{domain.CategoryCrypto, domain.CategoryPwn},
		Actions: []usecase.DraftActionRecordView{{
			Turn:              1,
			ActorID:           firstParticipantID,
			Action:            domain.DraftActionBan,
			Category:          actionCategory,
			OccurredAt:        deadline.Add(-time.Second),
			ScheduledDeadline: deadline,
			Automatic:         true,
			DecisionEvidence:  &evidence,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, secondParticipantID, *view.CurrentActorId)
	require.Equal(t, api.Ban, *view.CurrentAction)
	require.Equal(t, deadline, *view.TurnDeadline)
	require.Equal(t, []api.Category{api.CategoryCrypto, api.CategoryPwn}, view.LegalCategories)
	require.True(t, view.Actions[0].Automatic)
	require.NotNil(t, view.Actions[0].DecisionEvidence)
	require.Equal(t, evidence.ID, view.Actions[0].DecisionEvidence.Id)
	require.Equal(t, api.DraftDecisionEvidencePurposeCategory, view.Actions[0].DecisionEvidence.Purpose)
	require.Equal(t, evidence.OwnerID, view.Actions[0].DecisionEvidence.OwnerId)
	require.Equal(t, evidence.DecidedAt, view.Actions[0].DecisionEvidence.DecidedAt)
	require.Equal(t, evidence.Result, view.Actions[0].DecisionEvidence.Result)
}

func TestParticipantDraftResponseDisablesPersistedNonActiveStates(t *testing.T) {
	t.Parallel()

	firstParticipantID := uuid.New()
	secondParticipantID := uuid.New()
	states := []struct {
		name  string
		state usecase.DraftExecutionState
	}{
		{name: "paused", state: usecase.DraftExecutionState("paused")},
		{name: "recovery required", state: usecase.DraftExecutionState("recovery_required")},
		{name: "completed", state: usecase.DraftExecutionState("completed")},
		{name: "superseded", state: usecase.DraftExecutionState("superseded")},
	}
	for _, testCase := range states {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			currentActorID := &firstParticipantID
			currentAction := draftActionPointer(domain.DraftActionBan)
			turnDeadline := time.Date(2026, 9, 6, 10, 0, 15, 0, time.UTC)
			legalCategories := []domain.Category{domain.CategoryCrypto}
			if testCase.state == usecase.DraftExecutionState("completed") || testCase.state == usecase.DraftExecutionState("superseded") {
				currentActorID = nil
				currentAction = nil
				turnDeadline = time.Time{}
				legalCategories = nil
			}
			view, err := participantDraftResponse(usecase.DraftExecutionView{
				ID:                  uuid.New(),
				SeriesID:            uuid.New(),
				Format:              domain.SeriesFormatBO1,
				FirstParticipantID:  firstParticipantID,
				SecondParticipantID: secondParticipantID,
				Pool:                []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn},
				State:               testCase.state,
				Revision:            3,
				Turn:                2,
				CurrentActorID:      currentActorID,
				CurrentAction:       currentAction,
				TurnDeadline:        turnDeadline,
				LegalCategories:     legalCategories,
			})
			require.NoError(t, err)
			require.Nil(t, view.CurrentActorId)
			require.Nil(t, view.CurrentAction)
			require.Nil(t, view.TurnDeadline)
			require.Empty(t, view.LegalCategories)
			require.Equal(t, api.DraftState(testCase.state), view.State)
		})
	}
}

func TestSubmitParticipantDraftActionRequiresAuthenticatedActor(t *testing.T) {
	t.Parallel()

	server := New(Dependencies{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001/participant/series/20000000-0000-0000-0000-000000000002/draft/actions", nil)

	server.SubmitParticipantDraftAction(
		recorder,
		request,
		uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		uuid.MustParse("20000000-0000-0000-0000-000000000002"),
		api.SubmitParticipantDraftActionParams{},
	)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestAdminPausedDraftResponseSerializesRequiredLegalCategories(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	view, err := adminPausedDraftResponse(usecase.AdminDraftView{
		ID:                  uuid.New(),
		SeriesID:            uuid.New(),
		FirstParticipantID:  uuid.New(),
		SecondParticipantID: uuid.New(),
		Format:              domain.SeriesFormatBO1,
		Pool:                []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse},
		State:               domain.DraftStateActive,
		Turn:                1,
		TurnDeadline:        &deadline,
		Revision:            1,
	})
	require.NoError(t, err)
	require.NotNil(t, view.LegalCategories)
	require.Empty(t, view.LegalCategories)
}

func draftActionPointer(value domain.DraftActionType) *domain.DraftActionType {
	return &value
}
