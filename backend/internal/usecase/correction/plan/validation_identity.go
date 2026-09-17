package plan

import (
	"github.com/google/uuid"

	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

type correctionIdentityRole struct {
	id   uuid.UUID
	role string
}

func validateCorrectionUUIDRoles(
	command Command,
	authority Authority,
	intents []ProjectionIntent,
	snapshot resultprojection.RevisionDAGSnapshot,
) error {
	roles := newCorrectionRevisionDAGIdentityRegistry()
	if err := claimCorrectionDAGIdentities(roles, authority, snapshot); err != nil {
		return err
	}
	if err := claimCorrectionAuthorityIdentities(roles, command, authority); err != nil {
		return err
	}
	authorityIDs := make(map[uuid.UUID]struct{}, len(roles.roles))
	for id := range roles.roles {
		authorityIDs[id] = struct{}{}
	}
	proposed := correctionProposedIdentityRoles(command, intents)
	return claimCorrectionProposedIdentities(roles, proposed, authorityIDs, command.OperatorID)
}

func claimCorrectionDAGIdentities(
	roles *correctionRevisionDAGIdentityRegistry,
	authority Authority,
	snapshot resultprojection.RevisionDAGSnapshot,
) error {
	for _, projection := range snapshot.Projections {
		if err := claimCorrectionRevisionDAGGraphIdentity(roles, projection.Revision()); err != nil {
			return correctionIdentityAlias()
		}
	}
	for _, input := range authority.DAG.Inputs() {
		if input.NoGame != nil {
			if err := claimCorrectionNoGameDAGIdentities(roles, *input.NoGame); err != nil {
				return correctionIdentityAlias()
			}
			continue
		}
		if err := claimCorrectionOrdinaryDAGIdentities(roles, input); err != nil {
			return correctionIdentityAlias()
		}
	}
	if err := roles.validateCommandUses(); err != nil {
		return correctionIdentityAlias()
	}
	return nil
}

func claimCorrectionAuthorityIdentities(
	roles *correctionRevisionDAGIdentityRegistry,
	command Command,
	authority Authority,
) error {
	for _, value := range correctionAuthorityIdentityRoles(command, authority) {
		if err := claimCorrectionIdentity(roles, value); err != nil {
			return err
		}
	}
	return nil
}

func correctionAuthorityIdentityRoles(command Command, authority Authority) []correctionIdentityRole {
	values := make([]correctionIdentityRole, 0)
	if !correctionReadinessIsEmpty(authority.Readiness) {
		values = append(values,
			correctionIdentityRole{authority.Readiness.OwnerID, "Series"},
			correctionIdentityRole{authority.Readiness.WaveID, "Wave"},
			correctionIdentityRole{authority.Readiness.WindowID, "window"},
			correctionIdentityRole{authority.Readiness.RevisionID, "readiness revision"},
		)
		for _, participantID := range authority.Readiness.ParticipantIDs {
			values = append(values, correctionIdentityRole{participantID, "participant"})
		}
	}
	for _, reservation := range authority.Reservations {
		values = append(values,
			correctionIdentityRole{reservation.ID, "reservation"},
			correctionIdentityRole{reservation.OwnerID, "Series"},
			correctionIdentityRole{reservation.SourceRevisionID.UUID(), "projection revision"},
		)
	}
	for _, decision := range authority.Decisions {
		values = append(values,
			correctionIdentityRole{decision.ID, "projection decision"},
			correctionIdentityRole{decision.ProjectionRevisionID.UUID(), "projection revision"},
		)
	}
	for _, event := range authority.CutoffEvents {
		values = append(values,
			correctionIdentityRole{event.ID, "cutoff event"},
			correctionIdentityRole{event.SourceRevisionID.UUID(), "projection revision"},
		)
	}
	if authority.CurrentSolve.SubmissionID != nil {
		values = append(values, correctionIdentityRole{*authority.CurrentSolve.SubmissionID, "submission"})
	}
	if command.Patch.SolveMetadata.SubmissionID != nil {
		values = append(values, correctionIdentityRole{*command.Patch.SolveMetadata.SubmissionID, "submission"})
	}
	return values
}

func claimCorrectionIdentity(
	roles *correctionRevisionDAGIdentityRegistry,
	value correctionIdentityRole,
) error {
	if value.id == uuid.Nil || roles.claimRole(value.id, value.role) != nil {
		return correctionIdentityAlias()
	}
	return nil
}

func correctionProposedIdentityRoles(
	command Command,
	intents []ProjectionIntent,
) []correctionIdentityRole {
	proposed := make([]correctionIdentityRole, 0, 7+2*len(intents))
	proposed = append(proposed,
		correctionIdentityRole{command.CommandID, "command"},
		correctionIdentityRole{command.CascadeCommandID, "command"},
		correctionIdentityRole{command.OperatorID, "actor"},
		correctionIdentityRole{command.NextResultRevisionID.UUID(), "official result revision"},
		correctionIdentityRole{command.NextScoreRevisionID.UUID(), "score revision"},
		correctionIdentityRole{command.NextSeriesResultRevisionID.UUID(), "official result revision"},
		correctionIdentityRole{command.NextReadinessRevisionID, "readiness revision"},
	)
	for _, intent := range intents {
		proposed = append(proposed,
			correctionIdentityRole{intent.NextRevisionID.UUID(), "projection revision"},
			correctionIdentityRole{intent.DecisionID, "projection decision"},
		)
	}
	return proposed
}

func claimCorrectionProposedIdentities(
	roles *correctionRevisionDAGIdentityRegistry,
	proposed []correctionIdentityRole,
	authorityIDs map[uuid.UUID]struct{},
	operatorID uuid.UUID,
) error {
	seen := make(map[uuid.UUID]struct{}, len(proposed))
	for _, value := range proposed {
		if value.id == uuid.Nil {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "missing proposed identity",
			)
		}
		if _, duplicate := seen[value.id]; duplicate {
			return correctionIdentityAlias()
		}
		seen[value.id] = struct{}{}
		if value.id != operatorID {
			if _, exists := authorityIDs[value.id]; exists {
				return correctionIdentityAlias()
			}
		}
		if err := claimCorrectionIdentity(roles, value); err != nil {
			return err
		}
	}
	return nil
}

func correctionIdentityAlias() error {
	return rejectCorrection(
		RejectionIdentityAlias, ErrInvalid, "correction identity has multiple roles or owners",
	)
}

func correctionExpectationsEqual(first, second Expectation) bool {
	return first.TournamentState == second.TournamentState &&
		first.TournamentRevision == second.TournamentRevision &&
		first.CutoffEventDigest == second.CutoffEventDigest &&
		correctionDerivedRevisionsEqual(first.TargetProjection, second.TargetProjection) &&
		correctionDerivedRevisionsEqual(first.ScoreProjection, second.ScoreProjection) &&
		correctionDerivedRevisionsEqual(first.SeriesProjection, second.SeriesProjection) &&
		first.ResultRevisionID == second.ResultRevisionID &&
		first.ScoreRevisionID == second.ScoreRevisionID &&
		first.SeriesResultRevisionID == second.SeriesResultRevisionID &&
		correctionExpectationEvidenceEqual(first, second)
}

func correctionExpectationEvidenceEqual(first, second Expectation) bool {
	return first.SeriesRevision == second.SeriesRevision && first.AttemptRevision == second.AttemptRevision &&
		first.DAGDigest == second.DAGDigest && first.ReservationDigest == second.ReservationDigest &&
		first.DecisionDigest == second.DecisionDigest && first.ReadinessDigest == second.ReadinessDigest &&
		first.CurrentSolveDigest == second.CurrentSolveDigest
}
