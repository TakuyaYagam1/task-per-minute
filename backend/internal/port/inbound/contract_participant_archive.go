package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ParticipantArchiveUseCase is the read-only boundary for renewable access to
// immutable assignment source archives.
type ParticipantArchiveUseCase interface {
	GetSourceFile(ctx context.Context, query ParticipantArchiveQuery) (ParticipantArchiveDownload, error)
	SourceFileAvailable(ctx context.Context, query ParticipantArchiveQuery) (bool, error)
}

type ParticipantArchiveQuery struct {
	Actor        Identity
	TournamentID uuid.UUID
	AssignmentID uuid.UUID
}

type ParticipantArchiveDownload struct {
	URL       string
	ExpiresAt time.Time
}
