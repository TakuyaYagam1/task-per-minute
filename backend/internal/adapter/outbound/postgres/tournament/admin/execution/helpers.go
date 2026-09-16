package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	progression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

var ErrWaveNotFound = errors.New("wave repository: wave not found")

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func nullableTSTZ(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func utcNullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
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

func categoryJSON(categories []domain.Category) ([]byte, error) {
	values := make([]string, len(categories))
	for index, category := range categories {
		values[index] = string(category)
	}
	return marshalJSON("category sequence", values)
}

func lockTournamentResultScope(ctx context.Context, q *sqlc.Queries, tournamentID, rosterID uuid.UUID) error {
	_, err := q.LockTournamentResultScope(ctx, sqlc.LockTournamentResultScopeParams{
		TournamentID: tournamentID,
		RosterID:     uuid.NullUUID{UUID: rosterID, Valid: rosterID != uuid.Nil},
	})
	return err
}

func mapRepositoryWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func loadTournamentPreflightContent(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (tournamentPreflightContent, error) {
	loaded, err := rosterrepo.LoadPreflightContent(ctx, querier, tournamentID)
	if err != nil {
		return tournamentPreflightContent{}, err
	}
	return tournamentPreflightContent{configuration: loaded.Configuration}, nil
}

type tournamentPreflightContent struct {
	configuration domain.ContentConfiguration
}

type tournamentAdminPauseDocument struct {
	Version int                            `json:"version"`
	View    json.RawMessage                `json:"view"`
	Pause   *gameusecase.NormalPauseRecord `json:"normal_pause,omitempty"`
}

const tournamentAdminPauseDocumentVersion = 1

func encodeTournamentAdminWaveResult(record tournamentadmin.WaveCommandRecord) ([]byte, error) {
	if record.NormalPause == nil {
		return append([]byte(nil), record.ResultDocument...), nil
	}
	document, err := json.Marshal(tournamentAdminPauseDocument{
		Version: tournamentAdminPauseDocumentVersion,
		View:    append(json.RawMessage(nil), record.ResultDocument...),
		Pause:   record.NormalPause,
	})
	if err != nil {
		return nil, fmt.Errorf("encode normal Wave pause evidence: %w", err)
	}
	return document, nil
}

func decodeTournamentAdminWaveResult(action string, document []byte) ([]byte, *gameusecase.NormalPauseRecord) {
	if action != "pause" {
		return append([]byte(nil), document...), nil
	}
	var envelope tournamentAdminPauseDocument
	if err := json.Unmarshal(document, &envelope); err != nil || envelope.Version != tournamentAdminPauseDocumentVersion ||
		len(envelope.View) == 0 || envelope.Pause == nil {
		return append([]byte(nil), document...), nil
	}
	pause := *envelope.Pause
	return append([]byte(nil), envelope.View...), &pause
}

func loadRetainedSwissRoundProof(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID, rosterID uuid.UUID,
	source waveStartSwissRoundProof,
) (*swissusecase.RoundLockProof, error) {
	root, err := q.GetSwissRoundProofForUpdate(ctx, sqlc.GetSwissRoundProofForUpdateParams{
		RoundID: source.roundID, TournamentID: tournamentID, RosterID: rosterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	members, err := q.ListSwissRoundProofMembers(ctx, sqlc.ListSwissRoundProofMembersParams{
		RoundID: source.roundID, RosterID: rosterID,
	})
	if err != nil {
		return nil, err
	}
	series, err := q.ListSwissRoundProofSeries(ctx, sqlc.ListSwissRoundProofSeriesParams{
		RoundID: source.roundID, RosterID: rosterID,
	})
	if err != nil {
		return nil, err
	}
	proofs, err := progressionReceiptRoundProofs(
		progression.Authority{Tournament: inbound.TournamentView{ID: tournamentID, RosterID: rosterID, Preset: source.preset}},
		[]sqlc.SwissRoundLockProof{root}, members, series,
	)
	if err != nil {
		return nil, err
	}
	proof := proofs[source.roundID]
	return &proof, nil
}

func progressionReceiptRoundProofs(
	authority progression.Authority,
	roots []sqlc.SwissRoundLockProof,
	members []sqlc.SwissRoundLockProofMember,
	series []sqlc.SwissRoundLockProofSeries,
) (map[uuid.UUID]swissusecase.RoundLockProof, error) {
	if len(roots) == 0 {
		return nil, domain.ErrConflict
	}
	membersByRound := make(map[uuid.UUID][]uuid.UUID, len(roots))
	for _, member := range members {
		if member.RoundID == uuid.Nil || member.RosterID != authority.Tournament.RosterID ||
			member.ParticipantID == uuid.Nil || !member.CreatedAt.Valid {
			return nil, domain.ErrConflict
		}
		membersByRound[member.RoundID] = append(membersByRound[member.RoundID], member.ParticipantID)
	}
	seriesByRound := make(map[uuid.UUID][]swissusecase.LockedSeries, len(roots))
	for _, row := range series {
		if row.RoundID == uuid.Nil || row.RosterID != authority.Tournament.RosterID || row.SeriesID == uuid.Nil ||
			row.PairingID == uuid.Nil || row.FirstParticipantID == uuid.Nil || row.SecondParticipantID == uuid.Nil ||
			row.FirstParticipantID == row.SecondParticipantID || row.CategoryRevisionID == uuid.Nil ||
			row.CategoryRevision < 1 || row.AssignmentID == uuid.Nil || row.AssignmentRevision < 1 ||
			row.AssignmentPlanID == uuid.Nil || row.AssignmentPlanRevisionID == uuid.Nil || row.ReservationID == uuid.Nil ||
			row.ReservationRevision < 1 || !row.CreatedAt.Valid {
			return nil, domain.ErrConflict
		}
		seriesByRound[row.RoundID] = append(seriesByRound[row.RoundID], swissusecase.LockedSeries{
			SeriesID: row.SeriesID, PairingID: row.PairingID,
			FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
			CategoryRevisionID: row.CategoryRevisionID, CategoryRevision: row.CategoryRevision,
			AssignmentID: row.AssignmentID, AssignmentRevision: row.AssignmentRevision,
			AssignmentPlanID: row.AssignmentPlanID, AssignmentPlanRevisionID: row.AssignmentPlanRevisionID,
			ReservationID: row.ReservationID, ReservationRevision: row.ReservationRevision,
		})
	}
	proofs := make(map[uuid.UUID]swissusecase.RoundLockProof, len(roots))
	for _, root := range roots {
		if root.RoundID == uuid.Nil || root.TournamentID != authority.Tournament.ID ||
			root.RosterID != authority.Tournament.RosterID || root.Preset != string(authority.Tournament.Preset) ||
			root.RoundNumber < 1 || root.SourceProjectionRevisionID == uuid.Nil || root.PreflightRevisionID == uuid.Nil ||
			root.NormalPoolRevisionID == uuid.Nil || root.WaveID == uuid.Nil || root.WaveRevisionID == uuid.Nil ||
			root.RoundRevision < 1 || root.SourceProjectionRevision < 1 || root.RosterRevision < 1 ||
			root.HistoryRevision < 0 || root.NormalPoolRevision < 1 || root.WaveRevision < 1 ||
			!root.LockedAt.Valid || !root.CreatedAt.Valid || len(root.ProofHash) != sha256.Size {
			return nil, domain.ErrConflict
		}
		bye := uuid.Nil
		if root.ByeParticipantID.Valid {
			bye = root.ByeParticipantID.UUID
		}
		proof, err := swissusecase.NewRoundLockProof(swissusecase.RoundLockProofInput{
			TournamentID: root.TournamentID, RosterID: root.RosterID, RoundID: root.RoundID,
			Preset: domain.TournamentPreset(root.Preset), RoundNumber: int(root.RoundNumber),
			SourceProjectionRevisionID: root.SourceProjectionRevisionID, PreflightRevisionID: root.PreflightRevisionID,
			NormalPoolRevisionID: root.NormalPoolRevisionID, WaveID: root.WaveID,
			WaveRevisionID: domain.WaveRevisionID(root.WaveRevisionID),
			Revisions: swissusecase.RoundLockRevisions{
				Round: root.RoundRevision, SourceProjection: root.SourceProjectionRevision,
				Roster: root.RosterRevision, History: root.HistoryRevision,
				NormalPool: root.NormalPoolRevision, Wave: root.WaveRevision,
			},
			RosterParticipantIDs: membersByRound[root.RoundID], Series: seriesByRound[root.RoundID], ByeParticipantID: bye,
		})
		if err != nil || proof.ProofHash != hex.EncodeToString(root.ProofHash) {
			return nil, domain.ErrConflict
		}
		if _, duplicate := proofs[root.RoundID]; duplicate {
			return nil, domain.ErrConflict
		}
		proofs[root.RoundID] = proof
	}
	return proofs, nil
}
