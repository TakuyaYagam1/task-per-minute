package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
)

const tournamentCreateReceiptSchemaVersion int16 = 1

var errInvalidTournamentCreateReceipt = errors.New("invalid tournament create receipt")

type TournamentCreatePostgres struct {
	tournaments *TournamentPostgres
}

type tournamentCreateReceiptDocument struct {
	SchemaVersion int16     `json:"schema_version"`
	TournamentID  uuid.UUID `json:"tournament_id"`
	RosterID      uuid.UUID `json:"roster_id"`
	Preset        string    `json:"preset"`
	State         string    `json:"state"`
	Revision      int64     `json:"revision"`
	RosterSize    int       `json:"roster_size"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Changed       bool      `json:"changed"`
}

func NewTournamentCreatePostgres(tournaments *TournamentPostgres) *TournamentCreatePostgres {
	return &TournamentCreatePostgres{tournaments: tournaments}
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *TournamentCreatePostgres) Create(
	ctx context.Context,
	command catalogusecase.CreateReceiptCommand,
) (usecase.TournamentResult, error) {
	if r == nil || r.tournaments == nil || r.tournaments.tx == nil || !validTournamentCreateReceiptCommand(command) {
		return usecase.TournamentResult{}, domain.ErrValidation
	}

	contentConfigurationID := tournamentV1ContentID(command.TournamentID, command.RosterID, "configuration")
	var result usecase.TournamentResult
	err := r.tournaments.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tournaments.tx.Querier(txCtx)
		locked, err := querier.LockTournamentCreateCommand(txCtx, command.IdempotencyKey.String())
		if err != nil {
			return fmt.Errorf("TournamentCreatePostgres - Create - lock command: %w", err)
		}
		if locked != 1 {
			return fmt.Errorf("TournamentCreatePostgres - Create - lock command: %w", errInvalidTournamentCreateReceipt)
		}

		receipt, err := querier.GetTournamentCreateReceipt(txCtx, command.IdempotencyKey)
		if err == nil {
			result, err = tournamentCreateReceiptResult(receipt, command, contentConfigurationID)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("TournamentCreatePostgres - Create - get receipt: %w", err)
		}

		created, roster, err := r.tournaments.Create(
			txCtx,
			command.TournamentID,
			command.RosterID,
			command.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("TournamentCreatePostgres - Create - tournament: %w", err)
		}
		if created == nil || roster == nil || created.ID != command.TournamentID || roster.ID != command.RosterID ||
			roster.TournamentID != command.TournamentID || created.Preset != string(domain.TournamentPresetV1) ||
			created.State != domain.TournamentStateDraft || created.Revision != 1 ||
			!created.CreatedAt.Equal(created.UpdatedAt) {
			return errInvalidTournamentCreateReceipt
		}

		receipt, err = querier.InsertTournamentCreateReceipt(txCtx, sqlc.InsertTournamentCreateReceiptParams{
			CommandID:              command.IdempotencyKey,
			ActorID:                command.ActorID,
			RequestDigest:          command.PayloadDigest[:],
			TournamentID:           command.TournamentID,
			RosterID:               command.RosterID,
			ContentConfigurationID: contentConfigurationID,
			ResultSchemaVersion:    tournamentCreateReceiptSchemaVersion,
			ResultPreset:           string(domain.TournamentPresetV1),
			ResultState:            string(domain.TournamentStateDraft),
			ResultRevision:         1,
			ResultRosterSize:       0,
			ResultCreatedAt:        tstz(created.CreatedAt.UTC()),
			ResultUpdatedAt:        tstz(created.UpdatedAt.UTC()),
			ResultChanged:          true,
			CreatedAt:              tstz(created.CreatedAt.UTC()),
		})
		if err != nil {
			return fmt.Errorf("TournamentCreatePostgres - Create - insert receipt: %w", err)
		}
		result, err = tournamentCreateReceiptResult(receipt, command, contentConfigurationID)
		return err
	})
	if err != nil {
		if errors.Is(err, idempotency.ErrPayloadConflict) || errors.Is(err, domain.ErrValidation) ||
			errors.Is(err, domain.ErrInvalidContentConfiguration) || errors.Is(err, domain.ErrInternal) {
			return usecase.TournamentResult{}, err
		}
		return usecase.TournamentResult{}, fmt.Errorf("TournamentCreatePostgres - Create: %w", err)
	}
	return result, nil
}

func validTournamentCreateReceiptCommand(command catalogusecase.CreateReceiptCommand) bool {
	return command.ActorID != uuid.Nil && command.IdempotencyKey != uuid.Nil &&
		command.TournamentID != uuid.Nil && command.RosterID != uuid.Nil &&
		command.TournamentID != command.RosterID && command.PayloadDigest != [32]byte{} &&
		validServerTime(command.CreatedAt)
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func tournamentCreateReceiptResult(
	receipt sqlc.TournamentCreateCommandReceipt,
	command catalogusecase.CreateReceiptCommand,
	contentConfigurationID uuid.UUID,
) (usecase.TournamentResult, error) {
	if receipt.CommandID != command.IdempotencyKey || receipt.ActorID != command.ActorID ||
		receipt.TournamentID != command.TournamentID || receipt.RosterID != command.RosterID ||
		receipt.ContentConfigurationID != contentConfigurationID {
		return usecase.TournamentResult{}, idempotency.ErrPayloadConflict
	}
	if !bytes.Equal(receipt.RequestDigest, command.PayloadDigest[:]) {
		return usecase.TournamentResult{}, idempotency.ErrPayloadConflict
	}
	createdAt, err := tournamentCreateReceiptTime(receipt.ResultCreatedAt)
	if err != nil {
		return usecase.TournamentResult{}, err
	}
	updatedAt, err := tournamentCreateReceiptTime(receipt.ResultUpdatedAt)
	if err != nil {
		return usecase.TournamentResult{}, err
	}
	recordedAt, err := tournamentCreateReceiptTime(receipt.CreatedAt)
	if err != nil {
		return usecase.TournamentResult{}, err
	}
	if receipt.ResultSchemaVersion != tournamentCreateReceiptSchemaVersion ||
		receipt.ResultPreset != string(domain.TournamentPresetV1) ||
		receipt.ResultState != string(domain.TournamentStateDraft) || receipt.ResultRevision != 1 ||
		receipt.ResultRosterSize != 0 || !receipt.ResultChanged || !createdAt.Equal(updatedAt) ||
		createdAt.After(recordedAt) {
		return usecase.TournamentResult{}, errInvalidTournamentCreateReceipt
	}
	document, err := decodeTournamentCreateReceiptDocument(receipt.ResultDocument)
	if err != nil {
		return usecase.TournamentResult{}, err
	}
	if document.SchemaVersion != tournamentCreateReceiptSchemaVersion ||
		document.TournamentID != receipt.TournamentID || document.RosterID != receipt.RosterID ||
		document.Preset != receipt.ResultPreset || document.State != receipt.ResultState ||
		document.Revision != receipt.ResultRevision || document.RosterSize != int(receipt.ResultRosterSize) ||
		!document.CreatedAt.Equal(createdAt) || !document.UpdatedAt.Equal(updatedAt) ||
		!document.Changed {
		return usecase.TournamentResult{}, errInvalidTournamentCreateReceipt
	}
	return usecase.TournamentResult{
		Tournament: usecase.TournamentView{
			ID: receipt.TournamentID, RosterID: receipt.RosterID, Preset: domain.TournamentPreset(receipt.ResultPreset),
			State: domain.TournamentState(receipt.ResultState), Revision: receipt.ResultRevision,
			RosterSize: int(receipt.ResultRosterSize), CreatedAt: createdAt, UpdatedAt: updatedAt,
		},
		Changed: receipt.ResultChanged,
	}, nil
}

func tournamentCreateReceiptTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, errInvalidTournamentCreateReceipt
	}
	result := value.Time.UTC()
	if !validServerTime(result) {
		return time.Time{}, errInvalidTournamentCreateReceipt
	}
	return result, nil
}

func decodeTournamentCreateReceiptDocument(encoded []byte) (tournamentCreateReceiptDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()

	var document tournamentCreateReceiptDocument
	if err := decoder.Decode(&document); err != nil {
		return tournamentCreateReceiptDocument{}, fmt.Errorf("%w: decode document: %w", errInvalidTournamentCreateReceipt, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return tournamentCreateReceiptDocument{}, fmt.Errorf("%w: trailing document data", errInvalidTournamentCreateReceipt)
	}
	return document, nil
}

var _ catalogusecase.TournamentCreateStore = (*TournamentCreatePostgres)(nil)
