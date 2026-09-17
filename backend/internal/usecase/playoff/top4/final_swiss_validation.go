package top4

import (
	"bytes"
	"math"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func canonicalFinalSwissAuthority(command FinalSwissProjectionCommand) (finalSwissAuthority, error) {
	if err := validateFinalSwissCommandHeader(command); err != nil {
		return finalSwissAuthority{}, err
	}
	if err := preflightFinalSwissCardinality(command); err != nil {
		return finalSwissAuthority{}, err
	}
	participants, err := swissusecase.CanonicalLedgerParticipants(command.ParticipantIDs)
	if err != nil || !command.Preset.ValidRosterSize(len(participants)) {
		return finalSwissAuthority{}, finalSwissError("invalid roster: %v", err)
	}
	seeds, err := canonicalFinalSwissSeeds(participants, command.Seeds)
	if err != nil {
		return finalSwissAuthority{}, err
	}
	roundCount, err := command.Preset.SwissRounds(len(participants))
	if err != nil || len(command.Rounds) != roundCount {
		return finalSwissAuthority{}, finalSwissError("Swiss round coverage is incomplete")
	}
	rounds := canonicalFinalSwissRounds(command.Rounds)
	goldenGroups := canonicalFinalSwissGoldenIdentities(command.GoldenGroups)
	previous, err := validateFinalSwissPredecessor(command)
	if err != nil {
		return finalSwissAuthority{}, err
	}
	authority := finalSwissAuthority{
		TournamentID: command.TournamentID, Preset: command.Preset, ProjectionID: command.ProjectionID,
		RevisionID: command.RevisionID, RevisionNo: command.RevisionNo,
		PhysicalProjectionRevision: command.PhysicalProjectionRevision,
		Previous:                   previous, ParticipantIDs: participants, Seeds: seeds, Rounds: rounds,
		GoldenGroups: goldenGroups, CreatedAt: command.CreatedAt,
	}
	if err := validateFinalSwissIdentityRoles(authority); err != nil {
		return finalSwissAuthority{}, err
	}
	return authority, nil
}

func preflightFinalSwissCardinality(command FinalSwissProjectionCommand) error {
	participantCount := len(command.ParticipantIDs)
	if participantCount < domain.TournamentMinParticipants || participantCount > domain.TournamentMaxParticipants ||
		len(command.Seeds) != participantCount || len(command.GoldenGroups) > finalSwissTop4Cutoff {
		return finalSwissError("authority cardinality exceeds tournament bounds")
	}
	roundCount, err := command.Preset.SwissRounds(participantCount)
	if err != nil || len(command.Rounds) != roundCount {
		return finalSwissError("Swiss round coverage is incomplete")
	}
	for _, round := range command.Rounds {
		if err := preflightFinalSwissRound(round, participantCount); err != nil {
			return err
		}
	}
	return nil
}

func preflightFinalSwissRound(round FinalSwissRound, participantCount int) error {
	if len(round.Series) != participantCount/2 ||
		(participantCount%2 == 0 && round.Bye != nil) ||
		(participantCount%2 == 1 && round.Bye == nil) {
		return finalSwissError("round membership cardinality is invalid")
	}
	if len(round.LockProof.RosterParticipantIDs) > domain.TournamentMaxParticipants ||
		len(round.LockProof.Series) > domain.TournamentMaxParticipants/2 {
		return finalSwissError("round lock evidence exceeds tournament bounds")
	}
	for _, head := range round.Series {
		if err := head.Validate(); err != nil {
			return finalSwissError("invalid terminal Series evidence: %v", err)
		}
	}
	return nil
}

func preflightFinalSwissHead(head terminalSeriesRecord) error {
	if len(head.Series.Slots) > head.Series.Format.WinsRequired()*2-1 {
		return finalSwissError("Series topology exceeds its format")
	}
	attemptCount := 0
	for _, slot := range head.Series.Slots {
		if len(slot.Attempts) > domain.TournamentMaxParticipants-attemptCount {
			return finalSwissError("series attempt topology exceeds tournament bounds")
		}
		attemptCount += len(slot.Attempts)
	}
	if head.OfficialResult.NoGame != nil {
		if !boundedFinalSwissNoGame(*head.OfficialResult.NoGame) {
			return finalSwissError("no-game evidence exceeds tournament bounds")
		}
		return nil
	}
	if head.OfficialResult.Score != nil &&
		(len(head.OfficialResult.Score.Attempts) > domain.TournamentMaxParticipants ||
			(head.OfficialResult.Score.CommandAttempt != nil &&
				head.OfficialResult.Score.CommandAttempt.AttemptNo > domain.TournamentMaxParticipants)) {
		return finalSwissError("score evidence exceeds tournament bounds")
	}
	return nil
}

func boundedFinalSwissNoGame(recorded resultprojection.RecordedNoGameResult) bool {
	limit := domain.TournamentMaxParticipants
	return len(recorded.GameResults) <= limit && len(recorded.Topology) <= limit &&
		len(recorded.GameSourceRevisions) <= limit && len(recorded.GameProjections) <= limit &&
		len(recorded.GameDependencies) <= limit && len(recorded.Score.GameResultRevisionIDs) <= limit
}

func validateFinalSwissCommandHeader(command FinalSwissProjectionCommand) error {
	if command.TournamentID == uuid.Nil || !command.Preset.IsValid() || command.ProjectionID == uuid.Nil ||
		command.RevisionID.IsZero() || command.RevisionNo < 1 || command.RevisionNo == math.MaxInt ||
		command.PhysicalProjectionRevision < 1 || command.PhysicalProjectionRevision == math.MaxInt ||
		!validPlayoffTime(command.CreatedAt) {
		return finalSwissError("invalid projection identity or clock")
	}
	return nil
}

func canonicalFinalSwissRounds(input []FinalSwissRound) []finalSwissRound {
	rounds := make([]finalSwissRound, len(input))
	for index, round := range input {
		rounds[index] = finalSwissRound{
			RoundID: round.RoundID, RoundNumber: round.RoundNumber, RevisionID: round.RevisionID,
			LockProof: swissusecase.CloneRoundLockProof(round.LockProof),
			Series:    make([]terminalSeriesRecord, len(round.Series)),
		}
		for seriesIndex, head := range round.Series {
			rounds[index].Series[seriesIndex] = cloneFinalSwissSeriesHead(head.record)
		}
		if round.Bye != nil {
			bye := *round.Bye
			rounds[index].Bye = &bye
		}
	}
	sort.Slice(rounds, func(i, j int) bool { return rounds[i].RoundNumber < rounds[j].RoundNumber })
	for index := range rounds {
		sort.Slice(rounds[index].Series, func(i, j int) bool {
			return bytes.Compare(
				rounds[index].Series[i].Result.SeriesID[:], rounds[index].Series[j].Result.SeriesID[:],
			) < 0
		})
	}
	return rounds
}

func canonicalFinalSwissGoldenIdentities(
	input []FinalSwissGoldenGroupIdentity,
) []FinalSwissGoldenGroupIdentity {
	groups := append([]FinalSwissGoldenGroupIdentity(nil), input...)
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].PositionFrom != groups[j].PositionFrom {
			return groups[i].PositionFrom < groups[j].PositionFrom
		}
		return groups[i].PositionTo < groups[j].PositionTo
	})
	return groups
}

func canonicalFinalSwissSeeds(
	participants []uuid.UUID,
	input []swissusecase.ParticipantSeed,
) ([]swissusecase.ParticipantSeed, error) {
	if _, err := swissusecase.SeedMap(participants, input); err != nil {
		return nil, finalSwissError("invalid stable seeds: %v", err)
	}
	seeds := append([]swissusecase.ParticipantSeed(nil), input...)
	sort.Slice(seeds, func(i, j int) bool {
		return bytes.Compare(seeds[i].ParticipantID[:], seeds[j].ParticipantID[:]) < 0
	})
	return seeds, nil
}

func validateFinalSwissPredecessor(
	command FinalSwissProjectionCommand,
) (*finalSwissPredecessorReceipt, error) {
	if command.RevisionNo == 1 {
		if command.Previous != nil {
			return nil, finalSwissError("initial projection has a predecessor")
		}
		return nil, nil
	}
	if command.Previous == nil || command.Previous.Validate() != nil {
		return nil, finalSwissError("derived projection lacks a valid predecessor")
	}
	previous := command.Previous.Projection()
	revision := previous.Revision()
	if command.Previous.state.Authority.ProjectionID != command.ProjectionID ||
		revision.TournamentID() != command.TournamentID ||
		revision.Artifact() != (domain.ArtifactRef{Kind: domain.ArtifactKindStandings, EntityID: command.TournamentID}) ||
		revision.RevisionNo()+1 != command.RevisionNo ||
		command.Previous.PhysicalProjectionRevision() >= command.PhysicalProjectionRevision ||
		command.CreatedAt.Before(revision.CreatedAt()) {
		return nil, finalSwissError("predecessor is not the current standings lineage head")
	}
	reserved, err := finalSwissAuthorityIdentitySet(command.Previous.state.Authority)
	if err != nil {
		return nil, finalSwissError("invalid predecessor identity lineage")
	}
	if command.Previous.state.Authority.Previous != nil {
		if err := mergeFinalSwissReservedIdentities(
			reserved, command.Previous.state.Authority.Previous.Reserved,
		); err != nil {
			return nil, err
		}
	}
	if len(reserved) > maxPlayoffReservedIdentities {
		return nil, finalSwissError("standings predecessor identity receipt exceeds DAG bounds")
	}
	return &finalSwissPredecessorReceipt{
		Projection:                 cloneFinalSwissDomainProjection(previous),
		ProjectionID:               command.ProjectionID,
		PhysicalProjectionRevision: command.Previous.PhysicalProjectionRevision(),
		Reserved:                   canonicalFinalSwissReservedIdentities(reserved),
	}, nil
}

func validateFinalSwissPredecessorReceipt(authority finalSwissAuthority) error {
	if authority.Previous == nil {
		if authority.RevisionNo != 1 {
			return finalSwissError("missing bounded standings predecessor receipt")
		}
		return nil
	}
	receipt := authority.Previous
	revision := receipt.Projection.Revision()
	if receipt.Projection.Validate() != nil || receipt.ProjectionID != authority.ProjectionID ||
		revision.TournamentID() != authority.TournamentID ||
		revision.Artifact() != (domain.ArtifactRef{
			Kind: domain.ArtifactKindStandings, EntityID: authority.TournamentID,
		}) || revision.RevisionNo()+1 != authority.RevisionNo ||
		authority.Previous.PhysicalProjectionRevision < 1 ||
		authority.Previous.PhysicalProjectionRevision >= authority.PhysicalProjectionRevision ||
		authority.CreatedAt.Before(revision.CreatedAt()) {
		return finalSwissError("invalid bounded standings predecessor receipt")
	}
	return validateFinalSwissReservedReceipt(receipt, revision.ID().UUID())
}

func validateFinalSwissReservedReceipt(
	receipt *finalSwissPredecessorReceipt,
	revisionID uuid.UUID,
) error {
	if len(receipt.Reserved) == 0 || len(receipt.Reserved) > maxPlayoffReservedIdentities {
		return finalSwissError("standings predecessor identity receipt exceeds DAG bounds")
	}
	foundProjection, foundRevision := false, false
	for index, identity := range receipt.Reserved {
		if identity.ID == uuid.Nil || identity.Role == "" ||
			(index > 0 && bytes.Compare(receipt.Reserved[index-1].ID[:], identity.ID[:]) >= 0) {
			return finalSwissError("non-canonical standings predecessor identity receipt")
		}
		foundProjection = foundProjection || identity.ID == receipt.ProjectionID
		foundRevision = foundRevision || identity.ID == revisionID
	}
	if !foundProjection || !foundRevision {
		return finalSwissError("incomplete standings predecessor identity receipt")
	}
	return nil
}

func mergeFinalSwissReservedIdentities(
	reserved map[uuid.UUID]string,
	retained []finalSwissReservedIdentity,
) error {
	if len(reserved) > maxPlayoffReservedIdentities || len(retained) > maxPlayoffReservedIdentities {
		return finalSwissError("standings predecessor identity receipt exceeds DAG bounds")
	}
	for _, identity := range retained {
		if identity.ID == uuid.Nil || identity.Role == "" {
			return finalSwissError("invalid retained predecessor identity")
		}
		if role, exists := reserved[identity.ID]; exists {
			if role != identity.Role {
				return finalSwissError("retained identity role changed from %s to %s", identity.Role, role)
			}
			continue
		}
		if len(reserved) >= maxPlayoffReservedIdentities {
			return finalSwissError("standings predecessor identity receipt exceeds DAG bounds")
		}
		reserved[identity.ID] = identity.Role
	}
	return nil
}
