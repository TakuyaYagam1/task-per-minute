package participant

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

var (
	ErrPostSeriesUnavailable  = errors.New("post-series action is unavailable")
	ErrPostSeriesCommandReuse = errors.New("post-series command was reused")
)

type ParticipantClock interface {
	Now() time.Time
}

type PostSeriesRecord struct {
	CommandID                     uuid.UUID
	TournamentID                  uuid.UUID
	RosterID                      uuid.UUID
	SeriesID                      uuid.UUID
	ParticipantID                 uuid.UUID
	CurrentResultRevisionID       domain.OfficialResultRevisionID
	SourceProjectionRevisionID    uuid.UUID
	SourceProjectionRevision      int64
	ResultingProjectionRevisionID uuid.UUID
	ResultingProjectionRevision   int64
	Action                        usecase.PostSeriesAction
	OccurredAt                    time.Time
}

type PostSeriesCommit struct {
	Record                     PostSeriesRecord
	ExpectedSeriesState        domain.SeriesState
	ExpectedResultRevisionID   domain.OfficialResultRevisionID
	ExpectedProjectionRevision int64
}

// PostSeriesRepository owns the immutable action ledger. CommitPostSeries must
// compare the terminal Series head and published projection before appending.
type PostSeriesRepository interface {
	FindPostSeriesCommand(ctx context.Context, commandID uuid.UUID) (*PostSeriesRecord, error)
	CommitPostSeries(ctx context.Context, commit PostSeriesCommit) (*PostSeriesRecord, bool, error)
}

type PostSeriesUseCase struct {
	repository PostSeriesRepository
	clock      ParticipantClock
}

func NewPostSeriesUseCase(repository PostSeriesRepository, clock ParticipantClock) *PostSeriesUseCase {
	return &PostSeriesUseCase{repository: repository, clock: clock}
}

func (u *PostSeriesUseCase) ApplyPostSeries(
	ctx context.Context,
	resolved ResolvedPostSeries,
) (usecase.PostSeriesResult, bool, error) {
	if ctx == nil || u == nil || u.repository == nil || u.clock == nil {
		return usecase.PostSeriesResult{}, false, domain.ErrValidation
	}
	if err := validatePostSeriesResolution(resolved); err != nil {
		return usecase.PostSeriesResult{}, false, err
	}

	existing, err := u.repository.FindPostSeriesCommand(ctx, resolved.CommandID)
	if err != nil {
		return usecase.PostSeriesResult{}, false, fmt.Errorf("PostSeriesUseCase - find command: %w", err)
	}
	if existing != nil {
		return reconcilePostSeries(*existing, resolved)
	}

	occurredAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(occurredAt) {
		return usecase.PostSeriesResult{}, false, domain.ErrValidation
	}
	record := PostSeriesRecord{
		CommandID:                     resolved.CommandID,
		TournamentID:                  resolved.Authority.TournamentID,
		RosterID:                      resolved.Authority.RosterID,
		SeriesID:                      resolved.SeriesID,
		ParticipantID:                 resolved.Authority.ParticipantID,
		CurrentResultRevisionID:       resolved.CurrentResultRevisionID,
		SourceProjectionRevisionID:    resolved.Authority.ProjectionRevisionID,
		SourceProjectionRevision:      resolved.Authority.ProjectionRevision,
		ResultingProjectionRevisionID: resolved.Authority.ProjectionRevisionID,
		ResultingProjectionRevision:   resolved.Authority.ProjectionRevision,
		Action:                        resolved.Action,
		OccurredAt:                    occurredAt,
	}
	commit := PostSeriesCommit{
		Record:                     record,
		ExpectedSeriesState:        resolved.SeriesState,
		ExpectedResultRevisionID:   resolved.CurrentResultRevisionID,
		ExpectedProjectionRevision: resolved.Authority.ProjectionRevision,
	}
	if err := validatePostSeriesCommit(commit); err != nil {
		return usecase.PostSeriesResult{}, false, err
	}
	committed, changed, err := u.repository.CommitPostSeries(ctx, commit)
	if err != nil {
		return usecase.PostSeriesResult{}, false, fmt.Errorf("PostSeriesUseCase - commit: %w", err)
	}
	if committed == nil || validatePostSeriesRecord(*committed) != nil ||
		!postSeriesRecordsEqual(*committed, record) {
		return usecase.PostSeriesResult{}, false, domain.ErrInternal
	}
	return postSeriesResult(*committed), changed, nil
}

func validatePostSeriesResolution(resolved ResolvedPostSeries) error {
	if resolved.Authority.TournamentID == uuid.Nil || resolved.Authority.RosterID == uuid.Nil ||
		resolved.Authority.PlayerID == uuid.Nil || resolved.Authority.ParticipantID == uuid.Nil ||
		resolved.Authority.ProjectionRevisionID == uuid.Nil || resolved.Authority.ProjectionRevision < 1 ||
		resolved.SeriesID == uuid.Nil || !resolved.SeriesState.IsTerminal() ||
		resolved.CurrentResultRevisionID.IsZero() || resolved.CommandID == uuid.Nil || !resolved.Action.IsValid() {
		return domain.ErrValidation
	}
	return nil
}

func validatePostSeriesCommit(commit PostSeriesCommit) error {
	if validatePostSeriesRecord(commit.Record) != nil || !commit.ExpectedSeriesState.IsTerminal() ||
		commit.ExpectedResultRevisionID.IsZero() ||
		commit.ExpectedResultRevisionID != commit.Record.CurrentResultRevisionID ||
		commit.ExpectedProjectionRevision != commit.Record.SourceProjectionRevision {
		return domain.ErrValidation
	}
	return nil
}

func validatePostSeriesRecord(record PostSeriesRecord) error {
	if record.CommandID == uuid.Nil || record.TournamentID == uuid.Nil || record.RosterID == uuid.Nil ||
		record.SeriesID == uuid.Nil || record.ParticipantID == uuid.Nil ||
		record.CurrentResultRevisionID.IsZero() || record.SourceProjectionRevisionID == uuid.Nil ||
		record.SourceProjectionRevision < 1 || record.ResultingProjectionRevisionID == uuid.Nil ||
		record.ResultingProjectionRevision < record.SourceProjectionRevision || !record.Action.IsValid() ||
		!domain.IsValidServerTime(record.OccurredAt) {
		return domain.ErrValidation
	}
	return nil
}

func reconcilePostSeries(
	record PostSeriesRecord,
	resolved ResolvedPostSeries,
) (usecase.PostSeriesResult, bool, error) {
	if validatePostSeriesRecord(record) != nil || record.CommandID != resolved.CommandID ||
		record.TournamentID != resolved.Authority.TournamentID ||
		record.RosterID != resolved.Authority.RosterID || record.SeriesID != resolved.SeriesID ||
		record.ParticipantID != resolved.Authority.ParticipantID ||
		record.CurrentResultRevisionID != resolved.CurrentResultRevisionID ||
		record.SourceProjectionRevisionID != resolved.Authority.ProjectionRevisionID ||
		record.SourceProjectionRevision != resolved.Authority.ProjectionRevision || record.Action != resolved.Action {
		return usecase.PostSeriesResult{}, false, ErrPostSeriesCommandReuse
	}
	return postSeriesResult(record), false, nil
}

func postSeriesResult(record PostSeriesRecord) usecase.PostSeriesResult {
	return usecase.PostSeriesResult{
		TournamentID:       record.TournamentID,
		ParticipantID:      record.ParticipantID,
		SeriesID:           record.SeriesID,
		ProjectionRevision: record.ResultingProjectionRevision,
		AcceptedAction:     record.Action,
	}
}

func postSeriesRecordsEqual(first, second PostSeriesRecord) bool {
	return first == second
}

var _ PostSeriesWorkflow = (*PostSeriesUseCase)(nil)
