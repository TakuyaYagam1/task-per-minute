package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (repository *GoldenRuntimePostgres) OperatorView(
	ctx context.Context,
	query usecase.GoldenOperatorQuery,
) (usecase.GoldenOperatorView, error) {
	rows, err := repository.tx.Querier(ctx).ListGoldenRuntimeView(ctx, query.TournamentID)
	if err != nil {
		return usecase.GoldenOperatorView{}, fmt.Errorf("GoldenRuntimePostgres - OperatorView: %w", err)
	}
	return goldenOperatorRuntimeView(query.TournamentID, rows, time.Now().UTC())
}

func goldenOperatorRuntimeViewWithin(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID uuid.UUID,
	observedAt time.Time,
) (usecase.GoldenOperatorView, error) {
	rows, err := q.ListGoldenRuntimeView(ctx, tournamentID)
	if err != nil {
		return usecase.GoldenOperatorView{}, goldenRuntimeReadError("load Golden operator view", err)
	}
	return goldenOperatorRuntimeView(tournamentID, rows, observedAt)
}

func goldenOperatorRuntimeView(
	tournamentID uuid.UUID,
	rows []sqlc.ListGoldenRuntimeViewRow,
	observedAt time.Time,
) (usecase.GoldenOperatorView, error) {
	view := usecase.GoldenOperatorView{TournamentID: tournamentID, Groups: []usecase.GoldenOperatorGroupView{}, ObservedAt: observedAt}
	for _, row := range rows {
		if row.TournamentID != tournamentID || row.GroupID == uuid.Nil || row.AttemptID == uuid.Nil {
			return usecase.GoldenOperatorView{}, domain.ErrConflict
		}
		if len(view.Groups) == 0 || view.Groups[len(view.Groups)-1].GroupRevisionID != row.GroupRevisionID {
			view.Groups = append(view.Groups, usecase.GoldenOperatorGroupView{
				GroupID: row.GroupID, GroupRevisionID: row.GroupRevisionID, AttemptID: row.AttemptID,
				State: row.State, RuntimeRevision: row.RuntimeRevision, ReadyWindowID: row.ReadyWindowID,
				PositionFrom: int(row.PositionFrom), PositionTo: int(row.PositionTo),
				StartedAt: goldenRuntimeTime(row.StartedAt), Deadline: goldenRuntimeTime(row.Deadline),
				Members: []usecase.GoldenMemberView{},
			})
		}
		group := &view.Groups[len(view.Groups)-1]
		if group.AttemptID != row.AttemptID || group.State != row.State {
			return usecase.GoldenOperatorView{}, domain.ErrConflict
		}
		if group.RuntimeRevision != row.RuntimeRevision || group.ReadyWindowID != row.ReadyWindowID {
			return usecase.GoldenOperatorView{}, domain.ErrConflict
		}
		group.Members = append(group.Members, usecase.GoldenMemberView{
			ParticipantID: row.ParticipantID, Ready: row.ReadyAt.Valid,
			Submitted: row.SubmissionID.Valid, Position: goldenRuntimePosition(row.Position),
		})
	}
	return view, nil
}

func (repository *GoldenRuntimePostgres) ParticipantView(
	ctx context.Context,
	query usecase.GoldenParticipantQuery,
) (usecase.GoldenParticipantView, error) {
	rows, err := repository.tx.Querier(ctx).ListGoldenRuntimeView(ctx, query.TournamentID)
	if err != nil {
		return usecase.GoldenParticipantView{}, fmt.Errorf("GoldenRuntimePostgres - ParticipantView: %w", err)
	}
	return goldenParticipantRuntimeView(query.PlayerID, rows)
}

func goldenParticipantRuntimeView(
	playerID uuid.UUID,
	rows []sqlc.ListGoldenRuntimeViewRow,
) (usecase.GoldenParticipantView, error) {
	for _, row := range rows {
		if row.PlayerID != playerID {
			continue
		}
		view := usecase.GoldenParticipantView{
			TournamentID: row.TournamentID, ParticipantID: row.ParticipantID,
			GroupID: row.GroupID, GroupRevisionID: row.GroupRevisionID, AttemptID: row.AttemptID,
			State: row.State, RuntimeRevision: row.RuntimeRevision, ReadyWindowID: row.ReadyWindowID,
			Ready: row.ReadyAt.Valid, Submitted: row.SubmissionID.Valid,
			Position: goldenRuntimePosition(row.Position), StartedAt: goldenRuntimeTime(row.StartedAt),
			Deadline: goldenRuntimeTime(row.Deadline),
		}
		// Task metadata is disclosed only after the transaction that writes both
		// started_at and deadline has committed. The participant must also have
		// established participation, so no-show and excluded rows stay taskless.
		if row.StartedAt.Valid && row.Deadline.Valid && row.ParticipantEligible {
			view.Task = &usecase.GoldenTaskView{
				AssignmentID: row.AssignmentID, SnapshotID: row.SnapshotID, TaskID: row.TaskID,
				Version: int(row.TaskVersion), Title: row.Title, Description: row.Description,
				Category: row.Category, Difficulty: row.Difficulty,
				TimeLimitSeconds: int(row.TimeLimit), TaskURL: cloneParticipantStateString(row.TaskUrl),
				SourceFileAvailable: row.SourceFileAvailable,
			}
		}
		return view, nil
	}
	return usecase.GoldenParticipantView{}, domain.ErrTournamentNotFound
}

func goldenParticipantRuntimeViewWithin(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID, playerID uuid.UUID,
) (usecase.GoldenParticipantView, error) {
	rows, err := q.ListGoldenRuntimeView(ctx, tournamentID)
	if err != nil {
		return usecase.GoldenParticipantView{}, goldenRuntimeReadError("load Golden participant view", err)
	}
	return goldenParticipantRuntimeView(playerID, rows)
}

func goldenRuntimeTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid || value.Time.IsZero() {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func goldenRuntimePosition(position *int16) *int {
	if position == nil {
		return nil
	}
	value := int(*position)
	return &value
}
