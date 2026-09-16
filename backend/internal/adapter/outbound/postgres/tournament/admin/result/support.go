package result

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type ResultScope = resultrepo.ResultScope
type ResultSettlementIDs = resultrepo.ResultSettlementIDs
type ResultSettlementInput = resultrepo.ResultSettlementInput
type ResultCommitRecord = resultrepo.ResultCommitRecord
type ResultPostgres = resultrepo.ResultPostgres
type TxManager = db.TxManager

type resultProjectionTargetBinding = resultrepo.ResultProjectionTargetBinding

type scoreRevisionAttemptEvidence struct {
	SlotID               uuid.UUID
	SlotPosition         int16
	GameAttemptID        uuid.UUID
	AttemptNumber        int32
	GameResultRevisionID uuid.UUID
	ResultEventID        uuid.UUID
	ResultState          string
	ResultReason         string
	WinnerID             uuid.NullUUID
	OccurredAt           pgtype.Timestamptz
	CreatedAt            pgtype.Timestamptz
}

// PreStartSwissProofEnsurer is injected by the root facade because the Swiss
// proof workflow still belongs to the parent adapter during this extraction.
type PreStartSwissProofEnsurer func(
	context.Context,
	*db.TxManager,
	uuid.UUID,
	uuid.UUID,
	time.Time,
	string,
	uuid.UUID,
) error

type swissRoundProofOrigin struct {
	mode      string
	commandID uuid.UUID
}

func (r *TournamentAdminResultPostgres) ensurePreStartSwissRoundProof(
	ctx context.Context,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
	at time.Time,
	origin swissRoundProofOrigin,
) error {
	if r == nil || r.preStartProofEnsurer == nil {
		return domain.ErrValidation
	}
	return r.preStartProofEnsurer(ctx, r.tx, tournamentID, seriesID, at, origin.mode, origin.commandID)
}

const (
	resultActorOperator = "operator"
	resultOutboxTopic   = "tournament.result.committed"
)

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func optionalTrimmedString(value string) *string {
	if value == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
}

func marshalJSON(operation string, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return []byte("[]"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s - marshal JSON: %w", operation, err)
	}
	return data, nil
}

func lockTournamentResultScope(ctx context.Context, q *sqlc.Queries, tournamentID, rosterID uuid.UUID) error {
	_, err := q.LockTournamentResultScope(ctx, sqlc.LockTournamentResultScopeParams{
		TournamentID: tournamentID,
		RosterID:     uuid.NullUUID{UUID: rosterID, Valid: rosterID != uuid.Nil},
	})
	return err
}

func resultProjectionTarget(
	source sqlc.LockResultSourceProjectionRow,
	targetID uuid.UUID,
) (resultProjectionTargetBinding, bool) {
	target, ok := resultrepo.ResultProjectionTarget(source, targetID)
	if !ok {
		return resultProjectionTargetBinding{}, false
	}
	return resultProjectionTargetBinding{ID: target.ID, Revision: target.Revision}, true
}

func mapRepositoryWriteError(operation string, err error) error {
	return resultrepo.MapRepositoryWriteError(operation, err)
}

func resultCASWriteError(operation string, err error) error {
	return resultrepo.ResultCASWriteError(operation, err)
}

func createScoreRevisionAttempt(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	scoreRevisionID uuid.UUID,
	position int16,
	evidence scoreRevisionAttemptEvidence,
) error {
	return resultrepo.CreateScoreRevisionAttempt(ctx, querier, scope, scoreRevisionID, position, resultrepo.ScoreRevisionAttemptEvidence{
		SlotID:               evidence.SlotID,
		SlotPosition:         evidence.SlotPosition,
		GameAttemptID:        evidence.GameAttemptID,
		AttemptNumber:        evidence.AttemptNumber,
		GameResultRevisionID: evidence.GameResultRevisionID,
		ResultEventID:        evidence.ResultEventID,
		ResultState:          evidence.ResultState,
		ResultReason:         evidence.ResultReason,
		WinnerID:             evidence.WinnerID,
		OccurredAt:           evidence.OccurredAt,
		CreatedAt:            evidence.CreatedAt,
	})
}

func normalNoShowScoreEvidence(
	graph []sqlc.ListRecoverySeriesGraphRow,
	seriesID uuid.UUID,
	revisions []domain.NormalNoShowGameRevision,
	resultEventID uuid.UUID,
	resolvedAt pgtype.Timestamptz,
) ([]scoreRevisionAttemptEvidence, error) {
	evidence, err := resultrepo.NormalNoShowScoreEvidence(graph, seriesID, revisions, resultEventID, resolvedAt)
	if err != nil {
		return nil, err
	}
	result := make([]scoreRevisionAttemptEvidence, len(evidence))
	for index, item := range evidence {
		result[index] = scoreRevisionAttemptEvidence{
			SlotID:               item.SlotID,
			SlotPosition:         item.SlotPosition,
			GameAttemptID:        item.GameAttemptID,
			AttemptNumber:        item.AttemptNumber,
			GameResultRevisionID: item.GameResultRevisionID,
			ResultEventID:        item.ResultEventID,
			ResultState:          item.ResultState,
			ResultReason:         item.ResultReason,
			WinnerID:             item.WinnerID,
			OccurredAt:           item.OccurredAt,
			CreatedAt:            item.CreatedAt,
		}
	}
	return result, nil
}

var errRecoveryTerminalSnapshot = errors.New("invalid recovery terminal snapshot")

func recoverySeries(row sqlc.Series, graph []sqlc.ListRecoverySeriesGraphRow) (domain.Series, error) {
	series := domain.Series{
		ID: row.ID, TournamentID: row.TournamentID,
		FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
		Format: domain.SeriesFormat(row.Format), State: domain.SeriesState(row.State),
		Score: domain.SeriesScore{
			FirstParticipantWins:  int(row.FirstParticipantWins),
			SecondParticipantWins: int(row.SecondParticipantWins),
		},
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		series.WinnerID = &winnerID
	}
	if row.CurrentScoreRevisionID.Valid {
		revisionID := domain.SeriesScoreRevisionID(row.CurrentScoreRevisionID.UUID)
		series.CurrentScoreRevisionID = &revisionID
	}
	if row.CurrentResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(row.CurrentResultRevisionID.UUID)
		series.CurrentResultRevisionID = &revisionID
	}
	slots, err := recoverySeriesSlots(row, graph)
	if err != nil {
		return domain.Series{}, err
	}
	series.Slots = slots
	if err := series.Validate(); err != nil {
		return domain.Series{}, fmt.Errorf("%w: Series: %w", errRecoveryTerminalSnapshot, err)
	}
	return series, nil
}

func recoverySeriesSlots(row sqlc.Series, graph []sqlc.ListRecoverySeriesGraphRow) ([]domain.GameSlot, error) {
	if len(graph) == 0 {
		return nil, fmt.Errorf("%w: Series graph is empty", errRecoveryTerminalSnapshot)
	}
	slots := make([]domain.GameSlot, 0, len(graph))
	for index := 0; index < len(graph); {
		slotRow := graph[index].GameSlot
		if slotRow.SeriesID != row.ID || slotRow.RosterID != row.RosterID {
			return nil, fmt.Errorf("%w: foreign Game slot", errRecoveryTerminalSnapshot)
		}
		slot := domain.GameSlot{
			ID: slotRow.ID, SeriesID: slotRow.SeriesID, Position: int(slotRow.SlotNumber),
			Category: domain.Category(slotRow.Category),
			ScoreBefore: domain.SeriesScore{
				FirstParticipantWins:  int(slotRow.FirstParticipantWinsBefore),
				SecondParticipantWins: int(slotRow.SecondParticipantWinsBefore),
			},
		}
		for index < len(graph) && graph[index].GameSlot.ID == slotRow.ID {
			attemptRow := graph[index].GameAttempt
			if attemptRow.SeriesID != row.ID || attemptRow.RosterID != row.RosterID || attemptRow.SlotID != slotRow.ID {
				return nil, fmt.Errorf("%w: foreign Game attempt", errRecoveryTerminalSnapshot)
			}
			game, err := recoveryGame(attemptRow)
			if err != nil {
				return nil, err
			}
			slot.Attempts = append(slot.Attempts, game)
			index++
		}
		if err := slot.Validate(); err != nil {
			return nil, fmt.Errorf("%w: Game slot: %w", errRecoveryTerminalSnapshot, err)
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

func recoveryGame(row sqlc.GameAttempt) (domain.Game, error) {
	game := domain.Game{
		ID: row.ID, SlotID: row.SlotID, AttemptNo: int(row.AttemptNumber),
		State: domain.GameState(row.State), ResultReason: domain.GameResultReason(stringValue(row.ResultReason)),
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		game.WinnerID = &winnerID
	}
	if row.ResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(row.ResultRevisionID.UUID)
		game.ResultRevisionID = &revisionID
	}
	if err := game.Validate(); err != nil {
		return domain.Game{}, fmt.Errorf("%w: Game: %w", errRecoveryTerminalSnapshot, err)
	}
	return game, nil
}

func recoveryCurrentOrdinal(scoreHead sqlc.SeriesScoreHead) (int, error) {
	if scoreHead.Revision < 1 || scoreHead.Revision > int64((math.MaxInt-1)/2)+1 {
		return 0, fmt.Errorf("%w: score revision overflow", errRecoveryTerminalSnapshot)
	}
	return int(scoreHead.Revision*2 - 1), nil
}

func recoveryResultRevisionIDs(rows []uuid.UUID) ([]domain.OfficialResultRevisionID, error) {
	result := make([]domain.OfficialResultRevisionID, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for index, id := range rows {
		if id == uuid.Nil {
			return nil, fmt.Errorf("%w: empty Game result revision", errRecoveryTerminalSnapshot)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("%w: duplicate Game result revision", errRecoveryTerminalSnapshot)
		}
		seen[id] = struct{}{}
		result[index] = domain.OfficialResultRevisionID(id)
	}
	return result, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
