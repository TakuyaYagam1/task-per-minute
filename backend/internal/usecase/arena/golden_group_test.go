package arena_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenGroupSnapshot(t *testing.T) {
	t.Parallel()

	source := mustGoldenStandingsProjection(
		t, task046ID(200), task046ID(201), task046RevisionID(202), 1,
		goldenStandings([]int{9, 9, 9, 7, 6, 5}),
	)
	partition, err := arena.PartitionGoldenTies(source)
	require.NoError(t, err)
	seed := partition.GoldenGroups()[0]
	command := arena.GoldenGroupRevisionCommand{
		TournamentID: source.TournamentID,
		GroupID:      task046ID(203), RevisionID: task046RevisionID(204), RevisionNo: 1,
		ExpectedSourceRevisionID:    source.RevisionID,
		ExpectedSourcePayloadDigest: source.PayloadDigest,
		PositionFrom:                seed.PositionFrom, PositionTo: seed.PositionTo,
	}

	t.Run("freezes a seed-only revision with exact source and ordering inputs", func(t *testing.T) {
		t.Parallel()

		localSource := source.Snapshot()
		localSeed := seed.Snapshot()
		revision, buildErr := arena.BuildGoldenGroupRevision(command, localSource, localSeed, nil, nil)
		require.NoError(t, buildErr)
		require.NoError(t, revision.Validate())
		require.Equal(t, command.TournamentID, revision.TournamentID())
		require.Equal(t, command.GroupID, revision.GroupID())
		require.Equal(t, command.RevisionID, revision.RevisionID())
		require.Equal(t, command.RevisionNo, revision.RevisionNo())
		require.Nil(t, revision.PreviousRevisionID())
		require.Equal(t, arena.GoldenGroupRevisionPurposeSeedOnly, revision.Purpose())
		require.Equal(t, arena.GoldenGroupRevisionEffectNoSwissMutation, revision.Effect())
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

		standings := goldenStandings([]int{9, 9, 9, 7, 6, 5})
		standings[0].ParticipantID = task046ID(303)
		standings[1].ParticipantID = task046ID(301)
		standings[2].ParticipantID = task046ID(302)
		rankedIDs := []string{
			standings[0].ParticipantID.String(),
			standings[1].ParticipantID.String(),
			standings[2].ParticipantID.String(),
		}
		localSource := mustGoldenStandingsProjection(
			t, task046ID(310), task046ID(311), task046RevisionID(312), 1, standings,
		)
		partition, partitionErr := arena.PartitionGoldenTies(localSource)
		require.NoError(t, partitionErr)
		localSeed := partition.GoldenGroups()[0]
		memberIDs := []string{
			localSeed.Members[0].ParticipantID.String(),
			localSeed.Members[1].ParticipantID.String(),
			localSeed.Members[2].ParticipantID.String(),
		}
		require.Equal(t, []string{
			task046ID(301).String(),
			task046ID(302).String(),
			task046ID(303).String(),
		}, memberIDs)
		require.NotEqual(t, rankedIDs, memberIDs)
		require.Equal(t, []int{2, 3, 1}, []int{
			localSeed.Members[0].Seed,
			localSeed.Members[1].Seed,
			localSeed.Members[2].Seed,
		})

		localCommand := arena.GoldenGroupRevisionCommand{
			TournamentID: localSource.TournamentID,
			GroupID:      task046ID(313), RevisionID: task046RevisionID(314), RevisionNo: 1,
			ExpectedSourceRevisionID:    localSource.RevisionID,
			ExpectedSourcePayloadDigest: localSource.PayloadDigest,
			PositionFrom:                localSeed.PositionFrom,
			PositionTo:                  localSeed.PositionTo,
		}
		revision, buildErr := arena.BuildGoldenGroupRevision(localCommand, localSource, localSeed, nil, nil)
		require.NoError(t, buildErr)
		require.Equal(t, localSeed.Members, revision.Members())
		require.NotContains(t, string(revision.Payload()), `"position":`)
	})

	t.Run("requires exact predecessor lineage", func(t *testing.T) {
		t.Parallel()

		localSource := source.Snapshot()
		localSeed := seed.Snapshot()
		first, buildErr := arena.BuildGoldenGroupRevision(command, localSource, localSeed, nil, nil)
		require.NoError(t, buildErr)
		sameSourceCommand := command
		sameSourceCommand.RevisionID = task046RevisionID(207)
		sameSourceCommand.RevisionNo = 2
		sameSourcePrevious := first.RevisionID()
		sameSourceCommand.PreviousRevisionID = &sameSourcePrevious
		_, buildErr = arena.BuildGoldenGroupRevision(sameSourceCommand, localSource, localSeed, &first, nil)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenGroupRevision)

		standingsV2 := cloneSwissNormalStandings(localSource.Standings)
		standingsV2[5].Buchholz++
		previousSourceID := localSource.RevisionID
		sourceV2, sourceErr := arena.NewGoldenStandingsProjection(
			localSource.TournamentID, localSource.ProjectionID, task046RevisionID(206), 2,
			&previousSourceID, true, standingsV2,
		)
		require.NoError(t, sourceErr)
		partitionV2, partitionErr := arena.PartitionGoldenTies(sourceV2)
		require.NoError(t, partitionErr)
		seedV2 := partitionV2.GoldenGroups()[0]
		previousID := first.RevisionID()
		secondCommand := command
		secondCommand.RevisionID = task046RevisionID(205)
		secondCommand.RevisionNo = 2
		secondCommand.PreviousRevisionID = &previousID
		secondCommand.ExpectedSourceRevisionID = sourceV2.RevisionID
		secondCommand.ExpectedSourcePayloadDigest = sourceV2.PayloadDigest
		second, buildErr := arena.BuildGoldenGroupRevision(secondCommand, sourceV2, seedV2, &first, nil)
		require.NoError(t, buildErr)
		require.NoError(t, second.Validate())
		require.Equal(t, previousID, *second.PreviousRevisionID())
		require.Equal(t, first.Members(), second.Members())

		wrong := secondCommand
		wrongPrevious := task046RevisionID(999)
		wrong.PreviousRevisionID = &wrongPrevious
		_, buildErr = arena.BuildGoldenGroupRevision(wrong, sourceV2, seedV2, &first, nil)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenGroupRevision)

		missing := secondCommand
		missing.PreviousRevisionID = nil
		_, buildErr = arena.BuildGoldenGroupRevision(missing, sourceV2, seedV2, &first, nil)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenGroupRevision)

		usedPreviousSourceGroups := []arena.GoldenRevisionIdentity{
			{GroupID: previousSourceID.UUID(), RevisionID: task046RevisionID(215)},
		}
		_, buildErr = arena.BuildGoldenGroupRevision(
			secondCommand, sourceV2, seedV2, &first, usedPreviousSourceGroups,
		)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenGroupRevision)

		usedPreviousSourceRevisions := []arena.GoldenRevisionIdentity{
			{GroupID: task046ID(216), RevisionID: previousSourceID},
		}
		_, buildErr = arena.BuildGoldenGroupRevision(
			secondCommand, sourceV2, seedV2, &first, usedPreviousSourceRevisions,
		)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenGroupRevision)
	})

	t.Run("accepts only a maximal impacting partition segment", func(t *testing.T) {
		t.Parallel()

		partialCommand := command
		partialCommand.PositionTo = 2
		partialSeed := seed.Snapshot()
		partialSeed.PositionTo = 2
		partialSeed.Members = partialSeed.Members[:2]
		_, buildErr := arena.BuildGoldenGroupRevision(partialCommand, source.Snapshot(), partialSeed, nil, nil)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenGroupRevision)

		belowSource := mustGoldenStandingsProjection(
			t, task046ID(210), task046ID(211), task046RevisionID(212), 1,
			goldenStandings([]int{9, 8, 7, 6, 5, 5}),
		)
		belowSeed := arena.GoldenTieGroupSeed{
			TournamentID:                  belowSource.TournamentID,
			SourceProjectionRevisionID:    belowSource.RevisionID,
			SourceProjectionPayloadDigest: belowSource.PayloadDigest,
			PositionFrom:                  5, PositionTo: 6,
			Members: []arena.GoldenGroupMemberSeed{
				goldenMemberSeedFromStanding(belowSource.Standings[4]),
				goldenMemberSeedFromStanding(belowSource.Standings[5]),
			},
		}
		belowCommand := arena.GoldenGroupRevisionCommand{
			TournamentID: belowSource.TournamentID, GroupID: task046ID(213),
			RevisionID: task046RevisionID(214), RevisionNo: 1,
			ExpectedSourceRevisionID:    belowSource.RevisionID,
			ExpectedSourcePayloadDigest: belowSource.PayloadDigest,
			PositionFrom:                5, PositionTo: 6,
		}
		_, buildErr = arena.BuildGoldenGroupRevision(belowCommand, belowSource, belowSeed, nil, nil)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenGroupRevision)
	})

	t.Run("rejects stale changed foreign duplicate malformed and reused evidence", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			mutate    func(*arena.GoldenGroupRevisionCommand, *arena.GoldenStandingsProjection, *arena.GoldenTieGroupSeed, *[]arena.GoldenRevisionIdentity)
			wantError error
		}{
			{name: "cross tournament", mutate: func(command *arena.GoldenGroupRevisionCommand, _ *arena.GoldenStandingsProjection, _ *arena.GoldenTieGroupSeed, _ *[]arena.GoldenRevisionIdentity) {
				command.TournamentID = task046ID(990)
			}, wantError: arena.ErrInvalidGoldenGroupRevision},
			{name: "stale source", mutate: func(command *arena.GoldenGroupRevisionCommand, _ *arena.GoldenStandingsProjection, _ *arena.GoldenTieGroupSeed, _ *[]arena.GoldenRevisionIdentity) {
				command.ExpectedSourceRevisionID = task046RevisionID(991)
			}, wantError: arena.ErrGoldenGroupSourceStale},
			{name: "changed replay payload", mutate: func(_ *arena.GoldenGroupRevisionCommand, source *arena.GoldenStandingsProjection, _ *arena.GoldenTieGroupSeed, _ *[]arena.GoldenRevisionIdentity) {
				changed := goldenStandings([]int{10, 10, 10, 7, 6, 5})
				*source = mustGoldenStandingsProjection(t, source.TournamentID, source.ProjectionID, source.RevisionID, source.RevisionNo, changed)
			}, wantError: arena.ErrGoldenGroupSourceStale},
			{name: "malformed interval", mutate: func(command *arena.GoldenGroupRevisionCommand, _ *arena.GoldenStandingsProjection, _ *arena.GoldenTieGroupSeed, _ *[]arena.GoldenRevisionIdentity) {
				command.PositionTo++
			}, wantError: arena.ErrInvalidGoldenGroupRevision},
			{name: "foreign member", mutate: func(_ *arena.GoldenGroupRevisionCommand, _ *arena.GoldenStandingsProjection, seed *arena.GoldenTieGroupSeed, _ *[]arena.GoldenRevisionIdentity) {
				seed.Members[0].ParticipantID = task046ID(992)
			}, wantError: arena.ErrInvalidGoldenGroupRevision},
			{name: "duplicate member", mutate: func(_ *arena.GoldenGroupRevisionCommand, _ *arena.GoldenStandingsProjection, seed *arena.GoldenTieGroupSeed, _ *[]arena.GoldenRevisionIdentity) {
				seed.Members[1] = seed.Members[0]
			}, wantError: arena.ErrInvalidGoldenGroupRevision},
			{name: "reused revision", mutate: func(command *arena.GoldenGroupRevisionCommand, _ *arena.GoldenStandingsProjection, _ *arena.GoldenTieGroupSeed, used *[]arena.GoldenRevisionIdentity) {
				*used = append(*used, arena.GoldenRevisionIdentity{GroupID: task046ID(993), RevisionID: command.RevisionID})
			}, wantError: arena.ErrInvalidGoldenGroupRevision},
			{name: "source identity reused", mutate: func(command *arena.GoldenGroupRevisionCommand, _ *arena.GoldenStandingsProjection, _ *arena.GoldenTieGroupSeed, _ *[]arena.GoldenRevisionIdentity) {
				command.RevisionID = command.ExpectedSourceRevisionID
			}, wantError: arena.ErrInvalidGoldenGroupRevision},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				localCommand := command
				localSource := source.Snapshot()
				localSeed := seed.Snapshot()
				used := []arena.GoldenRevisionIdentity(nil)
				test.mutate(&localCommand, &localSource, &localSeed, &used)
				_, buildErr := arena.BuildGoldenGroupRevision(localCommand, localSource, localSeed, nil, used)
				require.Error(t, buildErr)
				require.ErrorIs(t, buildErr, test.wantError)
			})
		}
	})
}

func cloneSwissNormalStandings(input []arena.SwissNormalStanding) []arena.SwissNormalStanding {
	result := append([]arena.SwissNormalStanding(nil), input...)
	for index := range result {
		if input[index].AcceptedSolveTime != nil {
			value := *input[index].AcceptedSolveTime
			result[index].AcceptedSolveTime = &value
		}
	}
	return result
}

func goldenMemberSeedFromStanding(standing arena.SwissNormalStanding) arena.GoldenGroupMemberSeed {
	return arena.GoldenGroupMemberSeed{
		ParticipantID: standing.ParticipantID, Points: standing.Points, Buchholz: standing.Buchholz,
		HeadToHeadPoints: standing.HeadToHeadPoints, HeadToHeadApplied: standing.HeadToHeadApplied,
		EffectiveTime: standing.EffectiveTime, AcceptedSolveTime: standing.AcceptedSolveTime, Seed: standing.Seed,
	}
}
