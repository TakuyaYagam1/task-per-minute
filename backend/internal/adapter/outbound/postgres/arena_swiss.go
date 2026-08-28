package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

var ErrArenaSwissRoundNotFound = errors.New("arena swiss repository: round not found")

type ArenaSwissPostgres struct {
	tx *TxManager
}

type ArenaSwissRoundMeta struct {
	ID                    uuid.UUID
	RosterID              uuid.UUID
	RoundNumber           int16
	SourceRosterRevision  int64
	SourceHistoryRevision int64
	PairingIDs            []uuid.UUID
	PriorMeetingCounts    []int16
	Bye                   *arena.SwissByeSelection
	GeneratedAt           time.Time
	RecordedAt            time.Time
	LockedAt              *time.Time
}

type ArenaAutomaticSwissRoundInput struct {
	Meta    ArenaSwissRoundMeta
	Pairing arena.AutomaticSwissPairing
}

type ArenaManualSwissRoundInput struct {
	Meta          ArenaSwissRoundMeta
	Round         arena.ManualSwissRound
	PairingInputs []string
	Override      *arena.PairingRepeatOverride
}

type ArenaSwissRoundRecord struct {
	ID                    uuid.UUID
	RosterID              uuid.UUID
	RoundNumber           int
	Revision              int64
	SourceRosterRevision  int64
	SourceHistoryRevision int64
	GenerationKind        string
	PairingInputs         []string
	AutomaticEvidence     *domain.ArenaDecisionEvidence
	GeneratedAt           time.Time
	LockRevision          *int64
	LockedAt              *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type ArenaSwissPairingRecord struct {
	ID                 uuid.UUID
	RoundID            uuid.UUID
	RosterID           uuid.UUID
	SlotNumber         int
	Pair               arena.SwissPair
	PriorMeetingCount  int
	RepeatOverrideID   *uuid.UUID
	OpponentRecordedAt time.Time
}

type ArenaSwissRoundAggregate struct {
	Round    ArenaSwissRoundRecord
	Pairings []ArenaSwissPairingRecord
	Bye      *arena.SwissByeSelection
	Override *arena.PairingRepeatOverride
}

func NewArenaSwissPostgres(tx *TxManager) *ArenaSwissPostgres {
	return &ArenaSwissPostgres{tx: tx}
}

func (r *ArenaSwissPostgres) SaveAutomaticRound(
	ctx context.Context,
	in ArenaAutomaticSwissRoundInput,
	expectedRevision *int64,
) (*ArenaSwissRoundRecord, error) {
	var saved sqlc.ArenaSwissRound
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		participants, err := r.checkedInParticipantIDs(txCtx, in.Meta.RosterID)
		if err != nil {
			return err
		}
		if err := validateAutomaticSwissRoundInput(in, participants); err != nil {
			return err
		}

		querier := r.tx.Querier(txCtx)
		if expectedRevision != nil {
			if err := r.prepareRoundReplacement(txCtx, in.Meta, *expectedRevision); err != nil {
				return err
			}
			saved, err = querier.UpdateAutomaticArenaSwissRoundCAS(
				txCtx,
				automaticRoundUpdateParams(in, *expectedRevision),
			)
		} else {
			saved, err = querier.CreateAutomaticArenaSwissRound(txCtx, automaticRoundCreateParams(in))
		}
		if err != nil {
			return fmt.Errorf("write automatic round: %w", err)
		}
		if err := r.insertRoundChildren(
			txCtx,
			in.Meta,
			in.Pairing.Pairings,
			nil,
		); err != nil {
			return err
		}
		if in.Meta.LockedAt != nil {
			saved, err = querier.LockArenaSwissRoundCAS(txCtx, sqlc.LockArenaSwissRoundCASParams{
				LockedAt:         tstz(*in.Meta.LockedAt),
				ID:               in.Meta.ID,
				ExpectedRevision: saved.Revision,
			})
			if err != nil {
				return fmt.Errorf("lock automatic round: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, r.mapSwissWriteError("SaveAutomaticRound", err)
	}
	record, err := arenaSwissRoundRecord(saved)
	if err != nil {
		return nil, fmt.Errorf("ArenaSwissPostgres - SaveAutomaticRound - map saved round: %w", err)
	}
	return record, nil
}

func (r *ArenaSwissPostgres) SaveManualRound(
	ctx context.Context,
	in ArenaManualSwissRoundInput,
	expectedRevision *int64,
) (*ArenaSwissRoundRecord, error) {
	var saved sqlc.ArenaSwissRound
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		participants, err := r.checkedInParticipantIDs(txCtx, in.Meta.RosterID)
		if err != nil {
			return err
		}
		if err := validateManualSwissRoundInput(in, participants); err != nil {
			return err
		}

		querier := r.tx.Querier(txCtx)
		if expectedRevision != nil {
			if err := r.prepareRoundReplacement(txCtx, in.Meta, *expectedRevision); err != nil {
				return err
			}
			saved, err = querier.UpdateManualArenaSwissRoundCAS(
				txCtx,
				manualRoundUpdateParams(in, *expectedRevision),
			)
		} else {
			saved, err = querier.CreateManualArenaSwissRound(txCtx, manualRoundCreateParams(in))
		}
		if err != nil {
			return fmt.Errorf("write manual round: %w", err)
		}
		if err := r.insertRoundChildren(txCtx, in.Meta, in.Round.Pairings, in.Override); err != nil {
			return err
		}
		if in.Meta.LockedAt != nil {
			saved, err = querier.LockArenaSwissRoundCAS(txCtx, sqlc.LockArenaSwissRoundCASParams{
				LockedAt:         tstz(*in.Meta.LockedAt),
				ID:               in.Meta.ID,
				ExpectedRevision: saved.Revision,
			})
			if err != nil {
				return fmt.Errorf("lock manual round: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, r.mapSwissWriteError("SaveManualRound", err)
	}
	record, err := arenaSwissRoundRecord(saved)
	if err != nil {
		return nil, fmt.Errorf("ArenaSwissPostgres - SaveManualRound - map saved round: %w", err)
	}
	return record, nil
}

func (r *ArenaSwissPostgres) LockRound(
	ctx context.Context,
	roundID uuid.UUID,
	expectedRevision int64,
	lockedAt time.Time,
) (*ArenaSwissRoundRecord, bool, error) {
	if roundID == uuid.Nil || expectedRevision < 1 || !validServerTime(lockedAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).LockArenaSwissRoundCAS(ctx, sqlc.LockArenaSwissRoundCASParams{
		LockedAt:         tstz(lockedAt),
		ID:               roundID,
		ExpectedRevision: expectedRevision,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("ArenaSwissPostgres - LockRound - Querier.LockArenaSwissRoundCAS: %w", err)
	}
	record, err := arenaSwissRoundRecord(row)
	if err != nil {
		return nil, false, fmt.Errorf("ArenaSwissPostgres - LockRound - map round: %w", err)
	}
	return record, true, nil
}

func (r *ArenaSwissPostgres) Get(ctx context.Context, roundID uuid.UUID) (*ArenaSwissRoundAggregate, error) {
	row, err := r.tx.Querier(ctx).GetArenaSwissRound(ctx, roundID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArenaSwissRoundNotFound
		}
		return nil, fmt.Errorf("ArenaSwissPostgres - Get - Querier.GetArenaSwissRound: %w", err)
	}
	round, err := arenaSwissRoundRecord(row)
	if err != nil {
		return nil, fmt.Errorf("ArenaSwissPostgres - Get - map round: %w", err)
	}

	history, err := r.ListOpponentHistory(ctx, row.RosterID)
	if err != nil {
		return nil, err
	}
	pairings := make([]ArenaSwissPairingRecord, 0)
	for _, item := range history {
		if item.RoundID == roundID {
			pairings = append(pairings, item)
		}
	}

	override, err := r.getRepeatOverride(ctx, roundID)
	if err != nil {
		return nil, err
	}
	bye, err := r.getBye(ctx, roundID)
	if err != nil {
		return nil, err
	}
	aggregate := &ArenaSwissRoundAggregate{Round: *round, Pairings: pairings, Bye: bye, Override: override}
	if err := r.validateLoadedAggregate(ctx, aggregate); err != nil {
		return nil, fmt.Errorf("ArenaSwissPostgres - Get - validate aggregate: %w", err)
	}
	return aggregate, nil
}

func (r *ArenaSwissPostgres) ListRounds(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]ArenaSwissRoundRecord, error) {
	rows, err := r.tx.Querier(ctx).ListArenaSwissRounds(ctx, rosterID)
	if err != nil {
		return nil, fmt.Errorf("ArenaSwissPostgres - ListRounds - Querier.ListArenaSwissRounds: %w", err)
	}
	out := make([]ArenaSwissRoundRecord, 0, len(rows))
	for _, row := range rows {
		record, mapErr := arenaSwissRoundRecord(row)
		if mapErr != nil {
			return nil, fmt.Errorf("ArenaSwissPostgres - ListRounds - map round: %w", mapErr)
		}
		out = append(out, *record)
	}
	return out, nil
}

func (r *ArenaSwissPostgres) ListOpponentHistory(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]ArenaSwissPairingRecord, error) {
	rows, err := r.tx.Querier(ctx).ListArenaSwissOpponentHistory(ctx, rosterID)
	if err != nil {
		return nil, fmt.Errorf(
			"ArenaSwissPostgres - ListOpponentHistory - Querier.ListArenaSwissOpponentHistory: %w",
			err,
		)
	}
	out := make([]ArenaSwissPairingRecord, 0, len(rows))
	for _, row := range rows {
		var overrideID *uuid.UUID
		if row.RepeatOverrideID.Valid {
			value := row.RepeatOverrideID.UUID
			overrideID = &value
		}
		out = append(out, ArenaSwissPairingRecord{
			ID:         row.PairingID,
			RoundID:    row.RoundID,
			RosterID:   row.RosterID,
			SlotNumber: int(row.SlotNumber),
			Pair: arena.SwissPair{
				FirstParticipantID:  row.FirstParticipantID,
				SecondParticipantID: row.SecondParticipantID,
			},
			PriorMeetingCount:  int(row.PriorMeetingCount),
			RepeatOverrideID:   overrideID,
			OpponentRecordedAt: row.RecordedAt.Time,
		})
	}
	return out, nil
}

func (r *ArenaSwissPostgres) checkedInParticipantIDs(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]uuid.UUID, error) {
	participants, err := r.tx.Querier(ctx).ListArenaParticipants(ctx, rosterID)
	if err != nil {
		return nil, fmt.Errorf("list roster participants: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(participants))
	for _, participant := range participants {
		if domain.ArenaAttendanceState(participant.Attendance) == domain.ArenaAttendanceStateCheckedIn {
			ids = append(ids, participant.ID)
		}
	}
	return ids, nil
}

func (r *ArenaSwissPostgres) prepareRoundReplacement(
	ctx context.Context,
	meta ArenaSwissRoundMeta,
	expectedRevision int64,
) error {
	if expectedRevision < 1 {
		return domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	current, err := querier.LockArenaSwissRoundForUpdate(ctx, meta.ID)
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision || current.LockedAt.Valid || current.RosterID != meta.RosterID ||
		current.RoundNumber != meta.RoundNumber {
		return errArenaSwissRoundCAS
	}
	if meta.GeneratedAt.After(current.CreatedAt.Time) || meta.RecordedAt.Before(current.UpdatedAt.Time) {
		return domain.ErrValidation
	}
	if err := querier.DeleteArenaSwissOpponentHistory(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete opponent history: %w", err)
	}
	if err := querier.DeleteArenaSwissPairingMembers(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete pairing members: %w", err)
	}
	if err := querier.DeleteArenaSwissPairings(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete pairings: %w", err)
	}
	if err := querier.DeleteArenaSwissBye(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete bye: %w", err)
	}
	if err := querier.DeleteArenaSwissRepeatOverride(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete repeat override: %w", err)
	}
	return nil
}

func (r *ArenaSwissPostgres) insertRoundChildren(
	ctx context.Context,
	meta ArenaSwissRoundMeta,
	pairs []arena.SwissPair,
	override *arena.PairingRepeatOverride,
) error {
	querier := r.tx.Querier(ctx)
	var overrideID uuid.NullUUID
	if override != nil {
		params, err := repeatOverrideParams(meta, *override)
		if err != nil {
			return err
		}
		if err := querier.CreateArenaSwissRepeatOverride(ctx, params); err != nil {
			return fmt.Errorf("insert repeat override: %w", err)
		}
		overrideID = uuid.NullUUID{UUID: override.CommandID, Valid: true}
	}

	for index, pair := range pairs {
		pairingOverrideID := uuid.NullUUID{}
		if meta.PriorMeetingCounts[index] > 0 {
			pairingOverrideID = overrideID
		}
		pairingID := meta.PairingIDs[index]
		if err := querier.CreateArenaSwissPairing(ctx, sqlc.CreateArenaSwissPairingParams{
			ID:               pairingID,
			RoundID:          meta.ID,
			RosterID:         meta.RosterID,
			SlotNumber:       int16(index + 1),
			RepeatOverrideID: pairingOverrideID,
			CreatedAt:        tstz(meta.RecordedAt),
		}); err != nil {
			return fmt.Errorf("insert pairing %d: %w", index+1, err)
		}
		for seat, participantID := range []uuid.UUID{pair.FirstParticipantID, pair.SecondParticipantID} {
			if err := querier.CreateArenaSwissPairingMember(ctx, sqlc.CreateArenaSwissPairingMemberParams{
				PairingID:     pairingID,
				RoundID:       meta.ID,
				RosterID:      meta.RosterID,
				Seat:          int16(seat + 1),
				ParticipantID: participantID,
				CreatedAt:     tstz(meta.RecordedAt),
			}); err != nil {
				return fmt.Errorf("insert pairing %d seat %d: %w", index+1, seat+1, err)
			}
		}
		if err := querier.CreateArenaSwissOpponentHistory(ctx, sqlc.CreateArenaSwissOpponentHistoryParams{
			PairingID:         pairingID,
			RoundID:           meta.ID,
			RosterID:          meta.RosterID,
			PriorMeetingCount: meta.PriorMeetingCounts[index],
			RepeatOverrideID:  pairingOverrideID,
			RecordedAt:        tstz(meta.RecordedAt),
		}); err != nil {
			return fmt.Errorf("insert opponent history %d: %w", index+1, err)
		}
	}

	if meta.Bye == nil {
		return nil
	}
	params, err := byeParams(meta, *meta.Bye)
	if err != nil {
		return err
	}
	if err := querier.CreateArenaSwissBye(ctx, params); err != nil {
		return fmt.Errorf("insert bye: %w", err)
	}
	return nil
}

func (r *ArenaSwissPostgres) getRepeatOverride(
	ctx context.Context,
	roundID uuid.UUID,
) (*arena.PairingRepeatOverride, error) {
	row, err := r.tx.Querier(ctx).GetArenaSwissRepeatOverride(ctx, roundID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("ArenaSwissPostgres - getRepeatOverride: %w", err)
	}
	override, err := repeatOverrideFromRow(row)
	if err != nil {
		return nil, fmt.Errorf("ArenaSwissPostgres - getRepeatOverride - decode: %w", err)
	}
	return override, nil
}

func (r *ArenaSwissPostgres) getBye(ctx context.Context, roundID uuid.UUID) (*arena.SwissByeSelection, error) {
	row, err := r.tx.Querier(ctx).GetArenaSwissBye(ctx, roundID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("ArenaSwissPostgres - getBye: %w", err)
	}
	bye, err := byeFromRow(row)
	if err != nil {
		return nil, fmt.Errorf("ArenaSwissPostgres - getBye - decode: %w", err)
	}
	return bye, nil
}

func (r *ArenaSwissPostgres) validateLoadedAggregate(
	ctx context.Context,
	aggregate *ArenaSwissRoundAggregate,
) error {
	participants, err := r.checkedInParticipantIDs(ctx, aggregate.Round.RosterID)
	if err != nil {
		return err
	}
	pairs := make([]arena.SwissPair, len(aggregate.Pairings))
	for index, pairing := range aggregate.Pairings {
		pairs[index] = pairing.Pair
	}
	switch aggregate.Round.GenerationKind {
	case "automatic":
		return validateLoadedAutomaticRound(aggregate, participants, pairs)
	case "manual":
		return validateLoadedManualRound(aggregate, participants, pairs)
	default:
		return domain.ErrValidation
	}
}

func validateLoadedAutomaticRound(
	aggregate *ArenaSwissRoundAggregate,
	participants []uuid.UUID,
	pairs []arena.SwissPair,
) error {
	if aggregate.Round.AutomaticEvidence == nil {
		return domain.ErrValidation
	}
	if _, err := arena.ReplayAutomaticSwissPairing(arena.AutomaticSwissPairing{
		Pairings: pairs,
		Evidence: *aggregate.Round.AutomaticEvidence,
	}); err != nil {
		return err
	}
	for _, pairing := range aggregate.Pairings {
		if pairing.PriorMeetingCount != 0 || pairing.RepeatOverrideID != nil {
			return domain.ErrValidation
		}
	}
	return validateSwissCoverage(participants, pairs, aggregate.Bye)
}

func validateLoadedManualRound(
	aggregate *ArenaSwissRoundAggregate,
	participants []uuid.UUID,
	pairs []arena.SwissPair,
) error {
	for _, pairing := range aggregate.Pairings {
		isRepeated := aggregate.Override != nil &&
			containsSwissPair(aggregate.Override.RepeatedPairings, pairing.Pair)
		if (pairing.PriorMeetingCount > 0) != isRepeated ||
			(isRepeated && pairing.RepeatOverrideID == nil) ||
			(!isRepeated && pairing.RepeatOverrideID != nil) {
			return domain.ErrValidation
		}
	}
	byeID := uuid.Nil
	if aggregate.Bye != nil {
		byeID = aggregate.Bye.ParticipantID
	}
	return arena.ValidateManualSwissPairing(
		participants,
		arena.ManualSwissRound{ID: aggregate.Round.ID, Pairings: pairs, ByeParticipantID: byeID},
		overridePreviousMeetings(aggregate.Override),
		aggregate.Override,
	)
}

func validateAutomaticSwissRoundInput(
	in ArenaAutomaticSwissRoundInput,
	participants []uuid.UUID,
) error {
	if err := validateSwissRoundMeta(in.Meta, in.Pairing.Pairings); err != nil {
		return err
	}
	if _, err := arena.ReplayAutomaticSwissPairing(in.Pairing); err != nil {
		return fmt.Errorf("%w: automatic pairing evidence: %w", domain.ErrValidation, err)
	}
	if in.Pairing.Evidence.OwnerID != in.Meta.ID || !in.Pairing.Evidence.DecidedAt.Equal(in.Meta.GeneratedAt) {
		return domain.ErrValidation
	}
	for _, count := range in.Meta.PriorMeetingCounts {
		if count != 0 {
			return domain.ErrValidation
		}
	}
	return validateSwissCoverage(participants, in.Pairing.Pairings, in.Meta.Bye)
}

func validateManualSwissRoundInput(in ArenaManualSwissRoundInput, participants []uuid.UUID) error {
	if err := validateSwissRoundMeta(in.Meta, in.Round.Pairings); err != nil {
		return err
	}
	if in.Round.ID != in.Meta.ID || len(in.PairingInputs) == 0 {
		return domain.ErrValidation
	}
	if err := arena.ValidateManualSwissPairing(
		participants,
		in.Round,
		overridePreviousMeetings(in.Override),
		in.Override,
	); err != nil {
		return fmt.Errorf("%w: manual pairing: %w", domain.ErrValidation, err)
	}
	for index, count := range in.Meta.PriorMeetingCounts {
		isRepeated := in.Override != nil && containsSwissPair(in.Override.RepeatedPairings, in.Round.Pairings[index])
		if (count > 0) != isRepeated {
			return domain.ErrValidation
		}
	}
	if in.Override != nil {
		if err := in.Override.Validate(); err != nil {
			return fmt.Errorf("%w: repeat override: %w", domain.ErrValidation, err)
		}
		if in.Override.RoundID != in.Meta.ID {
			return domain.ErrValidation
		}
	}
	return validateSwissCoverage(participants, in.Round.Pairings, in.Meta.Bye)
}

func validateSwissRoundMeta(meta ArenaSwissRoundMeta, pairs []arena.SwissPair) error {
	if err := validateSwissRoundIdentity(meta, pairs); err != nil {
		return err
	}
	if err := validateSwissRoundTimes(meta); err != nil {
		return err
	}
	if err := validateSwissPairingMetadata(meta); err != nil {
		return err
	}
	return validateSwissByeMetadata(meta)
}

func validateSwissRoundIdentity(meta ArenaSwissRoundMeta, pairs []arena.SwissPair) error {
	if meta.ID == uuid.Nil || meta.RosterID == uuid.Nil || meta.RoundNumber < 1 || meta.RoundNumber > 4 ||
		meta.SourceRosterRevision < 1 || meta.SourceHistoryRevision < 0 ||
		len(meta.PairingIDs) != len(pairs) || len(meta.PriorMeetingCounts) != len(pairs) {
		return domain.ErrValidation
	}
	return nil
}

func validateSwissRoundTimes(meta ArenaSwissRoundMeta) error {
	if !validServerTime(meta.GeneratedAt) || !validServerTime(meta.RecordedAt) ||
		meta.GeneratedAt.After(meta.RecordedAt) {
		return domain.ErrValidation
	}
	if meta.LockedAt != nil && (!validServerTime(*meta.LockedAt) || meta.LockedAt.Before(meta.GeneratedAt)) {
		return domain.ErrValidation
	}
	return nil
}

func validateSwissPairingMetadata(meta ArenaSwissRoundMeta) error {
	seen := make(map[uuid.UUID]struct{}, len(meta.PairingIDs))
	for index, pairingID := range meta.PairingIDs {
		if pairingID == uuid.Nil || meta.PriorMeetingCounts[index] < 0 {
			return domain.ErrValidation
		}
		if _, duplicate := seen[pairingID]; duplicate {
			return domain.ErrValidation
		}
		seen[pairingID] = struct{}{}
	}
	return nil
}

func validateSwissByeMetadata(meta ArenaSwissRoundMeta) error {
	if meta.Bye != nil {
		if _, err := arena.ReplaySwissBye(*meta.Bye); err != nil || meta.Bye.Evidence.OwnerID != meta.ID ||
			meta.Bye.Evidence.DecidedAt.After(meta.RecordedAt) {
			return domain.ErrValidation
		}
	}
	return nil
}

func validateSwissCoverage(
	participants []uuid.UUID,
	pairs []arena.SwissPair,
	bye *arena.SwissByeSelection,
) error {
	expected := make(map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		if participantID == uuid.Nil {
			return domain.ErrValidation
		}
		expected[participantID] = struct{}{}
	}
	used := make(map[uuid.UUID]struct{}, len(participants))
	for _, pair := range pairs {
		if pair.FirstParticipantID == uuid.Nil || pair.SecondParticipantID == uuid.Nil ||
			pair.FirstParticipantID == pair.SecondParticipantID {
			return domain.ErrValidation
		}
		for _, participantID := range []uuid.UUID{pair.FirstParticipantID, pair.SecondParticipantID} {
			if _, ok := expected[participantID]; !ok {
				return domain.ErrValidation
			}
			if _, duplicate := used[participantID]; duplicate {
				return domain.ErrValidation
			}
			used[participantID] = struct{}{}
		}
	}
	if bye != nil {
		if _, ok := expected[bye.ParticipantID]; !ok {
			return domain.ErrValidation
		}
		if _, duplicate := used[bye.ParticipantID]; duplicate {
			return domain.ErrValidation
		}
		used[bye.ParticipantID] = struct{}{}
	}
	if len(used) != len(expected) || (len(expected)%2 == 1) != (bye != nil) {
		return domain.ErrValidation
	}
	return nil
}

func (r *ArenaSwissPostgres) mapSwissWriteError(operation string, err error) error {
	switch {
	case errors.Is(err, errArenaSwissRoundCAS):
		return domain.ErrConflict
	case errors.Is(err, pgx.ErrNoRows):
		return ErrArenaSwissRoundNotFound
	case isSwissConflict(err):
		return domain.WrapError(err, domain.ErrConflict)
	default:
		return fmt.Errorf("ArenaSwissPostgres - %s: %w", operation, err)
	}
}

var errArenaSwissRoundCAS = errors.New("arena swiss round compare-and-set failed")

func isSwissConflict(err error) bool {
	constraints := []string{
		"arena_swiss_rounds_pkey",
		"arena_swiss_rounds_roster_number_key",
		"arena_swiss_rounds_decision_evidence_id_key",
		"arena_swiss_repeat_overrides_round_key",
		"arena_swiss_pairings_round_slot_key",
		"arena_swiss_pairing_members_round_participant_key",
		"arena_swiss_byes_roster_participant_key",
	}
	for _, constraint := range constraints {
		if isUniqueViolation(err, constraint) {
			return true
		}
	}
	return false
}

func automaticRoundCreateParams(in ArenaAutomaticSwissRoundInput) sqlc.CreateAutomaticArenaSwissRoundParams {
	algorithm := in.Pairing.Evidence.AlgorithmVersion
	inputs := mustJSON(in.Pairing.Evidence.NormalizedInputs)
	result := mustJSON(in.Pairing.Evidence.Result)
	return sqlc.CreateAutomaticArenaSwissRoundParams{
		ID:                       in.Meta.ID,
		RosterID:                 in.Meta.RosterID,
		RoundNumber:              in.Meta.RoundNumber,
		SourceRosterRevision:     in.Meta.SourceRosterRevision,
		SourceHistoryRevision:    in.Meta.SourceHistoryRevision,
		PairingInputs:            inputs,
		DecisionEvidenceID:       uuid.NullUUID{UUID: in.Pairing.Evidence.ID, Valid: true},
		DecisionAlgorithmVersion: &algorithm,
		DecisionSeed:             append([]byte(nil), in.Pairing.Evidence.Seed[:]...),
		DecisionResult:           result,
		DecisionReplayDigest:     append([]byte(nil), in.Pairing.Evidence.ReplayDigest[:]...),
		DecisionOwnerID:          uuid.NullUUID{UUID: in.Pairing.Evidence.OwnerID, Valid: true},
		GeneratedAt:              tstz(in.Meta.GeneratedAt),
		CreatedAt:                tstz(in.Meta.RecordedAt),
	}
}

func automaticRoundUpdateParams(
	in ArenaAutomaticSwissRoundInput,
	expectedRevision int64,
) sqlc.UpdateAutomaticArenaSwissRoundCASParams {
	created := automaticRoundCreateParams(in)
	return sqlc.UpdateAutomaticArenaSwissRoundCASParams{
		SourceRosterRevision:     created.SourceRosterRevision,
		SourceHistoryRevision:    created.SourceHistoryRevision,
		PairingInputs:            created.PairingInputs,
		DecisionEvidenceID:       created.DecisionEvidenceID,
		DecisionAlgorithmVersion: created.DecisionAlgorithmVersion,
		DecisionSeed:             created.DecisionSeed,
		DecisionResult:           created.DecisionResult,
		DecisionReplayDigest:     created.DecisionReplayDigest,
		DecisionOwnerID:          created.DecisionOwnerID,
		GeneratedAt:              created.GeneratedAt,
		UpdatedAt:                tstz(in.Meta.RecordedAt),
		ID:                       in.Meta.ID,
		ExpectedRevision:         expectedRevision,
	}
}

func manualRoundCreateParams(in ArenaManualSwissRoundInput) sqlc.CreateManualArenaSwissRoundParams {
	return sqlc.CreateManualArenaSwissRoundParams{
		ID:                    in.Meta.ID,
		RosterID:              in.Meta.RosterID,
		RoundNumber:           in.Meta.RoundNumber,
		SourceRosterRevision:  in.Meta.SourceRosterRevision,
		SourceHistoryRevision: in.Meta.SourceHistoryRevision,
		PairingInputs:         mustJSON(in.PairingInputs),
		GeneratedAt:           tstz(in.Meta.GeneratedAt),
		CreatedAt:             tstz(in.Meta.RecordedAt),
	}
}

func manualRoundUpdateParams(
	in ArenaManualSwissRoundInput,
	expectedRevision int64,
) sqlc.UpdateManualArenaSwissRoundCASParams {
	created := manualRoundCreateParams(in)
	return sqlc.UpdateManualArenaSwissRoundCASParams{
		SourceRosterRevision:  created.SourceRosterRevision,
		SourceHistoryRevision: created.SourceHistoryRevision,
		PairingInputs:         created.PairingInputs,
		GeneratedAt:           created.GeneratedAt,
		UpdatedAt:             tstz(in.Meta.RecordedAt),
		ID:                    in.Meta.ID,
		ExpectedRevision:      expectedRevision,
	}
}

func repeatOverrideParams(
	meta ArenaSwissRoundMeta,
	override arena.PairingRepeatOverride,
) (sqlc.CreateArenaSwissRepeatOverrideParams, error) {
	if err := override.Validate(); err != nil {
		return sqlc.CreateArenaSwissRepeatOverrideParams{}, err
	}
	return sqlc.CreateArenaSwissRepeatOverrideParams{
		ID:                          override.CommandID,
		RoundID:                     meta.ID,
		RosterID:                    meta.RosterID,
		ActorID:                     override.ActorID,
		Reason:                      override.Reason,
		ConfirmedAt:                 tstz(override.ConfirmedAt),
		RosterParticipantIds:        mustJSON(override.RosterParticipantIDs),
		ProposedPairings:            mustJSON(override.ProposedPairings),
		ByeParticipantID:            nullableUUIDValue(override.ByeParticipantID),
		PreviousMeetings:            mustJSON(override.PreviousMeetings),
		RepeatedPairings:            mustJSON(override.RepeatedPairings),
		AlternativeAlgorithmVersion: override.AlternativeSearch.AlgorithmVersion,
		AlternativeSearchComplete:   override.AlternativeSearch.Complete,
		AlternativePairings:         mustJSON(override.AlternativeSearch.Pairings),
		CreatedAt:                   tstz(meta.RecordedAt),
	}, nil
}

func byeParams(meta ArenaSwissRoundMeta, bye arena.SwissByeSelection) (sqlc.CreateArenaSwissByeParams, error) {
	if _, err := arena.ReplaySwissBye(bye); err != nil {
		return sqlc.CreateArenaSwissByeParams{}, err
	}
	return sqlc.CreateArenaSwissByeParams{
		RoundID:                  meta.ID,
		RosterID:                 meta.RosterID,
		ParticipantID:            bye.ParticipantID,
		PointsAwarded:            int16(arena.SwissByePoints),
		DecisionEvidenceID:       bye.Evidence.ID,
		DecisionAlgorithmVersion: bye.Evidence.AlgorithmVersion,
		DecisionInputs:           mustJSON(bye.Evidence.NormalizedInputs),
		DecisionSeed:             append([]byte(nil), bye.Evidence.Seed[:]...),
		DecisionResult:           mustJSON(bye.Evidence.Result),
		DecisionReplayDigest:     append([]byte(nil), bye.Evidence.ReplayDigest[:]...),
		DecisionOwnerID:          bye.Evidence.OwnerID,
		DecidedAt:                tstz(bye.Evidence.DecidedAt),
		CreatedAt:                tstz(meta.RecordedAt),
	}, nil
}

func arenaSwissRoundRecord(row sqlc.ArenaSwissRound) (*ArenaSwissRoundRecord, error) {
	var inputs []string
	if err := json.Unmarshal(row.PairingInputs, &inputs); err != nil || len(inputs) == 0 {
		return nil, domain.ErrValidation
	}
	out := &ArenaSwissRoundRecord{
		ID:                    row.ID,
		RosterID:              row.RosterID,
		RoundNumber:           int(row.RoundNumber),
		Revision:              row.Revision,
		SourceRosterRevision:  row.SourceRosterRevision,
		SourceHistoryRevision: row.SourceHistoryRevision,
		GenerationKind:        row.GenerationKind,
		PairingInputs:         inputs,
		GeneratedAt:           row.GeneratedAt.Time,
		LockRevision:          row.LockRevision,
		LockedAt:              nullableTime(row.LockedAt),
		CreatedAt:             row.CreatedAt.Time,
		UpdatedAt:             row.UpdatedAt.Time,
	}
	if row.GenerationKind == "automatic" {
		evidence, err := roundEvidenceFromRow(row)
		if err != nil {
			return nil, err
		}
		out.AutomaticEvidence = evidence
	}
	return out, nil
}

func roundEvidenceFromRow(row sqlc.ArenaSwissRound) (*domain.ArenaDecisionEvidence, error) {
	if !row.DecisionEvidenceID.Valid || row.DecisionAlgorithmVersion == nil || !row.DecisionOwnerID.Valid {
		return nil, domain.ErrValidation
	}
	evidence, err := decisionEvidence(
		row.DecisionEvidenceID.UUID,
		*row.DecisionAlgorithmVersion,
		row.PairingInputs,
		row.DecisionSeed,
		row.DecisionResult,
		row.DecisionReplayDigest,
		row.DecisionOwnerID.UUID,
		row.GeneratedAt.Time,
	)
	if err != nil {
		return nil, err
	}
	return &evidence, nil
}

func repeatOverrideFromRow(row sqlc.ArenaSwissRepeatOverride) (*arena.PairingRepeatOverride, error) {
	override := &arena.PairingRepeatOverride{
		CommandID:        row.ID,
		RoundID:          row.RoundID,
		ActorID:          row.ActorID,
		Reason:           row.Reason,
		ConfirmedAt:      row.ConfirmedAt.Time.UTC(),
		ByeParticipantID: row.ByeParticipantID.UUID,
		AlternativeSearch: arena.PairingAlternativeSearch{
			AlgorithmVersion: row.AlternativeAlgorithmVersion,
			Complete:         row.AlternativeSearchComplete,
		},
	}
	decodes := []struct {
		data []byte
		out  any
	}{
		{row.RosterParticipantIds, &override.RosterParticipantIDs},
		{row.ProposedPairings, &override.ProposedPairings},
		{row.PreviousMeetings, &override.PreviousMeetings},
		{row.RepeatedPairings, &override.RepeatedPairings},
		{row.AlternativePairings, &override.AlternativeSearch.Pairings},
	}
	for _, item := range decodes {
		if err := json.Unmarshal(item.data, item.out); err != nil {
			return nil, err
		}
	}
	if err := override.Validate(); err != nil {
		return nil, err
	}
	return override, nil
}

func byeFromRow(row sqlc.ArenaSwissBye) (*arena.SwissByeSelection, error) {
	evidence, err := decisionEvidence(
		row.DecisionEvidenceID,
		row.DecisionAlgorithmVersion,
		row.DecisionInputs,
		row.DecisionSeed,
		row.DecisionResult,
		row.DecisionReplayDigest,
		row.DecisionOwnerID,
		row.DecidedAt.Time,
	)
	if err != nil {
		return nil, err
	}
	selection := &arena.SwissByeSelection{
		ParticipantID: row.ParticipantID,
		PointsAwarded: int(row.PointsAwarded),
		Evidence:      evidence,
	}
	if _, err := arena.ReplaySwissBye(*selection); err != nil {
		return nil, err
	}
	return selection, nil
}

func decisionEvidence(
	id uuid.UUID,
	algorithm string,
	inputsJSON []byte,
	seed []byte,
	resultJSON []byte,
	digest []byte,
	ownerID uuid.UUID,
	decidedAt time.Time,
) (domain.ArenaDecisionEvidence, error) {
	if len(seed) != domain.ArenaDecisionSeedSize || len(digest) != domain.ArenaDecisionSeedSize {
		return domain.ArenaDecisionEvidence{}, domain.ErrValidation
	}
	var inputs, result []string
	if err := json.Unmarshal(inputsJSON, &inputs); err != nil {
		return domain.ArenaDecisionEvidence{}, err
	}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return domain.ArenaDecisionEvidence{}, err
	}
	evidence := domain.ArenaDecisionEvidence{
		ID:               id,
		Purpose:          domain.ArenaDecisionPurposePairing,
		AlgorithmVersion: algorithm,
		NormalizedInputs: inputs,
		Result:           result,
		OwnerID:          ownerID,
		DecidedAt:        decidedAt.UTC(),
	}
	copy(evidence.Seed[:], seed)
	copy(evidence.ReplayDigest[:], digest)
	if err := evidence.Validate(); err != nil {
		return domain.ArenaDecisionEvidence{}, err
	}
	return evidence, nil
}

func overridePreviousMeetings(override *arena.PairingRepeatOverride) []arena.SwissPair {
	if override == nil {
		return nil
	}
	return override.PreviousMeetings
}

func containsSwissPair(pairs []arena.SwissPair, target arena.SwissPair) bool {
	for _, pair := range pairs {
		if (pair.FirstParticipantID == target.FirstParticipantID &&
			pair.SecondParticipantID == target.SecondParticipantID) ||
			(pair.FirstParticipantID == target.SecondParticipantID &&
				pair.SecondParticipantID == target.FirstParticipantID) {
			return true
		}
	}
	return false
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func mustJSON(value any) []byte {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return []byte("[]")
	}
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal validated Arena persistence value: %v", err))
	}
	return data
}
