package progression

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
)

func planStartGolden(
	command Command,
	authority Authority,
	evidence SwissEvidence,
	now time.Time,
) (Plan, error) {
	finalSwiss, source, err := verifyFinalSwiss(authority, evidence)
	if err != nil {
		return Plan{}, err
	}
	if finalSwiss.AdvanceDirectly() || len(finalSwiss.GoldenGroups()) == 0 {
		return Plan{}, progressionConflict("Swiss has no impactful exact tie")
	}
	tieGroups, err := goldenTieGroupRecords(command, finalSwiss)
	if err != nil {
		return Plan{}, err
	}
	proof, digest, err := goldenProof(command, authority, evidence, tieGroups, now)
	if err != nil {
		return Plan{}, err
	}
	groups := finalSwiss.GoldenGroups()
	return Plan{
		Record: Record{
			Command: command, Source: source,
			SourceState: authority.Tournament.State, Proof: proof, ProofDigest: digest, TieGroups: tieGroups,
		},
		FinalSwiss: finalSwiss, GoldenGroups: groups,
	}, nil
}

func planStartPlayoffsFromSwiss(
	command Command,
	authority Authority,
	evidence SwissEvidence,
	now time.Time,
) (Plan, error) {
	finalSwiss, source, err := verifyFinalSwiss(authority, evidence)
	if err != nil {
		return Plan{}, err
	}
	if !finalSwiss.AdvanceDirectly() || len(finalSwiss.GoldenGroups()) != 0 {
		return Plan{}, progressionConflict("Swiss still has impactful exact ties")
	}
	evidence.Current = source
	return planPlayoffArtifacts(command, authority, finalSwiss, nil, nil, evidence, now)
}

func planStartPlayoffsFromGolden(
	command Command,
	authority Authority,
	evidence GoldenEvidence,
	now time.Time,
) (Plan, error) {
	finalSwiss, source, err := verifyFinalSwiss(authority, evidence.Swiss)
	if err != nil {
		return Plan{}, err
	}
	if finalSwiss.AdvanceDirectly() || len(finalSwiss.GoldenGroups()) == 0 {
		return Plan{}, progressionConflict("Golden has no persisted impactful tie source")
	}
	evidence.Swiss.Current = source
	return planPlayoffArtifacts(
		command,
		authority,
		finalSwiss,
		evidence.CurrentTerminalSeries,
		evidence.Settlements,
		evidence.Swiss,
		now,
	)
}

func planPlayoffArtifacts(
	command Command,
	authority Authority,
	finalSwiss playoff.FinalSwissProjection,
	terminalSeries []playoff.TerminalSeriesEvidence,
	settlements []playoff.Top4GoldenSettlement,
	swissEvidence SwissEvidence,
	now time.Time,
) (Plan, error) {
	if command.Action != ActionStartPlayoffs {
		return Plan{}, progressionConflict("playoff artifact action is invalid")
	}
	publicationIDs, err := PlayoffPublicationIdentity(command.CommandID)
	if err != nil {
		return Plan{}, progressionConflict("reserve playoff publication identities: %v", err)
	}
	if terminalSeries == nil {
		terminalSeries = terminalSeriesFromSwiss(finalSwiss, swissEvidence.FinalSwiss)
	}
	top4, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
		TournamentID:          command.TournamentID,
		RevisionID:            domain.DerivedRevisionID(progressionID(command.CommandID, "top4-revision")),
		RevisionNo:            1,
		Source:                finalSwiss,
		CurrentTerminalSeries: append([]playoff.TerminalSeriesEvidence(nil), terminalSeries...),
		GoldenSettlements:     append([]playoff.Top4GoldenSettlement(nil), settlements...),
		CreatedAt:             now,
	})
	if err != nil {
		return Plan{}, progressionConflict("build exact Top4: %v", err)
	}
	bracket, err := playoff.PlanStrengthMatchedSemifinals(playoff.SemifinalBracketCommand{
		TournamentID: command.TournamentID,
		RevisionID:   domain.DerivedRevisionID(progressionID(command.CommandID, "semifinal-bracket-revision")),
		RevisionNo:   1,
		Top4:         top4,
		SeriesIDs: [2]uuid.UUID{
			progressionID(command.CommandID, "semifinal-series-1"),
			progressionID(command.CommandID, "semifinal-series-2"),
		},
		CreatedAt: now,
	})
	if err != nil {
		return Plan{}, progressionConflict("build exact semifinal bracket: %v", err)
	}
	proof, digest, err := playoffProof(command, authority, swissEvidence, finalSwiss, top4, bracket, now)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Record: Record{
			Command: command, Source: cloneProjectionReference(swissEvidence.Current),
			SourceState: authority.Tournament.State, Proof: proof, ProofDigest: digest,
		},
		FinalSwiss: finalSwiss, Top4: &top4, Bracket: &bracket, PublicationIDs: publicationIDs,
	}, nil
}

func verifyFinalSwiss(authority Authority, evidence SwissEvidence) (playoff.FinalSwissProjection, ProjectionReference, error) {
	if err := validateSwissEvidence(authority, evidence); err != nil {
		return playoff.FinalSwissProjection{}, ProjectionReference{}, err
	}
	canonical, err := resultprojection.BuildCanonicalMaterialization(canonicalSwissInput(evidence.Canonical))
	if err != nil {
		return playoff.FinalSwissProjection{}, ProjectionReference{}, progressionConflict("canonical Swiss ledger is invalid: %v", err)
	}
	source, err := canonicalSwissSource(evidence.Current, canonical)
	if err != nil {
		return playoff.FinalSwissProjection{}, ProjectionReference{}, err
	}
	finalSwiss, err := playoff.PlanFinalSwissProgression(evidence.FinalSwiss)
	if err != nil {
		return playoff.FinalSwissProjection{}, ProjectionReference{}, progressionConflict("final Swiss authority is incomplete: %v", err)
	}
	if !finalSwissMatchesCanonicalSwiss(finalSwiss, canonical) {
		return playoff.FinalSwissProjection{}, ProjectionReference{}, progressionConflict("final Swiss proof differs from canonical ledger")
	}
	return finalSwiss, source, nil
}

func canonicalSwissSource(current ProjectionReference, canonical resultprojection.CanonicalMaterialization) (ProjectionReference, error) {
	if !currentMatchesCanonicalSwiss(current, canonical) {
		return ProjectionReference{}, progressionConflict("published standings differ from canonical Swiss ledger")
	}
	payload := canonical.Artifacts[0].Payload
	if !json.Valid(payload) || sha256.Sum256(payload) != current.Digest {
		return ProjectionReference{}, progressionConflict("canonical Swiss payload does not match its digest")
	}
	source := cloneProjectionReference(current)
	source.Payload = append([]byte(nil), payload...)
	return source, nil
}

func canonicalSwissInput(input resultprojection.CanonicalMaterializationInput) resultprojection.CanonicalMaterializationInput {
	clone := input
	clone.Participants = append([]resultprojection.CanonicalSwissParticipant(nil), input.Participants...)
	clone.SwissLedger = append([]resultprojection.CanonicalSwissPointLedgerEntry(nil), input.SwissLedger...)
	clone.TopFour = nil
	clone.Bracket = nil
	clone.SwissComplete = true
	clone.ArtifactKinds = []domain.ArtifactKind{domain.ArtifactKindStandings}
	return clone
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validateSwissEvidence(authority Authority, evidence SwissEvidence) error {
	if evidence.ExpectedRounds < 1 || evidence.ExpectedRounds != evidence.TerminalRounds ||
		evidence.ExpectedWaves != evidence.TerminalWaves || evidence.ExpectedWaves != evidence.ExpectedRounds ||
		evidence.ExpectedSeries < 1 || evidence.ExpectedSeries != evidence.TerminalSeries ||
		evidence.Current.RevisionID != authority.ProjectionRevisionID ||
		evidence.Current.Revision != authority.ProjectionRevision ||
		evidence.Current.Kind != domain.ArtifactKindStandings ||
		evidence.FinalSwiss.TournamentID != authority.Tournament.ID ||
		evidence.FinalSwiss.Preset != authority.Tournament.Preset ||
		evidence.FinalSwiss.RevisionID.UUID() != authority.ProjectionRevisionID ||
		// The canonical Final Swiss receipt counter is intentionally distinct
		// from the physical projection_revisions counter. A receipt can begin
		// at physical revision N after earlier non-Final-Swiss publications.
		evidence.FinalSwiss.PhysicalProjectionRevision != int(authority.ProjectionRevision) ||
		evidence.Canonical.TournamentID != authority.Tournament.ID || !evidence.Canonical.SwissComplete {
		return progressionConflict("Swiss stage evidence is partial or stale")
	}
	if rounds := len(evidence.FinalSwiss.Rounds); rounds != evidence.ExpectedRounds {
		return progressionConflict("Swiss proof does not cover every expected round")
	}
	seriesCount := 0
	for _, round := range evidence.FinalSwiss.Rounds {
		seriesCount += len(round.Series)
	}
	if seriesCount != evidence.ExpectedSeries {
		return progressionConflict("Swiss proof does not cover every expected Series")
	}
	return nil
}

func currentMatchesCanonicalSwiss(
	current ProjectionReference,
	materialization resultprojection.CanonicalMaterialization,
) bool {
	if current.ArtifactID == uuid.Nil || current.RevisionID == uuid.Nil || current.Revision < 1 ||
		current.Kind != domain.ArtifactKindStandings || len(materialization.Artifacts) != 1 {
		return false
	}
	artifact := materialization.Artifacts[0]
	if artifact.Kind != domain.ArtifactKindStandings || artifact.PayloadDigest != current.Digest ||
		len(artifact.Members) != len(current.Members) {
		return false
	}
	for index, member := range artifact.Members {
		currentMember := current.Members[index]
		if member.ParticipantID != currentMember.ParticipantID || member.Position != currentMember.Position ||
			!sameScore(member.ScoreMilli, currentMember.ScoreMilli) {
			return false
		}
	}
	return true
}

func finalSwissMatchesCanonicalSwiss(
	finalSwiss playoff.FinalSwissProjection,
	materialization resultprojection.CanonicalMaterialization,
) bool {
	if finalSwiss.Validate() != nil || len(materialization.Artifacts) != 1 {
		return false
	}
	members := materialization.Artifacts[0].Members
	standings := finalSwiss.Standings()
	if len(members) != len(standings) {
		return false
	}
	for index, standing := range standings {
		if members[index].ParticipantID != standing.ParticipantID || members[index].Position != standing.Position {
			return false
		}
		points := int64(standing.Points) * 1000
		if !sameScore(members[index].ScoreMilli, &points) {
			return false
		}
	}
	return true
}

func sameScore(first, second *int64) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func goldenTieGroupRecords(command Command, finalSwiss playoff.FinalSwissProjection) ([]TieGroupRecord, error) {
	groups := finalSwiss.GoldenGroups()
	ties := finalSwiss.TieGroups()
	if len(groups) == 0 {
		return nil, progressionConflict("missing impactful tie groups")
	}
	records := make([]TieGroupRecord, len(groups))
	for index, group := range groups {
		state := group.State
		if state.ID != progressionID(command.CommandID, goldenGroupRole(state.PositionFrom, state.PositionTo, "id")) ||
			state.RevisionID.UUID() != progressionID(command.CommandID, goldenGroupRole(state.PositionFrom, state.PositionTo, "revision")) {
			return nil, progressionConflict("Golden group identity is not server deterministic")
		}
		participants := make([]uuid.UUID, len(state.Members))
		for memberIndex, member := range state.Members {
			participants[memberIndex] = member.ParticipantID
		}
		if !matchesImpactfulTie(ties, state.PositionFrom, state.PositionTo, participants) {
			return nil, progressionConflict("Golden group is not an exact maximal impactful tie")
		}
		proof, err := json.Marshal(struct {
			GroupID      uuid.UUID   `json:"group_id"`
			RevisionID   uuid.UUID   `json:"revision_id"`
			PositionFrom int         `json:"position_from"`
			PositionTo   int         `json:"position_to"`
			Participants []uuid.UUID `json:"participants"`
		}{
			GroupID: state.ID, RevisionID: state.RevisionID.UUID(), PositionFrom: state.PositionFrom,
			PositionTo: state.PositionTo, Participants: participants,
		})
		if err != nil {
			return nil, progressionConflict("encode Golden tie proof: %v", err)
		}
		records[index] = TieGroupRecord{
			GroupID: state.ID, RevisionID: state.RevisionID.UUID(), PositionFrom: state.PositionFrom,
			PositionTo: state.PositionTo, Participants: participants, Proof: proof, ProofDigest: sha256.Sum256(proof),
		}
	}
	return records, nil
}

func matchesImpactfulTie(
	ties []playoff.FinalSwissTieGroup,
	positionFrom, positionTo int,
	participants []uuid.UUID,
) bool {
	for _, tie := range ties {
		if tie.Impactful && tie.PositionFrom == positionFrom && tie.PositionTo == positionTo &&
			slices.Equal(tie.ParticipantIDs, participants) {
			return true
		}
	}
	return false
}

func terminalSeriesFromSwiss(
	finalSwiss playoff.FinalSwissProjection,
	input playoff.ProgressionSwissInput,
) []playoff.TerminalSeriesEvidence {
	if finalSwiss.Validate() != nil {
		return nil
	}
	count := 0
	for _, round := range input.Rounds {
		count += len(round.Series)
	}
	series := make([]playoff.TerminalSeriesEvidence, 0, count)
	for _, round := range input.Rounds {
		series = append(series, round.Series...)
	}
	return series
}

func progressionID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("tournament-stage-progression:"+role))
}

func goldenGroupRole(positionFrom, positionTo int, suffix string) string {
	return fmt.Sprintf("golden-group:%d:%d:%s", positionFrom, positionTo, suffix)
}

func cloneProjectionReference(input ProjectionReference) ProjectionReference {
	clone := input
	clone.Members = make([]ProjectionMember, len(input.Members))
	clone.Payload = append([]byte(nil), input.Payload...)
	for index, member := range input.Members {
		clone.Members[index] = member
		if member.ScoreMilli != nil {
			value := *member.ScoreMilli
			clone.Members[index].ScoreMilli = &value
		}
	}
	return clone
}
