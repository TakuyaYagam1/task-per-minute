package postgres

import (
	"crypto/sha256"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type progressionReceiptSourceKey struct {
	receiptProjectionRevisionID uuid.UUID
	resultKind                  domain.ArtifactKind
	entityID                    uuid.UUID
	resultRevisionID            uuid.UUID
}

type progressionReceiptSourceProjection struct {
	physicalProjectionRevisionID uuid.UUID
	physicalProjectionRevision   int64
	previousProjectionRevisionID *uuid.UUID
	state                        string
	artifacts                    map[domain.ArtifactKind]progressionPhysicalArtifact
}

type progressionPhysicalArtifact struct {
	id      uuid.UUID
	payload []byte
	digest  [sha256.Size]byte
}

// indexProgressionReceiptSources keeps physical publication proof distinct
// from logical result nodes. A superseded physical source remains valid only
// because the receipt names it exactly; this mapper never replaces it with a
// newer current projection.
//
//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func indexProgressionReceiptSources(
	rows []sqlc.LockTournamentProgressionFinalSwissReceiptSourceProjectionsRow,
) (map[progressionReceiptSourceKey]progressionReceiptSourceProjection, error) {
	if len(rows) == 0 {
		return nil, domain.ErrConflict
	}
	indexed := make(map[progressionReceiptSourceKey]progressionReceiptSourceProjection)
	for _, row := range rows {
		kind, ok := progressionResultArtifactKind(row.ResultKind)
		if !ok || row.ReceiptProjectionRevisionID == uuid.Nil || row.EntityID == uuid.Nil ||
			row.ResultRevisionID == uuid.Nil || row.SourceProjectionRevisionID == uuid.Nil ||
			row.SourceProjectionRevision < 1 || row.PhysicalProjectionRevisionID == uuid.Nil ||
			row.PhysicalProjectionRevision < 1 || row.PhysicalProjectionRevisionID != row.SourceProjectionRevisionID ||
			row.PhysicalProjectionRevision != row.SourceProjectionRevision ||
			(row.PhysicalProjectionState != "published" && row.PhysicalProjectionState != "superseded") ||
			len(row.Payload) == 0 || !row.PhysicalProjectionCreatedAt.Valid ||
			!domain.IsValidServerTime(row.PhysicalProjectionCreatedAt.Time.UTC()) ||
			!row.ArtifactCreatedAt.Valid || !domain.IsValidServerTime(row.ArtifactCreatedAt.Time.UTC()) {
			return nil, domain.ErrConflict
		}
		artifactKind, ok := progressionArtifactKind(row.ArtifactKind)
		if !ok || row.ArtifactID == uuid.Nil || sha256.Sum256(row.Payload) != progressionDigestMust(row.PayloadDigest) {
			return nil, domain.ErrConflict
		}
		key := progressionReceiptSourceKey{
			receiptProjectionRevisionID: row.ReceiptProjectionRevisionID,
			resultKind:                  kind,
			entityID:                    row.EntityID,
			resultRevisionID:            row.ResultRevisionID,
		}
		item, exists := indexed[key]
		if !exists {
			var previous *uuid.UUID
			if row.PhysicalPreviousProjectionRevisionID.Valid {
				value := row.PhysicalPreviousProjectionRevisionID.UUID
				if value == uuid.Nil || value == row.PhysicalProjectionRevisionID {
					return nil, domain.ErrConflict
				}
				previous = &value
			}
			item = progressionReceiptSourceProjection{
				physicalProjectionRevisionID: row.PhysicalProjectionRevisionID,
				physicalProjectionRevision:   row.PhysicalProjectionRevision,
				previousProjectionRevisionID: previous,
				state:                        row.PhysicalProjectionState,
				artifacts:                    make(map[domain.ArtifactKind]progressionPhysicalArtifact),
			}
		} else if item.physicalProjectionRevisionID != row.PhysicalProjectionRevisionID ||
			item.physicalProjectionRevision != row.PhysicalProjectionRevision ||
			item.state != row.PhysicalProjectionState ||
			!progressionUUIDPointersEqual(item.previousProjectionRevisionID, row.PhysicalPreviousProjectionRevisionID) {
			return nil, domain.ErrConflict
		}
		if _, duplicate := item.artifacts[artifactKind]; duplicate {
			return nil, domain.ErrConflict
		}
		item.artifacts[artifactKind] = progressionPhysicalArtifact{
			id: row.ArtifactID, payload: append([]byte(nil), row.Payload...),
			digest: progressionDigestMust(row.PayloadDigest),
		}
		indexed[key] = item
	}
	for _, item := range indexed {
		if len(item.artifacts) == 0 {
			return nil, domain.ErrConflict
		}
	}
	return indexed, nil
}

func progressionResultArtifactKind(value string) (domain.ArtifactKind, bool) {
	switch value {
	case string(domain.ArtifactKindGameResult):
		return domain.ArtifactKindGameResult, true
	case string(domain.ArtifactKindSeriesScore):
		return domain.ArtifactKindSeriesScore, true
	case string(domain.ArtifactKindSeriesResult):
		return domain.ArtifactKindSeriesResult, true
	default:
		return "", false
	}
}

func progressionArtifactKind(value string) (domain.ArtifactKind, bool) {
	switch value {
	case string(domain.ArtifactKindStandings):
		return domain.ArtifactKindStandings, true
	case string(domain.ArtifactKindTopFour):
		return domain.ArtifactKindTopFour, true
	case string(domain.ArtifactKindBracket):
		return domain.ArtifactKindBracket, true
	default:
		return "", false
	}
}

func progressionDigestMust(value []byte) [sha256.Size]byte {
	digest, ok := progressionDigest(value)
	if !ok {
		return [sha256.Size]byte{}
	}
	return digest
}

func progressionUUIDPointersEqual(left *uuid.UUID, right uuid.NullUUID) bool {
	if left == nil {
		return !right.Valid
	}
	return right.Valid && *left == right.UUID
}
