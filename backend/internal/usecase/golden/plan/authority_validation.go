package plan

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	"github.com/google/uuid"
)

func BuildAuthority(input Authority) (Authority, error) {
	canonical, evidence, err := normalizeGoldenExactPlanAuthority(input)
	if err != nil {
		return Authority{}, err
	}
	canonical.Evidence = evidence
	return canonical.Snapshot(), nil
}

func normalizeGoldenExactPlanAuthority(
	input Authority,
) (Authority, Expectation, error) {
	if err := validateGoldenSourceAuthority(input); err != nil {
		return Authority{}, Expectation{}, err
	}
	if err := validateGoldenRevisionIdentitySet(input.Scope, input.Revisions); err != nil {
		return Authority{}, Expectation{}, err
	}
	groups, participants, err := normalizeGoldenPlanGroups(input.Source, input.Groups)
	if err != nil {
		return Authority{}, Expectation{}, err
	}
	if err := validateGoldenAuthorityAliases(input.Scope, input.Revisions, input.Source, groups); err != nil {
		return Authority{}, Expectation{}, err
	}
	pool, err := domain.NormalizeTaskPoolRevision(input.Pool, domain.AssignmentTaskKindGolden)
	if err != nil || pool.ID != input.Revisions.PoolRevisionID || pool.Revision != input.Revisions.PoolRevision {
		return Authority{}, Expectation{}, goldenExactPlanError("invalid Golden pool authority")
	}
	candidates, err := normalizeGoldenExactCandidates(pool, input.Candidates)
	if err != nil {
		return Authority{}, Expectation{}, err
	}
	history, err := normalizeGoldenHistory(participants, input.History)
	if err != nil {
		return Authority{}, Expectation{}, err
	}
	participantReservations, err := normalizeGoldenParticipantReservations(input.Scope, participants, input.ParticipantReservations)
	if err != nil {
		return Authority{}, Expectation{}, err
	}
	taskReservations, err := normalizeGoldenTaskReservations(pool, input.ExistingTaskReservations)
	if err != nil {
		return Authority{}, Expectation{}, err
	}
	canonical := input
	canonical.Source = input.Source.Snapshot()
	canonical.Groups = groups
	canonical.Pool = pool
	canonical.History = history
	canonical.Candidates = candidates
	canonical.ParticipantReservations = participantReservations
	canonical.ExistingTaskReservations = taskReservations
	if err := validateGoldenRetainedAuthorityAliases(canonical); err != nil {
		return Authority{}, Expectation{}, err
	}
	evidence, err := goldenAuthorityEvidence(canonical)
	if err != nil {
		return Authority{}, Expectation{}, err
	}
	return canonical, evidence, nil
}

func validateGoldenSourceAuthority(input Authority) error {
	if !validGoldenExactPlanScope(input.Scope) || !validGoldenExactPlanRevisions(input.Revisions) ||
		input.Source.Validate() != nil || input.Source.TournamentID != input.Scope.TournamentID ||
		input.Revisions.SourceProjectionRevisionID != input.Source.RevisionID {
		return goldenExactPlanError("invalid source authority")
	}
	return nil
}

func validateGoldenRetainedAuthorityAliases(authority Authority) error {
	ids := AuthorityIdentityIDs(authority)
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return goldenExactPlanError("missing retained authority identity")
		}
		if _, duplicate := seen[id]; duplicate {
			return goldenExactPlanError("retained authority identity is cross-aliased")
		}
		seen[id] = struct{}{}
	}
	ownerPlans := make(map[uuid.UUID]uuid.UUID, len(authority.ExistingTaskReservations))
	ownerRevisions := make(map[uuid.UUID]uuid.UUID, len(authority.ExistingTaskReservations))
	for _, reservation := range authority.ExistingTaskReservations {
		if _, alias := seen[reservation.PlanID]; alias {
			return goldenExactPlanError("task reservation owner aliases retained authority")
		}
		if _, alias := seen[reservation.PlanRevisionID]; alias {
			return goldenExactPlanError("task reservation owner revision aliases retained authority")
		}
		if _, crossRole := ownerRevisions[reservation.PlanID]; crossRole {
			return goldenExactPlanError("task reservation owner identity changes role")
		}
		if _, crossRole := ownerPlans[reservation.PlanRevisionID]; crossRole {
			return goldenExactPlanError("task reservation owner revision changes role")
		}
		if revisionID, exists := ownerPlans[reservation.PlanID]; exists && revisionID != reservation.PlanRevisionID {
			return goldenExactPlanError("task reservation owner has multiple revisions")
		}
		if planID, exists := ownerRevisions[reservation.PlanRevisionID]; exists && planID != reservation.PlanID {
			return goldenExactPlanError("task reservation revision has multiple owners")
		}
		ownerPlans[reservation.PlanID] = reservation.PlanRevisionID
		ownerRevisions[reservation.PlanRevisionID] = reservation.PlanID
	}
	return nil
}

// AuthorityIdentityIDs returns every identity bound by the exact plan authority.
func AuthorityIdentityIDs(authority Authority) []uuid.UUID {
	historicalCount := 0
	if authority.Source.PreviousRevisionID != nil {
		historicalCount++
	}
	for _, group := range authority.Groups {
		if group.Revision.PreviousRevisionID() != nil {
			historicalCount++
		}
	}
	capacity := 11 + 2*len(authority.Groups) + len(authority.Candidates) +
		3*len(authority.ParticipantReservations) + len(authority.ExistingTaskReservations) + historicalCount
	ids := make([]uuid.UUID, 0, capacity)
	ids = append(ids,
		authority.Scope.TournamentID, authority.Scope.PlanSetID, authority.Source.ProjectionID,
		authority.Revisions.SourceProjectionRevisionID.UUID(), authority.Revisions.GroupSetRevisionID,
		authority.Revisions.PoolRevisionID, authority.Revisions.HistoryRevisionID,
		authority.Revisions.TaskHealthRevisionID, authority.Revisions.ArtifactRevisionID,
		authority.Revisions.ReservationRevisionID, authority.Revisions.MembershipRevisionID,
	)
	if authority.Source.PreviousRevisionID != nil {
		ids = append(ids, authority.Source.PreviousRevisionID.UUID())
	}
	for _, group := range authority.Groups {
		ids = append(ids, group.Revision.GroupID(), group.Revision.RevisionID().UUID())
		if previousRevisionID := group.Revision.PreviousRevisionID(); previousRevisionID != nil {
			ids = append(ids, previousRevisionID.UUID())
		}
	}
	for _, candidate := range authority.Candidates {
		ids = append(ids, candidate.Task.ID)
	}
	for _, reservation := range authority.ParticipantReservations {
		ids = append(ids, reservation.ParticipantID, reservation.PlayerID, reservation.Reservation.ReservationID)
	}
	for _, reservation := range authority.ExistingTaskReservations {
		ids = append(ids, reservation.ReservationID)
	}
	return ids
}

func validGoldenExactPlanScope(scope Scope) bool {
	return scope.TournamentID != uuid.Nil && scope.PlanSetID != uuid.Nil && scope.TournamentID != scope.PlanSetID
}

func validGoldenExactPlanRevisions(r Revisions) bool {
	return !r.SourceProjectionRevisionID.IsZero() &&
		r.GroupSetRevisionID != uuid.Nil && r.GroupSetRevision >= 1 &&
		r.PoolRevisionID != uuid.Nil && r.PoolRevision >= 1 &&
		r.HistoryRevisionID != uuid.Nil && r.HistoryRevision >= 1 &&
		r.TaskHealthRevisionID != uuid.Nil && r.TaskHealthRevision >= 1 &&
		r.ArtifactRevisionID != uuid.Nil && r.ArtifactRevision >= 1 &&
		r.ReservationRevisionID != uuid.Nil && r.ReservationRevision >= 1 &&
		r.MembershipRevisionID != uuid.Nil && r.MembershipRevision >= 1
}

func validateGoldenRevisionIdentitySet(scope Scope, revisions Revisions) error {
	ids := []uuid.UUID{
		scope.TournamentID, scope.PlanSetID, revisions.SourceProjectionRevisionID.UUID(),
		revisions.GroupSetRevisionID, revisions.PoolRevisionID, revisions.HistoryRevisionID,
		revisions.TaskHealthRevisionID, revisions.ArtifactRevisionID,
		revisions.ReservationRevisionID, revisions.MembershipRevisionID,
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			return goldenExactPlanError("authority revision identity is reused")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateGoldenAuthorityAliases(
	scope Scope,
	revisions Revisions,
	source StandingsProjection,
	groups []GroupAuthority,
) error {
	ids := make([]uuid.UUID, 0, 11+2*len(groups))
	ids = append(ids,
		scope.TournamentID, scope.PlanSetID, source.ProjectionID,
		revisions.SourceProjectionRevisionID.UUID(), revisions.GroupSetRevisionID,
		revisions.PoolRevisionID, revisions.HistoryRevisionID, revisions.TaskHealthRevisionID,
		revisions.ArtifactRevisionID, revisions.ReservationRevisionID, revisions.MembershipRevisionID,
	)
	for _, group := range groups {
		ids = append(ids, group.Revision.GroupID(), group.Revision.RevisionID().UUID())
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return goldenExactPlanError("missing authority identity")
		}
		if _, duplicate := seen[id]; duplicate {
			return goldenExactPlanError("authority identity is aliased")
		}
		seen[id] = struct{}{}
	}
	return nil
}
