package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

type TournamentParticipantPostgres struct {
	tx *TxManager
}

func NewTournamentParticipantPostgres(tx *TxManager) *TournamentParticipantPostgres {
	return &TournamentParticipantPostgres{tx: tx}
}

func (r *TournamentParticipantPostgres) ResolveReady(
	ctx context.Context,
	command usecase.ReadyCommand,
) (tournamentparticipant.ResolvedReadyCommand, error) {
	authority, err := r.lockCommandAuthority(ctx, command.Actor, command.TournamentID, command.ExpectedProjectionRevision)
	if err != nil {
		return tournamentparticipant.ResolvedReadyCommand{}, err
	}
	row, err := r.tx.Querier(ctx).LockParticipantReadyAuthority(
		ctx,
		sqlc.LockParticipantReadyAuthorityParams{
			ParticipantID: authority.ParticipantID,
			WaveID:        command.WaveID, TournamentID: command.TournamentID, RosterID: authority.RosterID,
		},
	)
	if err != nil {
		return tournamentparticipant.ResolvedReadyCommand{}, participantScopeError("resolve ready", err)
	}
	return tournamentparticipant.ResolvedReadyCommand{
		Authority: authority,
		Command: readiness.ParticipantCommand{
			Scope:                    readiness.ReadinessScope{WaveID: command.WaveID, WindowID: row.ReadyWindowID},
			CommandID:                command.CommandID,
			ActorParticipantID:       authority.ParticipantID,
			ParticipantID:            authority.ParticipantID,
			ExpectedWaveRevisionID:   domain.WaveRevisionID(row.WaveRevisionID),
			ExpectedWindowRevisionID: domain.ReadyWindowRevisionID(row.ReadyWindowRevisionID),
			Ready:                    command.Ready,
		},
	}, nil
}

func (r *TournamentParticipantPostgres) ResolveDraftAction(
	ctx context.Context,
	command usecase.DraftActionCommand,
) (tournamentparticipant.ResolvedDraftAction, error) {
	authority, err := r.lockCommandAuthority(ctx, command.Actor, command.TournamentID, command.ExpectedProjectionRevision)
	if err != nil {
		return tournamentparticipant.ResolvedDraftAction{}, err
	}
	row, err := r.tx.Querier(ctx).LockParticipantDraftAuthority(
		ctx,
		sqlc.LockParticipantDraftAuthorityParams{
			SeriesID: command.SeriesID, TournamentID: command.TournamentID,
			RosterID: authority.RosterID, ParticipantID: authority.ParticipantID,
		},
	)
	if err != nil {
		return tournamentparticipant.ResolvedDraftAction{}, participantScopeError("resolve draft", err)
	}
	return tournamentparticipant.ResolvedDraftAction{
		Authority: authority,
		SeriesID:  command.SeriesID,
		Command: draftusecase.PlayerActionCommand{
			DraftID:              row.DraftID,
			ExpectedRevisionID:   row.RevisionID,
			ExpectedRevision:     command.ExpectedDraftRevision,
			ExpectedServiceEpoch: row.ServiceEpoch,
			ExpectedTurn:         command.ExpectedTurn,
			CommandID:            command.CommandID,
			ResultRevisionID:     participantCommandID(command.CommandID, "draft-revision"),
			ActionID:             participantCommandID(command.CommandID, "draft-action"),
			ActorID:              authority.ParticipantID,
			Action:               command.Action,
			Category:             command.Category,
		},
	}, nil
}

func (r *TournamentParticipantPostgres) ResolveSubmission(
	ctx context.Context,
	command usecase.SubmissionCommand,
) (tournamentparticipant.ResolvedSubmission, error) {
	authority, err := r.lockCommandIdentity(ctx, command.Actor, command.TournamentID)
	if err != nil {
		return tournamentparticipant.ResolvedSubmission{}, err
	}
	querier := r.tx.Querier(ctx)
	replayScope, err := querier.LockParticipantSubmissionReplayScope(
		ctx,
		sqlc.LockParticipantSubmissionReplayScopeParams{
			GameID: command.GameID, ParticipantID: authority.ParticipantID, SeriesID: command.SeriesID,
			TournamentID: command.TournamentID, RosterID: authority.RosterID,
		},
	)
	if err != nil {
		return tournamentparticipant.ResolvedSubmission{}, participantScopeError("resolve submission replay scope", err)
	}
	replay, replayErr := querier.FindParticipantSubmissionReplay(
		ctx,
		sqlc.FindParticipantSubmissionReplayParams{
			CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: authority.RosterID,
			SeriesID: command.SeriesID, AttemptID: command.GameID, AssignmentID: replayScope.AssignmentID,
			ParticipantID: authority.ParticipantID,
		},
	)
	if replayErr == nil {
		resolved, err := participantSubmissionReplay(command, authority, replayScope, replay)
		if err != nil {
			return tournamentparticipant.ResolvedSubmission{}, err
		}
		return resolved, nil
	}
	if !errors.Is(replayErr, pgx.ErrNoRows) {
		return tournamentparticipant.ResolvedSubmission{}, fmt.Errorf(
			"TournamentParticipantPostgres - find submission replay: %w",
			replayErr,
		)
	}
	if err := validateParticipantAuthorityRevision(authority, command.ExpectedProjectionRevision); err != nil {
		return tournamentparticipant.ResolvedSubmission{}, err
	}
	row, err := querier.LockParticipantSubmissionAuthority(
		ctx,
		sqlc.LockParticipantSubmissionAuthorityParams{
			GameID: command.GameID, ParticipantID: authority.ParticipantID, SeriesID: command.SeriesID,
			TournamentID: command.TournamentID, RosterID: authority.RosterID,
		},
	)
	if err != nil {
		return tournamentparticipant.ResolvedSubmission{}, participantScopeError("resolve submission", err)
	}
	return resolvedSubmissionCommand(command, authority, row), nil
}

func resolvedSubmissionCommand(
	command usecase.SubmissionCommand,
	authority tournamentparticipant.ParticipantCommandAuthority,
	row sqlc.LockParticipantSubmissionAuthorityRow,
) tournamentparticipant.ResolvedSubmission {
	return tournamentparticipant.ResolvedSubmission{
		Authority: authority,
		Command: gameusecase.SubmissionCommand{
			Scope: gamedomain.SubmissionScope{
				WaveID: row.WaveID,
				Game: gamedomain.Scope{
					TournamentID: command.TournamentID,
					SeriesID:     command.SeriesID,
					SlotID:       row.SlotID,
					GameID:       command.GameID,
				},
				AssignmentID: row.AssignmentID,
			},
			CommandID:          command.CommandID,
			ActorParticipantID: authority.ParticipantID,
			ParticipantID:      authority.ParticipantID,
			SubmittedFlag:      command.SubmittedFlag,
		},
	}
}

func participantSubmissionReplay(
	command usecase.SubmissionCommand,
	authority tournamentparticipant.ParticipantCommandAuthority,
	scope sqlc.LockParticipantSubmissionReplayScopeRow,
	row sqlc.FindParticipantSubmissionReplayRow,
) (tournamentparticipant.ResolvedSubmission, error) {
	intentDigest, err := gameusecase.SubmissionIntentDigest(command.SubmittedFlag)
	if err != nil {
		return tournamentparticipant.ResolvedSubmission{}, domain.ErrValidation
	}
	if !bytes.Equal(row.IntentDigest, intentDigest[:]) {
		return tournamentparticipant.ResolvedSubmission{}, gameusecase.ErrSubmissionCommandReuse
	}
	resolved := tournamentparticipant.ResolvedSubmission{
		Authority: authority,
		Command: gameusecase.SubmissionCommand{
			Scope: gamedomain.SubmissionScope{
				WaveID: scope.WaveID,
				Game: gamedomain.Scope{
					TournamentID: command.TournamentID,
					SeriesID:     command.SeriesID,
					SlotID:       scope.SlotID,
					GameID:       command.GameID,
				},
				AssignmentID: scope.AssignmentID,
			},
			CommandID:          command.CommandID,
			ActorParticipantID: authority.ParticipantID,
			ParticipantID:      authority.ParticipantID,
			SubmittedFlag:      command.SubmittedFlag,
		},
	}
	recorded, projectionRevision, err := participantRecordedSubmissionReplay(resolved.Command, row, authority)
	if err != nil {
		return tournamentparticipant.ResolvedSubmission{}, err
	}
	resolved.Replay = &usecase.SubmissionResult{
		ProjectionRevision: projectionRevision,
		Submission:         recorded,
	}
	return resolved, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func participantRecordedSubmissionReplay(
	command gameusecase.SubmissionCommand,
	row sqlc.FindParticipantSubmissionReplayRow,
	authority tournamentparticipant.ParticipantCommandAuthority,
) (gamedomain.Submission, int64, error) {
	if row.TournamentID != command.Scope.Game.TournamentID || row.RosterID != authority.RosterID ||
		row.SeriesID != command.Scope.Game.SeriesID || row.AttemptID != command.Scope.Game.GameID ||
		row.AssignmentID != command.Scope.AssignmentID || row.ParticipantID != command.ParticipantID ||
		row.IdempotencyKey != command.CommandID || len(row.PayloadDigest) != 32 ||
		len(row.IntentDigest) != 32 || row.SnapshotID == uuid.Nil || row.TaskID == uuid.Nil ||
		!row.CreatedAt.Valid {
		return gamedomain.Submission{}, 0, domain.ErrInternal
	}
	correct := false
	switch row.Status {
	case submissionStatusAccepted:
		correct = true
	case submissionStatusRejected:
		if row.DecisionReason == nil || *row.DecisionReason != participantIncorrectFlagReason {
			return gamedomain.Submission{}, 0, domain.ErrInternal
		}
	default:
		return gamedomain.Submission{}, 0, domain.ErrInternal
	}
	var contentDigest [32]byte
	copy(contentDigest[:], row.PayloadDigest)
	recorded := gamedomain.Submission{
		Scope: command.Scope, CommandID: row.IdempotencyKey, ParticipantID: row.ParticipantID,
		Sequence: row.ServerSequence, CommittedAt: row.CreatedAt.Time.Round(0).UTC(), Correct: correct,
		SnapshotID: row.SnapshotID, TaskID: row.TaskID, ContentDigest: contentDigest,
	}
	if gamedomain.ValidateSubmission(recorded) != nil {
		return gamedomain.Submission{}, 0, domain.ErrInternal
	}
	if !correct {
		if row.ResultCommitID.Valid || row.ProjectionRevisionID.Valid || row.ProjectionRevision != 0 {
			return gamedomain.Submission{}, 0, domain.ErrInternal
		}
		return recorded, authority.ProjectionRevision, nil
	}
	if !row.ResultCommitID.Valid || !row.ProjectionRevisionID.Valid || row.ProjectionRevision < 1 ||
		(row.ProjectionState != "published" && row.ProjectionState != "superseded") ||
		!row.ProjectionPublishedAt.Valid {
		return gamedomain.Submission{}, 0, domain.ErrConflict
	}
	return recorded, row.ProjectionRevision, nil
}

func (r *TournamentParticipantPostgres) ResolveSurrender(
	ctx context.Context,
	command usecase.SurrenderCommand,
) (tournamentparticipant.ResolvedSurrender, error) {
	authority, err := r.lockCommandAuthority(ctx, command.Actor, command.TournamentID, command.ExpectedProjectionRevision)
	if err != nil {
		return tournamentparticipant.ResolvedSurrender{}, err
	}
	row, err := r.tx.Querier(ctx).LockParticipantSurrenderAuthority(
		ctx,
		sqlc.LockParticipantSurrenderAuthorityParams{
			SeriesID: command.SeriesID, TournamentID: command.TournamentID,
			RosterID: authority.RosterID, ParticipantID: authority.ParticipantID,
		},
	)
	if err != nil {
		return tournamentparticipant.ResolvedSurrender{}, participantScopeError("resolve surrender", err)
	}
	gameRevisionID := domain.OfficialResultRevisionID(participantCommandID(command.CommandID, "game-result"))
	return tournamentparticipant.ResolvedSurrender{
		Authority: authority,
		Reason:    strings.TrimSpace(command.Reason),
		Command: gameusecase.SurrenderCommand{
			Scope:                   gameusecase.Scope{TournamentID: command.TournamentID, SeriesID: command.SeriesID},
			CommandID:               command.CommandID,
			ActorParticipantID:      authority.ParticipantID,
			ForfeitingParticipantID: authority.ParticipantID,
			ExpectedGame: gameusecase.GameExpectation{
				SlotID: row.SlotID, GameID: row.GameID, AttemptNo: int(row.AttemptNumber), State: domain.GameState(row.State),
			},
			Revisions: gameusecase.ForfeitRevisionSet{
				GameResultRevisionID:   &gameRevisionID,
				ScoreRevisionID:        domain.SeriesScoreRevisionID(participantCommandID(command.CommandID, "score-revision")),
				SeriesResultRevisionID: domain.OfficialResultRevisionID(participantCommandID(command.CommandID, "series-result")),
				AuditEventID:           participantCommandID(command.CommandID, "audit-event"),
				OutboxEventID:          participantCommandID(command.CommandID, "outbox-event"),
				ProjectionRevisionID:   participantCommandID(command.CommandID, "projection-revision"),
			},
		},
	}, nil
}

func (r *TournamentParticipantPostgres) ResolvePostSeries(
	ctx context.Context,
	command usecase.PostSeriesCommand,
) (tournamentparticipant.ResolvedPostSeries, error) {
	authority, err := r.lockCommandAuthority(ctx, command.Actor, command.TournamentID, command.ExpectedProjectionRevision)
	if err != nil {
		return tournamentparticipant.ResolvedPostSeries{}, err
	}
	row, err := r.tx.Querier(ctx).LockParticipantPostSeriesAuthority(
		ctx,
		sqlc.LockParticipantPostSeriesAuthorityParams{
			SeriesID: command.SeriesID, TournamentID: command.TournamentID,
			RosterID: authority.RosterID, ParticipantID: authority.ParticipantID,
		},
	)
	if err != nil {
		return tournamentparticipant.ResolvedPostSeries{}, participantScopeError("resolve post-series", err)
	}
	if !row.CurrentResultRevisionID.Valid {
		return tournamentparticipant.ResolvedPostSeries{}, domain.ErrConflict
	}
	return tournamentparticipant.ResolvedPostSeries{
		Authority:               authority,
		SeriesID:                command.SeriesID,
		SeriesState:             domain.SeriesState(row.State),
		CurrentResultRevisionID: domain.OfficialResultRevisionID(row.CurrentResultRevisionID.UUID),
		CommandID:               command.CommandID,
		Action:                  command.Action,
	}, nil
}

func (r *TournamentParticipantPostgres) lockCommandAuthority(
	ctx context.Context,
	actor usecase.Identity,
	tournamentID uuid.UUID,
	expectedRevision int64,
) (tournamentparticipant.ParticipantCommandAuthority, error) {
	authority, err := r.lockCommandIdentity(ctx, actor, tournamentID)
	if err != nil {
		return tournamentparticipant.ParticipantCommandAuthority{}, err
	}
	if err := validateParticipantAuthorityRevision(authority, expectedRevision); err != nil {
		return tournamentparticipant.ParticipantCommandAuthority{}, err
	}
	return authority, nil
}

func (r *TournamentParticipantPostgres) lockCommandIdentity(
	ctx context.Context,
	actor usecase.Identity,
	tournamentID uuid.UUID,
) (tournamentparticipant.ParticipantCommandAuthority, error) {
	if !validParticipantIdentityRequest(ctx, r, actor, tournamentID) {
		return tournamentparticipant.ParticipantCommandAuthority{}, domain.ErrValidation
	}
	rosterID, err := r.tx.Querier(ctx).GetParticipantCommandRoster(ctx, sqlc.GetParticipantCommandRosterParams{
		TournamentID: tournamentID, PlayerID: actor.PlayerID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tournamentparticipant.ParticipantCommandAuthority{}, domain.ErrAssignmentParticipant
		}
		return tournamentparticipant.ParticipantCommandAuthority{}, fmt.Errorf("get participant command roster: %w", err)
	}
	if err := lockTournamentResultScope(ctx, r.tx.Querier(ctx), tournamentID, rosterID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tournamentparticipant.ParticipantCommandAuthority{}, domain.ErrAssignmentParticipant
		}
		return tournamentparticipant.ParticipantCommandAuthority{}, fmt.Errorf("lock participant result scope: %w", err)
	}
	row, err := r.tx.Querier(ctx).LockParticipantCommandAuthority(
		ctx,
		sqlc.LockParticipantCommandAuthorityParams{PlayerID: actor.PlayerID, TournamentID: tournamentID},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tournamentparticipant.ParticipantCommandAuthority{}, domain.ErrAssignmentParticipant
		}
		return tournamentparticipant.ParticipantCommandAuthority{}, fmt.Errorf(
			"TournamentParticipantPostgres - lock command authority: %w",
			err,
		)
	}
	authority, err := participantCommandAuthority(row, actor.PlayerID, tournamentID)
	if err != nil {
		return tournamentparticipant.ParticipantCommandAuthority{}, err
	}
	return authority, nil
}

func validParticipantIdentityRequest(
	ctx context.Context,
	repository *TournamentParticipantPostgres,
	actor usecase.Identity,
	tournamentID uuid.UUID,
) bool {
	return ctx != nil && repository != nil && repository.tx != nil &&
		actor.PlayerID != uuid.Nil && tournamentID != uuid.Nil
}

func participantCommandAuthority(
	row sqlc.LockParticipantCommandAuthorityRow,
	playerID uuid.UUID,
	tournamentID uuid.UUID,
) (tournamentparticipant.ParticipantCommandAuthority, error) {
	authority := tournamentparticipant.ParticipantCommandAuthority{
		TournamentID: tournamentID, RosterID: row.RosterID, PlayerID: playerID,
		ParticipantID: row.ParticipantID, TournamentState: domain.TournamentState(row.TournamentState),
		ProjectionRevisionID: row.ProjectionRevisionID, ProjectionRevision: row.ProjectionRevision,
	}
	if !authority.TournamentState.IsValid() || authority.RosterID == uuid.Nil ||
		authority.ParticipantID == uuid.Nil || authority.ProjectionRevisionID == uuid.Nil ||
		authority.ProjectionRevision < 1 {
		return tournamentparticipant.ParticipantCommandAuthority{}, domain.ErrInternal
	}
	return authority, nil
}

func validateParticipantAuthorityRevision(
	authority tournamentparticipant.ParticipantCommandAuthority,
	expectedRevision int64,
) error {
	if authority.ProjectionRevision != expectedRevision ||
		authority.TournamentState == domain.TournamentStateCancelled {
		return &usecase.RevisionConflictError{
			TournamentID:     authority.TournamentID,
			ExpectedRevision: expectedRevision,
			CurrentRevision:  authority.ProjectionRevision,
			CurrentState:     authority.TournamentState,
		}
	}
	return nil
}

func participantScopeError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return fmt.Errorf("TournamentParticipantPostgres - %s: %w", operation, err)
}

func participantCommandID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("participant-command:"+role))
}

var _ tournamentparticipant.CommandAuthority = (*TournamentParticipantPostgres)(nil)
