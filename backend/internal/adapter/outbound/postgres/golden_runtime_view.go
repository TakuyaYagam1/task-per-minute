package postgres

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
				State: row.State, PositionFrom: int(row.PositionFrom), PositionTo: int(row.PositionTo),
				StartedAt: goldenRuntimeTime(row.StartedAt), Deadline: goldenRuntimeTime(row.Deadline),
				Members: []usecase.GoldenMemberView{},
			})
		}
		group := &view.Groups[len(view.Groups)-1]
		if group.AttemptID != row.AttemptID || group.State != row.State {
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
	for _, row := range rows {
		if row.PlayerID != query.PlayerID {
			continue
		}
		view := usecase.GoldenParticipantView{
			TournamentID: row.TournamentID, ParticipantID: row.ParticipantID,
			GroupID: row.GroupID, GroupRevisionID: row.GroupRevisionID, AttemptID: row.AttemptID,
			State: row.State, Ready: row.ReadyAt.Valid, Submitted: row.SubmissionID.Valid,
			Position: goldenRuntimePosition(row.Position), StartedAt: goldenRuntimeTime(row.StartedAt),
			Deadline: goldenRuntimeTime(row.Deadline),
		}
		if row.State != "prepared" {
			view.Task = &usecase.GoldenTaskView{
				AssignmentID: row.AssignmentID, SnapshotID: row.SnapshotID, TaskID: row.TaskID,
				Title: row.Title, Category: row.Category, Difficulty: row.Difficulty,
				TimeLimitSeconds: int(row.TimeLimitSeconds),
			}
		}
		return view, nil
	}
	return usecase.GoldenParticipantView{}, domain.ErrTournamentNotFound
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
