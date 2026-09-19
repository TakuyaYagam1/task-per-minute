package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

type ParticipantStatePostgres struct {
	tx     *db.TxManager
	drafts *participantStateDraftReader
}

func NewParticipantStatePostgres(tx *db.TxManager) *ParticipantStatePostgres {
	return &ParticipantStatePostgres{
		tx:     tx,
		drafts: newParticipantStateDraftReader(tx),
	}
}

func (r *ParticipantStatePostgres) ReadParticipantState(
	ctx context.Context,
	query tournamentparticipant.StateQuery,
) (usecase.RecoveryView, error) {
	if ctx == nil || r == nil || r.tx == nil || r.drafts == nil ||
		query.TournamentID == uuid.Nil || query.PlayerID == uuid.Nil ||
		query.ExpectedProjectionRevision < 1 {
		return usecase.RecoveryView{}, domain.ErrValidation
	}

	var view usecase.RecoveryView
	err := r.tx.ReadSnapshot(ctx, func(txCtx context.Context) error {
		loaded, err := r.readParticipantState(txCtx, query)
		if err != nil {
			return err
		}
		view = loaded
		return nil
	})
	if err != nil {
		return usecase.RecoveryView{}, err
	}
	return view, nil
}

func (r *ParticipantStatePostgres) readParticipantState(
	ctx context.Context,
	query tournamentparticipant.StateQuery,
) (usecase.RecoveryView, error) {
	querier := r.tx.Querier(ctx)
	root, err := r.loadParticipantStateRoot(ctx, querier, query)
	if err != nil {
		return usecase.RecoveryView{}, err
	}
	lobby, err := loadParticipantLobby(ctx, querier, root)
	if err != nil {
		return usecase.RecoveryView{}, err
	}
	assignment, err := loadParticipantStateAssignment(ctx, querier, root)
	if err != nil {
		return usecase.RecoveryView{}, err
	}

	preferredSeriesID := uuid.NullUUID{}
	if assignment != nil {
		preferredSeriesID = uuid.NullUUID{UUID: assignment.SeriesID, Valid: true}
	}
	series, draftID, err := loadParticipantStateSeries(ctx, querier, root, preferredSeriesID)
	if err != nil {
		return usecase.RecoveryView{}, err
	}
	draft, err := r.loadParticipantStateDraft(ctx, draftID)
	if err != nil {
		return usecase.RecoveryView{}, err
	}
	wave, err := loadParticipantStateWave(ctx, querier, root, series)
	if err != nil {
		return usecase.RecoveryView{}, err
	}
	if participantIsAuthoritativeBye(wave, root.participantID) {
		// A bye Wave intentionally has no participant-owned Series. Do not
		// replay a prior completed Series or draft as current recovery state.
		series = nil
		draft = nil
	}

	view := usecase.RecoveryView{
		TournamentID:            root.tournamentID,
		ParticipantID:           root.participantID,
		ProjectionRevision:      root.projectionRevision,
		ParticipantViewRevision: root.participantViewRevision,
		EventSequence:           root.eventSequence,
		Lobby:                   lobby,
		Assignment:              assignment,
		Draft:                   draft,
		Series:                  series,
		Wave:                    wave,
		ObservedAt:              root.observedAt,
	}
	if err := validateParticipantStateAlignment(root, view); err != nil {
		return usecase.RecoveryView{}, err
	}
	return view, nil
}

func (r *ParticipantStatePostgres) loadParticipantStateRoot(
	ctx context.Context,
	querier *sqlc.Queries,
	query tournamentparticipant.StateQuery,
) (participantStateRoot, error) {
	row, err := querier.GetParticipantStateRoot(ctx, sqlc.GetParticipantStateRootParams{
		PlayerID: query.PlayerID, TournamentID: query.TournamentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return participantStateRoot{}, domain.ErrAssignmentParticipant
		}
		return participantStateRoot{}, participantStateQueryError("root", err)
	}
	root, err := participantStateRootFromRow(row, query)
	if err != nil {
		return participantStateRoot{}, err
	}
	if root.projectionRevision != query.ExpectedProjectionRevision {
		return participantStateRoot{}, &usecase.RevisionConflictError{
			TournamentID:     query.TournamentID,
			ExpectedRevision: query.ExpectedProjectionRevision,
			CurrentRevision:  root.projectionRevision,
			CurrentState:     root.tournamentState,
		}
	}
	return root, nil
}

func loadParticipantLobby(
	ctx context.Context,
	querier *sqlc.Queries,
	root participantStateRoot,
) (usecase.LobbyView, error) {
	rows, err := querier.ListParticipantLobbySeries(ctx, sqlc.ListParticipantLobbySeriesParams{
		TournamentID: root.tournamentID, PlayerID: root.playerID,
	})
	if err != nil {
		return usecase.LobbyView{}, participantStateQueryError("lobby", err)
	}
	series, err := participantLobbySeries(rows, root.participantID)
	if err != nil {
		return usecase.LobbyView{}, err
	}
	return usecase.LobbyView{
		TournamentID:       root.tournamentID,
		ParticipantID:      root.participantID,
		Attendance:         root.attendance,
		CurrentSwissRound:  cloneParticipantStateInt(root.currentSwissRound),
		SwissPoints:        root.swissPoints,
		ProjectionRevision: root.projectionRevision,
		State:              root.tournamentState,
		RosterLocked:       root.rosterLocked,
		Series:             series,
	}, nil
}

func loadParticipantStateAssignment(
	ctx context.Context,
	querier *sqlc.Queries,
	root participantStateRoot,
) (*usecase.TournamentParticipantAssignmentView, error) {
	row, err := querier.GetParticipantStateAssignment(ctx, sqlc.GetParticipantStateAssignmentParams{
		TournamentID: root.tournamentID, PlayerID: root.playerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, participantStateQueryError("assignment", err)
	}
	assignment, err := participantAssignmentFromRow(row)
	if err != nil {
		return nil, err
	}
	if assignment.ParticipantID != root.participantID {
		return nil, participantStateInvalid("assignment participant")
	}
	return &assignment, nil
}

func loadParticipantStateSeries(
	ctx context.Context,
	querier *sqlc.Queries,
	root participantStateRoot,
	preferredSeriesID uuid.NullUUID,
) (*domain.Series, *uuid.UUID, error) {
	row, err := querier.GetParticipantStateSeries(ctx, sqlc.GetParticipantStateSeriesParams{
		TournamentID: root.tournamentID, PlayerID: root.playerID, PreferredSeriesID: preferredSeriesID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, participantStateQueryError("series", err)
	}
	graph, err := querier.ListParticipantStateSeriesGraph(ctx, sqlc.ListParticipantStateSeriesGraphParams{
		SeriesID: row.Series.ID, RosterID: root.rosterID,
	})
	if err != nil {
		return nil, nil, participantStateQueryError("series graph", err)
	}
	series, err := participantSeriesFromRows(row.Series, graph, root)
	if err != nil {
		return nil, nil, err
	}
	draftID, err := optionalParticipantStateUUID(row.DraftID)
	if err != nil {
		return nil, nil, participantStateInvalid("draft id")
	}
	return &series, draftID, nil
}

func (r *ParticipantStatePostgres) loadParticipantStateDraft(
	ctx context.Context,
	draftID *uuid.UUID,
) (*usecase.DraftView, error) {
	if draftID == nil {
		return nil, nil
	}
	execution, err := r.drafts.LoadDraft(ctx, *draftID)
	if err != nil {
		return nil, participantStateQueryError("draft", err)
	}
	if execution == nil || execution.Validate() != nil {
		return nil, participantStateInvalid("draft")
	}
	return &usecase.DraftView{Execution: participantDraftExecutionView(*execution)}, nil
}

func participantDraftExecutionView(execution draftusecase.Execution) usecase.DraftExecutionView {
	view := usecase.DraftExecutionView{ID: execution.ID, SeriesID: execution.SeriesID, Format: execution.Format, FirstParticipantID: execution.FirstParticipantID, SecondParticipantID: execution.SecondParticipantID, Pool: append([]domain.Category(nil), execution.Pool...), State: usecase.DraftExecutionState(execution.State), RevisionID: execution.RevisionID, PreviousRevisionID: execution.PreviousRevisionID, Revision: execution.Revision, CommandID: execution.CommandID, ServiceEpoch: execution.ServiceEpoch, Turn: execution.Turn, CurrentActorID: participantCloneUUID(execution.CurrentActorID), CurrentAction: participantCloneDraftAction(execution.CurrentAction), TurnDeadline: execution.TurnDeadline, AbsoluteDeadline: participantCloneTimePointer(execution.AbsoluteDeadline), PausedRemaining: execution.PausedRemaining, LegalCategories: append([]domain.Category(nil), execution.LegalCategories...), SelectedCategories: append([]domain.Category(nil), execution.SelectedCategories...), FirstActorDecision: participantCloneDecisionEvidence(execution.FirstActorDecision), Actions: make([]usecase.DraftActionRecordView, len(execution.Actions))}
	if execution.Recovery != nil {
		view.Recovery = &usecase.DraftRecoveryEvidenceView{Policy: usecase.DraftRecoveryPolicy(execution.Recovery.Policy), Reason: usecase.DraftRecoveryReason(execution.Recovery.Reason), PreviousState: usecase.DraftExecutionState(execution.Recovery.PreviousState), PreviousServiceEpoch: execution.Recovery.PreviousServiceEpoch, CurrentServiceEpoch: execution.Recovery.CurrentServiceEpoch, PreviousDeadline: execution.Recovery.PreviousDeadline, RecordedAt: execution.Recovery.RecordedAt, ActorID: execution.Recovery.ActorID, Note: execution.Recovery.Note}
	}
	if execution.Transition != nil {
		view.Transition = &usecase.DraftTransitionEvidenceView{Operation: usecase.DraftTransitionOperation(execution.Transition.Operation), ActorID: execution.Transition.ActorID, Reason: execution.Transition.Reason, OccurredAt: execution.Transition.OccurredAt}
	}
	for index, action := range execution.Actions {
		view.Actions[index] = usecase.DraftActionRecordView{ID: action.ID, ResultRevisionID: action.ResultRevisionID, CommandID: action.CommandID, Turn: action.Turn, ActorID: action.ActorID, Action: action.Action, Category: action.Category, ScheduledDeadline: action.ScheduledDeadline, OccurredAt: action.OccurredAt, Automatic: action.Automatic, DecisionEvidence: participantCloneDecisionEvidencePointer(action.DecisionEvidence)}
	}
	return view
}

func participantCloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
func participantCloneDraftAction(value *domain.DraftActionType) *domain.DraftActionType {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func participantCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func participantCloneDecisionEvidence(value domain.DecisionEvidence) domain.DecisionEvidence {
	cloned := value
	cloned.NormalizedInputs = append([]string(nil), value.NormalizedInputs...)
	cloned.Result = append([]string(nil), value.Result...)
	return cloned
}
func participantCloneDecisionEvidencePointer(value *domain.DecisionEvidence) *domain.DecisionEvidence {
	if value == nil {
		return nil
	}
	cloned := participantCloneDecisionEvidence(*value)
	return &cloned
}

func loadParticipantStateWave(
	ctx context.Context,
	querier *sqlc.Queries,
	root participantStateRoot,
	series *domain.Series,
) (*usecase.WaveView, error) {
	seriesID := uuid.NullUUID{}
	if series != nil {
		seriesID = uuid.NullUUID{UUID: series.ID, Valid: true}
	}
	row, err := querier.GetParticipantStateWave(ctx, sqlc.GetParticipantStateWaveParams{
		TournamentID: root.tournamentID, PlayerID: root.playerID, SeriesID: seriesID,
	})
	fallbackByMembership := false
	if errors.Is(err, pgx.ErrNoRows) && series != nil {
		// The latest Wave may be a Swiss bye and therefore have no link to the
		// participant's previous Series. Re-read by participant membership only;
		// the SQL adapter still validates the authoritative bye link below.
		fallbackByMembership = true
		row, err = querier.GetParticipantStateWave(ctx, sqlc.GetParticipantStateWaveParams{
			TournamentID: root.tournamentID, PlayerID: root.playerID, SeriesID: uuid.NullUUID{},
		})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, participantStateQueryError("wave", err)
	}
	members, err := querier.ListParticipantStateWaveMembers(ctx, row.WaveID)
	if err != nil {
		return nil, participantStateQueryError("wave members", err)
	}
	view, err := participantWaveFromRows(row, members, root)
	if err != nil {
		return nil, err
	}
	if fallbackByMembership && !participantIsAuthoritativeBye(&view, root.participantID) {
		// A newly materialized Series can exist before its Wave. In that state the
		// membership fallback only sees an older Wave and must not bind it to the
		// new Series. The fallback is authoritative solely for a linked Swiss bye.
		return nil, nil
	}
	return &view, nil
}

func participantIsAuthoritativeBye(view *usecase.WaveView, participantID uuid.UUID) bool {
	return view != nil && view.ByeParticipantID != nil && *view.ByeParticipantID == participantID
}

func participantStateQueryError(operation string, err error) error {
	return fmt.Errorf("ParticipantStatePostgres - %s: %w", operation, err)
}

var _ tournamentparticipant.StateReader = (*ParticipantStatePostgres)(nil)
