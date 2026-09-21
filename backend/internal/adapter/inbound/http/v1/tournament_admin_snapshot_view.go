package v1

import (
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func tournamentOperatorSnapshotResponse(
	view inbound.AdminOperatorSnapshotView,
) (api.OperatorRecoverySnapshot, error) {
	tournament, err := tournamentResponse(view.Tournament)
	if err != nil {
		return api.OperatorRecoverySnapshot{}, err
	}
	roster, err := tournamentRosterResponse(view.Roster)
	if err != nil {
		return api.OperatorRecoverySnapshot{}, err
	}
	waves := make([]api.Wave, len(view.Waves))
	for index, wave := range view.Waves {
		waves[index], err = tournamentWaveResponse(wave)
		if err != nil {
			return api.OperatorRecoverySnapshot{}, err
		}
	}
	series := make([]api.Series, len(view.Series))
	for index, item := range view.Series {
		series[index], err = participantSeriesResponse(item)
		if err != nil {
			return api.OperatorRecoverySnapshot{}, err
		}
	}
	pauseGraph, err := tournamentPauseGraphResponse(view.PauseGraph)
	if err != nil {
		return api.OperatorRecoverySnapshot{}, err
	}
	recoveryControls, err := tournamentRecoveryControlsResponse(view.RecoveryControls)
	if err != nil {
		return api.OperatorRecoverySnapshot{}, err
	}
	return api.OperatorRecoverySnapshot{
		Tournament: tournament, Roster: roster, Waves: waves, Series: series, PauseGraph: pauseGraph,
		RecoveryControls: recoveryControls,
		NextCursor: api.OperatorRecoveryCursor{
			ProjectionRevision: view.NextCursor.ProjectionRevision,
			AuthorityRevision:  view.NextCursor.AuthorityRevision,
			AuditSequence:      view.NextCursor.AuditSequence,
		},
	}, nil
}

//nolint:gocyclo // The transport boundary validates every nested recovery control before serialization.
func tournamentRecoveryControlsResponse(
	controls []inbound.AdminRecoveryControl,
) ([]api.OperatorRecoveryControl, error) {
	type controlKey struct {
		kind       inbound.AdminRecoveryControlKind
		assignment uuid.UUID
		series     uuid.UUID
		slot       uuid.UUID
	}
	seen := make(map[controlKey]struct{}, len(controls))
	result := make([]api.OperatorRecoveryControl, len(controls))
	for index, control := range controls {
		if control.AssignmentID == uuid.Nil || control.SeriesID == uuid.Nil || control.SlotID == uuid.Nil ||
			control.OldWaveID == uuid.Nil || !api.Category(control.Category).Valid() ||
			!api.OperatorRecoveryControlKind(control.Kind).Valid() || control.ExpectedAuthorityRevision < 1 ||
			strings.TrimSpace(control.Reason) == "" || len(control.Reason) > 512 || len(control.Attempts) == 0 {
			return nil, domain.ErrInternal
		}
		key := controlKey{kind: control.Kind, assignment: control.AssignmentID, series: control.SeriesID, slot: control.SlotID}
		if _, duplicate := seen[key]; duplicate {
			return nil, domain.ErrInternal
		}
		seen[key] = struct{}{}
		attempts := make([]api.Game, len(control.Attempts))
		for attemptIndex, attempt := range control.Attempts {
			if attempt.SlotID != control.SlotID || attempt.AttemptNo != attemptIndex+1 {
				return nil, domain.ErrInternal
			}
			mapped, mapErr := participantGameResponse(attempt)
			if mapErr != nil {
				return nil, domain.ErrInternal
			}
			attempts[attemptIndex] = mapped
		}
		lastAttempt := control.Attempts[len(control.Attempts)-1]
		if lastAttempt.State != domain.GameStateVoid || !lastAttempt.ResultReason.IsLegalFor(domain.GameStateVoid) {
			return nil, domain.ErrInternal
		}
		var pauseReason *api.PauseReason
		if control.PauseReason != nil {
			value := api.PauseReason(*control.PauseReason)
			if !value.Valid() {
				return nil, domain.ErrInternal
			}
			pauseReason = &value
		}
		mapped := api.OperatorRecoveryControl{
			AssignmentId: control.AssignmentID, Attempts: attempts, Category: api.Category(control.Category),
			ExpectedAuthorityRevision: control.ExpectedAuthorityRevision, Kind: api.OperatorRecoveryControlKind(control.Kind),
			OldWaveId: control.OldWaveID, PauseReason: pauseReason, Reason: control.Reason,
			SeriesId: control.SeriesID, SlotId: control.SlotID,
		}
		switch control.Kind {
		case inbound.AdminRecoveryControlReplay:
			if control.Replay == nil || !control.Replay.Available || control.Replay.ExpectedClosureRevisionID == uuid.Nil || control.ReserveExhausted != nil {
				return nil, domain.ErrInternal
			}
			mapped.Replay = &api.OperatorRecoveryReplayDetails{Available: control.Replay.Available, ExpectedClosureRevisionId: control.Replay.ExpectedClosureRevisionID}
		case inbound.AdminRecoveryControlReserveExhausted:
			if control.Replay != nil || control.ReserveExhausted == nil {
				return nil, domain.ErrInternal
			}
			details, mapErr := tournamentRecoveryReserveExhaustedResponse(control.ReserveExhausted)
			if mapErr != nil {
				return nil, mapErr
			}
			mapped.ReserveExhausted = details
		default:
			return nil, domain.ErrInternal
		}
		result[index] = mapped
	}
	return result, nil
}

//nolint:gocyclo // Reserve evidence has independent fields that must fail closed at the HTTP boundary.
func tournamentRecoveryReserveExhaustedResponse(
	details *inbound.AdminRecoveryReserveExhaustedDetails,
) (*api.OperatorRecoveryReserveExhaustedDetails, error) {
	if details == nil || details.CurrentSnapshotID == uuid.Nil || details.ExpectedSnapshotID == uuid.Nil ||
		details.ExpectedExhaustionCommandID == uuid.Nil || details.ExpectedAssignmentRevision < 1 ||
		details.ExpectedPoolRevision < 1 || details.ExpectedHistoryRevision < 1 || details.ExpectedArtifactRevision < 1 ||
		details.ExpectedReservationRevision < 1 || details.ExpectedCategoryRevision < 1 || details.ExpectedPoolRevisionID == uuid.Nil ||
		details.ExpectedHistoryRevisionID == uuid.Nil || details.ExpectedArtifactRevisionID == uuid.Nil ||
		details.ExpectedReservationRevisionID == uuid.Nil || details.ExpectedCategoryRevisionID == uuid.Nil {
		return nil, domain.ErrInternal
	}
	if details.CurrentSnapshotID != details.ExpectedSnapshotID {
		return nil, domain.ErrInternal
	}
	candidates := make([]api.OperatorRecoveryReserveCandidate, len(details.Candidates))
	type candidateKey struct {
		taskID  uuid.UUID
		version int
	}
	seen := make(map[candidateKey]struct{}, len(details.Candidates))
	for index, candidate := range details.Candidates {
		if candidate.TaskID == uuid.Nil || !fitsAPIInt32(candidate.Version) || candidate.Version < 1 {
			return nil, domain.ErrInternal
		}
		key := candidateKey{taskID: candidate.TaskID, version: candidate.Version}
		if _, exists := seen[key]; exists {
			return nil, domain.ErrInternal
		}
		seen[key] = struct{}{}
		candidates[index] = api.OperatorRecoveryReserveCandidate{TaskId: candidate.TaskID, Version: response.IntToInt32(candidate.Version)}
	}
	return &api.OperatorRecoveryReserveExhaustedDetails{
		Candidates: candidates, CurrentSnapshotId: details.CurrentSnapshotID,
		ExpectedArtifactRevision: details.ExpectedArtifactRevision, ExpectedArtifactRevisionId: details.ExpectedArtifactRevisionID,
		ExpectedAssignmentRevision: details.ExpectedAssignmentRevision, ExpectedCategoryRevision: details.ExpectedCategoryRevision,
		ExpectedCategoryRevisionId: details.ExpectedCategoryRevisionID, ExpectedExhaustionCommandId: details.ExpectedExhaustionCommandID,
		ExpectedHistoryRevision: details.ExpectedHistoryRevision, ExpectedHistoryRevisionId: details.ExpectedHistoryRevisionID,
		ExpectedPoolRevision: details.ExpectedPoolRevision, ExpectedPoolRevisionId: details.ExpectedPoolRevisionID,
		ExpectedReservationRevision: details.ExpectedReservationRevision, ExpectedReservationRevisionId: details.ExpectedReservationRevisionID,
		ExpectedSnapshotId: details.ExpectedSnapshotID,
	}, nil
}

func tournamentPauseGraphResponse(graph *inbound.AdminPauseGraphView) (*api.PauseGraph, error) {
	if graph == nil {
		return nil, nil
	}
	wave, err := tournamentWaveResponse(graph.Wave)
	if err != nil {
		return nil, err
	}
	series, err := tournamentPauseSeriesResponses(graph)
	if err != nil {
		return nil, err
	}
	games, err := tournamentPauseGameResponses(graph)
	if err != nil {
		return nil, err
	}
	frozen, err := tournamentFrozenDeadlineResponses(graph)
	if err != nil {
		return nil, err
	}
	presence, err := tournamentPresenceResponses(graph)
	if err != nil {
		return nil, err
	}
	reconnect, err := tournamentReconnectResponses(graph)
	if err != nil {
		return nil, err
	}
	counters, err := tournamentReconnectCounterResponses(graph)
	if err != nil {
		return nil, err
	}
	draft, err := tournamentPausedDraftResponse(graph)
	if err != nil {
		return nil, err
	}
	return &api.PauseGraph{
		TournamentId: graph.TournamentID, RosterId: graph.RosterID,
		GraphRevision: graph.Revision, Wave: wave, Series: series, Games: games,
		Draft: draft, Presence: presence, Reconnect: reconnect, Counters: counters,
		FrozenDeadlines: frozen, ActivePauseId: cloneUUIDPointer(graph.ActivePauseID),
		PausedAt:               cloneTimePointer(graph.PausedAt),
		DeadlinesSuppressed:    graph.DeadlinesSuppressed,
		TerminalActionRevision: graph.TerminalActionRevision,
	}, nil
}

func tournamentPauseSeriesResponses(graph *inbound.AdminPauseGraphView) ([]api.PauseSeries, error) {
	series := make([]api.PauseSeries, len(graph.Series))
	for index, item := range graph.Series {
		mapped, mapErr := participantSeriesResponse(item.Series)
		if mapErr != nil {
			return nil, mapErr
		}
		var resumeState *api.SeriesState
		if item.ResumeState != nil {
			state := api.SeriesState(*item.ResumeState)
			if !state.Valid() {
				return nil, domain.ErrInternal
			}
			resumeState = &state
		}
		series[index] = api.PauseSeries{
			Series: mapped, Revision: item.Revision,
			CurrentGameId: cloneUUIDPointer(item.CurrentGameID), ResumeState: resumeState,
		}
	}
	return series, nil
}

func tournamentPauseGameResponses(graph *inbound.AdminPauseGraphView) ([]api.PauseGame, error) {
	games := make([]api.PauseGame, len(graph.Games))
	for index, item := range graph.Games {
		mapped, mapErr := participantGameResponse(item.Game)
		if mapErr != nil {
			return nil, mapErr
		}
		var resumeState *api.GameState
		if item.ResumeState != nil {
			state := api.GameState(*item.ResumeState)
			if !state.Valid() {
				return nil, domain.ErrInternal
			}
			resumeState = &state
		}
		games[index] = api.PauseGame{
			SeriesId: item.SeriesID, Game: mapped, Revision: item.Revision,
			Deadline: cloneTimePointer(item.Deadline), ResumeState: resumeState,
		}
	}
	return games, nil
}

func tournamentFrozenDeadlineResponses(graph *inbound.AdminPauseGraphView) ([]api.FrozenDeadline, error) {
	frozen := make([]api.FrozenDeadline, len(graph.FrozenDeadlines))
	for index, item := range graph.FrozenDeadlines {
		remainingMilliseconds := item.Remaining.Milliseconds()
		if item.Remaining < 0 || remainingMilliseconds < 0 {
			return nil, domain.ErrInternal
		}
		frozen[index] = api.FrozenDeadline{
			Kind: api.PauseDeadlineKind(item.Kind), OwnerId: item.OwnerID,
			OriginalDeadline: item.OriginalDeadline, FrozenAt: item.FrozenAt,
			RemainingMs: remainingMilliseconds, ResumedAt: cloneTimePointer(item.ResumedAt),
			ResumedDeadline: cloneTimePointer(item.ResumedDeadline), Revision: item.Revision,
		}
	}
	return frozen, nil
}

func tournamentPresenceResponses(graph *inbound.AdminPauseGraphView) ([]api.Presence, error) {
	presence := make([]api.Presence, len(graph.Presence))
	for index, item := range graph.Presence {
		state := api.PresenceState(item.State)
		if !state.Valid() {
			return nil, domain.ErrInternal
		}
		presence[index] = api.Presence{
			Id: item.ID, TournamentId: item.TournamentID, RosterId: item.RosterID,
			SeriesId: item.SeriesID, ParticipantId: item.ParticipantID, State: state,
			PresenceEpoch: item.PresenceEpoch, Revision: item.Revision,
			ConnectedAt: item.ConnectedAt, DisconnectedAt: cloneTimePointer(item.DisconnectedAt),
			UpdatedAt: item.UpdatedAt,
		}
	}
	return presence, nil
}

func tournamentReconnectResponses(graph *inbound.AdminPauseGraphView) ([]api.ReconnectInterval, error) {
	reconnect := make([]api.ReconnectInterval, len(graph.Reconnect))
	for index, item := range graph.Reconnect {
		if !fitsAPIInt32(item.Number) || !fitsAPIInt32(item.ContinuationNumber) {
			return nil, domain.ErrInternal
		}
		state := api.ReconnectState(item.State)
		if !state.Valid() {
			return nil, domain.ErrInternal
		}
		reconnect[index] = api.ReconnectInterval{
			Id: item.ID, PauseId: item.PauseID, RosterId: item.RosterID, SeriesId: item.SeriesID,
			GameId: item.GameID, ParticipantId: item.ParticipantID, PresenceEpoch: item.PresenceEpoch,
			Number: response.IntToInt32(item.Number), ContinuationNumber: response.IntToInt32(item.ContinuationNumber),
			ContinuedFromId:    cloneUUIDPointer(item.ContinuedFromID),
			SuspendedByPauseId: cloneUUIDPointer(item.SuspendedByPauseID), State: state,
			OpenedAt: item.OpenedAt, Deadline: item.Deadline, ClosedAt: cloneTimePointer(item.ClosedAt),
			Revision: item.Revision, UpdatedAt: item.UpdatedAt,
		}
	}
	return reconnect, nil
}

func tournamentReconnectCounterResponses(graph *inbound.AdminPauseGraphView) ([]api.PauseReconnectCounter, error) {
	counters := make([]api.PauseReconnectCounter, len(graph.Counters))
	for index, item := range graph.Counters {
		if !fitsAPIInt32(item.Limit) || !fitsAPIInt32(item.Used) {
			return nil, domain.ErrInternal
		}
		counters[index] = api.PauseReconnectCounter{
			PauseId: item.PauseID, RosterId: item.RosterID, ParticipantId: item.ParticipantID,
			Limit: response.IntToInt32(item.Limit), Used: response.IntToInt32(item.Used), Revision: item.Revision,
		}
	}
	return counters, nil
}

func tournamentPausedDraftResponse(graph *inbound.AdminPauseGraphView) (*api.Draft, error) {
	var draft *api.Draft
	if graph.Draft != nil {
		mapped, mapErr := adminPausedDraftResponse(*graph.Draft)
		if mapErr != nil {
			return nil, mapErr
		}
		draft = &mapped
	}
	return draft, nil
}

func adminPausedDraftResponse(draft inbound.AdminDraftView) (api.Draft, error) {
	format := api.SeriesFormat(draft.Format)
	state := api.DraftState(draft.State)
	if !format.Valid() || !state.Valid() || !fitsAPIInt32(draft.Turn) {
		return api.Draft{}, domain.ErrInternal
	}
	pool, err := participantCategories(draft.Pool)
	if err != nil {
		return api.Draft{}, err
	}
	selected, err := participantCategories(draft.SelectedCategories)
	if err != nil {
		return api.Draft{}, err
	}
	actions := make([]api.DraftAction, len(draft.Actions))
	for i, action := range draft.Actions {
		kind := api.DraftActionType(action.Action)
		category := api.Category(action.Category)
		if !kind.Valid() || !category.Valid() || !fitsAPIInt32(action.Turn) {
			return api.Draft{}, domain.ErrInternal
		}
		actions[i] = api.DraftAction{Turn: response.IntToInt32(action.Turn), ActorId: action.ActorID, Action: kind, Category: category, OccurredAt: action.OccurredAt, TurnDeadline: action.TurnDeadline}
	}
	return api.Draft{
		Id: draft.ID, SeriesId: draft.SeriesID, Format: format,
		FirstParticipantId: draft.FirstParticipantID, SecondParticipantId: draft.SecondParticipantID,
		Pool: pool, State: state, Turn: response.IntToInt32(draft.Turn),
		CurrentActorId: nil, CurrentAction: nil, TurnDeadline: cloneTimePointer(draft.TurnDeadline),
		LegalCategories: []api.Category{}, Actions: actions, SelectedCategories: selected, Revision: draft.Revision,
	}, nil
}
