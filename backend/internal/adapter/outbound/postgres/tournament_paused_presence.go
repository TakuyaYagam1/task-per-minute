package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

// TournamentPausedPresencePostgres is the participant-owned child of an
// active normal operator Wave pause. It deliberately has no independent
// command-receipt store: the enclosing participant connection lease
// transaction is the sole idempotency boundary for this mutation.
type TournamentPausedPresencePostgres struct {
	tx *TxManager
}

func NewTournamentPausedPresencePostgres(tx *TxManager) *TournamentPausedPresencePostgres {
	return &TournamentPausedPresencePostgres{tx: tx}
}

var _ gameusecase.PausedPresenceRepository = (*TournamentPausedPresencePostgres)(nil)

// FindPausedPresenceCommand intentionally returns no receipt. The normal
// participant lifecycle coordinator holds the durable connection lease and
// invokes this usecase in that same ambient transaction. Replaying the lease
// fence is therefore the only command idempotency check; creating a second
// child receipt here would split one lifecycle operation across boundaries.
func (repository *TournamentPausedPresencePostgres) FindPausedPresenceCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*gameusecase.PausedPresenceRecord, error) {
	if !validTournamentPausedPresenceRepository(ctx, repository) || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	return nil, nil
}

//nolint:gocyclo // One locked read validates the complete operator-pause graph before exposing authority.
func (repository *TournamentPausedPresencePostgres) LoadPausedPresenceAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
	participantID uuid.UUID,
) (gameusecase.PausedPresenceAuthority, error) {
	if !validTournamentPausedPresenceRepository(ctx, repository) || scope.Validate() != nil || participantID == uuid.Nil {
		return gameusecase.PausedPresenceAuthority{}, domain.ErrValidation
	}

	querier := repository.tx.Querier(ctx)
	rootRows, err := querier.LockTournamentPausedPresenceRoot(ctx, sqlc.LockTournamentPausedPresenceRootParams{
		TournamentID: scope.TournamentID,
		RosterID:     scope.RosterID,
		WaveID:       scope.WaveID,
	})
	if err != nil {
		return gameusecase.PausedPresenceAuthority{}, fmt.Errorf("lock paused presence root: %w", err)
	}
	if len(rootRows) == 0 {
		return gameusecase.PausedPresenceAuthority{}, gameusecase.ErrPausedPresenceSuppression
	}
	if len(rootRows) != 1 {
		return gameusecase.PausedPresenceAuthority{}, fmt.Errorf("multiple active normal operator pauses: %w", gameusecase.ErrPausedPresenceConflict)
	}
	root := rootRows[0]
	if err := validatePausedPresenceRoot(root, scope); err != nil {
		return gameusecase.PausedPresenceAuthority{}, err
	}
	_, stored := decodeTournamentAdminWaveResult("pause", root.ResultDocument)
	if stored == nil || !normalPauseRecordMatchesRoot(*stored, root, scope) {
		return gameusecase.PausedPresenceAuthority{}, fmt.Errorf("paused presence root evidence is invalid: %w", gameusecase.ErrPausedPresenceSuppression)
	}

	// Reuse the existing complete normal-pause loader. It locks the authority,
	// pause graph, live Presence, reconnect intervals, counters, and frozen
	// deadlines in the repository's established lock order.
	resume, err := NewTournamentAdminExecutionPostgres(repository.tx).LoadPauseResumeAuthority(ctx, scope, root.PauseID)
	if err != nil {
		return gameusecase.PausedPresenceAuthority{}, fmt.Errorf("load paused presence graph: %w", err)
	}
	if err := validateLoadedPausedPresenceGraph(resume, root.PauseID, root.PauseRevision, scope); err != nil {
		return gameusecase.PausedPresenceAuthority{}, err
	}

	presenceRows, err := querier.LockTournamentPausedPresenceParticipant(ctx, sqlc.LockTournamentPausedPresenceParticipantParams{
		TournamentID:  scope.TournamentID,
		RosterID:      scope.RosterID,
		WaveID:        scope.WaveID,
		ParticipantID: participantID,
	})
	if err != nil {
		return gameusecase.PausedPresenceAuthority{}, fmt.Errorf("lock selected paused presence: %w", err)
	}
	if len(presenceRows) != 1 {
		return gameusecase.PausedPresenceAuthority{}, fmt.Errorf("selected paused presence is ambiguous or missing: %w", gameusecase.ErrPausedPresenceConflict)
	}
	liveRows, err := recoveryPresence(presenceRows)
	if err != nil || len(liveRows) != 1 {
		return gameusecase.PausedPresenceAuthority{}, fmt.Errorf("map selected paused presence: %w", gameusecase.ErrPausedPresenceSuppression)
	}
	selected, err := pausedPresenceByParticipant(resume.Presence, participantID)
	if err != nil {
		return gameusecase.PausedPresenceAuthority{}, err
	}
	if !samePausedPresence(selected, liveRows[0]) {
		return gameusecase.PausedPresenceAuthority{}, fmt.Errorf("selected paused presence differs from locked graph: %w", gameusecase.ErrPausedPresenceConflict)
	}

	return gameusecase.PausedPresenceAuthority{
		Pause:                  resume.Pause,
		Presence:               liveRows[0],
		Reconnect:              resume.Reconnect,
		Counters:               resume.Counters,
		FrozenDeadlines:        resume.FrozenDeadlines,
		TerminalActionRevision: resume.TerminalActionRevision,
	}, nil
}

func (repository *TournamentPausedPresencePostgres) CommitPausedPresence(
	ctx context.Context,
	expected gameusecase.PausedPresenceExpectation,
	record gameusecase.PausedPresenceRecord,
) (*gameusecase.PausedPresenceRecord, bool, error) {
	if !validTournamentPausedPresenceRepository(ctx, repository) {
		return nil, false, domain.ErrValidation
	}
	if err := validatePausedPresenceCommit(expected, record); err != nil {
		return nil, false, err
	}

	// Re-read under the existing ambient transaction so the complete immutable
	// graph expectation is checked immediately before the Presence CAS. The
	// enclosing transaction already holds these locks after Load, making this
	// a consistency check rather than a second transaction boundary.
	current, err := repository.LoadPausedPresenceAuthority(ctx, record.Command.Scope, record.Command.ParticipantID)
	if err != nil {
		return nil, false, fmt.Errorf("reload paused presence authority: %w", err)
	}
	if err := matchPausedPresenceCommitAuthority(expected, record, current); err != nil {
		return nil, false, err
	}

	row, err := repository.tx.Querier(ctx).UpdateTournamentPausedPresenceCAS(ctx, sqlc.UpdateTournamentPausedPresenceCASParams{
		PauseID:                  expected.PauseID,
		TournamentID:             record.Command.Scope.TournamentID,
		RosterID:                 record.Command.Scope.RosterID,
		WaveID:                   record.Command.Scope.WaveID,
		ExpectedPauseRevision:    expected.PauseRevision,
		PresenceID:               expected.Presence.ID,
		SeriesID:                 expected.Presence.SeriesID,
		ParticipantID:            expected.Presence.ParticipantID,
		ExpectedPresenceEpoch:    expected.Presence.PresenceEpoch,
		ExpectedPresenceRevision: expected.Presence.Revision,
		NextState:                string(record.Command.NextState),
		NextPresenceEpoch:        record.Authority.Presence.PresenceEpoch,
		NextPresenceRevision:     record.Authority.Presence.Revision,
		ConnectedAt:              tstz(record.Authority.Presence.ConnectedAt),
		DisconnectedAt:           nullableTSTZ(record.Authority.Presence.DisconnectedAt),
		UpdatedAt:                tstz(record.Authority.Presence.UpdatedAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, domain.ErrConflict
	}
	if err != nil {
		return nil, false, fmt.Errorf("commit paused presence CAS: %w", err)
	}
	liveRows, err := recoveryPresence([]sqlc.PresenceState{row})
	if err != nil || len(liveRows) != 1 || !samePausedPresence(liveRows[0], record.Authority.Presence) {
		return nil, false, fmt.Errorf("committed paused presence differs from record: %w", domain.ErrInternal)
	}
	committed := record
	committed.Authority.Presence = liveRows[0]
	return &committed, true, nil
}

//nolint:gocyclo // The persisted pause root is an indivisible cross-field authority proof.
func validatePausedPresenceRoot(row sqlc.LockTournamentPausedPresenceRootRow, scope pausedomain.GraphScope) error {
	if row.PauseID == uuid.Nil || row.TournamentID != scope.TournamentID || row.RosterID != scope.RosterID || row.ScopeKind != string(pausedomain.ScopeWave) || row.ScopeID != scope.WaveID || !row.WaveID.Valid || row.WaveID.UUID != scope.WaveID || row.Reason != string(gameusecase.PauseReasonOperator) || row.State != string(gameusecase.PauseStateActive) || row.CurrentRevisionID == uuid.Nil || row.PauseRevision < 1 || !row.StartedAt.Valid || !domain.IsValidServerTime(row.StartedAt.Time.UTC()) || row.CommandID == uuid.Nil || len(row.ResultDocument) == 0 {
		return fmt.Errorf("invalid paused presence root: %w", gameusecase.ErrPausedPresenceSuppression)
	}
	return nil
}

//nolint:gocyclo // Matching the immutable receipt requires the full stored root identity.
func normalPauseRecordMatchesRoot(record gameusecase.NormalPauseRecord, row sqlc.LockTournamentPausedPresenceRootRow, scope pausedomain.GraphScope) bool {
	return record.Scope.TournamentID == scope.TournamentID && record.Scope.RosterID == scope.RosterID && record.Scope.WaveID == scope.WaveID && record.ScopeKind == pausedomain.ScopeWave && record.ScopeID == scope.WaveID && record.PauseID == row.PauseID && record.Reason == gameusecase.PauseReasonOperator && record.State == gameusecase.PauseStateActive && record.Revision >= 1 && record.Graph.ActivePauseID == row.PauseID && record.Graph.Scope.TournamentID == scope.TournamentID && record.Graph.Scope.RosterID == scope.RosterID && record.Graph.Scope.WaveID == scope.WaveID && record.Graph.PausedAt != nil && domain.IsValidServerTime(record.PausedAt) && domain.IsValidServerTime(record.Graph.PausedAt.UTC())
}

//nolint:gocyclo // The loaded graph is accepted only when every authority coordinate matches.
func validateLoadedPausedPresenceGraph(authority gameusecase.PauseResumeAuthority, pauseID uuid.UUID, pauseRevision int64, scope pausedomain.GraphScope) error {
	pause := authority.Pause
	if pause.PauseID != pauseID || pause.Scope != scope || pause.ScopeKind != pausedomain.ScopeWave || pause.ScopeID != scope.WaveID || pause.Reason != gameusecase.PauseReasonOperator || pause.State != gameusecase.PauseStateActive || pause.Graph.ActivePauseID != pauseID || pause.Graph.Scope != scope || pause.Graph.PausedAt == nil || !pause.Graph.DeadlinesSuppressed || authority.TerminalActionRevision != pause.Graph.TerminalActionRevision || pause.Revision != pauseRevision || pause.Revision < 1 || !domain.IsValidServerTime(pause.PausedAt) || !domain.IsValidServerTime(pause.Graph.PausedAt.UTC()) {
		return fmt.Errorf("loaded paused presence graph is invalid: %w", gameusecase.ErrPausedPresenceSuppression)
	}
	return nil
}

func pausedPresenceByParticipant(values []pausedomain.PausePresence, participantID uuid.UUID) (pausedomain.PausePresence, error) {
	var selected pausedomain.PausePresence
	found := false
	for _, value := range values {
		if value.ParticipantID != participantID {
			continue
		}
		if found {
			return pausedomain.PausePresence{}, fmt.Errorf("multiple Presence rows for participant: %w", gameusecase.ErrPausedPresenceConflict)
		}
		selected, found = value, true
	}
	if !found {
		return pausedomain.PausePresence{}, fmt.Errorf("participant Presence not found: %w", gameusecase.ErrPausedPresenceSuppression)
	}
	return selected, nil
}

//nolint:gocyclo // Commit validation keeps all CAS and presence invariants visible in one boundary.
func validatePausedPresenceCommit(expected gameusecase.PausedPresenceExpectation, record gameusecase.PausedPresenceRecord) error {
	scope := record.Command.Scope
	if expected.Presence.PresenceEpoch == math.MaxInt64 || expected.Presence.Revision == math.MaxInt64 {
		return gameusecase.ErrPausedPresenceOverflow
	}
	if scope.Validate() != nil || expected.Authority.Validate() != nil || record.Command.PauseID == uuid.Nil || record.Command.CommandID == uuid.Nil || record.Command.ParticipantID == uuid.Nil || record.Command.ExpectedGraphRevision < 1 || record.Command.ExpectedPauseRevision < 1 || record.Command.ExpectedPresenceEpoch < 1 || record.Command.ExpectedPresenceRevision < 1 || record.Command.NextState == "" || record.Authority.Pause.PauseID != expected.PauseID || record.Authority.Pause.Scope != scope || record.Authority.Pause.ScopeKind != pausedomain.ScopeWave || record.Authority.Pause.ScopeID != scope.WaveID || record.Authority.Pause.Reason != gameusecase.PauseReasonOperator || record.Authority.Pause.State != gameusecase.PauseStateActive || record.Authority.Pause.Revision != expected.PauseRevision || record.Authority.Pause.Graph.Revision != expected.GraphRevision || record.Authority.Pause.Graph.ActivePauseID != expected.PauseID || record.Authority.Pause.Graph.Scope != scope || record.Authority.Pause.Graph.PausedAt == nil || record.Authority.Presence.ID == uuid.Nil || record.Authority.Presence.TournamentID != scope.TournamentID || record.Authority.Presence.RosterID != scope.RosterID || record.Authority.Presence.ParticipantID != record.Command.ParticipantID || record.Authority.Presence.SeriesID == uuid.Nil || record.Authority.Presence.PresenceEpoch != expected.Presence.PresenceEpoch+1 || record.Authority.Presence.Revision != expected.Presence.Revision+1 || !domain.IsValidServerTime(record.ChangedAt) || !record.Authority.Presence.UpdatedAt.Equal(record.ChangedAt) {
		return domain.ErrValidation
	}
	if record.Command.PauseID != expected.PauseID || record.Command.ExpectedGraphRevision != expected.GraphRevision || record.Command.ExpectedPauseRevision != expected.PauseRevision || record.Command.ExpectedPresenceEpoch != expected.Presence.PresenceEpoch || record.Command.ExpectedPresenceRevision != expected.Presence.Revision || record.Command.Scope.Authority != expected.Authority || record.Authority.Presence.PresenceEpoch <= expected.Presence.PresenceEpoch || record.Authority.Presence.Revision <= expected.Presence.Revision {
		return domain.ErrConflict
	}
	if record.Command.NextState != record.Authority.Presence.State || (record.Command.NextState != pausedomain.PresenceStateConnected && record.Command.NextState != pausedomain.PresenceStateDisconnected) {
		return domain.ErrValidation
	}
	return nil
}

//nolint:gocyclo // Cross-field authority equality is deliberately fail-closed.
func matchPausedPresenceCommitAuthority(
	expected gameusecase.PausedPresenceExpectation,
	record gameusecase.PausedPresenceRecord,
	current gameusecase.PausedPresenceAuthority,
) error {
	if current.Pause.PauseID != expected.PauseID || current.Pause.Graph.Revision != expected.GraphRevision || current.Pause.Revision != expected.PauseRevision || current.Pause.Scope.Authority != expected.Authority || current.Presence.ID != expected.Presence.ID || current.Presence.TournamentID != expected.Presence.TournamentID || current.Presence.RosterID != expected.Presence.RosterID || current.Presence.SeriesID != expected.Presence.SeriesID || current.Presence.ParticipantID != expected.Presence.ParticipantID || current.Presence.PresenceEpoch != expected.Presence.PresenceEpoch || current.Presence.Revision != expected.Presence.Revision {
		return domain.ErrConflict
	}
	if !reflect.DeepEqual(current.Pause, record.Authority.Pause) || !reflect.DeepEqual(current.Reconnect, record.Authority.Reconnect) || !reflect.DeepEqual(current.Counters, record.Authority.Counters) || !reflect.DeepEqual(current.FrozenDeadlines, record.Authority.FrozenDeadlines) || current.TerminalActionRevision != record.Authority.TerminalActionRevision {
		return domain.ErrConflict
	}
	return nil
}

func samePausedPresence(left, right pausedomain.PausePresence) bool {
	if left.ID != right.ID || left.TournamentID != right.TournamentID || left.RosterID != right.RosterID || left.SeriesID != right.SeriesID || left.ParticipantID != right.ParticipantID || left.State != right.State || left.PresenceEpoch != right.PresenceEpoch || left.Revision != right.Revision || !left.ConnectedAt.Equal(right.ConnectedAt) || !left.UpdatedAt.Equal(right.UpdatedAt) {
		return false
	}
	if left.DisconnectedAt == nil || right.DisconnectedAt == nil {
		return left.DisconnectedAt == nil && right.DisconnectedAt == nil
	}
	return left.DisconnectedAt.Equal(*right.DisconnectedAt)
}

func validTournamentPausedPresenceRepository(ctx context.Context, repository *TournamentPausedPresencePostgres) bool {
	return ctx != nil && repository != nil && repository.tx != nil
}
