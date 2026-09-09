package golden

import (
	"fmt"

	"github.com/google/uuid"
)

type waveGoldenIdentityRole struct {
	value uuid.UUID
	role  string
}

func waveValidGoldenRevisionPredecessor(current uuid.UUID, revision int64, previous *uuid.UUID) bool {
	return revision == 1 && previous == nil ||
		revision > 1 && previous != nil && *previous != uuid.Nil && *previous != current
}

func validateGoldenExecutionIdentityRoles(e GoldenWaveExecution) error {
	roles := []waveGoldenIdentityRole{
		{e.Scope.TournamentID, "tournament"}, {e.Scope.GroupID, "group"},
		{e.Scope.GroupRevisionID.UUID(), "group-revision"},
		{e.Source.RevisionID, goldenExecutionSourceStateRole(e.Source.Revision)},
		{e.Source.Membership.RevisionID, goldenExecutionSourceMembershipRole(e.Source.Membership.Revision)},
		{e.Source.Plan.PlanID, "plan"}, {e.Source.Plan.RevisionID, "plan-revision"},
		{e.Source.SourceProjectionRevisionID.UUID(), "source-projection-revision"},
		{e.RevisionID, goldenExecutionRevisionRole(e.Revision)}, {e.Attempt.ID, "golden-attempt"},
		{e.Wave.ID, "golden-wave"}, {e.Wave.RevisionID.UUID(), "golden-wave-revision"},
		{e.Window.ID, "golden-window"}, {e.Wave.ReadyWindow.RevisionID.UUID(), "golden-wave-window-revision"},
		{e.Window.RevisionID, goldenExecutionWindowRevisionRole(e.Window.Revision)},
		{e.Window.ReadinessRevisionID, goldenExecutionReadinessRevisionRole(e.Window.ReadinessRevision)},
		{e.Window.PresenceRevisionID, goldenExecutionPresenceRevisionRole(e.Window.PresenceRevision)},
		{e.Membership.ID, "golden-membership-binding"}, {e.Membership.RevisionID, "golden-membership-binding-revision"},
		{e.Assignment.ID, "golden-assignment"}, {e.Assignment.RevisionID, "golden-assignment-revision"},
		{e.Assignment.EdgeID, "plan-edge"}, {e.Assignment.ReservationID, "task-reservation"},
		{e.Assignment.Snapshot.SnapshotID, "task-snapshot"}, {e.Assignment.Snapshot.TaskID, "task"},
	}
	if e.PreviousRevisionID != nil {
		roles = append(roles, waveGoldenIdentityRole{*e.PreviousRevisionID, goldenExecutionRevisionRole(e.Revision - 1)})
	}
	for _, private := range e.Assignment.Private {
		roles = append(roles, waveGoldenIdentityRole{private.ID, "golden-private-assignment:" + private.ParticipantID.String()})
	}
	for _, receipt := range e.Receipts {
		roles = append(roles,
			waveGoldenIdentityRole{receipt.CommandID, "golden-command:" + receipt.CommandID.String()},
			waveGoldenIdentityRole{receipt.Result.RevisionID, goldenExecutionRevisionRole(receipt.Result.Revision)},
			waveGoldenIdentityRole{receipt.Result.Window.RevisionID,
				goldenExecutionWindowRevisionRole(receipt.Result.Window.Revision)},
			waveGoldenIdentityRole{receipt.Result.Window.ReadinessRevisionID,
				goldenExecutionReadinessRevisionRole(receipt.Result.Window.ReadinessRevision)},
			waveGoldenIdentityRole{receipt.Result.Window.PresenceRevisionID,
				goldenExecutionPresenceRevisionRole(receipt.Result.Window.PresenceRevision)},
		)
		if receipt.Expected != nil {
			roles = append(roles,
				waveGoldenIdentityRole{receipt.Expected.RevisionID,
					goldenExecutionRevisionRole(receipt.Expected.Revision)},
				waveGoldenIdentityRole{receipt.Expected.Window.RevisionID,
					goldenExecutionWindowRevisionRole(receipt.Expected.Window.Revision)},
				waveGoldenIdentityRole{receipt.Expected.Window.ReadinessRevisionID,
					goldenExecutionReadinessRevisionRole(receipt.Expected.Window.ReadinessRevision)},
				waveGoldenIdentityRole{receipt.Expected.Window.PresenceRevisionID,
					goldenExecutionPresenceRevisionRole(receipt.Expected.Window.PresenceRevision)},
			)
		}
		for index, identity := range receipt.UnusedIdentityIDs {
			roles = append(roles, waveGoldenIdentityRole{
				identity,
				fmt.Sprintf("golden-command-unused:%s:%d", receipt.CommandID, index),
			})
		}
	}
	owners := make(map[uuid.UUID]string, len(roles))
	for _, role := range roles {
		if role.value == uuid.Nil {
			continue
		}
		if owner, exists := owners[role.value]; exists && owner != role.role {
			return goldenWaveError("identity is reused across execution roles")
		}
		owners[role.value] = role.role
	}
	return nil
}

func goldenExecutionRevisionRole(revision int64) string {
	return fmt.Sprintf("golden-execution-revision:%d", revision)
}

func goldenExecutionSourceStateRole(revision int64) string {
	return fmt.Sprintf("source-state-revision:%d", revision)
}

func goldenExecutionSourceMembershipRole(revision int64) string {
	return fmt.Sprintf("source-membership-revision:%d", revision)
}

func goldenExecutionWindowRevisionRole(revision int64) string {
	return fmt.Sprintf("golden-window-state-revision:%d", revision)
}

func goldenExecutionReadinessRevisionRole(revision int64) string {
	return fmt.Sprintf("golden-readiness-revision:%d", revision)
}

func goldenExecutionPresenceRevisionRole(revision int64) string {
	return fmt.Sprintf("golden-presence-revision:%d", revision)
}
