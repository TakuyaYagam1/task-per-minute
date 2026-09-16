package swiss

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func (r *SwissPostgres) checkedInParticipantIDs(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]uuid.UUID, error) {
	participants, err := r.tx.Querier(ctx).ListTournamentParticipants(ctx, rosterID)
	if err != nil {
		return nil, fmt.Errorf("list roster participants: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(participants))
	for _, participant := range participants {
		if domain.AttendanceState(participant.Attendance) == domain.AttendanceStateCheckedIn {
			ids = append(ids, participant.ID)
		}
	}
	return ids, nil
}

func (r *SwissPostgres) prepareRoundReplacement(
	ctx context.Context,
	meta SwissRoundMeta,
	expectedRevision int64,
) error {
	if expectedRevision < 1 {
		return domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	current, err := querier.LockSwissRoundForUpdate(ctx, meta.ID)
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision || current.LockedAt.Valid || current.RosterID != meta.RosterID ||
		current.RoundNumber != meta.RoundNumber {
		return errSwissRoundCAS
	}
	if meta.GeneratedAt.After(current.CreatedAt.Time) || meta.RecordedAt.Before(current.UpdatedAt.Time) {
		return domain.ErrValidation
	}
	if err := querier.DeleteSwissOpponentHistory(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete opponent history: %w", err)
	}
	if err := querier.DeleteSwissPairingMembers(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete pairing members: %w", err)
	}
	if err := querier.DeleteSwissPairings(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete pairings: %w", err)
	}
	if err := querier.DeleteSwissBye(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete bye: %w", err)
	}
	if err := querier.DeleteSwissRepeatOverride(ctx, meta.ID); err != nil {
		return fmt.Errorf("delete repeat override: %w", err)
	}
	return nil
}

func (r *SwissPostgres) insertRoundChildren(
	ctx context.Context,
	meta SwissRoundMeta,
	pairs []swissusecase.Pair,
	override *swissusecase.RepeatOverride,
) error {
	querier := r.tx.Querier(ctx)
	var overrideID uuid.NullUUID
	if override != nil {
		params, err := repeatOverrideParams(meta, *override)
		if err != nil {
			return err
		}
		if err := querier.CreateSwissRepeatOverride(ctx, params); err != nil {
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
		if err := querier.CreateSwissPairing(ctx, sqlc.CreateSwissPairingParams{
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
			if err := querier.CreateSwissPairingMember(ctx, sqlc.CreateSwissPairingMemberParams{
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
		if err := querier.CreateSwissOpponentHistory(ctx, sqlc.CreateSwissOpponentHistoryParams{
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
	if err := querier.CreateSwissBye(ctx, params); err != nil {
		return fmt.Errorf("insert bye: %w", err)
	}
	return nil
}

func (r *SwissPostgres) getRepeatOverride(
	ctx context.Context,
	roundID uuid.UUID,
) (*swissusecase.RepeatOverride, error) {
	row, err := r.tx.Querier(ctx).GetSwissRepeatOverride(ctx, roundID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("SwissPostgres - getRepeatOverride: %w", err)
	}
	override, err := repeatOverrideFromRow(row)
	if err != nil {
		return nil, fmt.Errorf("SwissPostgres - getRepeatOverride - decode: %w", err)
	}
	return override, nil
}

func (r *SwissPostgres) getBye(ctx context.Context, roundID uuid.UUID) (*swissusecase.ByeSelection, error) {
	row, err := r.tx.Querier(ctx).GetSwissBye(ctx, roundID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("SwissPostgres - getBye: %w", err)
	}
	bye, err := byeFromRow(row)
	if err != nil {
		return nil, fmt.Errorf("SwissPostgres - getBye - decode: %w", err)
	}
	return bye, nil
}
