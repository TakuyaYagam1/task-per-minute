package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
)

type tournamentExecutionSnapshot struct {
	Waves             []json.RawMessage `json:"waves"`
	Series            []json.RawMessage `json:"series"`
	Games             []json.RawMessage `json:"games"`
	Drafts            []json.RawMessage `json:"drafts"`
	ReadyWindows      []json.RawMessage `json:"ready_windows"`
	Readiness         []json.RawMessage `json:"readiness"`
	Assignments       []json.RawMessage `json:"assignments"`
	ChildPauses       []json.RawMessage `json:"child_pauses"`
	Reconnect         []json.RawMessage `json:"reconnect"`
	Golden            []json.RawMessage `json:"golden"`
	IncompleteCount   int               `json:"incomplete_count"`
	ActiveGoldenCount int               `json:"active_golden_count"`
}

func (r *TournamentAdminLifecyclePostgres) LockExecutionSnapshot(
	ctx context.Context,
	authority tournamentadmin.LifecycleAuthority,
) (tournamentadmin.LifecycleExecutionSnapshot, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) ||
		!validLifecycleTournamentView(authority.Tournament, authority.Tournament.ID) ||
		authority.ProjectionRevisionID == uuid.Nil || authority.ProjectionRevision < 1 {
		return tournamentadmin.LifecycleExecutionSnapshot{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	if err := lockTournamentExecutionRows(ctx, querier, authority); err != nil {
		return tournamentadmin.LifecycleExecutionSnapshot{}, err
	}
	document, err := querier.GetTournamentExecutionRevisionSnapshot(
		ctx,
		sqlc.GetTournamentExecutionRevisionSnapshotParams{
			TournamentID: authority.Tournament.ID,
			RosterID:     authority.Tournament.RosterID,
		},
	)
	if err != nil {
		return tournamentadmin.LifecycleExecutionSnapshot{}, fmt.Errorf(
			"TournamentAdminLifecyclePostgres - load execution snapshot: %w",
			err,
		)
	}
	return mapTournamentExecutionSnapshot(document, authority.ProjectionRevision)
}

func lockTournamentExecutionRows(
	ctx context.Context,
	querier *sqlc.Queries,
	authority tournamentadmin.LifecycleAuthority,
) error {
	tournamentID := authority.Tournament.ID
	rosterID := authority.Tournament.RosterID
	if _, err := querier.LockTournamentLifecycleWaves(
		ctx,
		sqlc.LockTournamentLifecycleWavesParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("waves", err)
	}
	if _, err := querier.LockTournamentLifecycleSeries(
		ctx,
		sqlc.LockTournamentLifecycleSeriesParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("series", err)
	}
	if _, err := querier.LockTournamentLifecycleGames(
		ctx,
		sqlc.LockTournamentLifecycleGamesParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("games", err)
	}
	if _, err := querier.LockTournamentLifecycleDraftRevisions(
		ctx,
		sqlc.LockTournamentLifecycleDraftRevisionsParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("draft revisions", err)
	}
	if _, err := querier.LockTournamentLifecycleReadyWindows(
		ctx,
		sqlc.LockTournamentLifecycleReadyWindowsParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("ready windows", err)
	}
	if _, err := querier.LockTournamentLifecycleReadiness(
		ctx,
		sqlc.LockTournamentLifecycleReadinessParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("readiness", err)
	}
	if _, err := querier.LockTournamentLifecycleAssignments(
		ctx,
		sqlc.LockTournamentLifecycleAssignmentsParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("assignments", err)
	}
	if _, err := querier.LockTournamentLifecycleChildPauses(
		ctx,
		sqlc.LockTournamentLifecycleChildPausesParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("child pauses", err)
	}
	if _, err := querier.LockTournamentLifecycleReconnect(
		ctx,
		sqlc.LockTournamentLifecycleReconnectParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("reconnect intervals", err)
	}
	if _, err := querier.LockTournamentLifecycleGolden(
		ctx,
		sqlc.LockTournamentLifecycleGoldenParams{TournamentID: tournamentID, RosterID: rosterID},
	); err != nil {
		return tournamentExecutionLockError("golden attempts", err)
	}
	return nil
}

func mapTournamentExecutionSnapshot(
	document json.RawMessage,
	projectionRevision int64,
) (tournamentadmin.LifecycleExecutionSnapshot, error) {
	if projectionRevision < 1 || !validLifecycleJSONObject(document) {
		return tournamentadmin.LifecycleExecutionSnapshot{}, domain.ErrInternal
	}
	var snapshot tournamentExecutionSnapshot
	if err := json.Unmarshal(document, &snapshot); err != nil || !validTournamentExecutionSnapshot(snapshot) {
		return tournamentadmin.LifecycleExecutionSnapshot{}, domain.ErrInternal
	}
	children := len(snapshot.Waves) + len(snapshot.Series) + len(snapshot.Games) + len(snapshot.Drafts) +
		len(snapshot.ReadyWindows) + len(snapshot.Readiness) + len(snapshot.Assignments) +
		len(snapshot.ChildPauses) + len(snapshot.Reconnect) + len(snapshot.Golden)
	if snapshot.IncompleteCount > children || snapshot.ActiveGoldenCount > len(snapshot.Golden) {
		return tournamentadmin.LifecycleExecutionSnapshot{}, domain.ErrInternal
	}
	return tournamentadmin.LifecycleExecutionSnapshot{
		Document: bytes.Clone(document), GraphRevision: projectionRevision,
		ExpectedChildren: children, ObservedChildren: children,
		IncompleteChildren: snapshot.IncompleteCount, ActiveGolden: snapshot.ActiveGoldenCount > 0,
	}, nil
}

func validTournamentExecutionSnapshot(snapshot tournamentExecutionSnapshot) bool {
	return snapshot.Waves != nil && snapshot.Series != nil && snapshot.Games != nil && snapshot.Drafts != nil &&
		snapshot.ReadyWindows != nil && snapshot.Readiness != nil && snapshot.Assignments != nil &&
		snapshot.ChildPauses != nil && snapshot.Reconnect != nil && snapshot.Golden != nil &&
		snapshot.IncompleteCount >= 0 && snapshot.ActiveGoldenCount >= 0
}

func tournamentExecutionLockError(scope string, err error) error {
	return fmt.Errorf("TournamentAdminLifecyclePostgres - lock %s: %w", scope, err)
}
