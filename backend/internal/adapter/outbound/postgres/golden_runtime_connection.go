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
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

//nolint:gocyclo // Connection transitions keep replay and membership evidence in one transaction.
func (repository *GoldenRuntimePostgres) SetConnected(
	ctx context.Context,
	command usecase.GoldenConnectionCommand,
	now time.Time,
) error {
	if repository == nil || repository.tx == nil {
		return domain.ErrInternal
	}
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		q := repository.tx.Querier(txCtx)
		scope, err := q.SelectGoldenRuntimeParticipantScope(txCtx, sqlc.SelectGoldenRuntimeParticipantScopeParams{
			TournamentID: command.TournamentID, PlayerID: command.PlayerID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return goldenRuntimeReadError("load Golden connection scope", err)
		}
		head, err := q.LockGoldenRuntimeHead(txCtx, sqlc.LockGoldenRuntimeHeadParams{
			TournamentID: command.TournamentID, RosterID: scope.RosterID,
		})
		if err != nil {
			return goldenRuntimeReadError("lock Golden runtime head", err)
		}
		participant, err := q.LockGoldenRuntimeParticipant(txCtx, sqlc.LockGoldenRuntimeParticipantParams{
			TournamentID: command.TournamentID, PlayerID: command.PlayerID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return goldenRuntimeReadError("load Golden connection participant", err)
		}
		spec := goldenRuntimeCommandSpec{
			CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: participant.RosterID,
			ActorKind: goldenRuntimeCommandActorKind(false), ActorID: command.ActorID,
			Scope: "participant", Kind: "connect", AttemptID: command.ExpectedAttemptID,
			ParticipantID: participant.ParticipantID, ExpectedRuntimeRevision: command.ExpectedRuntimeRevision,
			ExpectedReadyWindowID: command.ExpectedReadyWindowID,
			Payload: struct {
				Connected bool `json:"connected"`
			}{Connected: command.Connected},
		}
		_, replayed, replayErr := goldenRuntimeReplay(txCtx, q, spec)
		if replayErr != nil {
			return replayErr
		}
		if replayed {
			return nil
		}
		if authorityErr := goldenRuntimeAuthorityConflictForTarget(
			command.ExpectedRuntimeRevision, head.Revision,
			command.ExpectedReadyWindowID, participant.ReadyWindowID,
			command.ExpectedAttemptID, participant.AttemptID,
		); authorityErr != nil {
			return authorityErr
		}
		open, err := q.LockOpenGoldenReadyDisconnect(txCtx, sqlc.LockOpenGoldenReadyDisconnectParams{
			MembershipID: participant.MembershipID, AttemptID: participant.AttemptID,
			TournamentID: participant.TournamentID, RosterID: participant.RosterID,
		})
		if command.Connected {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return goldenRuntimeReadError("load Golden disconnect", err)
			}
			deadline := participant.ReadyWindowDeadline
			if participant.StartedAt.Valid {
				deadline = participant.Deadline
			}
			if !deadline.Valid || !now.Before(deadline.Time) {
				return domain.ErrConflict
			}
			_, err = q.CloseGoldenReadyDisconnectCAS(txCtx, sqlc.CloseGoldenReadyDisconnectCASParams{
				NextState: "reconnected", ReconnectedAt: tstz(now), ID: open.ID,
				AttemptID: participant.AttemptID, TournamentID: participant.TournamentID,
				RosterID: participant.RosterID,
			})
			if err != nil {
				return goldenRuntimeWriteError("reconnect Golden participant", err)
			}
			return repository.appendGoldenRuntimeConnectionEvidence(txCtx, q, spec, head, command.PlayerID, now)
		}
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return goldenRuntimeReadError("load Golden disconnect", err)
		}
		if participant.State != "prepared" && participant.State != "ready" && participant.State != "active" {
			return nil
		}
		sequence, err := q.NextGoldenReadyDisconnectSequence(txCtx, participant.MembershipID)
		if err != nil {
			return goldenRuntimeReadError("allocate Golden disconnect sequence", err)
		}
		if participant.ReadyAt.Valid && !participant.StartedAt.Valid {
			if _, err = q.ClearGoldenMembershipReady(txCtx, sqlc.ClearGoldenMembershipReadyParams{
				ID: participant.MembershipID, AttemptID: participant.AttemptID,
				TournamentID: participant.TournamentID, RosterID: participant.RosterID,
			}); err != nil {
				return goldenRuntimeWriteError("clear disconnected Golden readiness", err)
			}
		}
		_, err = q.CreateGoldenReadyDisconnect(txCtx, sqlc.CreateGoldenReadyDisconnectParams{
			ID: command.CommandID, MembershipID: participant.MembershipID,
			AttemptID: participant.AttemptID, TournamentID: participant.TournamentID,
			RosterID: participant.RosterID, ParticipantID: participant.ParticipantID,
			SequenceNumber: sequence, DisconnectedAt: tstz(now), CreatedAt: tstz(now),
		})
		if err != nil {
			return goldenRuntimeWriteError("disconnect Golden participant", err)
		}
		return repository.appendGoldenRuntimeConnectionEvidence(txCtx, q, spec, head, command.PlayerID, now)
	})
	if err != nil {
		return fmt.Errorf("GoldenRuntimePostgres - SetConnected: %w", err)
	}
	return nil
}

func (repository *GoldenRuntimePostgres) appendGoldenRuntimeConnectionEvidence(
	ctx context.Context,
	q *sqlc.Queries,
	spec goldenRuntimeCommandSpec,
	head sqlc.GoldenRuntimeHead,
	playerID uuid.UUID,
	now time.Time,
) error {
	advanced, err := q.AdvanceGoldenRuntimeHead(ctx, sqlc.AdvanceGoldenRuntimeHeadParams{
		TournamentID: spec.TournamentID, RosterID: head.RosterID,
		ExpectedRevision: head.Revision, NextRevision: head.Revision + 1, UpdatedAt: tstz(now),
	})
	if err != nil {
		return goldenRuntimeWriteError("advance Golden runtime head", err)
	}
	result, err := goldenParticipantRuntimeViewWithin(ctx, q, spec.TournamentID, playerID)
	if err != nil {
		return err
	}
	resultPayload, err := goldenRuntimeResultPayload(result)
	if err != nil {
		return err
	}
	return repository.appendGoldenRuntimeEvidence(ctx, q, spec, advanced, "participant", resultPayload, now)
}
