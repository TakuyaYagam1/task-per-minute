package arena_test

import "testing"

type arenaTransitionCase struct {
	name string
	run  func(*testing.T)
}

func runArenaTransitionMatrix(t *testing.T, cases []arenaTransitionCase) {
	t.Helper()

	for _, test := range cases {
		t.Run(test.name, test.run)
	}
}

func TestArenaLifecycleTransitionMatrices(t *testing.T) {
	runArenaTransitionMatrix(t, []arenaTransitionCase{
		{name: "Tournament create, list, and rejected commands", run: TestTournamentCreateAndList},
		{name: "Tournament lifecycle and active slot CAS", run: TestLifecycleActiveSlot},
		{name: "Series legal and rejected state edges", run: TestSeriesExecutionTransitions},
		{name: "Game attempt legal and rejected state edges", run: TestGameSlotAttemptTransitions},
		{name: "Series score and terminal winner progression", run: TestSeriesScoreProgression},
		{name: "official Game and Series result projections", run: TestOfficialResultProjection},
		{name: "Tournament cancellation and terminal rewrite denial", run: TestTournamentCancellation},
		{name: "semifinal advancement invariants", run: TestSemifinalAdvancement},
		{name: "final champion derivation", run: TestFinalChampion},
		{name: "single-winner settlement under contention", run: TestConcurrentWinnerSettlement},
		{
			name: "settlement rejects missing winner and invalid evidence",
			run:  TestConcurrentWinnerSettlementRejectsMissingWinnerOrInvalidEvidence,
		},
	})
}
