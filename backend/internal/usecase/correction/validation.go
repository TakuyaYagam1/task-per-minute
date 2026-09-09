package correction

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	maxCorrectionExplanationBytes = 512
	maxCorrectionFields           = 3
	maxCorrectionReservations     = 4096
	maxCorrectionUnlockIntents    = 4096
	maxCorrectionSeriesAttempts   = 16
)

func (v Validation) Validate() error {
	if err := preflightCorrectionInputs(v.command, v.authority); err != nil {
		return err
	}
	if correctionValidationDigest(v) != v.bindingDigest {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "correction validation was spliced",
		)
	}
	validated, err := validateCorrection(
		cloneCorrectionCommand(v.command), cloneCorrectionAuthority(v.authority),
	)
	if err != nil {
		return err
	}
	if validated.bindingDigest != v.bindingDigest ||
		!correctionDerivedRevisionsEqual(validated.target, v.target) {
		return rejectCorrection(
			RejectionStale, ErrInvalid, "correction validation was spliced",
		)
	}
	return nil
}

func Validate(
	command Command,
	authority Authority,
) (Validation, error) {
	if err := preflightCorrectionInputs(command, authority); err != nil {
		return Validation{}, err
	}
	return validateCorrection(cloneCorrectionCommand(command), cloneCorrectionAuthority(authority))
}

func validateCorrection(
	command Command,
	authority Authority,
) (Validation, error) {
	if err := validateCorrectionCommandShape(command); err != nil {
		return Validation{}, err
	}
	snapshot := authority.DAG.Snapshot()
	cutoffInput := CutoffInput{
		DAG: authority.DAG, TournamentID: command.TournamentID,
		TargetRevisionID: command.Expected.TargetProjection.ID(),
		TournamentState:  authority.TournamentState, Events: authority.CutoffEvents,
	}
	if err := preflightCorrectionCutoff(cutoffInput, snapshot); err != nil {
		return Validation{}, err
	}
	if err := validateCorrectionAuthority(authority, snapshot); err != nil {
		return Validation{}, err
	}
	cutoff, err := evaluateCorrectionCutoffPrepared(cutoffInput, snapshot)
	if err != nil {
		return Validation{}, err
	}
	actual, target, err := buildCorrectionExpectation(authority, cutoff, snapshot)
	if err != nil {
		return Validation{}, err
	}
	if !correctionExpectationsEqual(command.Expected, actual) {
		return Validation{}, rejectCorrection(
			RejectionStale, ErrInvalid, "correction expectation changed",
		)
	}
	if err := validateCorrectionPatch(command, authority); err != nil {
		return Validation{}, err
	}
	intents, err := validateCorrectionProjectionIntents(command, cutoff, target)
	if err != nil {
		return Validation{}, err
	}
	unlocks, err := validateCorrectionUnlockIntents(command, authority, cutoff, target)
	if err != nil {
		return Validation{}, err
	}
	if err := validateCorrectionUUIDRoles(command, authority, intents, snapshot); err != nil {
		return Validation{}, err
	}
	command.ProjectionIntents = intents
	command.UnlockIntents = unlocks
	command.Fields = canonicalCorrectionFields(command.Fields)
	authority.Reservations = canonicalCorrectionReservations(authority.Reservations)
	authority.Decisions = canonicalCorrectionDecisions(authority.Decisions)
	authority.Readiness.ParticipantIDs = canonicalCorrectionParticipants(
		authority.Readiness.ParticipantIDs,
	)
	validation := Validation{
		command: command, authority: authority, cutoff: cutoff, target: target, unlocks: unlocks,
		snapshot: snapshot,
	}
	validation.bindingDigest = correctionValidationDigest(validation)
	return validation, nil
}

func NewExpectation(
	authority Authority,
	targetRevisionID domain.DerivedRevisionID,
) (Expectation, error) {
	if err := preflightCorrectionAuthority(authority); err != nil {
		return Expectation{}, err
	}
	cloned := cloneCorrectionAuthority(authority)
	snapshot := cloned.DAG.Snapshot()
	cutoffInput := CutoffInput{
		DAG: cloned.DAG, TournamentID: cloned.Series.TournamentID,
		TargetRevisionID: targetRevisionID, TournamentState: cloned.TournamentState,
		Events: cloned.CutoffEvents,
	}
	if err := preflightCorrectionCutoff(cutoffInput, snapshot); err != nil {
		return Expectation{}, err
	}
	if err := validateCorrectionAuthority(cloned, snapshot); err != nil {
		return Expectation{}, err
	}
	cutoff, err := evaluateCorrectionCutoffPrepared(cutoffInput, snapshot)
	if err != nil {
		return Expectation{}, err
	}
	expectation, _, err := buildCorrectionExpectation(cloned, cutoff, snapshot)
	if err != nil {
		return Expectation{}, err
	}
	return expectation, nil
}
