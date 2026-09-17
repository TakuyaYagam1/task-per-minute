package reconnect_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

func testReconnectTimeoutReceipts(t *testing.T, base time.Time) {
	t.Helper()

	t.Run("void_receipt_requires_terminal_result_timestamp", func(t *testing.T) {
		for index, test := range []struct {
			name string
			at   func(time.Time) time.Time
		}{
			{name: "zero", at: func(time.Time) time.Time { return time.Time{} }},
			{name: "different", at: func(at time.Time) time.Time { return at.Add(-time.Nanosecond) }},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := task045Authority(base, true, true)
				deadline := authority.Reconnect[0].Deadline
				command := reconnectusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(1000 + index*10),
					ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
					Settlement: task045SettlementIDs(1001 + index*10)}
				repository := newTask045RepositoryHarness(t, authority)
				useCase := reconnectusecase.NewTimeoutUseCase(repository, newReconnectClock(t, deadline))
				terminal, changed, err := useCase.Expire(t.Context(), command)
				if err != nil || !changed || terminal.VoidGameResultRevision == nil || terminal.ReconnectAuthority.Current == nil {
					t.Fatalf("Expire(seed void %s) error = %v, changed = %v, record = %+v", test.name, err, changed, terminal)
				}
				repository.mutateReceipt(command.CommandID, func(record *reconnectusecase.ReconnectRecord) {
					corruptAt := test.at(record.ReconnectAuthority.Current.TerminalizedAt)
					record.VoidGameResultRevision.RecordedAt = corruptAt
					record.ReconnectAuthority.Current.VoidGameResultRevision.RecordedAt = corruptAt
				})
				_, changed, err = useCase.Expire(t.Context(), command)
				if !errors.Is(err, domain.ErrInternal) || changed || repository.writeCount() != 1 || repository.commitCount() != 1 {
					t.Fatalf("Expire(corrupt void %s) error = %v, changed = %v, writes = %d, commits = %d", test.name, err, changed, repository.writeCount(), repository.commitCount())
				}
			})
		}
	})
}
