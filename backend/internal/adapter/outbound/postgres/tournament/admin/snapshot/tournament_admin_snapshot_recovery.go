package snapshot

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

type recoveryControlKey struct {
	kind       tournamentadmin.RecoveryControlKind
	assignment uuid.UUID
	series     uuid.UUID
	slot       uuid.UUID
}

//nolint:gocyclo // Grouping repeated SQL rows preserves one control with its complete attempt history.
func tournamentAdminSnapshotRecoveryControls(
	rows []sqlc.ListTournamentAdminRecoveryControlsRow,
	candidateRows []sqlc.ListTournamentAdminRecoveryReserveCandidatesRow,
) ([]tournamentadmin.RecoveryControl, error) {
	controls := make(map[recoveryControlKey]*tournamentadmin.RecoveryControl, len(rows))
	order := make([]recoveryControlKey, 0, len(rows))
	seenAttempts := make(map[recoveryControlKey]map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		key, control, err := recoveryControlFromRow(row)
		if err != nil {
			return nil, err
		}
		current, exists := controls[key]
		if !exists {
			controls[key] = &control
			order = append(order, key)
			seenAttempts[key] = make(map[uuid.UUID]struct{})
			current = controls[key]
		} else if !recoveryControlEvidenceEqual(*current, control) {
			return nil, fmt.Errorf("recovery control evidence changed across attempts: %w", domain.ErrInternal)
		}
		if _, duplicate := seenAttempts[key][row.AttemptID]; duplicate {
			return nil, fmt.Errorf("duplicate recovery control attempt: %w", domain.ErrInternal)
		}
		seenAttempts[key][row.AttemptID] = struct{}{}
		current.Attempts = append(current.Attempts, recoveryGameFromRow(row))
	}

	for _, row := range candidateRows {
		if row.ExhaustionCommandID == uuid.Nil || row.TaskID == uuid.Nil || row.TaskVersion < 1 {
			return nil, domain.ErrInternal
		}
		var target *tournamentadmin.RecoveryControl
		for key, control := range controls {
			if key.kind == tournamentadmin.RecoveryControlReserveExhausted && control.ReserveExhausted != nil &&
				control.ReserveExhausted.ExpectedExhaustionCommandID == row.ExhaustionCommandID {
				target = control
				break
			}
		}
		if target == nil || target.ReserveExhausted == nil {
			return nil, fmt.Errorf("candidate lacks reserve exhaustion control: %w", domain.ErrInternal)
		}
		for _, candidate := range target.ReserveExhausted.Candidates {
			if candidate.TaskID == row.TaskID && candidate.Version == int(row.TaskVersion) {
				return nil, fmt.Errorf("duplicate recovery reserve candidate: %w", domain.ErrInternal)
			}
		}
		target.ReserveExhausted.Candidates = append(target.ReserveExhausted.Candidates, tournamentadmin.RecoveryReserveCandidate{
			TaskID: row.TaskID, Version: int(row.TaskVersion),
		})
	}

	result := make([]tournamentadmin.RecoveryControl, 0, len(order))
	for _, key := range order {
		control := *controls[key]
		if !validRecoveryControlProjection(control) {
			return nil, domain.ErrInternal
		}
		result = append(result, control)
	}
	return result, nil
}

//nolint:gocyclo // The row mapper rejects malformed persisted evidence before it crosses the usecase boundary.
func recoveryControlFromRow(
	row sqlc.ListTournamentAdminRecoveryControlsRow,
) (recoveryControlKey, tournamentadmin.RecoveryControl, error) {
	kind := tournamentadmin.RecoveryControlKind(row.ControlKind)
	key := recoveryControlKey{kind: kind, assignment: row.AssignmentID, series: row.SeriesID, slot: row.SlotID}
	if row.AssignmentID == uuid.Nil || row.SeriesID == uuid.Nil || row.SlotID == uuid.Nil ||
		!domain.Category(row.Category).IsValid() || row.ExpectedAuthorityRevision < 1 ||
		strings.TrimSpace(row.Reason) == "" || len(row.Reason) > 512 || row.AttemptID == uuid.Nil ||
		row.AttemptSlotID != row.SlotID || row.AttemptNumber < 1 {
		return recoveryControlKey{}, tournamentadmin.RecoveryControl{}, domain.ErrInternal
	}
	control := tournamentadmin.RecoveryControl{
		AssignmentID: row.AssignmentID, Category: domain.Category(row.Category), ExpectedAuthorityRevision: row.ExpectedAuthorityRevision,
		Kind: kind, OldWaveID: row.OldWaveID, Reason: row.Reason, SeriesID: row.SeriesID, SlotID: row.SlotID,
	}
	if row.OldWaveID == uuid.Nil {
		return recoveryControlKey{}, tournamentadmin.RecoveryControl{}, domain.ErrInternal
	}
	if row.PauseReason != nil {
		reason := pauseusecase.PauseReason(*row.PauseReason)
		if !validRecoveryPauseReason(reason) {
			return recoveryControlKey{}, tournamentadmin.RecoveryControl{}, domain.ErrInternal
		}
		control.PauseReason = &reason
	}
	switch kind {
	case tournamentadmin.RecoveryControlReplay:
		if row.ReplayExpectedClosureRevisionID == uuid.Nil ||
			row.ExhaustionCommandID.Valid || row.CurrentSnapshotID.Valid {
			return recoveryControlKey{}, tournamentadmin.RecoveryControl{}, domain.ErrInternal
		}
		control.Replay = &tournamentadmin.RecoveryReplayDetails{
			Available: true, ExpectedClosureRevisionID: row.ReplayExpectedClosureRevisionID,
		}
	case tournamentadmin.RecoveryControlReserveExhausted:
		details, err := recoveryReserveDetails(row)
		if err != nil {
			return recoveryControlKey{}, tournamentadmin.RecoveryControl{}, err
		}
		control.ReserveExhausted = details
	default:
		return recoveryControlKey{}, tournamentadmin.RecoveryControl{}, domain.ErrInternal
	}
	return key, control, nil
}

func recoveryReserveDetails(
	row sqlc.ListTournamentAdminRecoveryControlsRow,
) (*tournamentadmin.RecoveryReserveExhaustedDetails, error) {
	currentSnapshot, ok := validRecoveryUUID(row.CurrentSnapshotID)
	if !ok {
		return nil, domain.ErrInternal
	}
	exhaustionCommand, ok := validRecoveryUUID(row.ExhaustionCommandID)
	if !ok {
		return nil, domain.ErrInternal
	}
	expectedSnapshot, ok := validRecoveryUUID(row.ExpectedSnapshotID)
	if !ok {
		return nil, domain.ErrInternal
	}
	poolRevisionID, ok := validRecoveryUUID(row.ExpectedPoolRevisionID)
	if !ok {
		return nil, domain.ErrInternal
	}
	historyRevisionID, ok := validRecoveryUUID(row.ExpectedHistoryRevisionID)
	if !ok {
		return nil, domain.ErrInternal
	}
	artifactRevisionID, ok := validRecoveryUUID(row.ExpectedArtifactRevisionID)
	if !ok {
		return nil, domain.ErrInternal
	}
	reservationRevisionID, ok := validRecoveryUUID(row.ExpectedReservationRevisionID)
	if !ok {
		return nil, domain.ErrInternal
	}
	categoryRevisionID, ok := validRecoveryUUID(row.ExpectedCategoryRevisionID)
	if !ok {
		return nil, domain.ErrInternal
	}
	assignmentRevision, ok := validRecoveryInt64(row.ExpectedAssignmentRevision)
	if !ok {
		return nil, domain.ErrInternal
	}
	poolRevision, ok := validRecoveryInt64(row.ExpectedPoolRevision)
	if !ok {
		return nil, domain.ErrInternal
	}
	historyRevision, ok := validRecoveryInt64(row.ExpectedHistoryRevision)
	if !ok {
		return nil, domain.ErrInternal
	}
	artifactRevision, ok := validRecoveryInt64(row.ExpectedArtifactRevision)
	if !ok {
		return nil, domain.ErrInternal
	}
	reservationRevision, ok := validRecoveryInt64(row.ExpectedReservationRevision)
	if !ok {
		return nil, domain.ErrInternal
	}
	categoryRevision, ok := validRecoveryInt64(row.ExpectedCategoryRevision)
	if !ok {
		return nil, domain.ErrInternal
	}
	return &tournamentadmin.RecoveryReserveExhaustedDetails{
		CurrentSnapshotID: currentSnapshot, ExpectedArtifactRevision: artifactRevision, ExpectedArtifactRevisionID: artifactRevisionID,
		ExpectedAssignmentRevision: assignmentRevision, ExpectedCategoryRevision: categoryRevision, ExpectedCategoryRevisionID: categoryRevisionID,
		ExpectedExhaustionCommandID: exhaustionCommand, ExpectedHistoryRevision: historyRevision, ExpectedHistoryRevisionID: historyRevisionID,
		ExpectedPoolRevision: poolRevision, ExpectedPoolRevisionID: poolRevisionID, ExpectedReservationRevision: reservationRevision,
		ExpectedReservationRevisionID: reservationRevisionID, ExpectedSnapshotID: expectedSnapshot,
	}, nil
}

func recoveryGameFromRow(row sqlc.ListTournamentAdminRecoveryControlsRow) domain.Game {
	game := domain.Game{
		ID: row.AttemptID, SlotID: row.AttemptSlotID, AttemptNo: int(row.AttemptNumber),
		State: domain.GameState(row.AttemptState), ResultReason: domain.GameResultReason(valueOrEmpty(row.AttemptResultReason)),
	}
	if winner, ok := validRecoveryUUID(row.AttemptWinnerID); ok {
		game.WinnerID = &winner
	}
	if revision, ok := validRecoveryUUID(row.AttemptResultRevisionID); ok {
		resultRevision := domain.OfficialResultRevisionID(revision)
		game.ResultRevisionID = &resultRevision
	}
	return game
}

func recoveryControlEvidenceEqual(first, second tournamentadmin.RecoveryControl) bool {
	if first.AssignmentID != second.AssignmentID || first.SeriesID != second.SeriesID || first.SlotID != second.SlotID ||
		first.Category != second.Category || first.ExpectedAuthorityRevision != second.ExpectedAuthorityRevision ||
		first.Kind != second.Kind || first.OldWaveID != second.OldWaveID || first.Reason != second.Reason ||
		!recoveryPauseReasonsEqual(first.PauseReason, second.PauseReason) {
		return false
	}
	if (first.Replay == nil) != (second.Replay == nil) || (first.ReserveExhausted == nil) != (second.ReserveExhausted == nil) {
		return false
	}
	if first.Replay != nil && *first.Replay != *second.Replay {
		return false
	}
	return recoveryReserveDetailsEqual(first.ReserveExhausted, second.ReserveExhausted)
}

//nolint:gocyclo // Every source revision is part of the compare-and-set evidence.
func recoveryReserveDetailsEqual(first, second *tournamentadmin.RecoveryReserveExhaustedDetails) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.CurrentSnapshotID == second.CurrentSnapshotID && first.ExpectedArtifactRevision == second.ExpectedArtifactRevision &&
		first.ExpectedArtifactRevisionID == second.ExpectedArtifactRevisionID && first.ExpectedAssignmentRevision == second.ExpectedAssignmentRevision &&
		first.ExpectedCategoryRevision == second.ExpectedCategoryRevision && first.ExpectedCategoryRevisionID == second.ExpectedCategoryRevisionID &&
		first.ExpectedExhaustionCommandID == second.ExpectedExhaustionCommandID && first.ExpectedHistoryRevision == second.ExpectedHistoryRevision &&
		first.ExpectedHistoryRevisionID == second.ExpectedHistoryRevisionID && first.ExpectedPoolRevision == second.ExpectedPoolRevision &&
		first.ExpectedPoolRevisionID == second.ExpectedPoolRevisionID && first.ExpectedReservationRevision == second.ExpectedReservationRevision &&
		first.ExpectedReservationRevisionID == second.ExpectedReservationRevisionID && first.ExpectedSnapshotID == second.ExpectedSnapshotID
}

func recoveryPauseReasonsEqual(first, second *pauseusecase.PauseReason) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func validRecoveryControlProjection(control tournamentadmin.RecoveryControl) bool {
	return control.AssignmentID != uuid.Nil && control.SeriesID != uuid.Nil && control.SlotID != uuid.Nil &&
		control.OldWaveID != uuid.Nil && control.Category.IsValid() && control.ExpectedAuthorityRevision >= 1 &&
		strings.TrimSpace(control.Reason) == control.Reason && control.Reason != "" && len(control.Reason) <= 512 &&
		len(control.Attempts) > 0
}

func validRecoveryPauseReason(reason pauseusecase.PauseReason) bool {
	switch reason {
	case pauseusecase.PauseReasonOperator, pauseusecase.PauseReasonDisconnect,
		pauseusecase.PauseReasonPlatform, pauseusecase.PauseReasonExecutionEpoch:
		return true
	default:
		return false
	}
}

func validRecoveryUUID(value uuid.NullUUID) (uuid.UUID, bool) {
	return value.UUID, value.Valid && value.UUID != uuid.Nil
}

func validRecoveryInt64(value *int64) (int64, bool) {
	return valueOrZero(value), value != nil && *value >= 1
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
