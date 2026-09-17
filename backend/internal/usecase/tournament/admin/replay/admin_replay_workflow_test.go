package replay

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamereplay "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay"
)

func TestOperatorReserveRepositoryReturnsRecordedCommandBeforeRevisionCheck(t *testing.T) {
	t.Parallel()

	command := replayWorkflowReserveCommand()
	recorded := gamereplay.OperatorReserve{CommandID: command.CommandID}
	repository := replayWorkflowRepositoryStub{
		reserveAuthority: OperatorReserveAuthority{
			TournamentState: domain.TournamentStateSwiss,
			Replay: gamereplay.OperatorReserveAuthority{
				Revision: command.ExpectedAuthorityRevision + 1,
				Current:  &recorded,
			},
		},
	}
	adapter := operatorReserveRepository{repository: repository, command: command}

	authority, err := adapter.LoadOperatorReserveAuthority(context.Background(), gamereplay.ReplayReplacementScope{})

	if err != nil {
		t.Fatalf("LoadOperatorReserveAuthority() error = %v, want recorded command authority", err)
	}
	if authority.Current == nil || authority.Current.CommandID != command.CommandID {
		t.Fatalf("LoadOperatorReserveAuthority() current = %#v, want command %s", authority.Current, command.CommandID)
	}
}

func TestReplayReplacementRepositoryReturnsRecordedCommandBeforeRevisionCheck(t *testing.T) {
	t.Parallel()

	command := replayWorkflowReplayCommand()
	recorded := gamereplay.ReplayReplacement{CommandID: command.CommandID}
	repository := replayWorkflowRepositoryStub{
		replacementAuthority: ReplayReplacementAuthority{
			TournamentState: domain.TournamentStateSwiss,
			Replay: gamereplay.ReplayReplacementAuthority{
				Revision: command.ExpectedAuthorityRevision + 1,
				Current:  &recorded,
			},
		},
	}
	adapter := replayReplacementRepository{repository: repository, command: command}

	authority, err := adapter.LoadReplayReplacementAuthority(context.Background(), gamereplay.ReplayReplacementScope{})

	if err != nil {
		t.Fatalf("LoadReplayReplacementAuthority() error = %v, want recorded command authority", err)
	}
	if authority.Current == nil || authority.Current.CommandID != command.CommandID {
		t.Fatalf("LoadReplayReplacementAuthority() current = %#v, want command %s", authority.Current, command.CommandID)
	}
}

type replayWorkflowRepositoryStub struct {
	reserveAuthority     OperatorReserveAuthority
	replacementAuthority ReplayReplacementAuthority
}

func (s replayWorkflowRepositoryStub) ReadReplayTime(context.Context, uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}

func (s replayWorkflowRepositoryStub) LoadOperatorReserveAuthority(
	context.Context,
	ReserveCommand,
) (OperatorReserveAuthority, error) {
	return s.reserveAuthority, nil
}

func (s replayWorkflowRepositoryStub) CommitOperatorReserve(
	context.Context,
	ReserveCommand,
	[32]byte,
	gamereplay.OperatorReserve,
) (*gamereplay.OperatorReserve, bool, error) {
	return nil, false, nil
}

func (s replayWorkflowRepositoryStub) LoadReplayReplacementAuthority(
	context.Context,
	ReplayCommand,
) (ReplayReplacementAuthority, error) {
	return s.replacementAuthority, nil
}

func (s replayWorkflowRepositoryStub) CommitReplayReplacement(
	context.Context,
	ReplayCommand,
	[32]byte,
	gamereplay.ReplayReplacement,
) (*gamereplay.ReplayReplacement, bool, error) {
	return nil, false, nil
}

func replayWorkflowReserveCommand() ReserveCommand {
	return ReserveCommand{CommandScope: CommandScope{CommandID: uuid.New()}, ExpectedAuthorityRevision: 3}
}

func replayWorkflowReplayCommand() ReplayCommand {
	return ReplayCommand{CommandScope: CommandScope{CommandID: uuid.New()}, ExpectedAuthorityRevision: 3}
}
