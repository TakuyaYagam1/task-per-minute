package golden_test

import (
	"testing"

	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	"github.com/stretchr/testify/require"
)

func TestGoldenGroupSnapshot(t *testing.T) {
	t.Parallel()

	source := topologyMustGoldenPlanProjection(
		t, topologyGoldenPlanID(200), topologyGoldenPlanID(201), topologyGoldenPlanRevisionID(202), 1,
		topologyGoldenPlanStandings([]int{9, 9, 9, 7, 6, 5}),
	)
	partition, err := goldenusecase.PartitionTies(source)
	require.NoError(t, err)
	seed := partition.Groups()[0]
	command := goldenusecase.GroupRevisionCommand{
		TournamentID: source.TournamentID,
		GroupID:      topologyGoldenPlanID(203), RevisionID: topologyGoldenPlanRevisionID(204), RevisionNo: 1,
		ExpectedSourceRevisionID:    source.RevisionID,
		ExpectedSourcePayloadDigest: source.PayloadDigest,
		PositionFrom:                seed.PositionFrom, PositionTo: seed.PositionTo,
	}

	t.Run("freezes a seed-only revision with exact source and ordering inputs", func(t *testing.T) {
		t.Parallel()

		localSource := source.Snapshot()
		localSeed := seed.Snapshot()
		revision, buildErr := goldenusecase.BuildGroupRevision(command, localSource, localSeed, nil, nil)
		require.NoError(t, buildErr)
		require.NoError(t, revision.Validate())
		require.Equal(t, command.TournamentID, revision.TournamentID())
		require.Equal(t, command.GroupID, revision.GroupID())
		require.Equal(t, command.RevisionID, revision.RevisionID())
		require.Equal(t, command.RevisionNo, revision.RevisionNo())
		require.Nil(t, revision.PreviousRevisionID())
		require.Equal(t, goldenusecase.GroupRevisionPurposeSeedOnly, revision.Purpose())
		require.Equal(t, goldenusecase.GroupRevisionEffectNoSwissMutation, revision.Effect())
		require.Equal(t, source.RevisionID, revision.SourceProjectionRevisionID())
		require.Equal(t, source.PayloadDigest, revision.SourceProjectionPayloadDigest())
		from, to := revision.Positions()
		require.Equal(t, seed.PositionFrom, from)
		require.Equal(t, seed.PositionTo, to)
		require.Equal(t, seed.Members, revision.Members())
		require.NotEmpty(t, revision.Payload())
		require.NotEmpty(t, revision.ProofHash())

		localSeed.Members[0].Points = -100
		localSource.Standings[0].Points = -100
		members := revision.Members()
		members[0].Points = -100
		payload := revision.Payload()
		payload[0] ^= 0xff
		require.NoError(t, revision.Validate())
		require.Equal(t, 9, revision.Members()[0].Points)
	})

	t.Run("canonicalizes tied membership independently of normal rank", func(t *testing.T) {
		t.Parallel()

		standings := topologyGoldenPlanStandings([]int{9, 9, 9, 7, 6, 5})
		standings[0].ParticipantID = topologyGoldenPlanID(303)
		standings[1].ParticipantID = topologyGoldenPlanID(301)
		standings[2].ParticipantID = topologyGoldenPlanID(302)
		rankedIDs := []string{
			standings[0].ParticipantID.String(),
			standings[1].ParticipantID.String(),
			standings[2].ParticipantID.String(),
		}
		localSource := topologyMustGoldenPlanProjection(
			t, topologyGoldenPlanID(310), topologyGoldenPlanID(311), topologyGoldenPlanRevisionID(312), 1, standings,
		)
		partition, partitionErr := goldenusecase.PartitionTies(localSource)
		require.NoError(t, partitionErr)
		localSeed := partition.Groups()[0]
		memberIDs := []string{
			localSeed.Members[0].ParticipantID.String(),
			localSeed.Members[1].ParticipantID.String(),
			localSeed.Members[2].ParticipantID.String(),
		}
		require.Equal(t, []string{
			topologyGoldenPlanID(301).String(),
			topologyGoldenPlanID(302).String(),
			topologyGoldenPlanID(303).String(),
		}, memberIDs)
		require.NotEqual(t, rankedIDs, memberIDs)
		require.Equal(t, []int{2, 3, 1}, []int{
			localSeed.Members[0].Seed,
			localSeed.Members[1].Seed,
			localSeed.Members[2].Seed,
		})

		localCommand := goldenusecase.GroupRevisionCommand{
			TournamentID: localSource.TournamentID,
			GroupID:      topologyGoldenPlanID(313), RevisionID: topologyGoldenPlanRevisionID(314), RevisionNo: 1,
			ExpectedSourceRevisionID:    localSource.RevisionID,
			ExpectedSourcePayloadDigest: localSource.PayloadDigest,
			PositionFrom:                localSeed.PositionFrom,
			PositionTo:                  localSeed.PositionTo,
		}
		revision, buildErr := goldenusecase.BuildGroupRevision(localCommand, localSource, localSeed, nil, nil)
		require.NoError(t, buildErr)
		require.Equal(t, localSeed.Members, revision.Members())
		require.NotContains(t, string(revision.Payload()), `"position":`)
	})

	t.Run("requires exact predecessor lineage", func(t *testing.T) {
		t.Parallel()

		localSource := source.Snapshot()
		localSeed := seed.Snapshot()
		first, buildErr := goldenusecase.BuildGroupRevision(command, localSource, localSeed, nil, nil)
		require.NoError(t, buildErr)
		sameSourceCommand := command
		sameSourceCommand.RevisionID = topologyGoldenPlanRevisionID(207)
		sameSourceCommand.RevisionNo = 2
		sameSourcePrevious := first.RevisionID()
		sameSourceCommand.PreviousRevisionID = &sameSourcePrevious
		_, buildErr = goldenusecase.BuildGroupRevision(sameSourceCommand, localSource, localSeed, &first, nil)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGroupRevision)

		standingsV2 := topologyCloneSwissNormalStandings(localSource.Standings)
		standingsV2[5].Buchholz++
		previousSourceID := localSource.RevisionID
		sourceV2, sourceErr := goldenusecase.NewStandingsProjection(
			localSource.TournamentID, localSource.ProjectionID, topologyGoldenPlanRevisionID(206), 2,
			&previousSourceID, true, standingsV2,
		)
		require.NoError(t, sourceErr)
		partitionV2, partitionErr := goldenusecase.PartitionTies(sourceV2)
		require.NoError(t, partitionErr)
		seedV2 := partitionV2.Groups()[0]
		previousID := first.RevisionID()
		secondCommand := command
		secondCommand.RevisionID = topologyGoldenPlanRevisionID(205)
		secondCommand.RevisionNo = 2
		secondCommand.PreviousRevisionID = &previousID
		secondCommand.ExpectedSourceRevisionID = sourceV2.RevisionID
		secondCommand.ExpectedSourcePayloadDigest = sourceV2.PayloadDigest
		second, buildErr := goldenusecase.BuildGroupRevision(secondCommand, sourceV2, seedV2, &first, nil)
		require.NoError(t, buildErr)
		require.NoError(t, second.Validate())
		require.Equal(t, previousID, *second.PreviousRevisionID())
		require.Equal(t, first.Members(), second.Members())

		wrong := secondCommand
		wrongPrevious := topologyGoldenPlanRevisionID(999)
		wrong.PreviousRevisionID = &wrongPrevious
		_, buildErr = goldenusecase.BuildGroupRevision(wrong, sourceV2, seedV2, &first, nil)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGroupRevision)

		missing := secondCommand
		missing.PreviousRevisionID = nil
		_, buildErr = goldenusecase.BuildGroupRevision(missing, sourceV2, seedV2, &first, nil)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGroupRevision)

		usedPreviousSourceGroups := []goldenusecase.RevisionIdentity{
			{GroupID: previousSourceID.UUID(), RevisionID: topologyGoldenPlanRevisionID(215)},
		}
		_, buildErr = goldenusecase.BuildGroupRevision(
			secondCommand, sourceV2, seedV2, &first, usedPreviousSourceGroups,
		)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGroupRevision)

		usedPreviousSourceRevisions := []goldenusecase.RevisionIdentity{
			{GroupID: topologyGoldenPlanID(216), RevisionID: previousSourceID},
		}
		_, buildErr = goldenusecase.BuildGroupRevision(
			secondCommand, sourceV2, seedV2, &first, usedPreviousSourceRevisions,
		)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGroupRevision)
	})

	t.Run("accepts only a maximal impacting partition segment", func(t *testing.T) {
		t.Parallel()

		partialCommand := command
		partialCommand.PositionTo = 2
		partialSeed := seed.Snapshot()
		partialSeed.PositionTo = 2
		partialSeed.Members = partialSeed.Members[:2]
		_, buildErr := goldenusecase.BuildGroupRevision(partialCommand, source.Snapshot(), partialSeed, nil, nil)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGroupRevision)

		belowSource := topologyMustGoldenPlanProjection(
			t, topologyGoldenPlanID(210), topologyGoldenPlanID(211), topologyGoldenPlanRevisionID(212), 1,
			topologyGoldenPlanStandings([]int{9, 8, 7, 6, 5, 5}),
		)
		belowSeed := goldenusecase.TieGroupSeed{
			TournamentID:                  belowSource.TournamentID,
			SourceProjectionRevisionID:    belowSource.RevisionID,
			SourceProjectionPayloadDigest: belowSource.PayloadDigest,
			PositionFrom:                  5, PositionTo: 6,
			Members: []goldenusecase.GroupMemberSeed{
				goldenMemberSeedFromStanding(belowSource.Standings[4]),
				goldenMemberSeedFromStanding(belowSource.Standings[5]),
			},
		}
		belowCommand := goldenusecase.GroupRevisionCommand{
			TournamentID: belowSource.TournamentID, GroupID: topologyGoldenPlanID(213),
			RevisionID: topologyGoldenPlanRevisionID(214), RevisionNo: 1,
			ExpectedSourceRevisionID:    belowSource.RevisionID,
			ExpectedSourcePayloadDigest: belowSource.PayloadDigest,
			PositionFrom:                5, PositionTo: 6,
		}
		_, buildErr = goldenusecase.BuildGroupRevision(belowCommand, belowSource, belowSeed, nil, nil)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGroupRevision)
	})

	t.Run("rejects stale changed foreign duplicate malformed and reused evidence", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			mutate    func(*goldenusecase.GroupRevisionCommand, *goldenusecase.StandingsProjection, *goldenusecase.TieGroupSeed, *[]goldenusecase.RevisionIdentity)
			wantError error
		}{
			{name: "cross tournament", mutate: func(command *goldenusecase.GroupRevisionCommand, _ *goldenusecase.StandingsProjection, _ *goldenusecase.TieGroupSeed, _ *[]goldenusecase.RevisionIdentity) {
				command.TournamentID = topologyGoldenPlanID(990)
			}, wantError: goldenusecase.ErrInvalidGroupRevision},
			{name: "stale source", mutate: func(command *goldenusecase.GroupRevisionCommand, _ *goldenusecase.StandingsProjection, _ *goldenusecase.TieGroupSeed, _ *[]goldenusecase.RevisionIdentity) {
				command.ExpectedSourceRevisionID = topologyGoldenPlanRevisionID(991)
			}, wantError: goldenusecase.ErrGroupSourceStale},
			{name: "changed replay payload", mutate: func(_ *goldenusecase.GroupRevisionCommand, source *goldenusecase.StandingsProjection, _ *goldenusecase.TieGroupSeed, _ *[]goldenusecase.RevisionIdentity) {
				changed := topologyGoldenPlanStandings([]int{10, 10, 10, 7, 6, 5})
				*source = topologyMustGoldenPlanProjection(t, source.TournamentID, source.ProjectionID, source.RevisionID, source.RevisionNo, changed)
			}, wantError: goldenusecase.ErrGroupSourceStale},
			{name: "malformed interval", mutate: func(command *goldenusecase.GroupRevisionCommand, _ *goldenusecase.StandingsProjection, _ *goldenusecase.TieGroupSeed, _ *[]goldenusecase.RevisionIdentity) {
				command.PositionTo++
			}, wantError: goldenusecase.ErrInvalidGroupRevision},
			{name: "foreign member", mutate: func(_ *goldenusecase.GroupRevisionCommand, _ *goldenusecase.StandingsProjection, seed *goldenusecase.TieGroupSeed, _ *[]goldenusecase.RevisionIdentity) {
				seed.Members[0].ParticipantID = topologyGoldenPlanID(992)
			}, wantError: goldenusecase.ErrInvalidGroupRevision},
			{name: "duplicate member", mutate: func(_ *goldenusecase.GroupRevisionCommand, _ *goldenusecase.StandingsProjection, seed *goldenusecase.TieGroupSeed, _ *[]goldenusecase.RevisionIdentity) {
				seed.Members[1] = seed.Members[0]
			}, wantError: goldenusecase.ErrInvalidGroupRevision},
			{name: "reused revision", mutate: func(command *goldenusecase.GroupRevisionCommand, _ *goldenusecase.StandingsProjection, _ *goldenusecase.TieGroupSeed, used *[]goldenusecase.RevisionIdentity) {
				*used = append(*used, goldenusecase.RevisionIdentity{GroupID: topologyGoldenPlanID(993), RevisionID: command.RevisionID})
			}, wantError: goldenusecase.ErrInvalidGroupRevision},
			{name: "source identity reused", mutate: func(command *goldenusecase.GroupRevisionCommand, _ *goldenusecase.StandingsProjection, _ *goldenusecase.TieGroupSeed, _ *[]goldenusecase.RevisionIdentity) {
				command.RevisionID = command.ExpectedSourceRevisionID
			}, wantError: goldenusecase.ErrInvalidGroupRevision},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				localCommand := command
				localSource := source.Snapshot()
				localSeed := seed.Snapshot()
				used := []goldenusecase.RevisionIdentity(nil)
				test.mutate(&localCommand, &localSource, &localSeed, &used)
				_, buildErr := goldenusecase.BuildGroupRevision(localCommand, localSource, localSeed, nil, used)
				require.Error(t, buildErr)
				require.ErrorIs(t, buildErr, test.wantError)
			})
		}
	})
}

func topologyCloneSwissNormalStandings(input []swissusecase.NormalStanding) []swissusecase.NormalStanding {
	result := append([]swissusecase.NormalStanding(nil), input...)
	for index := range result {
		if input[index].AcceptedSolveTime != nil {
			value := *input[index].AcceptedSolveTime
			result[index].AcceptedSolveTime = &value
		}
	}
	return result
}

func goldenMemberSeedFromStanding(standing swissusecase.NormalStanding) goldenusecase.GroupMemberSeed {
	return goldenusecase.GroupMemberSeed{
		ParticipantID: standing.ParticipantID, Points: standing.Points, Buchholz: standing.Buchholz,
		HeadToHeadPoints: standing.HeadToHeadPoints, HeadToHeadApplied: standing.HeadToHeadApplied,
		EffectiveTime: standing.EffectiveTime, AcceptedSolveTime: standing.AcceptedSolveTime, Seed: standing.Seed,
	}
}
