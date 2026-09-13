package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

//nolint:gocyclo // One transaction keeps the full pause graph atomic and fail-closed.
func (r *TournamentAdminExecutionPostgres) CommitNormalPause(
	ctx context.Context,
	expected gameusecase.PauseGraphRevisions,
	record gameusecase.NormalPauseRecord,
) (*gameusecase.NormalPauseRecord, bool, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) || !reflect.DeepEqual(expected, record.Expected) {
		return nil, false, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	if err := pauseTournamentAdminNormalRoot(ctx, querier, record); err != nil {
		return nil, false, err
	}
	if frozen, ok := normalPauseFrozenDeadline(record.Graph.FrozenDeadlines, gameusecase.PauseDeadlineReadyWindow); ok {
		created, err := querier.CreateTournamentAdminNormalPauseReadyWindowClock(ctx, sqlc.CreateTournamentAdminNormalPauseReadyWindowClockParams{
			PauseID: record.PauseID, ReadyWindowID: frozen.OwnerID, WaveID: record.Scope.WaveID,
			RosterID: record.Scope.RosterID, OriginalDeadline: tstz(frozen.OriginalDeadline), FrozenAt: tstz(frozen.FrozenAt),
		})
		if err != nil || created != record.PauseID {
			return nil, false, normalPauseCAS("create ready-window clock", err)
		}
	}
	seriesPauseIDs := make(map[uuid.UUID]uuid.UUID, len(record.Graph.Series))
	gamePauseIDs := make(map[uuid.UUID]uuid.UUID, len(record.Graph.Games))
	for _, series := range record.Graph.Series {
		if series.Execution.Series.State != domain.SeriesStateTechnicalPause || series.Execution.ResumeState == nil {
			continue
		}
		seriesID := series.Execution.Series.ID
		pauseID := tournamentAdminNormalPauseID(record.PauseID, "series", seriesID)
		seriesPauseIDs[seriesID] = pauseID
		if err := createTournamentAdminNormalPauseRow(ctx, querier, tournamentAdminNormalPauseRow{
			ID: pauseID, ScopeKind: "series", ScopeID: seriesID,
			SeriesID: seriesID, Origin: string(*series.Execution.ResumeState), Record: record,
		}); err != nil {
			return nil, false, err
		}
		if _, err := querier.PauseTournamentAdminNormalSeriesCAS(ctx, sqlc.PauseTournamentAdminNormalSeriesCASParams{
			PausedAt: tstz(record.PausedAt), ID: seriesID, TournamentID: record.Scope.TournamentID,
			ExpectedRevision: childExpectedRevision(record.Expected.Series, seriesID), ExpectedState: string(*series.Execution.ResumeState),
		}); err != nil {
			return nil, false, normalPauseCAS("pause Series", err)
		}
	}
	for _, game := range record.Graph.Games {
		if game.Game.State != domain.GameStatePaused || game.ResumeState == nil {
			continue
		}
		pauseID := tournamentAdminNormalPauseID(record.PauseID, "game", game.Game.ID)
		gamePauseIDs[game.SeriesID] = pauseID
		if err := createTournamentAdminNormalPauseRow(ctx, querier, tournamentAdminNormalPauseRow{
			ID: pauseID, ParentID: seriesPauseIDs[game.SeriesID], ScopeKind: "game_attempt", ScopeID: game.Game.ID,
			SeriesID: game.SeriesID, GameID: game.Game.ID, Depth: 1, Origin: string(*game.ResumeState), Record: record,
		}); err != nil {
			return nil, false, err
		}
		if _, err := querier.PauseTournamentAdminNormalGameCAS(ctx, sqlc.PauseTournamentAdminNormalGameCASParams{
			PausedAt: tstz(record.PausedAt), ID: game.Game.ID,
			ExpectedRevision: childExpectedRevision(record.Expected.Games, game.Game.ID),
		}); err != nil {
			return nil, false, normalPauseCAS("pause Game", err)
		}
		frozen, ok := frozenDeadline(record.Graph.FrozenDeadlines, game.Game.ID)
		if !ok {
			return nil, false, domain.ErrInternal
		}
		created, err := querier.CreateTournamentAdminNormalPauseClock(ctx, sqlc.CreateTournamentAdminNormalPauseClockParams{
			PauseID: pauseID, GameAttemptID: game.Game.ID, OriginalDeadline: tstz(frozen.OriginalDeadline),
			FrozenAt: tstz(frozen.FrozenAt), FrozenRemainingMs: frozen.Remaining.Milliseconds(),
		})
		if err != nil || created != pauseID {
			return nil, false, normalPauseCAS("create Game clock", err)
		}
	}
	for _, presence := range record.Graph.Presence {
		pauseIDs := []uuid.UUID{record.PauseID, seriesPauseIDs[presence.SeriesID], gamePauseIDs[presence.SeriesID]}
		for _, pauseID := range pauseIDs {
			if pauseID == uuid.Nil {
				continue
			}
			created, err := querier.CreateTournamentAdminNormalPausePresenceSnapshot(ctx, sqlc.CreateTournamentAdminNormalPausePresenceSnapshotParams{
				PauseID: pauseID, RosterID: presence.RosterID, SeriesID: presence.SeriesID,
				ParticipantID: presence.ParticipantID, PresenceState: string(presence.State),
				PresenceEpoch: presence.PresenceEpoch, PresenceRevision: presence.Revision, CapturedAt: tstz(record.PausedAt),
			})
			if err != nil || created != presence.ParticipantID {
				return nil, false, normalPauseCAS("snapshot Presence", err)
			}
		}
	}
	createdGamePauses := make(map[uuid.UUID]struct{}, len(gamePauseIDs))
	for _, pauseID := range gamePauseIDs {
		createdGamePauses[pauseID] = struct{}{}
	}
	for _, counter := range record.Graph.Counters {
		if _, createdForGraph := createdGamePauses[counter.PauseID]; !createdForGraph {
			continue
		}
		if counter.Validate() != nil || counter.Limit > math.MaxInt16 || counter.Used > math.MaxInt16 {
			return nil, false, domain.ErrInternal
		}
		createdParticipant, err := querier.CreateTournamentAdminNormalPauseCounter(ctx, sqlc.CreateTournamentAdminNormalPauseCounterParams{
			PauseID: counter.PauseID, RosterID: counter.RosterID, ParticipantID: counter.ParticipantID,
			// Bounds are checked immediately above before conversion to PostgreSQL smallint.
			SlotLimit: int16(counter.Limit), SlotsUsed: int16(counter.Used), Revision: counter.Revision, //nolint:gosec // Bounds checked above.
			CreatedAt: tstz(record.PausedAt),
		})
		if err != nil || createdParticipant != counter.ParticipantID {
			return nil, false, normalPauseCAS("create reconnect counter", err)
		}
	}
	for _, suspended := range record.SuspendedReconnect {
		updated, err := querier.SuspendTournamentAdminNormalPauseReconnectCAS(ctx, sqlc.SuspendTournamentAdminNormalPauseReconnectCASParams{
			PausedAt: tstz(record.PausedAt), NormalPauseID: nullableUUID(&record.PauseID), ID: suspended.ID,
			ExpectedRevision: suspended.Revision,
		})
		if err != nil || updated != suspended.ID {
			return nil, false, normalPauseCAS("suspend reconnect", err)
		}
	}
	if err := appendTournamentAdminNormalPauseDraft(ctx, querier, record.Expected.Draft, record.Graph.Draft, record.PausedAt); err != nil {
		return nil, false, err
	}
	clone := record
	return &clone, true, nil
}

func pauseTournamentAdminNormalRoot(ctx context.Context, querier *sqlc.Queries, record gameusecase.NormalPauseRecord) error {
	origin := record.Graph.Wave.Wave.State
	if origin == domain.WaveStatePaused {
		origin = domain.WaveStateActive
	}
	if err := createTournamentAdminNormalPauseRow(ctx, querier, tournamentAdminNormalPauseRow{
		ID: record.PauseID, ScopeKind: "wave", ScopeID: record.Scope.WaveID, WaveID: record.Scope.WaveID,
		Origin: string(origin), Record: record,
	}); err != nil {
		return err
	}
	expectedState := string(record.Expected.TournamentState)
	if _, err := querier.PauseTournamentAdminNormalTournamentCAS(ctx, sqlc.PauseTournamentAdminNormalTournamentCASParams{
		ExpectedState: &expectedState, PausedAt: tstz(record.PausedAt),
		ID: record.Scope.TournamentID, ExpectedRevision: record.Expected.TournamentRevision,
	}); err != nil {
		return normalPauseCAS("pause Tournament", err)
	}
	if record.Graph.Wave.Revision == record.Expected.WaveRevision {
		return nil
	}
	expectedWaveState := record.Graph.Wave.Wave.State
	pausedAt := nullableTSTZ(nil)
	if expectedWaveState == domain.WaveStatePaused {
		expectedWaveState = domain.WaveStateActive
		pausedAt = tstz(record.PausedAt)
	}
	if _, err := querier.PauseTournamentAdminNormalWaveCAS(ctx, sqlc.PauseTournamentAdminNormalWaveCASParams{
		NextState: string(record.Graph.Wave.Wave.State), PausedAt: pausedAt,
		ID: record.Scope.WaveID, TournamentID: record.Scope.TournamentID,
		ExpectedRevision: record.Expected.WaveRevision, ExpectedState: string(expectedWaveState),
	}); err != nil {
		return normalPauseCAS("pause Wave", err)
	}
	return nil
}

type tournamentAdminNormalPauseRow struct {
	ID, ParentID, ScopeID, WaveID, SeriesID, GameID uuid.UUID
	ScopeKind, Origin                               string
	Depth                                           int16
	Record                                          gameusecase.NormalPauseRecord
}

func createTournamentAdminNormalPauseRow(ctx context.Context, querier *sqlc.Queries, row tournamentAdminNormalPauseRow) error {
	revisionID := tournamentAdminNormalPauseID(row.ID, "revision", row.ID)
	created, err := querier.CreateTournamentAdminNormalPause(ctx, sqlc.CreateTournamentAdminNormalPauseParams{
		ID: row.ID, TournamentID: row.Record.Scope.TournamentID, RosterID: row.Record.Scope.RosterID,
		ScopeKind: row.ScopeKind, ScopeID: row.ScopeID, WaveID: nullableUUID(uuidPointer(row.WaveID)),
		SeriesID: nullableUUID(uuidPointer(row.SeriesID)), GameAttemptID: nullableUUID(uuidPointer(row.GameID)),
		ParentPauseID: nullableUUID(uuidPointer(row.ParentID)), Depth: row.Depth, Reason: string(row.Record.Reason),
		PausedFromState: row.Origin, CurrentRevisionID: revisionID, StartedAt: tstz(row.Record.PausedAt),
	})
	if err != nil || created != row.ID {
		return normalPauseCAS("create pause", err)
	}
	createdRevision, err := querier.CreateTournamentAdminNormalPauseRevision(ctx, sqlc.CreateTournamentAdminNormalPauseRevisionParams{
		ID: revisionID, PauseID: row.ID, RevisionNumber: 1, State: string(gameusecase.PauseStateActive),
		CreatedAt: tstz(row.Record.PausedAt),
	})
	if err != nil || createdRevision != revisionID {
		return normalPauseCAS("create pause revision", err)
	}
	return nil
}

func (r *TournamentAdminExecutionPostgres) CommitPauseResume(
	ctx context.Context,
	expected gameusecase.PauseResumeExpectation,
	record gameusecase.PauseResumeRecord,
) (*gameusecase.PauseResumeRecord, bool, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) || !reflect.DeepEqual(expected, record.Expected) {
		return nil, false, domain.ErrValidation
	}
	document, err := r.tx.Querier(ctx).GetTournamentAdminNormalPauseReceipt(ctx, sqlc.GetTournamentAdminNormalPauseReceiptParams{
		TournamentID: record.Scope.TournamentID, WaveID: record.Scope.WaveID, PauseID: record.PauseID,
	})
	if err != nil {
		return nil, false, normalPauseCAS("load pause receipt", err)
	}
	_, paused := decodeTournamentAdminWaveResult("pause", document)
	if paused == nil {
		return nil, false, domain.ErrInternal
	}
	querier := r.tx.Querier(ctx)
	if _, err := querier.ResumeTournamentAdminNormalTournamentCAS(ctx, sqlc.ResumeTournamentAdminNormalTournamentCASParams{
		ResumeState: string(expected.TournamentState), ResumedAt: tstz(record.ResumedAt),
		ID: record.Scope.TournamentID, ExpectedRevision: expected.TournamentRevision,
	}); err != nil {
		return nil, false, normalPauseCAS("resume Tournament", err)
	}
	if tournamentAdminNormalPauseHasActiveGames(record.Graph.Games) {
		if err := r.RebindNormalPauseWave(ctx, record.Scope.TournamentID, record.Scope.WaveID,
			record.CommandID, record.Scope.Authority, record.ResumedAt); err != nil {
			return nil, false, err
		}
	}
	if err := resumeTournamentAdminNormalChildren(ctx, querier, *paused, record); err != nil {
		return nil, false, err
	}
	if err := appendTournamentAdminNormalPauseDraft(ctx, querier, expected.Draft, record.Graph.Draft, record.ResumedAt); err != nil {
		return nil, false, err
	}
	if err := resumeTournamentAdminNormalReadyWindow(ctx, querier, *paused, record); err != nil {
		return nil, false, err
	}
	rootOldRevisionID := tournamentAdminNormalPauseID(record.PauseID, "revision", record.PauseID)
	rootNewRevisionID := tournamentAdminNormalPauseID(record.CommandID, "resume-revision", record.PauseID)
	if err := resumeTournamentAdminPauseRow(ctx, querier, record.PauseID, rootOldRevisionID, rootNewRevisionID,
		expected.PauseRevision, record.ResumedAt); err != nil {
		return nil, false, err
	}
	if record.Graph.Wave.Revision != expected.WaveRevision {
		if _, err := querier.ResumeTournamentAdminNormalWaveCAS(ctx, sqlc.ResumeTournamentAdminNormalWaveCASParams{
			ResumeState: string(record.Graph.Wave.Wave.State), ExpectedState: string(paused.Graph.Wave.Wave.State),
			ResumedAt: tstz(record.ResumedAt), ID: record.Scope.WaveID, TournamentID: record.Scope.TournamentID,
			ExpectedRevision: expected.WaveRevision,
		}); err != nil {
			return nil, false, normalPauseCAS("resume Wave", err)
		}
	}
	clone := record
	return &clone, true, nil
}

//nolint:gocyclo // Child restoration covers Series, Game, Draft, presence, and every frozen clock.
func resumeTournamentAdminNormalChildren(
	ctx context.Context,
	querier *sqlc.Queries,
	paused gameusecase.NormalPauseRecord,
	record gameusecase.PauseResumeRecord,
) error {
	livePresence := make(map[uuid.UUID]pausedomain.PausePresence, len(record.Graph.Presence))
	prePresence := make(map[uuid.UUID]pausedomain.PausePresence, len(paused.Graph.Presence))
	for _, value := range record.Graph.Presence {
		livePresence[value.ParticipantID] = value
	}
	for _, value := range paused.Graph.Presence {
		prePresence[value.ParticipantID] = value
	}
	for _, series := range paused.Graph.Series {
		if series.Execution.Series.State != domain.SeriesStateTechnicalPause || series.Execution.ResumeState == nil {
			continue
		}
		seriesID := series.Execution.Series.ID
		resumedSeries, ok := normalPauseSeries(record.Graph.Series, seriesID)
		if !ok || resumedSeries.Execution.Series.State != *series.Execution.ResumeState || resumedSeries.Execution.ResumeState != nil {
			continue
		}
		seriesPauseID := tournamentAdminNormalPauseID(paused.PauseID, "series", seriesID)
		first := livePresence[series.Execution.Series.FirstParticipantID]
		second := livePresence[series.Execution.Series.SecondParticipantID]
		firstPre := prePresence[first.ParticipantID]
		secondPre := prePresence[second.ParticipantID]
		decisionID := tournamentAdminNormalPauseID(record.CommandID, "resume-decision", seriesID)
		created, err := querier.CreateTournamentAdminNormalResumeDecision(ctx, sqlc.CreateTournamentAdminNormalResumeDecisionParams{
			ID: decisionID, PauseID: seriesPauseID, DecisionNumber: 1,
			Action:             string(gameusecase.PauseResumeActionResume),
			FirstParticipantID: first.ParticipantID, SecondParticipantID: second.ParticipantID,
			FirstPrePauseState: string(firstPre.State), SecondPrePauseState: string(secondPre.State),
			FirstLiveState: string(first.State), SecondLiveState: string(second.State),
			FirstPresenceEpoch: first.PresenceEpoch, SecondPresenceEpoch: second.PresenceEpoch,
			FirstPresenceRevision: first.Revision, SecondPresenceRevision: second.Revision,
			DecidedAt: tstz(record.ResumedAt),
		})
		if err != nil || created != decisionID {
			return normalPauseCAS("create resume decision", err)
		}
		for _, game := range record.Graph.Games {
			if game.SeriesID != seriesID || game.Game.State != domain.GameStateActive {
				continue
			}
			gamePauseID := tournamentAdminNormalPauseID(paused.PauseID, "game", game.Game.ID)
			gameDecisionID := tournamentAdminNormalPauseID(record.CommandID, "resume-game-decision", game.Game.ID)
			created, err = querier.CreateTournamentAdminNormalResumeDecision(ctx, sqlc.CreateTournamentAdminNormalResumeDecisionParams{
				ID: gameDecisionID, PauseID: gamePauseID, DecisionNumber: 1,
				Action:             string(gameusecase.PauseResumeActionResume),
				FirstParticipantID: first.ParticipantID, SecondParticipantID: second.ParticipantID,
				FirstPrePauseState: string(firstPre.State), SecondPrePauseState: string(secondPre.State),
				FirstLiveState: string(first.State), SecondLiveState: string(second.State),
				FirstPresenceEpoch: first.PresenceEpoch, SecondPresenceEpoch: second.PresenceEpoch,
				FirstPresenceRevision: first.Revision, SecondPresenceRevision: second.Revision,
				DecidedAt: tstz(record.ResumedAt),
			})
			if err != nil || created != gameDecisionID {
				return normalPauseCAS("create Game resume decision", err)
			}
			frozen, ok := frozenDeadline(record.Graph.FrozenDeadlines, game.Game.ID)
			if !ok || frozen.ResumedDeadline == nil {
				return domain.ErrInternal
			}
			if _, err := querier.ResumeTournamentAdminNormalPauseClockCAS(ctx, sqlc.ResumeTournamentAdminNormalPauseClockCASParams{
				ResumedAt: tstz(record.ResumedAt), ResumedDeadline: tstz(*frozen.ResumedDeadline),
				PauseID: gamePauseID, ExpectedRevision: frozen.Revision - 1,
			}); err != nil {
				return normalPauseCAS("resume Game clock", err)
			}
			oldRevisionID := tournamentAdminNormalPauseID(gamePauseID, "revision", gamePauseID)
			newRevisionID := tournamentAdminNormalPauseID(record.CommandID, "resume-game-revision", game.Game.ID)
			if err := resumeTournamentAdminPauseRow(ctx, querier, gamePauseID, oldRevisionID, newRevisionID, 1, record.ResumedAt); err != nil {
				return err
			}
			if _, err := querier.ResumeTournamentAdminNormalGameCAS(ctx, sqlc.ResumeTournamentAdminNormalGameCASParams{
				ResumedAt: tstz(record.ResumedAt), ID: game.Game.ID, ExpectedRevision: game.Revision - 1,
			}); err != nil {
				return normalPauseCAS("resume Game", err)
			}
		}
		oldRevisionID := tournamentAdminNormalPauseID(seriesPauseID, "revision", seriesPauseID)
		newRevisionID := tournamentAdminNormalPauseID(record.CommandID, "resume-series-revision", seriesID)
		if err := resumeTournamentAdminPauseRow(ctx, querier, seriesPauseID, oldRevisionID, newRevisionID, 1, record.ResumedAt); err != nil {
			return err
		}
		if _, err := querier.ResumeTournamentAdminNormalSeriesCAS(ctx, sqlc.ResumeTournamentAdminNormalSeriesCASParams{
			ResumedAt: tstz(record.ResumedAt), ID: seriesID, TournamentID: record.Scope.TournamentID,
			ExpectedRevision: series.Revision, ResumeState: string(resumedSeries.Execution.Series.State),
		}); err != nil {
			return normalPauseCAS("resume Series", err)
		}
	}
	return nil
}

func normalPauseSeries(values []gameusecase.PauseSeries, id uuid.UUID) (gameusecase.PauseSeries, bool) {
	for _, value := range values {
		if value.Execution.Series.ID == id {
			return value, true
		}
	}
	return gameusecase.PauseSeries{}, false
}

func resumeTournamentAdminPauseRow(
	ctx context.Context,
	querier *sqlc.Queries,
	pauseID, oldRevisionID, newRevisionID uuid.UUID,
	expectedRevision int64,
	resumedAt time.Time,
) error {
	reason := "operator resume"
	created, err := querier.CreateTournamentAdminNormalPauseRevision(ctx, sqlc.CreateTournamentAdminNormalPauseRevisionParams{
		ID: newRevisionID, PauseID: pauseID, PreviousRevisionID: nullableUUID(&oldRevisionID),
		RevisionNumber: expectedRevision + 1, State: string(gameusecase.PauseStateResumed),
		TransitionReason: &reason, CreatedAt: tstz(resumedAt),
	})
	if err != nil || created != newRevisionID {
		return normalPauseCAS("create resume revision", err)
	}
	updated, err := querier.ResumeTournamentAdminNormalPauseCAS(ctx, sqlc.ResumeTournamentAdminNormalPauseCASParams{
		CurrentRevisionID: newRevisionID, ResumedAt: tstz(resumedAt), ID: pauseID,
		ExpectedRevision: expectedRevision, ExpectedRevisionID: oldRevisionID,
	})
	if err != nil || updated != pauseID {
		return normalPauseCAS("resume pause", err)
	}
	return nil
}

func childExpectedRevision(values []gameusecase.PauseChildRevision, id uuid.UUID) int64 {
	for _, value := range values {
		if value.ID == id {
			return value.Revision
		}
	}
	return 0
}

func tournamentAdminNormalPauseHasActiveGames(values []gameusecase.PauseGame) bool {
	for _, game := range values {
		if game.Game.State == domain.GameStateActive {
			return true
		}
	}
	return false
}

func frozenDeadline(values []gameusecase.PauseFrozenDeadline, id uuid.UUID) (gameusecase.PauseFrozenDeadline, bool) {
	for _, value := range values {
		if value.Kind == gameusecase.PauseDeadlineGame && value.OwnerID == id {
			return value, true
		}
	}
	return gameusecase.PauseFrozenDeadline{}, false
}

func normalPauseFrozenDeadline(
	values []gameusecase.PauseFrozenDeadline,
	kind gameusecase.PauseDeadlineKind,
) (gameusecase.PauseFrozenDeadline, bool) {
	for _, value := range values {
		if value.Kind == kind {
			return value, true
		}
	}
	return gameusecase.PauseFrozenDeadline{}, false
}

func resumeTournamentAdminNormalReadyWindow(
	ctx context.Context,
	querier *sqlc.Queries,
	paused gameusecase.NormalPauseRecord,
	record gameusecase.PauseResumeRecord,
) error {
	before, ok := normalPauseFrozenDeadline(paused.Graph.FrozenDeadlines, gameusecase.PauseDeadlineReadyWindow)
	if !ok {
		return nil
	}
	after, ok := normalPauseFrozenDeadline(record.Graph.FrozenDeadlines, gameusecase.PauseDeadlineReadyWindow)
	if !ok || after.ResumedDeadline == nil {
		return domain.ErrInternal
	}
	updated, err := querier.ResumeTournamentAdminNormalPauseReadyWindowClockCAS(ctx, sqlc.ResumeTournamentAdminNormalPauseReadyWindowClockCASParams{
		ResumedAt: tstz(record.ResumedAt), ResumedDeadline: tstz(*after.ResumedDeadline),
		PauseID: record.PauseID, ExpectedRevision: before.Revision,
	})
	if err != nil || updated != record.PauseID {
		return normalPauseCAS("resume ready-window clock", err)
	}
	windowID, err := querier.ShiftTournamentAdminNormalPauseReadyWindowDeadlineCAS(ctx, sqlc.ShiftTournamentAdminNormalPauseReadyWindowDeadlineCASParams{
		ResumedDeadline: tstz(*after.ResumedDeadline), ReadyWindowID: before.OwnerID,
		WaveID: record.Scope.WaveID, RosterID: record.Scope.RosterID, OriginalDeadline: tstz(before.OriginalDeadline),
	})
	if err != nil || windowID != before.OwnerID {
		return normalPauseCAS("shift ready-window deadline", err)
	}
	return nil
}

//nolint:gocyclo // One transaction resolves both participants and the root graph atomically.
func (r *TournamentAdminExecutionPostgres) CommitPauseResumePresence(
	ctx context.Context,
	expected gameusecase.PauseResumePresenceExpectation,
	record gameusecase.PauseResumePresenceRecord,
) (*gameusecase.PauseResumePresenceRecord, bool, error) {
	command := record.Command
	commandExpected := gameusecase.PauseResumePresenceExpectation{
		Resume: command.Resume.Expected, Series: command.SeriesExpected, Game: command.GameExpected,
		Presence: command.Presence, Reconnect: command.Reconnect, Counters: command.Counters,
		FrozenDeadlines: command.FrozenDeadlines,
	}
	if !validTournamentAdminExecutionRepository(ctx, r) || !reflect.DeepEqual(expected, commandExpected) {
		return nil, false, domain.ErrValidation
	}
	if record.GameDecision.Action == gameusecase.PauseResumeActionResume {
		return nil, false, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	document, err := querier.GetTournamentAdminNormalPauseReceipt(ctx, sqlc.GetTournamentAdminNormalPauseReceiptParams{
		TournamentID: command.Resume.Scope.TournamentID, WaveID: command.Resume.Scope.WaveID,
		PauseID: command.Resume.PauseID,
	})
	if err != nil {
		return nil, false, normalPauseCAS("load pause receipt", err)
	}
	_, paused := decodeTournamentAdminWaveResult("pause", document)
	if paused == nil {
		return nil, false, domain.ErrInternal
	}
	if err := persistTournamentAdminNormalReconnectResolution(ctx, querier, record.First, paused.Graph.Reconnect, record.DecidedAt); err != nil {
		return nil, false, err
	}
	if err := persistTournamentAdminNormalReconnectResolution(ctx, querier, record.Second, paused.Graph.Reconnect, record.DecidedAt); err != nil {
		return nil, false, err
	}
	firstPre, firstLive, ok := tournamentAdminNormalPresenceEvidence(paused.Graph.Presence, command.Presence, record.First.ParticipantID)
	if !ok {
		return nil, false, domain.ErrInternal
	}
	secondPre, secondLive, ok := tournamentAdminNormalPresenceEvidence(paused.Graph.Presence, command.Presence, record.Second.ParticipantID)
	if !ok {
		return nil, false, domain.ErrInternal
	}
	created, err := querier.CreateTournamentAdminNormalResumeDecision(ctx, sqlc.CreateTournamentAdminNormalResumeDecisionParams{
		ID: record.GameDecision.ID, PauseID: record.GameDecision.PauseID,
		DecisionNumber: record.GameDecision.DecisionNumber, Action: string(record.GameDecision.Action),
		FirstParticipantID: firstLive.ParticipantID, SecondParticipantID: secondLive.ParticipantID,
		FirstPrePauseState: string(firstPre.State), SecondPrePauseState: string(secondPre.State),
		FirstLiveState: string(firstLive.State), SecondLiveState: string(secondLive.State),
		FirstPresenceEpoch: firstLive.PresenceEpoch, SecondPresenceEpoch: secondLive.PresenceEpoch,
		FirstPresenceRevision: firstLive.Revision, SecondPresenceRevision: secondLive.Revision,
		FirstReconnectIntervalID:  nullableUUID(record.GameDecision.FirstReconnectIntervalID),
		SecondReconnectIntervalID: nullableUUID(record.GameDecision.SecondReconnectIntervalID),
		DecidedAt:                 tstz(record.DecidedAt),
	})
	if err != nil || created != record.GameDecision.ID {
		return nil, false, normalPauseCAS("create reconnect resume decision", err)
	}
	base := command.Resume.Expected
	if _, err := querier.ResumeTournamentAdminNormalTournamentCAS(ctx, sqlc.ResumeTournamentAdminNormalTournamentCASParams{
		ResumeState: string(base.TournamentState), ResumedAt: tstz(record.DecidedAt),
		ID: command.Resume.Scope.TournamentID, ExpectedRevision: base.TournamentRevision,
	}); err != nil {
		return nil, false, normalPauseCAS("resume Tournament with disconnected Game", err)
	}
	connected := tournamentAdminNormalConnectedResume(*paused, record)
	if tournamentAdminNormalPauseHasActiveGames(connected.Graph.Games) {
		if err := r.RebindNormalPauseWave(ctx, command.Resume.Scope.TournamentID, command.Resume.Scope.WaveID,
			command.Resume.CommandID, command.Resume.Scope.Authority, record.DecidedAt); err != nil {
			return nil, false, err
		}
	}
	if err := resumeTournamentAdminNormalChildren(ctx, querier, *paused, connected); err != nil {
		return nil, false, err
	}
	rootOldRevisionID := tournamentAdminNormalPauseID(command.Resume.PauseID, "revision", command.Resume.PauseID)
	rootNewRevisionID := tournamentAdminNormalPauseID(command.Resume.CommandID, "resume-revision", command.Resume.PauseID)
	if err := resumeTournamentAdminPauseRow(ctx, querier, command.Resume.PauseID, rootOldRevisionID, rootNewRevisionID,
		base.PauseRevision, record.DecidedAt); err != nil {
		return nil, false, err
	}
	if _, err := querier.ResumeTournamentAdminNormalWaveCAS(ctx, sqlc.ResumeTournamentAdminNormalWaveCASParams{
		ResumeState: string(domain.WaveStateActive), ExpectedState: string(domain.WaveStatePaused),
		ResumedAt: tstz(record.DecidedAt), ID: command.Resume.Scope.WaveID,
		TournamentID: command.Resume.Scope.TournamentID, ExpectedRevision: base.WaveRevision,
	}); err != nil {
		return nil, false, normalPauseCAS("resume Wave with disconnected Game", err)
	}
	clone := record
	return &clone, true, nil
}

func tournamentAdminNormalConnectedResume(
	paused gameusecase.NormalPauseRecord,
	presence gameusecase.PauseResumePresenceRecord,
) gameusecase.PauseResumeRecord {
	graph := paused.Graph
	graph.Series = append([]gameusecase.PauseSeries(nil), paused.Graph.Series...)
	graph.Games = append([]gameusecase.PauseGame(nil), paused.Graph.Games...)
	graph.FrozenDeadlines = append([]gameusecase.PauseFrozenDeadline(nil), paused.Graph.FrozenDeadlines...)
	graph.Presence = append([]pausedomain.PausePresence(nil), presence.Command.Presence...)
	live := make(map[uuid.UUID]pausedomain.PresenceState, len(graph.Presence))
	for _, value := range graph.Presence {
		live[value.ParticipantID] = value.State
	}
	connectedSeries := make(map[uuid.UUID]struct{}, len(graph.Series))
	for index := range graph.Series {
		series := &graph.Series[index]
		if live[series.Execution.Series.FirstParticipantID] != pausedomain.PresenceStateConnected ||
			live[series.Execution.Series.SecondParticipantID] != pausedomain.PresenceStateConnected ||
			series.Execution.ResumeState == nil {
			continue
		}
		series.Execution.Series.State = *series.Execution.ResumeState
		series.Execution.ResumeState = nil
		series.Revision++
		connectedSeries[series.Execution.Series.ID] = struct{}{}
	}
	for index := range graph.Games {
		game := &graph.Games[index]
		if _, ok := connectedSeries[game.SeriesID]; !ok || game.ResumeState == nil {
			continue
		}
		game.Game.State = *game.ResumeState
		game.ResumeState = nil
		game.Revision++
		for frozenIndex := range graph.FrozenDeadlines {
			frozen := &graph.FrozenDeadlines[frozenIndex]
			if frozen.Kind != gameusecase.PauseDeadlineGame || frozen.OwnerID != game.Game.ID {
				continue
			}
			deadline := presence.DecidedAt.Add(frozen.Remaining)
			resumedAt := presence.DecidedAt
			frozen.ResumedAt = &resumedAt
			frozen.ResumedDeadline = &deadline
			frozen.Revision++
			game.Deadline = &deadline
		}
	}
	return gameusecase.PauseResumeRecord{
		Scope: presence.Command.Resume.Scope, PauseID: presence.Command.Resume.PauseID,
		CommandID: presence.Command.Resume.CommandID, ActorID: presence.Command.Resume.ActorID,
		Expected: presence.Command.Resume.Expected, Graph: graph, ResumedAt: presence.DecidedAt,
	}
}

//nolint:gocyclo // Reconnect continuation and fresh-cycle persistence have separate CAS evidence.
func persistTournamentAdminNormalReconnectResolution(
	ctx context.Context,
	querier *sqlc.Queries,
	resolution gameusecase.PauseResumeParticipantResolution,
	durableReconnect []pausedomain.PauseReconnectInterval,
	decidedAt time.Time,
) error {
	interval := resolution.CurrentInterval
	if interval == nil {
		return nil
	}
	pauseID := interval.PauseID
	if resolution.Disposition == gameusecase.PauseResumeParticipantContinuation && resolution.SourceInterval != nil {
		for _, source := range durableReconnect {
			if source.ID == resolution.SourceInterval.ID {
				pauseID = source.PauseID
				break
			}
		}
	}
	if interval.Validate() != nil || interval.Number > math.MaxInt16 || interval.ContinuationNumber > math.MaxInt32 {
		return domain.ErrInternal
	}
	created, err := querier.CreateTournamentAdminNormalPauseReconnectInterval(ctx, sqlc.CreateTournamentAdminNormalPauseReconnectIntervalParams{
		ID: interval.ID, PauseID: pauseID, RosterID: interval.RosterID,
		SeriesID: interval.SeriesID, GameAttemptID: interval.GameID, ParticipantID: interval.ParticipantID,
		PresenceEpoch: interval.PresenceEpoch, IntervalNumber: int16(interval.Number), //nolint:gosec // Bound checked above.
		OpenedAt: tstz(interval.OpenedAt), DeadlineAt: tstz(interval.Deadline),
		ContinuationNumber: int32(interval.ContinuationNumber), ContinuedFromID: nullableUUID(interval.ContinuedFromID), //nolint:gosec // Bound checked above.
	})
	if err != nil || created != interval.ID {
		return normalPauseCAS("create reconnect interval", err)
	}
	if resolution.Disposition != gameusecase.PauseResumeParticipantFresh {
		return nil
	}
	if resolution.Counter.Used < 1 || resolution.Counter.Revision < 2 {
		return domain.ErrInternal
	}
	if resolution.Counter.Used-1 > math.MaxInt16 {
		return domain.ErrInternal
	}
	participantID, err := querier.AdvanceTournamentAdminNormalPauseCounterCAS(ctx, sqlc.AdvanceTournamentAdminNormalPauseCounterCASParams{
		UpdatedAt: tstz(decidedAt), PauseID: resolution.Counter.PauseID,
		ParticipantID: resolution.ParticipantID, ExpectedRevision: resolution.Counter.Revision - 1,
		ExpectedSlotsUsed: int16(resolution.Counter.Used - 1), //nolint:gosec // Bound checked above.
	})
	if err != nil || participantID != resolution.ParticipantID {
		return normalPauseCAS("advance reconnect counter", err)
	}
	return nil
}

func tournamentAdminNormalPresenceEvidence(
	prePause []pausedomain.PausePresence,
	live []pausedomain.PausePresence,
	participantID uuid.UUID,
) (pausedomain.PausePresence, pausedomain.PausePresence, bool) {
	var before, current pausedomain.PausePresence
	foundBefore, foundCurrent := false, false
	for _, value := range prePause {
		if value.ParticipantID == participantID {
			before, foundBefore = value, true
		}
	}
	for _, value := range live {
		if value.ParticipantID == participantID {
			current, foundCurrent = value, true
		}
	}
	return before, current, foundBefore && foundCurrent
}

func appendTournamentAdminNormalPauseDraft(
	ctx context.Context,
	querier *sqlc.Queries,
	expected *draftusecase.RevisionExpectation,
	draft *draftusecase.Execution,
	createdAt time.Time,
) error {
	if expected == nil && draft == nil {
		return nil
	}
	if expected == nil || draft == nil || draft.PreviousRevisionID != expected.RevisionID ||
		draft.Revision != expected.Revision+1 || draft.ServiceEpoch != expected.ServiceEpoch {
		return domain.ErrInternal
	}
	transition := draft.Transition
	if draft.State == draftusecase.ExecutionStateActive {
		// Active Draft revisions cannot carry recovery_evidence. The immutable
		// pause receipt retains the resume transition while the Draft row stores
		// the resumed state, shifted deadline, and CAS lineage.
		transition = nil
	}
	evidence, err := participantDraftEvidenceMap(draft.Recovery, transition)
	if err != nil {
		return err
	}
	input := DraftRevisionInput{
		ID: draft.RevisionID, CommandID: draft.CommandID, ServiceEpoch: draft.ServiceEpoch,
		State: DraftPersistenceState(draft.State), TurnNumber: draft.Turn,
		CurrentActorID: draft.CurrentActorID, CurrentAction: draft.CurrentAction,
		AbsoluteDeadline: draft.AbsoluteDeadline, RecoveryEvidence: evidence,
		SelectedCategories: append([]domain.Category(nil), draft.SelectedCategories...), CreatedAt: createdAt,
	}
	if draft.PausedRemaining > 0 {
		milliseconds := int(draft.PausedRemaining / time.Millisecond)
		input.PausedRemainingMS = &milliseconds
	}
	if draft.Recovery != nil {
		reason := string(draft.Recovery.Reason)
		input.RecoveryReason = &reason
	}
	params, err := draftRevisionParams(draft.ID, DraftRevisionExpectation{
		ID: expected.RevisionID, Revision: expected.Revision, ServiceEpoch: expected.ServiceEpoch,
	}, input)
	if err != nil {
		return err
	}
	created, err := querier.AppendDraftRevisionCAS(ctx, params)
	if err != nil || created.ID != draft.RevisionID {
		return normalPauseCAS("append Draft revision", err)
	}
	return nil
}

func tournamentAdminNormalPauseID(namespace uuid.UUID, kind string, id uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(kind+":"+id.String()))
}

func uuidPointer(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	return &value
}

func normalPauseCAS(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) || err == nil {
		return fmt.Errorf("%s: %w", operation, domain.ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
