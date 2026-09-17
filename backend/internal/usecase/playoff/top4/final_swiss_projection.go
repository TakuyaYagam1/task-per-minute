package top4

import (
	"bytes"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

type finalSwissCanonicalMaterialization struct {
	SourceHeads  []terminalSeriesRecord
	Standings    []FinalSwissStanding
	PreviousID   *domain.DerivedRevisionID
	GoldenSource goldenplan.StandingsProjection
	Partition    goldenplan.TiePartition
	TieGroups    []FinalSwissTieGroup
}

func buildFinalSwissProjection(authority finalSwissAuthority) (FinalSwissProjection, error) {
	canonical, err := deriveFinalSwissCanonicalMaterialization(authority)
	if err != nil {
		return FinalSwissProjection{}, err
	}
	if authority.ReceiptOnly {
		if len(authority.GoldenGroups) != 0 {
			return FinalSwissProjection{}, finalSwissError("Swiss receipt contains future Golden identities")
		}
	} else {
		if err := validateFinalSwissGoldenIdentities(authority.GoldenGroups, canonical.TieGroups); err != nil {
			return FinalSwissProjection{}, err
		}
	}
	payload, err := finalSwissPayload(
		authority,
		canonical.Standings,
		canonical.TieGroups,
		canonical.SourceHeads,
		canonical.GoldenSource,
	)
	if err != nil || len(payload) == 0 || len(payload) > maxFinalSwissPayload {
		return FinalSwissProjection{}, finalSwissError("encode bounded standings payload")
	}
	projection, err := domain.NewProjectionRevision(
		authority.RevisionID,
		authority.TournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindStandings, EntityID: authority.TournamentID},
		authority.RevisionNo,
		canonical.PreviousID,
		authority.CreatedAt,
		payload,
	)
	if err != nil {
		return FinalSwissProjection{}, finalSwissError("build standings revision: %v", err)
	}
	seriesDependencies := finalSwissSeriesDependencies(canonical.SourceHeads, authority.RevisionID)
	dependencies := append([]domain.RevisionDependency(nil), seriesDependencies...)
	if authority.Previous != nil {
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID:  authority.Previous.Projection.Revision().ID(),
			DerivedRevisionID: authority.RevisionID,
		})
	}
	var goldenGroups []FinalSwissGoldenGroup
	if !authority.ReceiptOnly {
		goldenGroups, err = buildFinalSwissGoldenGroups(authority, canonical.GoldenSource, canonical.Partition.Groups())
		if err != nil {
			return FinalSwissProjection{}, err
		}
	}
	return FinalSwissProjection{state: finalSwissProjectionState{
		Authority: cloneFinalSwissAuthority(authority), Projection: projection,
		GoldenSource:       canonical.GoldenSource.Snapshot(),
		Standings:          cloneFinalSwissStandings(canonical.Standings),
		TieGroups:          cloneFinalSwissTieGroups(canonical.TieGroups),
		GoldenGroups:       cloneFinalSwissGoldenGroups(goldenGroups),
		SeriesDependencies: append([]domain.RevisionDependency(nil), seriesDependencies...),
		Dependencies:       append([]domain.RevisionDependency(nil), dependencies...),
		AdvanceDirectly:    len(canonical.Partition.Groups()) == 0,
	}}, nil
}

func deriveFinalSwissCanonicalMaterialization(
	authority finalSwissAuthority,
) (finalSwissCanonicalMaterialization, error) {
	if err := validateFinalSwissPredecessorReceipt(authority); err != nil {
		return finalSwissCanonicalMaterialization{}, err
	}
	progressionRounds, sourceHeads, err := validateFinalSwissRounds(authority)
	if err != nil {
		return finalSwissCanonicalMaterialization{}, err
	}
	normalStandings, err := swissusecase.DeriveRoundStandings(
		authority.ParticipantIDs,
		authority.Seeds,
		progressionRounds,
		true,
	)
	if err != nil {
		return finalSwissCanonicalMaterialization{}, finalSwissError("derive canonical ledger: %v", err)
	}
	standings := finalSwissStandings(normalStandings)
	goldenStandings := finalSwissNormalStandings(standings)
	var previousID *domain.DerivedRevisionID
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID()
		previousID = &value
	}
	goldenSource, err := goldenplan.NewStandingsProjection(
		authority.TournamentID, authority.ProjectionID, authority.RevisionID,
		authority.RevisionNo, previousID, true, goldenStandings,
	)
	if err != nil {
		return finalSwissCanonicalMaterialization{}, finalSwissError("build canonical Golden standings source: %v", err)
	}
	partition, err := goldenplan.PartitionTies(goldenSource)
	if err != nil {
		return finalSwissCanonicalMaterialization{}, finalSwissError("partition final point ties: %v", err)
	}
	tieGroups := finalSwissTies(partition)
	return finalSwissCanonicalMaterialization{
		SourceHeads:  sourceHeads,
		Standings:    standings,
		PreviousID:   previousID,
		GoldenSource: goldenSource,
		Partition:    partition,
		TieGroups:    tieGroups,
	}, nil
}

func finalSwissStandings(normal []swissusecase.NormalStanding) []FinalSwissStanding {
	standings := make([]FinalSwissStanding, len(normal))
	for index, standing := range normal {
		standings[index] = FinalSwissStanding{
			ParticipantID: standing.ParticipantID, Points: standing.Points,
			PointsLabel: standing.PointsLabel, Buchholz: standing.Buchholz,
			BuchholzStatus:   SwissBuchholzFinal,
			HeadToHeadPoints: standing.HeadToHeadPoints, HeadToHeadApplied: standing.HeadToHeadApplied,
			EffectiveTime:     standing.EffectiveTime,
			AcceptedSolveTime: cloneFinalSwissDurationPointer(standing.AcceptedSolveTime), StableSeed: standing.Seed,
		}
	}
	for index := range standings {
		standings[index].Position = index + 1
	}
	return standings
}

func finalSwissNormalStandings(standings []FinalSwissStanding) []swissusecase.NormalStanding {
	normal := make([]swissusecase.NormalStanding, len(standings))
	for index, standing := range standings {
		normal[index] = swissusecase.NormalStanding{
			ParticipantID: standing.ParticipantID, Position: standing.Position,
			Points: standing.Points, PointsLabel: standing.PointsLabel,
			Buchholz: standing.Buchholz, HeadToHeadPoints: standing.HeadToHeadPoints,
			HeadToHeadApplied: standing.HeadToHeadApplied, EffectiveTime: standing.EffectiveTime,
			AcceptedSolveTime: cloneFinalSwissDurationPointer(standing.AcceptedSolveTime), Seed: standing.StableSeed,
		}
	}
	return normal
}

func finalSwissTies(partition goldenplan.TiePartition) []FinalSwissTieGroup {
	groups := make([]FinalSwissTieGroup, 0)
	for _, segment := range partition.Segments {
		count := segment.PositionTo - segment.PositionFrom + 1
		if count < 2 {
			continue
		}
		participants := make([]uuid.UUID, count)
		if segment.Golden {
			for index, member := range segment.Members {
				participants[index] = member.ParticipantID
			}
		} else {
			for index, standing := range segment.NormalStandings {
				participants[index] = standing.ParticipantID
			}
		}
		groups = append(groups, FinalSwissTieGroup{
			PositionFrom: segment.PositionFrom, PositionTo: segment.PositionTo,
			Impactful: segment.Golden, ParticipantIDs: participants,
		})
	}
	return groups
}

func validateFinalSwissGoldenIdentities(
	identities []FinalSwissGoldenGroupIdentity,
	ties []FinalSwissTieGroup,
) error {
	impactful := make([]FinalSwissTieGroup, 0, len(ties))
	for _, tie := range ties {
		if tie.Impactful {
			impactful = append(impactful, tie)
		}
	}
	if len(identities) != len(impactful) {
		return finalSwissError("Golden identities do not cover exact maximal impactful ties")
	}
	for index, identity := range identities {
		if identity.GroupID == uuid.Nil || identity.RevisionID.IsZero() ||
			identity.PositionFrom != impactful[index].PositionFrom || identity.PositionTo != impactful[index].PositionTo {
			return finalSwissError("Golden identity does not match an exact maximal impactful tie")
		}
	}
	return nil
}

func buildFinalSwissGoldenGroups(
	authority finalSwissAuthority,
	source goldenplan.StandingsProjection,
	seeds []goldenplan.TieGroupSeed,
) ([]FinalSwissGoldenGroup, error) {
	groups := make([]FinalSwissGoldenGroup, len(seeds))
	used := make([]goldenplan.RevisionIdentity, 0, len(seeds))
	for index, seed := range seeds {
		identity := authority.GoldenGroups[index]
		revision, err := goldenplan.BuildGroupRevision(
			goldenplan.GroupRevisionCommand{
				TournamentID: authority.TournamentID, GroupID: identity.GroupID,
				RevisionID: identity.RevisionID, RevisionNo: 1,
				ExpectedSourceRevisionID:    source.RevisionID,
				ExpectedSourcePayloadDigest: source.PayloadDigest,
				PositionFrom:                seed.PositionFrom, PositionTo: seed.PositionTo,
			},
			source, seed, nil, used,
		)
		if err != nil {
			return nil, finalSwissError("build canonical Golden topology: %v", err)
		}
		used = append(used, goldenplan.RevisionIdentity{GroupID: identity.GroupID, RevisionID: identity.RevisionID})
		members := make([]domain.GoldenMember, len(seed.Members))
		for memberIndex, member := range seed.Members {
			members[memberIndex] = domain.GoldenMember{ParticipantID: member.ParticipantID}
		}
		state := domain.GoldenGroupState{
			ID: identity.GroupID, TournamentID: authority.TournamentID,
			RevisionID: identity.RevisionID, SourceProjectionRevisionID: authority.RevisionID,
			PositionFrom: seed.PositionFrom, PositionTo: seed.PositionTo, Members: members,
		}
		group, err := domain.NewGoldenGroup(state)
		if err != nil {
			return nil, finalSwissError("build Golden group: %v", err)
		}
		state = group.Snapshot()
		payload := revision.Payload()
		if len(payload) == 0 || len(payload) > maxFinalSwissPayload {
			return nil, finalSwissError("encode bounded Golden group payload")
		}
		projection, err := domain.NewProjectionRevision(
			identity.RevisionID,
			authority.TournamentID,
			domain.ArtifactRef{Kind: domain.ArtifactKindGoldenGroup, EntityID: identity.GroupID},
			1, nil, authority.CreatedAt, payload,
		)
		if err != nil {
			return nil, finalSwissError("build Golden projection: %v", err)
		}
		groups[index] = FinalSwissGoldenGroup{
			State: state, Revision: revision.Snapshot(), Projection: projection,
			Dependency: domain.RevisionDependency{
				SourceRevisionID: authority.RevisionID, DerivedRevisionID: identity.RevisionID,
			},
		}
	}
	return groups, nil
}

func finalSwissSeriesDependencies(
	heads []terminalSeriesRecord,
	derived domain.DerivedRevisionID,
) []domain.RevisionDependency {
	dependencies := make([]domain.RevisionDependency, len(heads))
	for index, head := range heads {
		dependencies[index] = domain.RevisionDependency{
			SourceRevisionID: head.Projection.Revision().ID(), DerivedRevisionID: derived,
		}
	}
	sort.Slice(dependencies, func(i, j int) bool {
		first := dependencies[i].SourceRevisionID.UUID()
		second := dependencies[j].SourceRevisionID.UUID()
		return bytes.Compare(
			first[:],
			second[:],
		) < 0
	})
	return dependencies
}
