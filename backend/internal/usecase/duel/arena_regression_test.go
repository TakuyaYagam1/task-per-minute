package duel_test

import "testing"

func TestArenaCasualFocusedRegression(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "Arena reservation conflict precedes queue mutation", run: TestMatchmakingReservationCAS_ArenaConflictBeforeQueueMutation},
		{name: "duplicate queue command keeps one reservation", run: TestMatchmakingReservationCAS_DuplicateQueueUsesSameClaim},
		{name: "enqueue failure releases the owned reservation", run: TestMatchmakingReservationCAS_EnqueueFailureReleasesOwnClaim},
		{name: "unmatched player remains queued", run: TestMatchmakingUsecase_JoinQueue_NoPairEnqueuesAndMarksQueued},
		{name: "failed pair pop rolls back the queue write", run: TestMatchmakingUsecase_JoinQueue_RollsBackEnqueueWhenPopPairFails},
		{name: "active Duel blocks duplicate matchmaking", run: TestMatchmakingUsecase_JoinQueue_RejectsPlayerInDuel},
		{name: "casual players receive the same eligible unsolved task", run: TestMatchmakingUsecase_JoinQueue_AssignsSameSharedUnsolvedTaskWhenManyAvailable},
		{name: "casual task filtering requires a common unsolved task", run: TestMatchmakingUsecase_JoinQueue_AssignsOnlyCommonUnsolvedTaskToBothPlayers},
		{name: "correct casual flag finishes the Duel", run: TestFlagSubmitUsecase_SubmitFlag_CorrectFinishesDuel},
		{name: "incorrect casual flag preserves the Duel", run: TestFlagSubmitUsecase_SubmitFlag_IncorrectFlag},
		{name: "expired casual submission is rejected", run: TestFlagSubmitUsecase_SubmitFlag_DeadlinePassed},
		{name: "concurrent casual finish has one terminal result", run: TestFlagSubmitUsecase_SubmitFlag_FinishRaceReturnsAlreadyFinished},
		{name: "casual disconnect freezes timer and broadcasts", run: TestReconnectManager_HandleDisconnect_FreezesTimerAndBroadcasts},
		{name: "casual reconnect resumes with a server deadline", run: TestReconnectManager_ConsumeReconnect_ResumesAndUpdatesDeadline},
		{name: "casual pause lifecycle remains intact", run: TestReconnectManager_DuelPausedLifecycle},
		{name: "casual player forfeit keeps one opponent winner", run: TestReconnectManager_FinalizePlayerForfeit_OpponentWinsAndIsIdempotent},
		{name: "casual draw never changes leaderboard score", run: TestReconnectManager_FinalizeDraw_DoesNotBumpLeaderboard},
		{name: "casual read remains participant scoped", run: TestReadUsecase_GetDuel_RejectsStranger},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}
