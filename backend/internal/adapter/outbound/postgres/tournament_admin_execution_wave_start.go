package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

type waveStartSnapshot struct {
	authority          gameusecase.StartAuthority
	rosterID           uuid.UUID
	tournamentRevision int64
	rosterRevision     int64
	games              []sqlc.LockWaveStartGamesRow
	seriesIDs          []uuid.UUID
	swissProof         *waveStartSwissRoundProof
	byeParticipantID   *uuid.UUID
}

type waveStartSwissRoundProof struct {
	preset               domain.TournamentPreset
	roundID              uuid.UUID
	roundNumber          int
	roundRevision        int64
	historyRevision      int64
	preflightRevisionID  uuid.UUID
	normalPoolRevisionID uuid.UUID
	normalPoolRevision   int64
	retained             *swissusecase.RoundLockProof
}

type waveStartGraphEvidence struct {
	GameIDs        []uuid.UUID `json:"game_ids"`
	ReservationIDs []uuid.UUID `json:"reservation_ids"`
	ReceiptCount   int         `json:"receipt_count"`
}

// ReadWaveStartTime keeps Wave activation timestamps and derived deadlines on
// PostgreSQL time. The application uses the injected process clock only for
// observation duration, never as mutation evidence.
func (r *TournamentAdminExecutionPostgres) ReadWaveStartTime(ctx context.Context) (time.Time, error) {
	return r.ReadExecutionTime(ctx)
}

func (r *TournamentAdminExecutionPostgres) LoadWaveStartAuthority(
	ctx context.Context,
	scope gameusecase.StartScope,
) (gameusecase.StartAuthority, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) || !validWaveStartScope(scope) {
		return gameusecase.StartAuthority{}, domain.ErrValidation
	}
	current, err := r.loadPersistedWaveStart(ctx, scope)
	if err != nil || current != nil {
		return waveStartAuthorityFromCurrent(scope, current, err)
	}
	snapshot, err := r.lockWaveStartSnapshot(ctx, scope)
	if err != nil {
		return gameusecase.StartAuthority{}, err
	}
	return snapshot.authority, nil
}

func (r *TournamentAdminExecutionPostgres) CommitWaveStart(
	ctx context.Context,
	record gameusecase.StartRecord,
) (*gameusecase.StartRecord, bool, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) || record.Validate() != nil {
		return nil, false, domain.ErrValidation
	}
	var result *gameusecase.StartRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		stored, err := r.findWaveStartCommand(txCtx, record.CommandID)
		if err != nil {
			return err
		}
		if stored != nil {
			if !sameWaveStartRequest(*stored, record) {
				return gameusecase.ErrWaveStartConflict
			}
			result = stored
			return nil
		}

		snapshot, err := r.lockWaveStartSnapshot(txCtx, record.Scope)
		if err != nil {
			return err
		}
		if !waveStartRecordMatchesSnapshot(record, snapshot) {
			return domain.ErrConflict
		}
		if err := r.persistWaveStart(txCtx, record, snapshot); err != nil {
			return err
		}
		committed := record
		result = &committed
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if result == nil {
		return nil, false, domain.ErrInternal
	}
	return result, changed, nil
}

func (r *TournamentAdminExecutionPostgres) loadPersistedWaveStart(
	ctx context.Context,
	scope gameusecase.StartScope,
) (*gameusecase.StartRecord, error) {
	row, err := r.tx.Querier(ctx).FindLatestWaveStartCommand(ctx, sqlc.FindLatestWaveStartCommandParams{
		TournamentID: scope.TournamentID,
		WaveID:       scope.WaveID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load current Wave start command: %w", err)
	}
	record, err := waveStartRecordFromControl(row)
	if err != nil {
		return nil, err
	}
	if record.Scope != scope {
		return nil, gameusecase.ErrWaveStartConflict
	}
	return &record, nil
}

func waveStartAuthorityFromCurrent(
	scope gameusecase.StartScope,
	current *gameusecase.StartRecord,
	err error,
) (gameusecase.StartAuthority, error) {
	if err != nil {
		return gameusecase.StartAuthority{}, err
	}
	if current == nil {
		return gameusecase.StartAuthority{}, domain.ErrInternal
	}
	return gameusecase.StartAuthority{
		Scope: scope, WaveRevision: current.ExpectedWaveRevision + 1,
		Revisions: current.Revisions, Current: current,
	}, nil
}

func (r *TournamentAdminExecutionPostgres) findWaveStartCommand(
	ctx context.Context,
	commandID uuid.UUID,
) (*gameusecase.StartRecord, error) {
	row, err := r.tx.Querier(ctx).FindWaveStartCommandByID(ctx, commandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find Wave start command: %w", err)
	}
	record, err := waveStartRecordFromControl(row)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *TournamentAdminExecutionPostgres) lockWaveStartSnapshot(
	ctx context.Context,
	scope gameusecase.StartScope,
) (waveStartSnapshot, error) {
	querier := r.tx.Querier(ctx)
	if err := lockTournamentResultScope(ctx, querier, scope.TournamentID, uuid.Nil); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return waveStartSnapshot{}, gameusecase.ErrWaveStartAuthorityConflict
		}
		return waveStartSnapshot{}, fmt.Errorf("lock Wave start result scope: %w", err)
	}
	header, err := querier.LockWaveStartAuthority(ctx, sqlc.LockWaveStartAuthorityParams{
		TournamentID: scope.TournamentID,
		WaveID:       scope.WaveID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return waveStartSnapshot{}, gameusecase.ErrWaveStartAuthorityConflict
	}
	if err != nil {
		return waveStartSnapshot{}, fmt.Errorf("lock Wave start authority: %w", err)
	}
	authority, rosterID, err := waveStartAuthorityHeader(header, scope)
	if err != nil {
		return waveStartSnapshot{}, err
	}
	var swissProof *waveStartSwissRoundProof
	if header.SwissRoundID.Valid {
		lockedProof, err := querier.LockWaveStartSwissRoundProof(ctx, sqlc.LockWaveStartSwissRoundProofParams{
			TournamentID: scope.TournamentID,
			RosterID:     rosterID,
			WaveID:       scope.WaveID,
			RoundID:      header.SwissRoundID.UUID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return waveStartSnapshot{}, gameusecase.ErrWaveStartAuthorityConflict
		}
		if err != nil {
			return waveStartSnapshot{}, fmt.Errorf("lock Swiss round proof authority: %w", err)
		}
		mappedProof, err := waveStartSwissRoundProofFromRow(lockedProof)
		if err != nil {
			return waveStartSnapshot{}, err
		}
		mappedProof.retained, err = loadRetainedSwissRoundProof(ctx, querier, scope.TournamentID, rosterID, mappedProof)
		if err != nil {
			return waveStartSnapshot{}, err
		}
		swissProof = &mappedProof
	}
	readiness, err := querier.LockWaveStartReadiness(ctx, sqlc.LockWaveStartReadinessParams{
		WaveID:        scope.WaveID,
		ReadyWindowID: uuid.NullUUID{UUID: scope.WindowID, Valid: true},
	})
	if err != nil {
		return waveStartSnapshot{}, fmt.Errorf("lock Wave start readiness: %w", err)
	}
	if err := applyWaveStartReadiness(&authority, readiness); err != nil {
		return waveStartSnapshot{}, err
	}
	memberships, err := querier.LockWaveStartSeriesMemberships(ctx, scope.WaveID)
	if err != nil {
		return waveStartSnapshot{}, fmt.Errorf("lock Wave start Series memberships: %w", err)
	}
	seriesIDs, err := waveStartMembershipSeriesIDs(scope, rosterID, memberships)
	if err != nil {
		return waveStartSnapshot{}, err
	}
	games, err := querier.LockWaveStartGames(ctx, scope.WaveID)
	if err != nil {
		return waveStartSnapshot{}, fmt.Errorf("lock Wave start Games: %w", err)
	}
	authority.Games, err = waveStartGameAuthorities(authority, rosterID, games)
	if err != nil {
		return waveStartSnapshot{}, err
	}
	if !waveStartGamesCoverSeries(authority.Games, seriesIDs) {
		return waveStartSnapshot{}, gameusecase.ErrWaveStartAuthorityConflict
	}
	byeParticipantID := nullableWaveStartUUID(header.ByeParticipantID)
	return waveStartSnapshot{
		authority: authority, rosterID: rosterID, tournamentRevision: header.TournamentRevision,
		rosterRevision: header.RosterRevision, games: games, seriesIDs: seriesIDs, swissProof: swissProof,
		byeParticipantID: byeParticipantID,
	}, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func waveStartAuthorityHeader(
	header sqlc.LockWaveStartAuthorityRow,
	scope gameusecase.StartScope,
) (gameusecase.StartAuthority, uuid.UUID, error) {
	if header.TournamentID != scope.TournamentID || header.ID != scope.WaveID ||
		header.TournamentState != string(domain.TournamentStateSwiss) || header.RosterID == uuid.Nil ||
		header.RosterRevision < 1 || header.TournamentRevision < 1 || header.ProjectionRevisionID == uuid.Nil ||
		header.ProjectionRevision < 1 || header.RevisionID == uuid.Nil || header.Revision < 1 ||
		header.ArtifactRevisionID == uuid.Nil ||
		header.ArtifactRevision < 1 || header.StartedAt.Valid || header.PausedAt.Valid || header.ClosedAt.Valid {
		return gameusecase.StartAuthority{}, uuid.Nil, gameusecase.ErrWaveStartAuthorityConflict
	}
	createdAt, err := requiredWaveStartTime(header.CreatedAt)
	if err != nil {
		return gameusecase.StartAuthority{}, uuid.Nil, err
	}
	updatedAt, err := requiredWaveStartTime(header.UpdatedAt)
	if err != nil || updatedAt.Before(createdAt) {
		return gameusecase.StartAuthority{}, uuid.Nil, domain.ErrInternal
	}
	openedAt, err := requiredWaveStartTime(header.OpenedAt)
	if err != nil {
		return gameusecase.StartAuthority{}, uuid.Nil, err
	}
	deadline, err := requiredWaveStartTime(header.Deadline)
	if err != nil {
		return gameusecase.StartAuthority{}, uuid.Nil, err
	}
	if header.ReadyWindowID != scope.WindowID || header.ReadyWindowRevisionID == uuid.Nil ||
		header.ReadyWindowState != string(domain.ReadyWindowStateOpen) || header.ConsumedAt.Valid ||
		!domain.IsValidReadyWindowInterval(openedAt, deadline) {
		return gameusecase.StartAuthority{}, uuid.Nil, gameusecase.ErrWaveStartAuthorityConflict
	}
	wave := domain.Wave{
		ID: header.ID, TournamentID: header.TournamentID,
		RevisionID: domain.WaveRevisionID(header.RevisionID), State: domain.WaveState(header.State),
		ReadyWindow: &domain.ReadyWindow{
			ID: header.ReadyWindowID, WaveID: header.ID,
			RevisionID: domain.ReadyWindowRevisionID(header.ReadyWindowRevisionID),
			State:      domain.ReadyWindowState(header.ReadyWindowState), OpenedAt: openedAt, Deadline: deadline,
		},
	}
	if wave.State != domain.WaveStateReady {
		return gameusecase.StartAuthority{}, uuid.Nil, gameusecase.ErrWaveStartAuthorityConflict
	}
	return gameusecase.StartAuthority{
		Scope: scope, WaveRevision: header.Revision,
		Revisions: domain.ReadyWindowSourceRevisions{
			WaveRevisionID: domain.WaveRevisionID(header.RevisionID), WaveRevision: header.Revision,
			ProjectionRevisionID: header.ProjectionRevisionID, ProjectionRevision: header.ProjectionRevision,
			ArtifactRevisionID: header.ArtifactRevisionID, ArtifactRevision: header.ArtifactRevision,
		},
		Wave: wave,
	}, header.RosterID, nil
}

func requiredWaveStartTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, domain.ErrInternal
	}
	result := value.Time.UTC()
	if !validServerTime(result) {
		return time.Time{}, domain.ErrInternal
	}
	return result, nil
}

func waveStartSwissRoundProofFromRow(
	row sqlc.LockWaveStartSwissRoundProofRow,
) (waveStartSwissRoundProof, error) {
	if !row.PreflightRevisionID.Valid || row.PreflightRevisionID.UUID == uuid.Nil {
		return waveStartSwissRoundProof{}, gameusecase.ErrWaveStartAuthorityConflict
	}
	proof := waveStartSwissRoundProof{
		preset:               domain.TournamentPreset(row.Preset),
		roundID:              row.RoundID,
		roundNumber:          int(row.RoundNumber),
		roundRevision:        row.RoundRevision,
		historyRevision:      row.HistoryRevision,
		preflightRevisionID:  row.PreflightRevisionID.UUID,
		normalPoolRevisionID: row.NormalPoolRevisionID,
		normalPoolRevision:   row.NormalPoolRevision,
	}
	if !proof.preset.IsValid() || proof.roundID == uuid.Nil || proof.roundNumber < 1 ||
		proof.roundRevision < 1 || proof.historyRevision < 0 ||
		proof.normalPoolRevisionID == uuid.Nil || proof.normalPoolRevision < 1 {
		return waveStartSwissRoundProof{}, gameusecase.ErrWaveStartAuthorityConflict
	}
	return proof, nil
}

func waveStartMembershipSeriesIDs(
	scope gameusecase.StartScope,
	rosterID uuid.UUID,
	rows []sqlc.LockWaveStartSeriesMembershipsRow,
) ([]uuid.UUID, error) {
	if rosterID == uuid.Nil || len(rows) == 0 {
		return nil, gameusecase.ErrWaveStartAuthorityConflict
	}
	seriesIDs := make([]uuid.UUID, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		if row.WaveID != scope.WaveID || row.TournamentID != scope.TournamentID || row.RosterID != rosterID ||
			row.SeriesID == uuid.Nil {
			return nil, gameusecase.ErrWaveStartAuthorityConflict
		}
		if _, duplicate := seen[row.SeriesID]; duplicate {
			return nil, domain.ErrInternal
		}
		seen[row.SeriesID] = struct{}{}
		seriesIDs[index] = row.SeriesID
	}
	return seriesIDs, nil
}

func waveStartGamesCoverSeries(games []gameusecase.GameAuthority, seriesIDs []uuid.UUID) bool {
	if len(games) != len(seriesIDs) || len(games) == 0 {
		return false
	}
	expected := make(map[uuid.UUID]struct{}, len(seriesIDs))
	for _, seriesID := range seriesIDs {
		if seriesID == uuid.Nil {
			return false
		}
		expected[seriesID] = struct{}{}
	}
	if len(expected) != len(seriesIDs) {
		return false
	}
	for _, game := range games {
		if _, found := expected[game.Scope.SeriesID]; !found {
			return false
		}
		delete(expected, game.Scope.SeriesID)
	}
	return len(expected) == 0
}

func nullableWaveStartUUID(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	result := value.UUID
	return &result
}

func nullableWaveStartUUIDValue(value uuid.UUID) uuid.NullUUID {
	if value == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: value, Valid: true}
}

func applyWaveStartReadiness(
	authority *gameusecase.StartAuthority,
	rows []sqlc.LockWaveStartReadinessRow,
) error {
	if authority == nil || len(rows) < 2 {
		return gameusecase.ErrWaveStartAuthorityConflict
	}
	members := make([]domain.WaveMember, len(rows))
	revisions := make(map[uuid.UUID]int64, len(rows))
	for index, row := range rows {
		if row.ParticipantID == uuid.Nil || row.ReadinessRevision < 1 {
			return domain.ErrInternal
		}
		if _, duplicate := revisions[row.ParticipantID]; duplicate {
			return domain.ErrInternal
		}
		members[index] = domain.WaveMember{ParticipantID: row.ParticipantID, Ready: row.Ready}
		revisions[row.ParticipantID] = row.ReadinessRevision
	}
	authority.Wave.Members = members
	authority.ReadinessRevisions = revisions
	return nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func waveStartGameAuthorities(
	authority gameusecase.StartAuthority,
	rosterID uuid.UUID,
	rows []sqlc.LockWaveStartGamesRow,
) ([]gameusecase.GameAuthority, error) {
	if rosterID == uuid.Nil || len(rows) == 0 {
		return nil, gameusecase.ErrWaveStartAuthorityConflict
	}
	members := make(map[uuid.UUID]struct{}, len(authority.Wave.Members))
	for _, member := range authority.Wave.Members {
		members[member.ParticipantID] = struct{}{}
	}
	result := make([]gameusecase.GameAuthority, len(rows))
	seenGames := make(map[uuid.UUID]struct{}, len(rows))
	seenSeries := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		if row.TournamentID != authority.Scope.TournamentID || row.RosterID != rosterID ||
			row.SeriesID == uuid.Nil || row.SlotID == uuid.Nil || row.GameID == uuid.Nil ||
			row.AssignmentID == uuid.Nil || row.AssignmentRevision < 1 || row.ReservationID == uuid.Nil ||
			row.ReservationRevision < 1 || row.ReservationState != "committed" || row.DisclosedAt.Valid ||
			row.SnapshotID == uuid.Nil || row.TaskID == uuid.Nil || row.TaskVersion < 1 ||
			row.PlanRevisionID == uuid.Nil || row.TimeLimit <= 0 {
			return nil, gameusecase.ErrWaveStartAuthorityConflict
		}
		if _, duplicate := seenGames[row.GameID]; duplicate {
			return nil, domain.ErrInternal
		}
		if _, duplicate := seenSeries[row.SeriesID]; duplicate {
			return nil, gameusecase.ErrWaveStartAuthorityConflict
		}
		seenGames[row.GameID] = struct{}{}
		seenSeries[row.SeriesID] = struct{}{}
		if _, firstMember := members[row.FirstParticipantID]; !firstMember {
			return nil, gameusecase.ErrWaveStartAuthorityConflict
		}
		if _, secondMember := members[row.SecondParticipantID]; !secondMember ||
			row.FirstParticipantID == row.SecondParticipantID {
			return nil, gameusecase.ErrWaveStartAuthorityConflict
		}
		digest, err := waveStartDigest(row.ContentDigest)
		if err != nil {
			return nil, err
		}
		series := seriesdomain.Execution{Series: domain.Series{
			ID: row.SeriesID, TournamentID: row.TournamentID,
			FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
			Format: domain.SeriesFormat(row.SeriesFormat), State: domain.SeriesState(row.SeriesState),
			Score: domain.SeriesScore{
				FirstParticipantWins:  int(row.FirstParticipantWins),
				SecondParticipantWins: int(row.SecondParticipantWins),
			},
			Slots: []domain.GameSlot{{
				ID: row.SlotID, SeriesID: row.SeriesID, Position: int(row.SlotNumber),
				Category: domain.Category(row.Category),
				ScoreBefore: domain.SeriesScore{
					FirstParticipantWins:  int(row.FirstParticipantWinsBefore),
					SecondParticipantWins: int(row.SecondParticipantWinsBefore),
				},
				Attempts: []domain.Game{{
					ID: row.GameID, SlotID: row.SlotID, AttemptNo: int(row.AttemptNumber),
					State: domain.GameState(row.GameState),
				}},
			}},
		}}
		if err := series.Validate(); err != nil {
			return nil, domain.ErrInternal
		}
		result[index] = gameusecase.GameAuthority{
			Scope: gamedomain.Scope{
				TournamentID: row.TournamentID, SeriesID: row.SeriesID, SlotID: row.SlotID, GameID: row.GameID,
			},
			ParticipantIDs: [2]uuid.UUID{row.FirstParticipantID, row.SecondParticipantID}, Series: series,
			AssignmentID: row.AssignmentID, AssignmentRevision: row.AssignmentRevision,
			PlanRevisionID: row.PlanRevisionID, SnapshotID: row.SnapshotID,
			ContentDigest: digest, DeadlineSeconds: int(row.TimeLimit),
		}
	}
	return result, nil
}

func waveStartDigest(value []byte) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(value) != len(digest) {
		return digest, domain.ErrInternal
	}
	copy(digest[:], value)
	if digest == [sha256.Size]byte{} {
		return digest, domain.ErrInternal
	}
	return digest, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func waveStartRecordMatchesSnapshot(record gameusecase.StartRecord, snapshot waveStartSnapshot) bool {
	authority := snapshot.authority
	if record.Validate() != nil || authority.Scope != record.Scope || authority.WaveRevision != record.ExpectedWaveRevision ||
		authority.Revisions != record.Revisions ||
		authority.Revisions.ProjectionRevision != record.ExpectedProjectionRevision ||
		!maps.Equal(authority.ReadinessRevisions, record.ReadinessRevisions) ||
		record.Wave.ID != authority.Wave.ID || record.Wave.TournamentID != authority.Wave.TournamentID ||
		record.Wave.RevisionID != authority.Wave.RevisionID || record.Wave.State != domain.WaveStateActive ||
		record.Wave.StartedAt == nil || !record.Wave.StartedAt.Equal(record.StartedAt) ||
		record.Wave.ReadyWindow == nil || authority.Wave.ReadyWindow == nil ||
		record.Wave.ReadyWindow.ID != authority.Wave.ReadyWindow.ID ||
		record.Wave.ReadyWindow.State != domain.ReadyWindowStateConsumed ||
		len(record.Games) != len(snapshot.games) {
		return false
	}
	byGameID := make(map[uuid.UUID]gamedomain.Started, len(record.Games))
	for _, game := range record.Games {
		if _, duplicate := byGameID[game.Scope.GameID]; duplicate {
			return false
		}
		byGameID[game.Scope.GameID] = game
	}
	for _, row := range snapshot.games {
		game, exists := byGameID[row.GameID]
		if !exists || !waveStartGameMatchesRow(game, row, record.StartedAt) {
			return false
		}
	}
	return true
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func waveStartGameMatchesRow(
	game gamedomain.Started,
	row sqlc.LockWaveStartGamesRow,
	startedAt time.Time,
) bool {
	digest, err := waveStartDigest(row.ContentDigest)
	if err != nil {
		return false
	}
	if game.Scope.TournamentID != row.TournamentID || game.Scope.SeriesID != row.SeriesID ||
		game.Scope.SlotID != row.SlotID || game.Scope.GameID != row.GameID ||
		game.ParticipantIDs != [2]uuid.UUID{row.FirstParticipantID, row.SecondParticipantID} ||
		game.AssignmentID != row.AssignmentID || game.AssignmentRevision != row.AssignmentRevision ||
		game.PlanRevisionID != row.PlanRevisionID || game.SnapshotID != row.SnapshotID ||
		game.ContentDigest != digest || game.DeadlineSeconds != int(row.TimeLimit) ||
		!game.StartedAt.Equal(startedAt) || !game.Deadline.Equal(startedAt.Add(time.Duration(row.TimeLimit)*time.Second)) ||
		!game.DeliveryEnabled || game.Series.Series.ID != row.SeriesID ||
		game.Series.Series.State != domain.SeriesStateActive || len(game.Series.Series.Slots) != 1 {
		return false
	}
	slot := game.Series.Series.Slots[0]
	return slot.ID == row.SlotID && slot.SeriesID == row.SeriesID && len(slot.Attempts) == 1 &&
		slot.Attempts[0].ID == row.GameID && slot.Attempts[0].State == domain.GameStateActive
}

func (r *TournamentAdminExecutionPostgres) persistWaveStart(
	ctx context.Context,
	record gameusecase.StartRecord,
	snapshot waveStartSnapshot,
) error {
	querier := r.tx.Querier(ctx)
	if err := persistWaveStartSwissRoundLockProof(ctx, querier, record, snapshot); err != nil {
		return err
	}
	startedAt := tstz(record.StartedAt)
	if _, err := querier.StartWaveCAS(ctx, sqlc.StartWaveCASParams{
		StartedAt: startedAt, ID: record.Scope.WaveID, TournamentID: record.Scope.TournamentID,
		ExpectedRevision: record.ExpectedWaveRevision,
	}); err != nil {
		return waveStartCASWriteError("activate Wave", err)
	}
	if _, err := querier.ConsumeReadyWindowCAS(ctx, sqlc.ConsumeReadyWindowCASParams{
		StartedAt: startedAt, ID: record.Scope.WindowID, WaveID: record.Scope.WaveID,
	}); err != nil {
		return waveStartCASWriteError("consume ready window", err)
	}
	if err := startWaveSeries(ctx, querier, snapshot.games, record.StartedAt); err != nil {
		return err
	}
	if err := startWaveGames(ctx, querier, snapshot.games, record); err != nil {
		return err
	}
	if err := discloseWaveStartReservations(ctx, querier, snapshot.games, record.StartedAt); err != nil {
		return err
	}
	if err := createWaveStartReceipts(ctx, querier, record, snapshot.games); err != nil {
		return err
	}
	return saveWaveStartCommand(ctx, querier, record, snapshot)
}

func persistWaveStartSwissRoundLockProof(
	ctx context.Context,
	querier *sqlc.Queries,
	record gameusecase.StartRecord,
	snapshot waveStartSnapshot,
) error {
	if snapshot.swissProof == nil {
		return nil
	}
	proof, err := waveStartSwissRoundLockProof(record, snapshot)
	if err != nil {
		return err
	}
	return ensureSwissRoundLockProof(ctx, querier, proof, record.StartedAt)
}

type swissRoundProofOrigin struct {
	mode      string
	commandID uuid.UUID
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func ensureSwissRoundLockProof(ctx context.Context, querier *sqlc.Queries, proof swissusecase.RoundLockProof, lockedAt time.Time, origins ...swissRoundProofOrigin) error {
	if proof.Validate() != nil || !domain.IsValidServerTime(lockedAt) {
		return domain.ErrConflict
	}
	retained, err := loadRetainedSwissRoundProof(ctx, querier, proof.TournamentID, proof.RosterID,
		waveStartSwissRoundProof{roundID: proof.RoundID, preset: proof.Preset})
	if err != nil {
		return err
	}
	if retained != nil {
		if retained.ProofHash != proof.ProofHash {
			return domain.ErrConflict
		}
		return nil
	}
	proofHash, err := decodeRoundLockProofHash(proof.ProofHash)
	if err != nil {
		return err
	}
	createdAt := tstz(lockedAt)
	var mode *string
	var commandID uuid.NullUUID
	if len(origins) == 1 {
		mode = &origins[0].mode
		commandID = nullableUUIDValue(origins[0].commandID)
	} else if len(origins) > 1 {
		return domain.ErrConflict
	}
	if err := querier.CreateSwissRoundLockProof(ctx, sqlc.CreateSwissRoundLockProofParams{
		RoundID:      proof.RoundID,
		TournamentID: proof.TournamentID,
		RosterID:     proof.RosterID,
		Preset:       string(proof.Preset),
		//nolint:gosec // Domain validation bounds this value before the storage conversion.
		RoundNumber:                int16(proof.RoundNumber),
		SourceProjectionRevisionID: proof.SourceProjectionRevisionID,
		PreflightRevisionID:        proof.PreflightRevisionID,
		NormalPoolRevisionID:       proof.NormalPoolRevisionID,
		WaveID:                     proof.WaveID,
		WaveRevisionID:             uuid.UUID(proof.WaveRevisionID),
		RoundRevision:              proof.Revisions.Round,
		SourceProjectionRevision:   proof.Revisions.SourceProjection,
		RosterRevision:             proof.Revisions.Roster,
		HistoryRevision:            proof.Revisions.History,
		NormalPoolRevision:         proof.Revisions.NormalPool,
		WaveRevision:               proof.Revisions.Wave,
		ByeParticipantID:           nullableWaveStartUUIDValue(proof.ByeParticipantID),
		ProofHash:                  proofHash,
		ProofMode:                  mode,
		TerminalCommandID:          commandID,
		LockedAt:                   createdAt,
		CreatedAt:                  createdAt,
	}); err != nil {
		return waveStartCASWriteError("persist Swiss round lock proof", err)
	}
	for _, participantID := range proof.RosterParticipantIDs {
		if err := querier.CreateSwissRoundLockProofMember(ctx, sqlc.CreateSwissRoundLockProofMemberParams{
			RoundID: proof.RoundID, RosterID: proof.RosterID, ParticipantID: participantID, CreatedAt: createdAt,
		}); err != nil {
			return waveStartCASWriteError("persist Swiss round lock proof member", err)
		}
	}
	for _, series := range proof.Series {
		if err := querier.CreateSwissRoundLockProofSeries(ctx, sqlc.CreateSwissRoundLockProofSeriesParams{
			RoundID: proof.RoundID, RosterID: proof.RosterID,
			PairingID:                series.PairingID,
			SeriesID:                 series.SeriesID,
			FirstParticipantID:       series.FirstParticipantID,
			SecondParticipantID:      series.SecondParticipantID,
			CategoryRevisionID:       series.CategoryRevisionID,
			CategoryRevision:         series.CategoryRevision,
			AssignmentID:             series.AssignmentID,
			AssignmentRevision:       series.AssignmentRevision,
			AssignmentPlanID:         series.AssignmentPlanID,
			AssignmentPlanRevisionID: series.AssignmentPlanRevisionID,
			ReservationID:            series.ReservationID,
			ReservationRevision:      series.ReservationRevision,
			CreatedAt:                createdAt,
		}); err != nil {
			return waveStartCASWriteError("persist Swiss round lock proof Series", err)
		}
	}
	locked, err := querier.LockSwissRoundCAS(ctx, sqlc.LockSwissRoundCASParams{
		LockedAt: createdAt, ID: proof.RoundID, ExpectedRevision: proof.Revisions.Round,
	})
	if err != nil {
		return waveStartCASWriteError("lock Swiss round", err)
	}
	if locked.ID != proof.RoundID || locked.Revision != proof.Revisions.Round || locked.LockRevision == nil ||
		*locked.LockRevision != proof.Revisions.Round || !locked.LockedAt.Valid ||
		!locked.LockedAt.Time.UTC().Equal(lockedAt) {
		return domain.ErrInternal
	}
	return nil
}

func waveStartSwissRoundLockProof(
	record gameusecase.StartRecord,
	snapshot waveStartSnapshot,
) (swissusecase.RoundLockProof, error) {
	if snapshot.swissProof == nil || record.Validate() != nil ||
		!waveStartGamesCoverSeries(snapshot.authority.Games, snapshot.seriesIDs) ||
		len(snapshot.games) != len(snapshot.authority.Games) {
		return swissusecase.RoundLockProof{}, gameusecase.ErrWaveStartAuthorityConflict
	}
	return buildSwissRoundLockProof(record.Scope, record.Revisions, record.ExpectedWaveRevision, snapshot)
}

func buildSwissRoundLockProof(scope gameusecase.StartScope, revisions domain.ReadyWindowSourceRevisions, waveRevision int64, snapshot waveStartSnapshot) (swissusecase.RoundLockProof, error) {
	lockedSeries := make([]swissusecase.LockedSeries, len(snapshot.games))
	for index, row := range snapshot.games {
		if row.PairingID == uuid.Nil || row.CategoryRevisionID == uuid.Nil || row.CategoryRevision < 1 ||
			row.AssignmentID == uuid.Nil || row.AssignmentRevision < 1 || row.AssignmentPlanID == uuid.Nil ||
			row.PlanRevisionID == uuid.Nil || row.ReservationID == uuid.Nil || row.ReservationRevision < 1 {
			return swissusecase.RoundLockProof{}, gameusecase.ErrWaveStartAuthorityConflict
		}
		lockedSeries[index] = swissusecase.LockedSeries{
			SeriesID:                 row.SeriesID,
			PairingID:                row.PairingID,
			FirstParticipantID:       row.FirstParticipantID,
			SecondParticipantID:      row.SecondParticipantID,
			CategoryRevisionID:       row.CategoryRevisionID,
			CategoryRevision:         row.CategoryRevision,
			AssignmentID:             row.AssignmentID,
			AssignmentRevision:       row.AssignmentRevision,
			AssignmentPlanID:         row.AssignmentPlanID,
			AssignmentPlanRevisionID: row.PlanRevisionID,
			ReservationID:            row.ReservationID,
			ReservationRevision:      row.ReservationRevision,
		}
	}
	participantIDs := make([]uuid.UUID, len(snapshot.authority.Wave.Members))
	for index, member := range snapshot.authority.Wave.Members {
		participantIDs[index] = member.ParticipantID
	}
	input := swissusecase.RoundLockProofInput{
		TournamentID:               scope.TournamentID,
		RosterID:                   snapshot.rosterID,
		RoundID:                    snapshot.swissProof.roundID,
		Preset:                     snapshot.swissProof.preset,
		RoundNumber:                snapshot.swissProof.roundNumber,
		SourceProjectionRevisionID: revisions.ProjectionRevisionID,
		PreflightRevisionID:        snapshot.swissProof.preflightRevisionID,
		NormalPoolRevisionID:       snapshot.swissProof.normalPoolRevisionID,
		WaveID:                     scope.WaveID,
		WaveRevisionID:             revisions.WaveRevisionID,
		Revisions: swissusecase.RoundLockRevisions{
			SourceProjection: revisions.ProjectionRevision,
			Roster:           snapshot.rosterRevision,
			Round:            snapshot.swissProof.roundRevision,
			History:          snapshot.swissProof.historyRevision,
			NormalPool:       snapshot.swissProof.normalPoolRevision,
			Wave:             waveRevision,
		},
		RosterParticipantIDs: participantIDs,
		Series:               lockedSeries,
		ByeParticipantID:     waveStartByeParticipantID(snapshot.byeParticipantID),
	}
	if retained := snapshot.swissProof.retained; retained != nil {
		input.SourceProjectionRevisionID = retained.SourceProjectionRevisionID
		input.Revisions.SourceProjection = retained.Revisions.SourceProjection
		input.WaveRevisionID, input.Revisions.Wave = retained.WaveRevisionID, retained.Revisions.Wave
	}
	proof, err := swissusecase.NewRoundLockProof(input)
	if err != nil {
		return swissusecase.RoundLockProof{}, gameusecase.ErrWaveStartAuthorityConflict
	}
	return proof, nil
}

func decodeRoundLockProofHash(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, domain.ErrInternal
	}
	var zero [sha256.Size]byte
	if string(decoded) == string(zero[:]) {
		return nil, domain.ErrInternal
	}
	return decoded, nil
}

func waveStartByeParticipantID(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
}

func startWaveSeries(
	ctx context.Context,
	querier *sqlc.Queries,
	rows []sqlc.LockWaveStartGamesRow,
	startedAt time.Time,
) error {
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		if _, duplicate := seen[row.SeriesID]; duplicate {
			return domain.ErrConflict
		}
		seen[row.SeriesID] = struct{}{}
		started, err := querier.StartWaveSeriesCAS(ctx, sqlc.StartWaveSeriesCASParams{
			StartedAt: tstz(startedAt), ID: row.SeriesID, RosterID: row.RosterID,
			ExpectedRevision: row.SeriesRevision,
		})
		if err != nil {
			return waveStartCASWriteError("activate Series", err)
		}
		if started.ID != row.SeriesID || started.State != string(domain.SeriesStateActive) || !started.StartedAt.Valid ||
			!started.StartedAt.Time.UTC().Equal(startedAt) {
			return domain.ErrInternal
		}
	}
	return nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func startWaveGames(
	ctx context.Context,
	querier *sqlc.Queries,
	rows []sqlc.LockWaveStartGamesRow,
	record gameusecase.StartRecord,
) error {
	startedAt := record.StartedAt
	for _, row := range rows {
		started, err := querier.StartWaveGameCAS(ctx, sqlc.StartWaveGameCASParams{
			StartedAt: tstz(startedAt), ID: row.GameID, SeriesID: row.SeriesID, RosterID: row.RosterID,
			ExpectedRevision: row.GameRevision, ExpectedState: row.GameState,
		})
		if err != nil {
			return waveStartCASWriteError("activate Game", err)
		}
		if started.ID != row.GameID || started.State != string(domain.GameStateActive) || !started.StartedAt.Valid ||
			!started.StartedAt.Time.UTC().Equal(startedAt) {
			return domain.ErrInternal
		}
		bound, err := querier.BindExecutionGameEpoch(ctx, sqlc.BindExecutionGameEpochParams{
			GameAttemptID: row.GameID, TournamentID: record.Scope.TournamentID,
			RosterID: row.RosterID, WaveID: record.Scope.WaveID, SeriesID: row.SeriesID,
			AuthorityHolderID: record.ExecutionAuthority.HolderID,
			AuthorityLeaseID:  record.ExecutionAuthority.LeaseID,
			AuthorityEpoch:    record.ExecutionAuthority.Epoch,
			BoundAt:           tstz(startedAt),
		})
		if err != nil {
			return waveStartCASWriteError("bind execution Game epoch", err)
		}
		if bound.GameAttemptID != row.GameID || bound.TournamentID != record.Scope.TournamentID ||
			bound.RosterID != row.RosterID || bound.WaveID != record.Scope.WaveID ||
			bound.SeriesID != row.SeriesID || bound.SlotID != row.SlotID ||
			bound.AuthorityHolderID != record.ExecutionAuthority.HolderID ||
			bound.AuthorityLeaseID != record.ExecutionAuthority.LeaseID ||
			bound.AuthorityEpoch != record.ExecutionAuthority.Epoch || bound.AuthorityRevision < 1 ||
			!bound.BoundAt.Valid || !bound.BoundAt.Time.UTC().Equal(startedAt) {
			return domain.ErrInternal
		}
	}
	return nil
}

func discloseWaveStartReservations(
	ctx context.Context,
	querier *sqlc.Queries,
	rows []sqlc.LockWaveStartGamesRow,
	startedAt time.Time,
) error {
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		if _, duplicate := seen[row.ReservationID]; duplicate {
			return domain.ErrConflict
		}
		seen[row.ReservationID] = struct{}{}
		disclosed, err := querier.DiscloseWaveStartReservationCAS(ctx, sqlc.DiscloseWaveStartReservationCASParams{
			DisclosedAt: tstz(startedAt), ID: row.ReservationID, ExpectedRevision: row.ReservationRevision,
		})
		if err != nil {
			return waveStartCASWriteError("disclose task reservation", err)
		}
		if disclosed.ID != row.ReservationID || !disclosed.DisclosedAt.Valid ||
			!disclosed.DisclosedAt.Time.UTC().Equal(startedAt) {
			return domain.ErrInternal
		}
	}
	return nil
}

func createWaveStartReceipts(
	ctx context.Context,
	querier *sqlc.Queries,
	record gameusecase.StartRecord,
	rows []sqlc.LockWaveStartGamesRow,
) error {
	for _, row := range rows {
		for _, participantID := range []uuid.UUID{row.FirstParticipantID, row.SecondParticipantID} {
			receiptID := waveStartReceiptID(record.CommandID, row.AssignmentID, participantID)
			receipt, err := querier.CreateAssignmentTaskDeliveryReceipt(ctx, sqlc.CreateAssignmentTaskDeliveryReceiptParams{
				ID: receiptID, AssignmentID: row.AssignmentID, AttemptID: row.GameID, RosterID: row.RosterID,
				ParticipantID: participantID,
				InstanceID:    domain.ParticipantTaskInstanceID(row.AssignmentID, participantID),
				SnapshotID:    row.SnapshotID, TaskID: row.TaskID, TaskVersion: row.TaskVersion,
				DeliveredAt: tstz(record.StartedAt),
			})
			if err != nil {
				return waveStartCASWriteError("create task delivery receipt", err)
			}
			if receipt.ID != receiptID || receipt.AssignmentID != row.AssignmentID ||
				receipt.ParticipantID != participantID || !receipt.DeliveredAt.Valid ||
				!receipt.DeliveredAt.Time.UTC().Equal(record.StartedAt) {
				return domain.ErrInternal
			}
		}
	}
	return nil
}

func waveStartReceiptID(commandID, assignmentID, participantID uuid.UUID) uuid.UUID {
	material := make([]byte, 0, len("task-delivery")+len(assignmentID)+len(participantID))
	material = append(material, "task-delivery"...)
	material = append(material, assignmentID[:]...)
	material = append(material, participantID[:]...)
	return uuid.NewSHA1(commandID, material)
}

func saveWaveStartCommand(
	ctx context.Context,
	querier *sqlc.Queries,
	record gameusecase.StartRecord,
	snapshot waveStartSnapshot,
) error {
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	sourceRevisions, err := json.Marshal(record.Revisions)
	if err != nil {
		return fmt.Errorf("encode Wave start source revisions: %w", err)
	}
	graph, err := waveStartGraph(snapshot.games)
	if err != nil {
		return err
	}
	sourceGraph, err := json.Marshal(graph)
	if err != nil {
		return fmt.Errorf("encode Wave start graph: %w", err)
	}
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	resultDocument, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode Wave start result: %w", err)
	}
	created, err := querier.CreateWaveControlCommand(ctx, sqlc.CreateWaveControlCommandParams{
		CommandID: record.CommandID, TournamentID: record.Scope.TournamentID, RosterID: snapshot.rosterID,
		WaveID: record.Scope.WaveID, ActorID: record.ActorID, Action: "start",
		SourceProjectionRevisionID: record.Revisions.ProjectionRevisionID,
		SourceProjectionRevision:   record.Revisions.ProjectionRevision,
		SourceTournamentRevision:   snapshot.tournamentRevision,
		SourceRosterRevision:       snapshot.rosterRevision,
		SourceWaveRevision:         record.ExpectedWaveRevision,
		ResultingWaveRevision:      record.ExpectedWaveRevision + 1,
		SourceRevisions:            sourceRevisions, SourceGraph: sourceGraph,
		RequestDigest: append([]byte(nil), record.RequestDigest[:]...), ResultDocument: resultDocument,
		ExecutedAt: tstz(record.StartedAt),
	})
	if err != nil {
		return waveStartCASWriteError("save Wave start command", err)
	}
	if created != record.CommandID {
		return domain.ErrInternal
	}
	return nil
}

func waveStartGraph(rows []sqlc.LockWaveStartGamesRow) (waveStartGraphEvidence, error) {
	if len(rows) == 0 {
		return waveStartGraphEvidence{}, domain.ErrInternal
	}
	graph := waveStartGraphEvidence{
		GameIDs: make([]uuid.UUID, len(rows)), ReservationIDs: make([]uuid.UUID, len(rows)),
		ReceiptCount: len(rows) * 2,
	}
	seenGames := make(map[uuid.UUID]struct{}, len(rows))
	seenReservations := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		if _, duplicate := seenGames[row.GameID]; duplicate {
			return waveStartGraphEvidence{}, domain.ErrInternal
		}
		if _, duplicate := seenReservations[row.ReservationID]; duplicate {
			return waveStartGraphEvidence{}, domain.ErrInternal
		}
		seenGames[row.GameID] = struct{}{}
		seenReservations[row.ReservationID] = struct{}{}
		graph.GameIDs[index] = row.GameID
		graph.ReservationIDs[index] = row.ReservationID
	}
	return graph, nil
}

func waveStartCASWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapRepositoryWriteError("TournamentAdminExecutionPostgres - "+operation, err)
}

func sameWaveStartRequest(first, second gameusecase.StartRecord) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID && first.ActorID == second.ActorID &&
		first.ExpectedWaveRevision == second.ExpectedWaveRevision &&
		first.ExpectedProjectionRevision == second.ExpectedProjectionRevision && first.Revisions == second.Revisions &&
		maps.Equal(first.ReadinessRevisions, second.ReadinessRevisions) && first.RequestDigest == second.RequestDigest
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func waveStartRecordFromControl(row sqlc.WaveControlCommand) (gameusecase.StartRecord, error) {
	if row.Action != "start" || row.CommandID == uuid.Nil || row.TournamentID == uuid.Nil || row.RosterID == uuid.Nil ||
		row.WaveID == uuid.Nil || row.ActorID == uuid.Nil || !row.ExecutedAt.Valid || !json.Valid(row.ResultDocument) ||
		!json.Valid(row.SourceRevisions) || !json.Valid(row.SourceGraph) {
		return gameusecase.StartRecord{}, domain.ErrInternal
	}
	var record gameusecase.StartRecord
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	if err := json.Unmarshal(row.ResultDocument, &record); err != nil || record.Validate() != nil {
		return gameusecase.StartRecord{}, domain.ErrInternal
	}
	var sourceRevisions domain.ReadyWindowSourceRevisions
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	if err := json.Unmarshal(row.SourceRevisions, &sourceRevisions); err != nil || sourceRevisions != record.Revisions {
		return gameusecase.StartRecord{}, domain.ErrInternal
	}
	digest, err := waveStartDigest(row.RequestDigest)
	if err != nil || digest != record.RequestDigest || record.CommandID != row.CommandID ||
		record.ActorID != row.ActorID || record.Scope.TournamentID != row.TournamentID ||
		record.Scope.WaveID != row.WaveID || record.ExpectedProjectionRevision != row.SourceProjectionRevision ||
		record.ExpectedWaveRevision != row.SourceWaveRevision ||
		row.ResultingWaveRevision != row.SourceWaveRevision+1 ||
		!row.ExecutedAt.Time.UTC().Equal(record.StartedAt) {
		return gameusecase.StartRecord{}, domain.ErrInternal
	}
	return record, nil
}

func validWaveStartScope(scope gameusecase.StartScope) bool {
	return scope.TournamentID != uuid.Nil && scope.WaveID != uuid.Nil && scope.WindowID != uuid.Nil
}

var _ gameusecase.StartRepository = (*TournamentAdminExecutionPostgres)(nil)
