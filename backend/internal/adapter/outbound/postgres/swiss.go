package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

var ErrSwissRoundNotFound = errors.New("swiss repository: round not found")

type SwissPostgres struct {
	tx *TxManager
}

type SwissRoundMeta struct {
	ID                    uuid.UUID
	RosterID              uuid.UUID
	RoundNumber           int16
	SourceRosterRevision  int64
	SourceHistoryRevision int64
	PairingIDs            []uuid.UUID
	PriorMeetingCounts    []int16
	Bye                   *swissusecase.ByeSelection
	GeneratedAt           time.Time
	RecordedAt            time.Time
	LockedAt              *time.Time
}

type AutomaticSwissRoundInput struct {
	Meta    SwissRoundMeta
	Pairing swissusecase.AutomaticPairing
}

type ManualSwissRoundInput struct {
	Meta          SwissRoundMeta
	Round         swissusecase.ManualRound
	PairingInputs []string
	Override      *swissusecase.RepeatOverride
}

type SwissRoundRecord struct {
	ID                    uuid.UUID
	RosterID              uuid.UUID
	RoundNumber           int
	Revision              int64
	SourceRosterRevision  int64
	SourceHistoryRevision int64
	GenerationKind        string
	PairingInputs         []string
	AutomaticEvidence     *domain.DecisionEvidence
	GeneratedAt           time.Time
	LockRevision          *int64
	LockedAt              *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type SwissPairingRecord struct {
	ID                 uuid.UUID
	RoundID            uuid.UUID
	RosterID           uuid.UUID
	SlotNumber         int
	Pair               swissusecase.Pair
	PriorMeetingCount  int
	RepeatOverrideID   *uuid.UUID
	OpponentRecordedAt time.Time
}

type SwissRoundAggregate struct {
	Round    SwissRoundRecord
	Pairings []SwissPairingRecord
	Bye      *swissusecase.ByeSelection
	Override *swissusecase.RepeatOverride
}

func NewSwissPostgres(tx *TxManager) *SwissPostgres {
	return &SwissPostgres{tx: tx}
}

func (r *SwissPostgres) SaveAutomaticRound(
	ctx context.Context,
	in AutomaticSwissRoundInput,
	expectedRevision *int64,
) (*SwissRoundRecord, error) {
	var saved sqlc.SwissRound
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
			params, paramErr := automaticRoundUpdateParams(in, *expectedRevision)
			if paramErr != nil {
				return paramErr
			}
			saved, err = querier.UpdateAutomaticSwissRoundCAS(
				txCtx,
				params,
			)
		} else {
			params, jsonErr := automaticRoundCreateParams(in)
			if jsonErr != nil {
				return jsonErr
			}
			saved, err = querier.CreateAutomaticSwissRound(txCtx, params)
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
			saved, err = querier.LockSwissRoundCAS(txCtx, sqlc.LockSwissRoundCASParams{
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
	record, err := swissRoundRecord(saved)
	if err != nil {
		return nil, fmt.Errorf("SwissPostgres - SaveAutomaticRound - map saved round: %w", err)
	}
	return record, nil
}

func (r *SwissPostgres) SaveManualRound(
	ctx context.Context,
	in ManualSwissRoundInput,
	expectedRevision *int64,
) (*SwissRoundRecord, error) {
	var saved sqlc.SwissRound
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
			params, paramErr := manualRoundUpdateParams(in, *expectedRevision)
			if paramErr != nil {
				return paramErr
			}
			saved, err = querier.UpdateManualSwissRoundCAS(
				txCtx,
				params,
			)
		} else {
			params, jsonErr := manualRoundCreateParams(in)
			if jsonErr != nil {
				return jsonErr
			}
			saved, err = querier.CreateManualSwissRound(txCtx, params)
		}
		if err != nil {
			return fmt.Errorf("write manual round: %w", err)
		}
		if err := r.insertRoundChildren(txCtx, in.Meta, in.Round.Pairings, in.Override); err != nil {
			return err
		}
		if in.Meta.LockedAt != nil {
			saved, err = querier.LockSwissRoundCAS(txCtx, sqlc.LockSwissRoundCASParams{
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
	record, err := swissRoundRecord(saved)
	if err != nil {
		return nil, fmt.Errorf("SwissPostgres - SaveManualRound - map saved round: %w", err)
	}
	return record, nil
}

func (r *SwissPostgres) LockRound(
	ctx context.Context,
	roundID uuid.UUID,
	expectedRevision int64,
	lockedAt time.Time,
) (*SwissRoundRecord, bool, error) {
	if roundID == uuid.Nil || expectedRevision < 1 || !validServerTime(lockedAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).LockSwissRoundCAS(ctx, sqlc.LockSwissRoundCASParams{
		LockedAt:         tstz(lockedAt),
		ID:               roundID,
		ExpectedRevision: expectedRevision,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("SwissPostgres - LockRound - Querier.LockSwissRoundCAS: %w", err)
	}
	record, err := swissRoundRecord(row)
	if err != nil {
		return nil, false, fmt.Errorf("SwissPostgres - LockRound - map round: %w", err)
	}
	return record, true, nil
}

func (r *SwissPostgres) Get(ctx context.Context, roundID uuid.UUID) (*SwissRoundAggregate, error) {
	row, err := r.tx.Querier(ctx).GetSwissRound(ctx, roundID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSwissRoundNotFound
		}
		return nil, fmt.Errorf("SwissPostgres - Get - Querier.GetSwissRound: %w", err)
	}
	round, err := swissRoundRecord(row)
	if err != nil {
		return nil, fmt.Errorf("SwissPostgres - Get - map round: %w", err)
	}

	history, err := r.ListOpponentHistory(ctx, row.RosterID)
	if err != nil {
		return nil, err
	}
	pairings := make([]SwissPairingRecord, 0)
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
	aggregate := &SwissRoundAggregate{Round: *round, Pairings: pairings, Bye: bye, Override: override}
	if err := r.validateLoadedAggregate(ctx, aggregate); err != nil {
		return nil, fmt.Errorf("SwissPostgres - Get - validate aggregate: %w", err)
	}
	return aggregate, nil
}

func (r *SwissPostgres) ListRounds(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]SwissRoundRecord, error) {
	rows, err := r.tx.Querier(ctx).ListSwissRounds(ctx, rosterID)
	if err != nil {
		return nil, fmt.Errorf("SwissPostgres - ListRounds - Querier.ListSwissRounds: %w", err)
	}
	out := make([]SwissRoundRecord, 0, len(rows))
	for _, row := range rows {
		record, mapErr := swissRoundRecord(row)
		if mapErr != nil {
			return nil, fmt.Errorf("SwissPostgres - ListRounds - map round: %w", mapErr)
		}
		out = append(out, *record)
	}
	return out, nil
}

func (r *SwissPostgres) ListOpponentHistory(
	ctx context.Context,
	rosterID uuid.UUID,
) ([]SwissPairingRecord, error) {
	rows, err := r.tx.Querier(ctx).ListSwissOpponentHistory(ctx, rosterID)
	if err != nil {
		return nil, fmt.Errorf(
			"SwissPostgres - ListOpponentHistory - Querier.ListSwissOpponentHistory: %w",
			err,
		)
	}
	out := make([]SwissPairingRecord, 0, len(rows))
	for _, row := range rows {
		var overrideID *uuid.UUID
		if row.RepeatOverrideID.Valid {
			value := row.RepeatOverrideID.UUID
			overrideID = &value
		}
		out = append(out, SwissPairingRecord{
			ID:         row.PairingID,
			RoundID:    row.RoundID,
			RosterID:   row.RosterID,
			SlotNumber: int(row.SlotNumber),
			Pair: swissusecase.Pair{
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
