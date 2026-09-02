package arena_test

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestArenaCoreMetricsCallSites(t *testing.T) {
	now := time.Date(2026, time.September, 2, 15, 0, 0, 0, time.UTC)
	metrics := observability.NewArenaMetrics()

	t.Run("lifecycle and wave authority", func(t *testing.T) {
		tournamentID := task045ID(7301)
		lifecycleRepository := newLifecycleRepositoryFake(lifecycleTournamentRecord(
			tournamentID,
			domain.ArenaTournamentStateRosterLocked,
			3,
			now,
		))
		command := arena.TournamentLifecycleCommand{
			TournamentID:     tournamentID,
			ExpectedRevision: 3,
			NextState:        domain.ArenaTournamentStateSwiss,
		}
		usecase := arena.NewTournamentLifecycleUseCase(
			lifecycleRepository,
			fixedArenaClock{now: now},
			metrics,
		)
		_, changed, err := usecase.Transition(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		_, changed, err = usecase.Transition(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)

		authority, waveCommand := waveStartFixture(t, now)
		_, changed, err = arena.NewWaveStartUseCase(
			&waveStartRepositoryFake{authority: authority},
			fixedArenaClock{now: now},
			metrics,
		).Start(t.Context(), waveCommand)
		require.NoError(t, err)
		require.True(t, changed)

		require.InDelta(t, 1, arenaCoreMetricValue(t, metrics, "lifecycle", "success"), 0)
		require.InDelta(t, 1, arenaCoreMetricValue(t, metrics, "wave", "success"), 0)
	})

	t.Run("submission retry and terminal outcome", func(t *testing.T) {
		authority, command := task037SubmissionFixture(t, now)
		repository := &submissionConflictOnceRepository{delegate: &arenaSubmissionRepositoryFake{
			authority: authority, committedAt: now,
		}}
		_, changed, err := arena.NewArenaSubmissionUseCase(repository, metrics).
			Submit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.InDelta(t, 1, arenaCoreMetricValue(t, metrics, "submission", "retry"), 0)
		require.InDelta(t, 1, arenaCoreMetricValue(t, metrics, "submission", "success"), 0)
	})

	t.Run("reconnect and deadline authority", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		interval := authority.Reconnect[0]
		command := arena.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(7330),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: interval.ID,
			Settlement: task045SettlementIDs(7331),
		}
		usecase := arena.NewReconnectUseCase(
			newTask045RepositoryFake(authority),
			fixedArenaClock{now: now},
			metrics,
		)
		_, changed, err := usecase.Reconnect(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		_, changed, err = usecase.Reconnect(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)

		deadlineAuthority := task045Authority(now, true, false)
		deadline := deadlineAuthority.Reconnect[0].Deadline
		timeout := arena.ReconnectTimeoutCommand{
			Scope: deadlineAuthority.Scope, CommandID: task045ID(7340),
			ParticipantID: deadlineAuthority.Series.FirstParticipantID,
			IntervalID:    deadlineAuthority.Reconnect[0].ID,
			Settlement:    task045SettlementIDs(7341),
		}
		_, changed, err = arena.NewReconnectTimeoutUseCase(
			newTask045RepositoryFake(deadlineAuthority),
			fixedArenaClock{now: deadline.Add(500 * time.Millisecond)},
			metrics,
		).Expire(t.Context(), timeout)
		require.NoError(t, err)
		require.True(t, changed)

		require.InDelta(t, 1, arenaCoreMetricValue(t, metrics, "reconnect", "success"), 0)
		require.InDelta(t, 1, arenaCoreMetricValue(t, metrics, "deadline", "success"), 0)
		require.Equal(t, uint64(1), arenaCoreHistogramCount(t, metrics, "tpm_arena_lag_seconds", "kind", "deadline"))
	})

	t.Run("reserve and Golden terminal Game", func(t *testing.T) {
		authority, command := task041OperatorReserveFixture(t)
		_, changed, err := arena.NewOperatorReserveUseCase(
			&operatorReserveRepositoryFake{authority: authority},
			metrics,
		).Reserve(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)

		execution := task049StartedExecution(t, now)
		scope := task049SubmissionScope(execution)
		submissions, err := arena.NewGoldenSubmissionLedger(scope, task049ID(73110))
		require.NoError(t, err)
		submissionRepository := newTask049SubmissionRepository(
			execution,
			submissions,
			now.Add(5*time.Second),
		)
		participantID := execution.Membership.ParticipantIDs[0]
		verification := task049Verification(scope, execution, participantID, 73120)
		submissionRepository.verifications[verification.ID] = verification
		submitted, submittedChanged, err := arena.NewGoldenSubmissionUseCase(submissionRepository).
			Submit(t.Context(), arena.GoldenSubmissionCommand{
				Scope: scope, CommandID: task049ID(73130), ActorParticipantID: participantID,
				ParticipantID: participantID, VerificationID: verification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(73131),
			})
		require.NoError(t, err)
		require.True(t, submittedChanged)

		sentinel := task049SwissPointSentinel(73140)
		positions, err := arena.NewGoldenPositionLedger(
			execution.Scope,
			execution.Group.PositionFrom,
			execution.Group.PositionTo,
			task049ID(73150),
		)
		require.NoError(t, err)
		repository := newTask049CommitRepository(submissionRepository, *submitted, positions, sentinel)
		_, changed, err = arena.NewGoldenAttemptCommitUseCase(
			repository,
			fixedArenaClock{now: execution.Start.Deadline},
			metrics,
		).CommitAttempt(t.Context(), arena.GoldenAttemptCommitCommand{
			Scope: scope, CommandID: task049ID(73160), CommitID: task049ID(73161),
			ExpectedExecution: execution.Expectation(), ExpectedSubmissions: submitted.Expectation(),
			ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
			NextPositionRevisionID: task049ID(73162), Reason: arena.GoldenAttemptTerminalDeadline,
		})
		require.NoError(t, err)
		require.True(t, changed)

		require.InDelta(t, 1, arenaCoreMetricValue(t, metrics, "reserve", "success"), 0)
		require.InDelta(t, 1, arenaCoreMetricValue(t, metrics, "golden", "success"), 0)
		require.InDelta(t, 1, arenaCoreMetricValue(t, metrics, "game", "success"), 0)
	})
}

func arenaCoreMetricValue(
	t *testing.T,
	metrics *observability.ArenaMetrics,
	operation string,
	outcome string,
) float64 {
	t.Helper()
	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	metric := arenaCoreMetric(t, families, "tpm_arena_operations_total", map[string]string{
		"operation": operation,
		"outcome":   outcome,
	})
	return metric.GetCounter().GetValue()
}

func arenaCoreHistogramCount(
	t *testing.T,
	metrics *observability.ArenaMetrics,
	name string,
	label string,
	value string,
) uint64 {
	t.Helper()
	families, err := metrics.Gatherer().Gather()
	require.NoError(t, err)
	metric := arenaCoreMetric(t, families, name, map[string]string{label: value})
	return metric.GetHistogram().GetSampleCount()
}

func arenaCoreMetric(
	t *testing.T,
	families []*dto.MetricFamily,
	name string,
	want map[string]string,
) *dto.Metric {
	t.Helper()
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, pair := range metric.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			if arenaCoreLabelsEqual(labels, want) {
				return metric
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, want)
	return nil
}

func arenaCoreLabelsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
