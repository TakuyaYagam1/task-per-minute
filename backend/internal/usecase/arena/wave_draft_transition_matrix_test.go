package arena_test

import "testing"

func TestArenaWaveDraftTransitionMatrices(t *testing.T) {
	runArenaTransitionMatrix(t, []arenaTransitionCase{
		{name: "atomic Wave start and stale authority", run: TestAtomicWaveStart},
		{name: "ready command and pre-start disconnect", run: TestReadyCommandAndPreStartDisconnect},
		{name: "server-owned ready window", run: TestOpenReadyWindow},
		{name: "cancellation and technical pause", run: TestCancelAndTechnicalPause},
		{name: "pause and resume compare-and-swap", run: TestPauseResumeCAS},
		{name: "single presence pause matrix", run: TestPauseResumeSinglePresenceMatrix},
		{name: "both absent pause matrix", run: TestPauseResumeBothAbsentMatrix},
		{name: "paused presence revisions", run: TestPausedPresenceUpdates},
		{name: "no-solve replay pipeline", run: TestNoSolveReplayPipeline},
		{name: "replay exhaustion rejection", run: TestReplayReserveExhaustionRejectsIncompleteOrStaleEvidence},
		{name: "execution epoch and actor order", run: TestExecutionAuthorityEpoch},
		{name: "execution replay epoch", run: TestExecutionAuthorityEpochReplay},
		{name: "execution replay boundaries", run: TestExecutionAuthorityEpochReplayBoundaries},
		{name: "Draft action order and completion", run: TestDraftActionValidation},
		{name: "Draft timeout", run: TestDraftTimeout},
		{name: "Draft pause and epoch recovery", run: TestDraftPauseAndEpochRecovery},
		{name: "old Wave closure", run: TestOldWaveClosure},
	})
}
