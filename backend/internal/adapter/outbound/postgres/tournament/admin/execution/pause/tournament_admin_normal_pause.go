package pause

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	snapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/snapshot"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	adminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	adminoperation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	rostercapability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	adminsnapshot "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

const tournamentAdminPauseDocumentVersion = 1

type tournamentAdminPauseDocument struct {
	Version int                            `json:"version"`
	View    json.RawMessage                `json:"view"`
	Pause   *gameusecase.NormalPauseRecord `json:"normal_pause,omitempty"`
}

func decodeTournamentAdminWaveResult(action string, document []byte) ([]byte, *gameusecase.NormalPauseRecord) {
	if action != string(adminexecution.WaveActionPause) {
		return append([]byte(nil), document...), nil
	}
	var envelope tournamentAdminPauseDocument

	if err := json.Unmarshal(document, &envelope); err != nil || envelope.Version != tournamentAdminPauseDocumentVersion ||
		len(envelope.View) == 0 || envelope.Pause == nil {
		// Old pause receipts remain replayable, but cannot be used as new durable
		// normal-pause authority because they predate the complete evidence.
		return append([]byte(nil), document...), nil
	}
	pause := *envelope.Pause
	return append([]byte(nil), envelope.View...), &pause
}

func (r *TournamentAdminNormalPausePostgres) FindNormalPauseCommand(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*gameusecase.NormalPauseRecord, error) {
	// The outer wave_control_commands receipt is written in the same admin
	// transaction and is the sole public idempotency authority.
	return nil, nil
}

func (r *TournamentAdminNormalPausePostgres) FindPauseResumeCommand(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*gameusecase.PauseResumeRecord, error) {
	return nil, nil
}

func (r *TournamentAdminNormalPausePostgres) FindPauseResumePresenceCommand(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*gameusecase.PauseResumePresenceRecord, error) {
	// The enclosing Wave command is the public idempotency authority. Presence
	// decisions are committed in that same transaction and never replayed alone.
	return nil, nil
}

func (r *TournamentAdminNormalPausePostgres) ActiveNormalPauseID(
	ctx context.Context,
	scope pausedomain.GraphScope,
) (uuid.UUID, error) {
	if !validNormalPauseRepository(ctx, r) || scope.Validate() != nil {
		return uuid.Nil, domain.ErrValidation
	}
	id, err := r.tx.Querier(ctx).GetTournamentAdminActiveNormalPause(ctx, sqlc.GetTournamentAdminActiveNormalPauseParams{
		TournamentID: scope.TournamentID,
		RosterID:     scope.RosterID,
		WaveID:       uuid.NullUUID{UUID: scope.WaveID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, domain.ErrConflict
	}
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("load active normal Wave pause: %w", err)
	}
	return id, nil
}

func (r *TournamentAdminNormalPausePostgres) LoadNormalPauseAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
) (gameusecase.NormalPauseAuthority, error) {
	if !validNormalPauseRepository(ctx, r) || scope.Validate() != nil {
		return gameusecase.NormalPauseAuthority{}, domain.ErrValidation
	}
	graph, _, err := r.loadTournamentAdminNormalPauseGraph(ctx, scope)
	if err != nil {
		return gameusecase.NormalPauseAuthority{}, err
	}
	revisions := gameusecase.PauseGraphRevisionsFrom(graph)
	return gameusecase.NormalPauseAuthority{
		Scope: scope, Revisions: revisions, Graph: graph,
		ActiveGolden: graph.Tournament.State == domain.TournamentStateGolden,
		Complete:     true,
	}, nil
}

func (r *TournamentAdminNormalPausePostgres) loadTournamentAdminNormalPauseGraph(
	ctx context.Context,
	scope pausedomain.GraphScope,
) (gameusecase.PauseGraph, []sqlc.Pause, error) {
	querier := r.tx.Querier(ctx)
	pauseRows, err := querier.LockTournamentAdminNormalPauseRows(ctx, sqlc.LockTournamentAdminNormalPauseRowsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID, WaveID: uuid.NullUUID{UUID: scope.WaveID, Valid: true},
	})
	if err != nil {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("lock normal pause rows: %w", err)
	}
	if err := rejectActiveNormalWavePause(pauseRows); err != nil {
		return gameusecase.PauseGraph{}, nil, err
	}
	activity, err := lockTournamentAdminNormalPauseActivity(ctx, querier, scope.WaveID)
	if err != nil {
		return gameusecase.PauseGraph{}, nil, err
	}
	deadlines, err := querier.ListTournamentAdminNormalPauseGameDeadlines(ctx, scope.WaveID)
	if err != nil {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("load normal pause Game deadlines: %w", err)
	}
	snapshot, err := snapshotrepo.NewTournamentAdminSnapshotPostgres(r.tx).LoadOperatorSnapshot(ctx, adminsnapshot.SnapshotQuery{
		Operator: adminoperation.OperatorIdentity{ActorID: scope.Authority.HolderID}, TournamentID: scope.TournamentID,
	})
	if err != nil {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("load normal pause snapshot: %w", err)
	}
	seriesGraph, err := snapshotrepo.LoadSeries(ctx, querier, scope.TournamentID, snapshot.Roster)
	if err != nil {
		return gameusecase.PauseGraph{}, nil, err
	}
	wave, found := pauseWaveByID(snapshot.Waves, scope.WaveID)
	if !found || snapshot.Roster.ID != scope.RosterID {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("load normal pause Wave and roster: %w", domain.ErrInternal)
	}
	presence, err := recoveryPresence(activity.presence)
	if err != nil {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("load normal pause Presence: %w", domain.ErrInternal)
	}
	reconnect, err := recoveryIntervals(activity.reconnect)
	if err != nil {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("load normal pause reconnect intervals: %w", domain.ErrInternal)
	}
	counters, err := recoveryCounters(activity.counters)
	if err != nil {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("load normal pause reconnect counters: %w", domain.ErrInternal)
	}
	seriesValues, games, err := tournamentAdminActivePauseExecutions(wave, seriesGraph, deadlines)
	if err != nil {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("load normal pause executions: %w", err)
	}
	if err := r.attachTournamentAdminNormalPauseSourcePauses(ctx, querier, scope, pauseRows, activity.clocks, games); err != nil {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("load normal pause source pauses: %w", err)
	}
	draft, err := r.loadTournamentAdminNormalPauseDraft(ctx, querier, wave, snapshot.Roster, seriesValues)
	if err != nil {
		return gameusecase.PauseGraph{}, nil, fmt.Errorf("load normal pause Draft: %w", err)
	}
	graph := gameusecase.PauseGraph{
		Scope: scope, Revision: 1,
		Tournament: gameusecase.TournamentRecord{
			ID: snapshot.Tournament.ID, RosterID: snapshot.Tournament.RosterID, Preset: snapshot.Tournament.Preset,
			State: snapshot.Tournament.State, PausedFromState: snapshot.Tournament.PausedFromState,
			Revision: snapshot.Tournament.Revision, RosterSize: snapshot.Tournament.RosterSize,
			CreatedAt: snapshot.Tournament.CreatedAt, UpdatedAt: snapshot.Tournament.UpdatedAt,
			StartedAt: snapshot.Tournament.StartedAt, FinishedAt: snapshot.Tournament.FinishedAt,
		},
		Wave:   gameusecase.PauseWave{Wave: wave.Wave, Revision: wave.Revision},
		Series: seriesValues, Games: games, Draft: draft, Presence: presence, Reconnect: reconnect, Counters: counters,
	}
	return graph, pauseRows, nil
}

type tournamentAdminNormalPauseActivity struct {
	presence  []sqlc.PresenceState
	reconnect []sqlc.ReconnectInterval
	counters  []sqlc.ReconnectSlotCounter
	clocks    []sqlc.PauseClock
}

func lockTournamentAdminNormalPauseActivity(ctx context.Context, querier *sqlc.Queries, waveID uuid.UUID) (tournamentAdminNormalPauseActivity, error) {
	var rows tournamentAdminNormalPauseActivity
	var err error
	rows.presence, err = querier.LockTournamentAdminNormalPausePresence(ctx, waveID)
	if err != nil {
		return rows, fmt.Errorf("lock normal pause presence: %w", err)
	}
	rows.reconnect, err = querier.LockTournamentAdminNormalPauseReconnect(ctx, waveID)
	if err != nil {
		return rows, fmt.Errorf("lock normal pause reconnect: %w", err)
	}
	rows.counters, err = querier.LockTournamentAdminNormalPauseCounters(ctx, waveID)
	if err != nil {
		return rows, fmt.Errorf("lock normal pause counters: %w", err)
	}
	rows.clocks, err = querier.LockTournamentAdminNormalPauseClocks(ctx, waveID)
	if err != nil {
		return rows, fmt.Errorf("lock normal pause clocks: %w", err)
	}
	return rows, nil
}

func rejectActiveNormalWavePause(rows []sqlc.Pause) error {
	for _, row := range rows {
		if row.State == string(gameusecase.PauseStateActive) && row.ScopeKind == "wave" && !row.ParentPauseID.Valid {
			return domain.ErrConflict
		}
	}
	return nil
}

func (r *TournamentAdminNormalPausePostgres) attachTournamentAdminNormalPauseSourcePauses(
	ctx context.Context,
	querier *sqlc.Queries,
	scope pausedomain.GraphScope,
	pauseRows []sqlc.Pause,
	clockRows []sqlc.PauseClock,
	games []gameusecase.PauseGame,
) error {
	gameIndexes := make(map[uuid.UUID]int, len(games))
	for index := range games {
		gameIndexes[games[index].Game.ID] = index
	}
	activeRootsByGame := make(map[uuid.UUID][]sqlc.Pause, len(games))
	for _, row := range pauseRows {
		if row.ScopeKind != string(gameusecase.PauseResumeDecisionScopeGameAttempt) || row.State != string(gameusecase.PauseStateActive) ||
			row.ParentPauseID.Valid || row.Depth != 0 {
			continue
		}
		if _, current := gameIndexes[row.ScopeID]; current {
			activeRootsByGame[row.ScopeID] = append(activeRootsByGame[row.ScopeID], row)
		}
	}
	for index := range games {
		game := &games[index]
		roots := activeRootsByGame[game.Game.ID]
		if len(roots) == 0 {
			if game.Game.State == domain.GameStatePaused {
				return gameusecase.ErrNormalPauseGraphIncomplete
			}
			continue
		}
		if len(roots) != 1 || game.Game.State != domain.GameStatePaused {
			return gameusecase.ErrNormalPauseGraphIncomplete
		}
		source, err := loadTournamentAdminNormalSourcePause(ctx, querier, scope, roots[0], clockRows, *game)
		if err != nil {
			return err
		}
		game.SourcePause = source
	}
	return nil
}

func loadTournamentAdminNormalSourcePause(
	ctx context.Context,
	querier *sqlc.Queries,
	scope pausedomain.GraphScope,
	row sqlc.Pause,
	clockRows []sqlc.PauseClock,
	game gameusecase.PauseGame,
) (*gameusecase.PauseGameSourcePause, error) {
	if !tournamentAdminSourcePauseScopeMatches(row, scope, game) {
		return nil, gameusecase.ErrNormalPauseGraphIncomplete
	}
	clockRow, ok := tournamentAdminNormalPauseClockByID(clockRows, row.ID)
	if !ok {
		return nil, gameusecase.ErrNormalPauseGraphIncomplete
	}
	clock, err := recoveryGameClock(clockRow)
	if err != nil {
		return nil, gameusecase.ErrNormalPauseGraphIncomplete
	}
	startedAt, err := requiredRecoveryTime(row.StartedAt)
	if err != nil {
		return nil, gameusecase.ErrNormalPauseGraphIncomplete
	}
	snapshotRows, err := querier.LockTournamentReconnectPausePresenceSnapshots(ctx, sqlc.LockTournamentReconnectPausePresenceSnapshotsParams{
		PauseID: row.ID, RosterID: scope.RosterID, SeriesID: game.SeriesID,
	})
	if err != nil {
		return nil, fmt.Errorf("lock source pause presence snapshots: %w", err)
	}
	presence, err := tournamentAdminNormalPauseSourceSnapshots(snapshotRows, row.ID, scope.RosterID, game.SeriesID)
	if err != nil {
		return nil, gameusecase.ErrNormalPauseGraphIncomplete
	}
	decisionNumber, err := querier.GetTournamentAdminNormalPauseDecisionNumber(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("load source pause decision number: %w", err)
	}
	return &gameusecase.PauseGameSourcePause{
		PauseID: row.ID, ScopeKind: row.ScopeKind, ScopeID: row.ScopeID, SeriesID: row.SeriesID.UUID,
		GameID: row.GameAttemptID.UUID, Reason: gameusecase.PauseReason(row.Reason),
		ParentPauseID: optionalRecoveryUUID(row.ParentPauseID), Depth: int(row.Depth),
		State: gameusecase.PauseState(row.State), CurrentRevisionID: row.CurrentRevisionID, Revision: row.Revision,
		StartedAt: startedAt, DecisionNumber: decisionNumber,
		Clock: gameusecase.PauseFrozenDeadline{
			Kind: gameusecase.PauseDeadlineGame, OwnerID: clock.GameID, OriginalDeadline: clock.OriginalDeadline,
			FrozenAt: clock.FrozenAt, Remaining: clock.Remaining, ResumedAt: clock.ResumedAt,
			ResumedDeadline: clock.ResumedDeadline, Revision: clock.Revision,
		},
		Presence: presence,
	}, nil
}

func tournamentAdminSourcePauseScopeMatches(row sqlc.Pause, scope pausedomain.GraphScope, game gameusecase.PauseGame) bool {
	return row.SeriesID.Valid && row.GameAttemptID.Valid && row.GameAttemptID.UUID == game.Game.ID &&
		row.ScopeID == game.Game.ID && row.SeriesID.UUID == game.SeriesID && row.TournamentID == scope.TournamentID && row.RosterID == scope.RosterID
}

func tournamentAdminNormalPauseSourceSnapshots(
	rows []sqlc.PausePresenceSnapshot,
	pauseID, rosterID, seriesID uuid.UUID,
) ([]gameusecase.PausePresenceSnapshot, error) {
	result := make([]gameusecase.PausePresenceSnapshot, len(rows))
	for index, row := range rows {
		if row.PauseID != pauseID || row.RosterID != rosterID || row.SeriesID != seriesID {
			return nil, gameusecase.ErrNormalPauseGraphIncomplete
		}
		capturedAt, err := requiredRecoveryTime(row.CapturedAt)
		if err != nil {
			return nil, err
		}
		result[index] = gameusecase.PausePresenceSnapshot{
			ParticipantID: row.ParticipantID, State: pausedomain.PresenceState(row.PresenceState),
			PresenceEpoch: row.PresenceEpoch, Revision: row.PresenceRevision, CapturedAt: capturedAt,
		}
	}
	return result, nil
}

//nolint:gocyclo // Loading a Draft is a fail-closed identity, state, and topology boundary.
func (r *TournamentAdminNormalPausePostgres) loadTournamentAdminNormalPauseDraft(
	ctx context.Context,
	querier *sqlc.Queries,
	wave adminexecution.WaveView,
	roster rostercapability.RosterView,
	seriesValues []gameusecase.PauseSeries,
) (*draftusecase.Execution, error) {
	draftIDs, err := querier.LockTournamentAdminNormalPauseActiveDraftIDs(ctx, wave.Wave.ID)
	if err != nil {
		return nil, fmt.Errorf("load normal pause Draft identity: %w", err)
	}
	if len(draftIDs) > 1 || len(draftIDs) == 1 && draftIDs[0] == uuid.Nil {
		return nil, fmt.Errorf("normal pause requires at most one active Draft: %w", gameusecase.ErrNormalPauseGraphIncomplete)
	}
	if len(draftIDs) == 0 {
		for _, series := range seriesValues {
			if series.Execution.Series.State == domain.SeriesStateDraft {
				return nil, gameusecase.ErrNormalPauseGraphIncomplete
			}
		}
		return nil, nil
	}
	aggregate, err := draftrepo.NewDraftPostgres(r.tx).Get(ctx, draftIDs[0])
	if err != nil || aggregate == nil || aggregate.Draft.RosterID != roster.ID || len(aggregate.Revisions) == 0 {
		return nil, fmt.Errorf("normal pause Draft aggregate invalid: %w", gameusecase.ErrNormalPauseGraphIncomplete)
	}
	execution, err := r.loadDraftExecution(aggregate, aggregate.Revisions[len(aggregate.Revisions)-1].Revision)
	if err != nil || execution == nil || execution.State != draftusecase.ExecutionStateActive ||
		!tournamentAdminNormalPauseDraftMatchesSeries(*execution, seriesValues) {
		return nil, fmt.Errorf("normal pause active Draft does not match Wave Series: %w", gameusecase.ErrNormalPauseGraphIncomplete)
	}
	return execution, nil
}

func tournamentAdminNormalPauseDraftMatchesSeries(
	draft draftusecase.Execution,
	seriesValues []gameusecase.PauseSeries,
) bool {
	for _, series := range seriesValues {
		value := series.Execution.Series
		if value.ID != draft.SeriesID {
			continue
		}
		participantsMatch := value.FirstParticipantID == draft.FirstParticipantID &&
			value.SecondParticipantID == draft.SecondParticipantID ||
			value.FirstParticipantID == draft.SecondParticipantID &&
				value.SecondParticipantID == draft.FirstParticipantID
		return (value.State == domain.SeriesStatePlanned || value.State == domain.SeriesStateDraft) &&
			participantsMatch && value.Format == draft.Format
	}
	return false
}

func tournamentAdminActivePauseExecutions(
	wave adminexecution.WaveView,
	seriesGraph snapshotrepo.SeriesGraph,
	deadlines []sqlc.ListTournamentAdminNormalPauseGameDeadlinesRow,
) ([]gameusecase.PauseSeries, []gameusecase.PauseGame, error) {
	deadlineByGame := make(map[uuid.UUID]time.Time, len(deadlines))
	for _, row := range deadlines {
		if row.GameAttemptID == uuid.Nil {
			return nil, nil, fmt.Errorf("invalid Game deadline row: %w", domain.ErrInternal)
		}
		if !row.Deadline.Valid {
			continue
		}
		deadlineByGame[row.GameAttemptID] = row.Deadline.Time.UTC()
	}
	selected := make(map[uuid.UUID]struct{}, len(wave.SeriesIDs)/2)
	for _, seriesID := range wave.SeriesIDs {
		selected[seriesID] = struct{}{}
	}
	seriesValues := make([]gameusecase.PauseSeries, 0, len(selected))
	games := make([]gameusecase.PauseGame, 0, len(selected))
	for _, value := range seriesGraph.Values {
		if _, ok := selected[value.ID]; !ok {
			continue
		}
		series := gameusecase.PauseSeries{
			Execution: seriesdomain.Execution{Series: value}, Revision: seriesGraph.RevisionByID[value.ID],
		}
		current, hasCurrent := pauseCurrentGame(value)
		if hasCurrent {
			gameID := current.ID
			series.CurrentGameID = &gameID
			deadline, hasDeadline := deadlineByGame[gameID]
			if current.State == domain.GameStateActive && !hasDeadline {
				return nil, nil, fmt.Errorf("active Game deadline missing: %w", gameusecase.ErrNormalPauseGraphIncomplete)
			}
			game := gameusecase.PauseGame{SeriesID: value.ID, Game: *current, Revision: seriesGraph.GameRevisionByID[gameID]}
			if current.State == domain.GameStateActive && hasDeadline {
				game.Deadline = &deadline
			}
			games = append(games, game)
		} else if value.State == domain.SeriesStateActive {
			return nil, nil, fmt.Errorf("active Series current Game missing: %w", gameusecase.ErrNormalPauseGraphIncomplete)
		}
		seriesValues = append(seriesValues, series)
	}
	if len(seriesValues) != len(selected) {
		return nil, nil, gameusecase.ErrNormalPauseGraphIncomplete
	}
	return seriesValues, games, nil
}

//nolint:gocyclo // Recovery validates every durable graph component before returning authority.
func (r *TournamentAdminNormalPausePostgres) LoadPauseResumeAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
	pauseID uuid.UUID,
) (gameusecase.PauseResumeAuthority, error) {
	if !validNormalPauseRepository(ctx, r) || scope.Validate() != nil || pauseID == uuid.Nil {
		return gameusecase.PauseResumeAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	activePauseID, err := r.ActiveNormalPauseID(ctx, scope)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	if activePauseID != pauseID {
		return gameusecase.PauseResumeAuthority{}, domain.ErrConflict
	}
	document, err := querier.GetTournamentAdminNormalPauseReceipt(ctx, sqlc.GetTournamentAdminNormalPauseReceiptParams{
		TournamentID: scope.TournamentID, WaveID: scope.WaveID, PauseID: pauseID,
	})
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, fmt.Errorf("load normal pause receipt: %w", err)
	}
	decodedDocument, stored := decodeTournamentAdminWaveResult(string(adminexecution.WaveActionPause), document)
	if len(decodedDocument) == 0 || stored == nil || stored.PauseID != pauseID || stored.Scope.TournamentID != scope.TournamentID ||
		stored.Scope.RosterID != scope.RosterID || stored.Scope.WaveID != scope.WaveID {
		return gameusecase.PauseResumeAuthority{}, domain.ErrInternal
	}
	if err := r.verifyTournamentAdminNormalPauseDraft(ctx, querier, stored); err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	pauseRows, err := querier.LockTournamentAdminNormalPauseRows(ctx, sqlc.LockTournamentAdminNormalPauseRowsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		WaveID: uuid.NullUUID{UUID: scope.WaveID, Valid: true},
	})
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	presenceRows, err := querier.LockTournamentAdminNormalPausePresence(ctx, scope.WaveID)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	reconnectRows, err := querier.LockTournamentAdminNormalPauseReconnect(ctx, scope.WaveID)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	counterRows, err := querier.LockTournamentAdminNormalPauseCounters(ctx, scope.WaveID)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	clockRows, err := querier.LockTournamentAdminNormalPauseClocks(ctx, scope.WaveID)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	if err := verifyTournamentAdminNormalPauseSourcePauses(ctx, querier, scope, stored, pauseRows, clockRows); err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	readyClockRows, err := querier.LockTournamentAdminNormalPauseReadyWindowClocks(ctx, scope.WaveID)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	presence, err := recoveryPresence(presenceRows)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, domain.ErrInternal
	}
	reconnect, err := recoveryIntervals(reconnectRows)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, domain.ErrInternal
	}
	counters, err := recoveryCounters(counterRows)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, domain.ErrInternal
	}
	frozen, err := tournamentAdminNormalPauseFrozen(clockRows, readyClockRows, stored.Graph.FrozenDeadlines)
	if err != nil {
		return gameusecase.PauseResumeAuthority{}, err
	}
	stored.Scope.Authority = scope.Authority
	stored.Graph.Scope.Authority = scope.Authority
	if len(reconnect) == 0 && len(stored.Graph.Reconnect) == 0 {
		reconnect = stored.Graph.Reconnect
	}
	if len(counters) == 0 && len(stored.Graph.Counters) == 0 {
		counters = stored.Graph.Counters
	}
	authority := gameusecase.PauseResumeAuthority{
		Pause: *stored, Presence: presence, Reconnect: reconnect, Counters: counters,
		FrozenDeadlines: frozen, TerminalActionRevision: stored.Graph.TerminalActionRevision,
	}
	if !reflect.DeepEqual(counters, stored.Graph.Counters) || !reflect.DeepEqual(reconnect, stored.Graph.Reconnect) {
		return gameusecase.PauseResumeAuthority{}, gameusecase.ErrPauseResumeIncomplete
	}
	return authority, nil
}

func verifyTournamentAdminNormalPauseSourcePauses(
	ctx context.Context,
	querier *sqlc.Queries,
	scope pausedomain.GraphScope,
	stored *gameusecase.NormalPauseRecord,
	pauseRows []sqlc.Pause,
	clockRows []sqlc.PauseClock,
) error {
	if stored == nil {
		return gameusecase.ErrPauseResumeIncomplete
	}
	for _, game := range stored.Graph.Games {
		if game.SourcePause == nil {
			continue
		}
		if err := verifyTournamentAdminNormalSourcePause(ctx, querier, scope, game, pauseRows, clockRows); err != nil {
			return err
		}
	}
	return nil
}

func verifyTournamentAdminNormalSourcePause(
	ctx context.Context,
	querier *sqlc.Queries,
	scope pausedomain.GraphScope,
	game gameusecase.PauseGame,
	pauseRows []sqlc.Pause,
	clockRows []sqlc.PauseClock,
) error {
	source := game.SourcePause
	row, ok := tournamentAdminNormalPauseRowByID(pauseRows, source.PauseID)
	if !ok || !tournamentAdminSourcePauseRowMatches(row, scope, game) {
		return gameusecase.ErrPauseResumeIncomplete
	}
	startedAt, err := requiredRecoveryTime(row.StartedAt)
	if err != nil || !startedAt.Equal(source.StartedAt) {
		return gameusecase.ErrPauseResumeIncomplete
	}
	if !hasUniqueTournamentAdminSourcePause(pauseRows, game.Game.ID) {
		return gameusecase.ErrPauseResumeIncomplete
	}
	if !tournamentAdminSourcePauseClockMatches(clockRows, *source) {
		return gameusecase.ErrPauseResumeIncomplete
	}
	snapshotRows, err := querier.LockTournamentReconnectPausePresenceSnapshots(ctx, sqlc.LockTournamentReconnectPausePresenceSnapshotsParams{
		PauseID: source.PauseID, RosterID: scope.RosterID, SeriesID: game.SeriesID,
	})
	if err != nil {
		return err
	}
	snapshots, err := tournamentAdminNormalPauseSourceSnapshots(snapshotRows, source.PauseID, scope.RosterID, game.SeriesID)
	if err != nil || !reflect.DeepEqual(snapshots, source.Presence) {
		return gameusecase.ErrPauseResumeIncomplete
	}
	decisionNumber, err := querier.GetTournamentAdminNormalPauseDecisionNumber(ctx, source.PauseID)
	if err != nil || decisionNumber != source.DecisionNumber {
		return gameusecase.ErrPauseResumeIncomplete
	}
	return nil
}

func tournamentAdminSourcePauseRowMatches(row sqlc.Pause, scope pausedomain.GraphScope, game gameusecase.PauseGame) bool {
	source := game.SourcePause
	return source.State == gameusecase.PauseStateActive && source.ResolvedAt == nil &&
		row.ScopeKind == string(gameusecase.PauseResumeDecisionScopeGameAttempt) && tournamentAdminSourcePauseScopeMatches(row, scope, game) &&
		row.State == string(gameusecase.PauseStateActive) && row.Reason == string(gameusecase.PauseReasonDisconnect) &&
		!row.ParentPauseID.Valid && row.Depth == 0 && row.CurrentRevisionID == source.CurrentRevisionID && row.Revision == source.Revision
}

func hasUniqueTournamentAdminSourcePause(rows []sqlc.Pause, gameID uuid.UUID) bool {
	rootCount := 0
	for _, row := range rows {
		if row.ScopeKind == string(gameusecase.PauseResumeDecisionScopeGameAttempt) &&
			row.State == string(gameusecase.PauseStateActive) && row.ScopeID == gameID && !row.ParentPauseID.Valid && row.Depth == 0 {
			rootCount++
		}
	}
	return rootCount == 1
}

func tournamentAdminSourcePauseClockMatches(rows []sqlc.PauseClock, source gameusecase.PauseGameSourcePause) bool {
	row, ok := tournamentAdminNormalPauseClockByID(rows, source.PauseID)
	if !ok {
		return false
	}
	clock, err := recoveryGameClock(row)
	return err == nil && clock.GameID == source.GameID && clock.OriginalDeadline.Equal(source.Clock.OriginalDeadline) &&
		clock.FrozenAt.Equal(source.Clock.FrozenAt) && clock.Remaining == source.Clock.Remaining &&
		clock.Revision == source.Clock.Revision && clock.ResumedAt == nil && clock.ResumedDeadline == nil
}

func (r *TournamentAdminNormalPausePostgres) verifyTournamentAdminNormalPauseDraft(
	ctx context.Context,
	querier *sqlc.Queries,
	stored *gameusecase.NormalPauseRecord,
) error {
	ids, err := querier.LockTournamentAdminNormalPauseActiveDraftIDs(ctx, stored.Scope.WaveID)
	if err != nil {
		return err
	}
	if stored.Graph.Draft == nil {
		if len(ids) != 0 {
			return gameusecase.ErrPauseResumeIncomplete
		}
		return nil
	}
	if len(ids) != 1 || ids[0] != stored.Graph.Draft.ID {
		return gameusecase.ErrPauseResumeIncomplete
	}
	aggregate, err := draftrepo.NewDraftPostgres(r.tx).Get(ctx, ids[0])
	if err != nil || aggregate == nil || len(aggregate.Revisions) == 0 {
		return gameusecase.ErrPauseResumeIncomplete
	}
	current, err := r.loadDraftExecution(aggregate, aggregate.Revisions[len(aggregate.Revisions)-1].Revision)
	if err != nil || current == nil || !reflect.DeepEqual(*current, *stored.Graph.Draft) {
		return gameusecase.ErrPauseResumeIncomplete
	}
	return nil
}

//nolint:gocyclo // Presence resume binds root, child, clock, and reconnect evidence in one load.
func (r *TournamentAdminNormalPausePostgres) LoadPauseResumePresenceAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
	normalPauseID, seriesPauseID, gamePauseID uuid.UUID,
) (gameusecase.PauseResumePresenceAuthority, error) {
	if gamePauseID == uuid.Nil || seriesPauseID == gamePauseID {
		return gameusecase.PauseResumePresenceAuthority{}, domain.ErrValidation
	}
	resume, err := r.LoadPauseResumeAuthority(ctx, scope, normalPauseID)
	if err != nil {
		return gameusecase.PauseResumePresenceAuthority{}, err
	}
	if seriesPauseID == uuid.Nil {
		return tournamentAdminSourceResumeAuthority(resume, gamePauseID)
	}
	resume = tournamentAdminNormalPausePresenceProjection(resume, gamePauseID)
	querier := r.tx.Querier(ctx)
	rows, err := querier.LockTournamentAdminNormalPauseRows(ctx, sqlc.LockTournamentAdminNormalPauseRowsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		WaveID: uuid.NullUUID{UUID: scope.WaveID, Valid: true},
	})
	if err != nil {
		return gameusecase.PauseResumePresenceAuthority{}, err
	}
	seriesRow, ok := tournamentAdminNormalPauseRowByID(rows, seriesPauseID)
	if !ok || seriesRow.ScopeKind != string(gameusecase.PauseResumeDecisionScopeSeries) ||
		!seriesRow.SeriesID.Valid || seriesRow.ParentPauseID.Valid || seriesRow.Depth != 0 {
		return gameusecase.PauseResumePresenceAuthority{}, gameusecase.ErrPauseResumePresenceIncomplete
	}
	gameRow, ok := tournamentAdminNormalPauseRowByID(rows, gamePauseID)
	if !ok || gameRow.ScopeKind != string(gameusecase.PauseResumeDecisionScopeGameAttempt) ||
		!gameRow.SeriesID.Valid || !gameRow.GameAttemptID.Valid || !gameRow.ParentPauseID.Valid ||
		gameRow.ParentPauseID.UUID != seriesPauseID || gameRow.Depth != 1 || gameRow.SeriesID.UUID != seriesRow.SeriesID.UUID {
		return gameusecase.PauseResumePresenceAuthority{}, gameusecase.ErrPauseResumePresenceIncomplete
	}
	clocks, err := querier.LockTournamentAdminNormalPauseClocks(ctx, scope.WaveID)
	if err != nil {
		return gameusecase.PauseResumePresenceAuthority{}, err
	}
	clockRow, ok := tournamentAdminNormalPauseClockByID(clocks, gamePauseID)
	if !ok {
		return gameusecase.PauseResumePresenceAuthority{}, gameusecase.ErrPauseResumePresenceIncomplete
	}
	clock, err := recoveryGameClock(clockRow)
	if err != nil {
		return gameusecase.PauseResumePresenceAuthority{}, gameusecase.ErrPauseResumePresenceIncomplete
	}
	seriesDecisionNumber, err := querier.GetTournamentAdminNormalPauseDecisionNumber(ctx, seriesPauseID)
	if err != nil {
		return gameusecase.PauseResumePresenceAuthority{}, err
	}
	gameDecisionNumber, err := querier.GetTournamentAdminNormalPauseDecisionNumber(ctx, gamePauseID)
	if err != nil {
		return gameusecase.PauseResumePresenceAuthority{}, err
	}
	// Normal operator pause creates the independent child rows at the same
	// server instant. The presence policy requires strict historical ordering;
	// expose the immediately preceding instant without changing durable clocks.
	startedAt := resume.Pause.PausedAt.Add(-time.Nanosecond)
	return gameusecase.PauseResumePresenceAuthority{
		Resume: resume,
		SeriesDecision: gameusecase.PauseResumeDecisionAuthority{
			PauseID: seriesRow.ID, ScopeKind: gameusecase.PauseResumeDecisionScopeSeries,
			CurrentRevisionID: seriesRow.CurrentRevisionID, State: gameusecase.PauseState(seriesRow.State),
			Revision: seriesRow.Revision, SeriesID: seriesRow.SeriesID.UUID, Depth: int(seriesRow.Depth),
			DecisionNumber: seriesDecisionNumber, StartedAt: startedAt,
		},
		GameDecision: gameusecase.PauseResumeDecisionAuthority{
			PauseID: gameRow.ID, ScopeKind: gameusecase.PauseResumeDecisionScopeGameAttempt,
			CurrentRevisionID: gameRow.CurrentRevisionID, State: gameusecase.PauseState(gameRow.State),
			Revision: gameRow.Revision, SeriesID: gameRow.SeriesID.UUID, GameID: gameRow.GameAttemptID.UUID,
			ParentPauseID: &seriesPauseID, Depth: int(gameRow.Depth), DecisionNumber: gameDecisionNumber,
			StartedAt: startedAt, GameClock: &clock,
		},
	}, nil
}

func tournamentAdminSourceResumeAuthority(
	resume gameusecase.PauseResumeAuthority,
	pauseID uuid.UUID,
) (gameusecase.PauseResumePresenceAuthority, error) {
	for _, game := range resume.Pause.Graph.Games {
		source := game.SourcePause
		if source == nil || source.PauseID != pauseID {
			continue
		}
		series, ok := normalPauseSeries(resume.Pause.Graph.Series, game.SeriesID)
		if !ok || series.Execution.Series.State != domain.SeriesStateActive || series.Execution.ResumeState != nil ||
			series.CurrentGameID == nil || *series.CurrentGameID != game.Game.ID ||
			source.State != gameusecase.PauseStateActive || source.Reason != gameusecase.PauseReasonDisconnect ||
			source.ParentPauseID != nil || source.Depth != 0 || game.Game.State != domain.GameStatePaused {
			return gameusecase.PauseResumePresenceAuthority{}, gameusecase.ErrPauseResumePresenceIncomplete
		}
		clock := pausedomain.PauseResumeGameClock{
			PauseID: source.PauseID, GameID: source.GameID, OriginalDeadline: source.Clock.OriginalDeadline,
			FrozenAt: source.Clock.FrozenAt, Remaining: source.Clock.Remaining, Revision: source.Clock.Revision,
		}
		return gameusecase.PauseResumePresenceAuthority{
			SourceAdoption: true, Resume: resume,
			GameDecision: gameusecase.PauseResumeDecisionAuthority{
				PauseID: source.PauseID, ScopeKind: gameusecase.PauseResumeDecisionScopeGameAttempt,
				CurrentRevisionID: source.CurrentRevisionID, State: source.State, Revision: source.Revision,
				SeriesID: source.SeriesID, GameID: source.GameID, DecisionNumber: source.DecisionNumber,
				StartedAt: source.StartedAt, GameClock: &clock,
			},
		}, nil
	}
	return gameusecase.PauseResumePresenceAuthority{}, gameusecase.ErrPauseResumePresenceIncomplete
}

//nolint:gocyclo // Projection preserves suspended lineage while rebinding active policy identifiers.
func tournamentAdminNormalPausePresenceProjection(
	resume gameusecase.PauseResumeAuthority,
	gamePauseID uuid.UUID,
) gameusecase.PauseResumeAuthority {
	sourcePauseIDs := make(map[uuid.UUID]struct{})
	for _, game := range resume.Pause.Graph.Games {
		if game.SourcePause != nil {
			sourcePauseIDs[game.SourcePause.PauseID] = struct{}{}
		}
	}
	suspendedIDs := make(map[uuid.UUID]struct{}, len(resume.Pause.SuspendedReconnect))
	for _, value := range resume.Pause.SuspendedReconnect {
		suspendedIDs[value.ID] = struct{}{}
	}
	sourcePauseByParticipant := make(map[uuid.UUID]uuid.UUID, len(suspendedIDs))
	for _, value := range resume.Pause.Graph.Reconnect {
		if _, suspended := suspendedIDs[value.ID]; suspended {
			sourcePauseByParticipant[value.ParticipantID] = value.PauseID
		}
	}
	filterCounters := func(values []pausedomain.PauseReconnectCounter) []pausedomain.PauseReconnectCounter {
		result := make([]pausedomain.PauseReconnectCounter, 0, len(values))
		for _, value := range values {
			if len(sourcePauseIDs) > 0 {
				_, source := sourcePauseIDs[value.PauseID]
				if source || value.PauseID == gamePauseID {
					result = append(result, value)
				}
				continue
			}
			expectedPauseID := gamePauseID
			if sourcePauseID, ok := sourcePauseByParticipant[value.ParticipantID]; ok {
				expectedPauseID = sourcePauseID
			}
			if value.PauseID != expectedPauseID {
				continue
			}
			value.PauseID = gamePauseID
			result = append(result, value)
		}
		return result
	}
	filterReconnect := func(values []pausedomain.PauseReconnectInterval) []pausedomain.PauseReconnectInterval {
		result := make([]pausedomain.PauseReconnectInterval, 0, len(values))
		for _, value := range values {
			if len(sourcePauseIDs) > 0 {
				_, source := sourcePauseIDs[value.PauseID]
				if source || value.PauseID == gamePauseID {
					result = append(result, value)
				}
				continue
			}
			_, suspended := suspendedIDs[value.ID]
			if value.PauseID != gamePauseID && !suspended {
				continue
			}
			// The presence policy owns one Game pause. A suspended predecessor can
			// originate from the earlier reconnect pause, so project its ownership
			// to that Game pause while retaining the exact durable interval ID.
			// Commit restores the predecessor's stored PauseID for the continuation.
			value.PauseID = gamePauseID
			result = append(result, value)
		}
		return result
	}
	resume.Counters = filterCounters(resume.Counters)
	resume.Reconnect = filterReconnect(resume.Reconnect)
	resume.Pause.Graph.Counters = filterCounters(resume.Pause.Graph.Counters)
	resume.Pause.Graph.Reconnect = filterReconnect(resume.Pause.Graph.Reconnect)
	resume.Pause.Expected.Counters = nil
	for _, value := range resume.Pause.Graph.Counters {
		resume.Pause.Expected.Counters = append(resume.Pause.Expected.Counters, gameusecase.PauseReconnectCounterRevision{
			PauseID: value.PauseID, RosterID: value.RosterID, ParticipantID: value.ParticipantID, Revision: value.Revision,
		})
	}
	retainedReconnect := make(map[uuid.UUID]struct{}, len(resume.Pause.Graph.Reconnect))
	resume.Pause.Expected.Reconnect = nil
	for _, value := range resume.Pause.Graph.Reconnect {
		retainedReconnect[value.ID] = struct{}{}
		revision := value.Revision
		if value.SuspendedByPauseID != nil && *value.SuspendedByPauseID == resume.Pause.PauseID {
			revision--
		}
		resume.Pause.Expected.Reconnect = append(resume.Pause.Expected.Reconnect, gameusecase.PauseChildRevision{ID: value.ID, Revision: revision})
	}
	filteredSuspended := make([]gameusecase.PauseChildRevision, 0, len(resume.Pause.SuspendedReconnect))
	for _, value := range resume.Pause.SuspendedReconnect {
		if _, ok := retainedReconnect[value.ID]; ok {
			filteredSuspended = append(filteredSuspended, value)
		}
	}
	resume.Pause.SuspendedReconnect = filteredSuspended
	return resume
}

func tournamentAdminNormalPauseRowByID(rows []sqlc.Pause, id uuid.UUID) (sqlc.Pause, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	return sqlc.Pause{}, false
}

func tournamentAdminNormalPauseClockByID(rows []sqlc.PauseClock, pauseID uuid.UUID) (sqlc.PauseClock, bool) {
	for _, row := range rows {
		if row.PauseID == pauseID && !row.ResumedAt.Valid && !row.ResumedDeadline.Valid {
			return row, true
		}
	}
	return sqlc.PauseClock{}, false
}

//nolint:gocyclo // Each deadline kind has distinct durable identity and timeline evidence.
func tournamentAdminNormalPauseFrozen(
	rows []sqlc.PauseClock,
	readyRows []sqlc.ReadyWindowPauseClock,
	expected []gameusecase.PauseFrozenDeadline,
) ([]gameusecase.PauseFrozenDeadline, error) {
	byGame := make(map[uuid.UUID]sqlc.PauseClock, len(rows))
	for _, row := range rows {
		if row.ResumedAt.Valid || row.ResumedDeadline.Valid {
			continue
		}
		byGame[row.GameAttemptID] = row
	}
	result := make([]gameusecase.PauseFrozenDeadline, 0, len(expected))
	for _, frozen := range expected {
		var current gameusecase.PauseFrozenDeadline
		switch frozen.Kind {
		case gameusecase.PauseDeadlineGame:
			row, ok := byGame[frozen.OwnerID]
			if !ok || !row.OriginalDeadline.Valid || !row.FrozenAt.Valid {
				return nil, gameusecase.ErrPauseResumeIncomplete
			}
			originalDeadline, err := requiredRecoveryTime(row.OriginalDeadline)
			if err != nil {
				return nil, gameusecase.ErrPauseResumeIncomplete
			}
			frozenAt, err := requiredRecoveryTime(row.FrozenAt)
			if err != nil {
				return nil, gameusecase.ErrPauseResumeIncomplete
			}
			remaining, err := recoveredFrozenDuration(originalDeadline, frozenAt, row.FrozenRemainingMs)
			if err != nil {
				return nil, gameusecase.ErrPauseResumeIncomplete
			}
			current = gameusecase.PauseFrozenDeadline{
				Kind: frozen.Kind, OwnerID: frozen.OwnerID, OriginalDeadline: originalDeadline,
				FrozenAt: frozenAt, Remaining: remaining,
				Revision: row.Revision,
			}
		case gameusecase.PauseDeadlineReadyWindow:
			row, ok := tournamentAdminNormalReadyClock(readyRows, frozen.OwnerID)
			if !ok || row.PauseID == uuid.Nil || !row.OriginalDeadline.Valid || !row.FrozenAt.Valid ||
				!row.FrozenRemaining.Valid || row.FrozenRemaining.Months != 0 {
				return nil, gameusecase.ErrPauseResumeIncomplete
			}
			remaining := time.Duration(row.FrozenRemaining.Days)*24*time.Hour +
				time.Duration(row.FrozenRemaining.Microseconds)*time.Microsecond
			current = gameusecase.PauseFrozenDeadline{
				Kind: frozen.Kind, OwnerID: frozen.OwnerID, OriginalDeadline: row.OriginalDeadline.Time.UTC(),
				FrozenAt: row.FrozenAt.Time.UTC(), Remaining: remaining, Revision: row.Revision,
			}
		case gameusecase.PauseDeadlineDraft:
			current = frozen
		default:
			return nil, domain.ErrInternal
		}
		if !reflect.DeepEqual(current, frozen) {
			return nil, gameusecase.ErrPauseResumeIncomplete
		}
		result = append(result, current)
	}
	return result, nil
}

func tournamentAdminNormalReadyClock(
	rows []sqlc.ReadyWindowPauseClock,
	windowID uuid.UUID,
) (sqlc.ReadyWindowPauseClock, bool) {
	var result sqlc.ReadyWindowPauseClock
	found := false
	for _, row := range rows {
		if row.ReadyWindowID != windowID || row.ResumedAt.Valid || row.ResumedDeadline.Valid {
			continue
		}
		if found {
			return sqlc.ReadyWindowPauseClock{}, false
		}
		result, found = row, true
	}
	return result, found
}

func (r *TournamentAdminNormalPausePostgres) RebindNormalPauseWave(
	ctx context.Context,
	tournamentID, waveID, commandID uuid.UUID,
	authority authoritydomain.Identity,
	reboundAt time.Time,
) error {
	rebound, err := r.tx.Querier(ctx).RebindTournamentAdminNormalPauseGameEpochs(ctx, sqlc.RebindTournamentAdminNormalPauseGameEpochsParams{
		TournamentID: tournamentID, WaveID: waveID, CommandID: commandID,
		AuthorityHolderID: authority.HolderID, AuthorityLeaseID: authority.LeaseID,
		AuthorityEpoch: authority.Epoch, ReboundAt: tstz(reboundAt),
	})
	if err != nil {
		return executionWriteError("rebind resumed normal-pause Games", err)
	}
	if len(rebound) == 0 || !uniqueTournamentAdminExecutionIDs(rebound) {
		return domain.ErrConflict
	}
	return nil
}

var _ gameusecase.NormalPauseRepository = (*TournamentAdminNormalPausePostgres)(nil)
var _ gameusecase.PauseResumeRepository = (*TournamentAdminNormalPausePostgres)(nil)
var _ gameusecase.PauseResumePresenceRepository = (*TournamentAdminNormalPausePostgres)(nil)
