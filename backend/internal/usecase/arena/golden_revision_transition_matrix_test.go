package arena_test

import "testing"

func TestArenaGoldenRevisionTransitionMatrices(t *testing.T) {
	runArenaTransitionMatrix(t, []arenaTransitionCase{
		{name: "Golden tie selection", run: TestGoldenTiePartition},
		{name: "Golden retained assignment plan", run: TestGoldenExactPlan},
		{name: "Golden attempt commit and duplicate conflict", run: TestGoldenAttemptCommit},
		{name: "Golden failure replay", run: TestGoldenFailureReplay},
		{name: "Golden replay validation", run: TestGoldenFailureReplayValidation},
		{name: "Golden reserve exhaustion", run: TestGoldenReserveExhaustion},
		{name: "Golden direct fallback", run: TestGoldenDirectFallback},
		{name: "Golden permanent exclusion", run: TestGoldenPermanentExclusion},
		{name: "Golden readiness and disconnect", run: TestGoldenReadyDisconnect},
		{name: "Golden server-owned ready window", run: TestGoldenReadyWindow},
		{name: "retained Golden pre-start pause", run: TestRetainedGoldenPrestartPause},
		{name: "derived projection rebuild", run: TestPureProjectionRebuild},
		{name: "score, result, and projection revision rebuild", run: TestAtomicCorrectionRebuild},
		{name: "correction cutoff traversal", run: TestCorrectionCutoffTraversal},
		{name: "correction rollback stages", run: TestCorrectionStageRollback},
		{name: "correction validation and terminal rewrite denial", run: TestCorrectionValidation},
		{name: "revision DAG successor and supersession", run: TestRevisionDAG},
		{name: "superseded unstarted execution", run: TestSupersededUnstartedExecution},
		{name: "official result public and operator revisions", run: TestOfficialResultProjection},
	})
}
