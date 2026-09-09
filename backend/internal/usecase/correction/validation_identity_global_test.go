package correction_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
)

func TestCorrectionRejectsGlobalIdentityRoleCollisions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*correctionusecase.Command, correctionusecase.Authority)
	}{
		{
			name: "command aliases authority projection revision",
			mutate: func(command *correctionusecase.Command, _ correctionusecase.Authority) {
				command.CommandID = command.Expected.TargetProjection.ID().UUID()
			},
		},
		{
			name: "cascade command aliases authority result revision",
			mutate: func(command *correctionusecase.Command, authority correctionusecase.Authority) {
				command.CascadeCommandID = authority.GameResult.ID.UUID()
			},
		},
		{
			name: "operator aliases Tournament",
			mutate: func(command *correctionusecase.Command, _ correctionusecase.Authority) {
				command.OperatorID = command.TournamentID
			},
		},
		{
			name: "Game result revision aliases Series",
			mutate: func(command *correctionusecase.Command, _ correctionusecase.Authority) {
				command.NextResultRevisionID = domain.OfficialResultRevisionID(command.SeriesID)
			},
		},
		{
			name: "score revision aliases participant",
			mutate: func(command *correctionusecase.Command, authority correctionusecase.Authority) {
				command.NextScoreRevisionID = domain.SeriesScoreRevisionID(authority.Series.FirstParticipantID)
			},
		},
		{
			name: "Series result revision aliases score revision",
			mutate: func(command *correctionusecase.Command, authority correctionusecase.Authority) {
				command.NextSeriesResultRevisionID = domain.OfficialResultRevisionID(authority.Score.ID.UUID())
			},
		},
		{
			name: "readiness revision aliases window",
			mutate: func(command *correctionusecase.Command, authority correctionusecase.Authority) {
				command.NextReadinessRevisionID = authority.Readiness.WindowID
			},
		},
		{
			name: "projection revision aliases projection decision",
			mutate: func(command *correctionusecase.Command, authority correctionusecase.Authority) {
				command.ProjectionIntents[0].NextRevisionID = domain.DerivedRevisionID(authority.Decisions[0].ID)
			},
		},
		{
			name: "projection decision aliases projection revision",
			mutate: func(command *correctionusecase.Command, _ correctionusecase.Authority) {
				command.ProjectionIntents[0].DecisionID = command.Expected.TargetProjection.ID().UUID()
			},
		},
		{
			name: "commands reuse the same proposed identity",
			mutate: func(command *correctionusecase.Command, _ correctionusecase.Authority) {
				command.CascadeCommandID = command.CommandID
			},
		},
		{
			name: "result revisions reuse the same proposed identity",
			mutate: func(command *correctionusecase.Command, _ correctionusecase.Authority) {
				command.NextSeriesResultRevisionID = command.NextResultRevisionID
			},
		},
		{
			name: "projection revisions reuse the same proposed identity",
			mutate: func(command *correctionusecase.Command, _ correctionusecase.Authority) {
				command.ProjectionIntents[1].NextRevisionID = command.ProjectionIntents[0].NextRevisionID
			},
		},
		{
			name: "projection decisions reuse the same proposed identity",
			mutate: func(command *correctionusecase.Command, _ correctionusecase.Authority) {
				command.ProjectionIntents[1].DecisionID = command.ProjectionIntents[0].DecisionID
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command, authority := task056CorrectionFixture(t)
			test.mutate(&command, authority)
			validation, err := correctionusecase.Validate(command, authority)
			require.ErrorIs(t, err, correctionusecase.ErrInvalid)
			require.Equal(t, correctionusecase.RejectionIdentityAlias, correctionusecase.Code(err))
			require.Equal(t, correctionusecase.Validation{}, validation)
		})
	}
}
