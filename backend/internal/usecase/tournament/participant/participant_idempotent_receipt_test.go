package participant

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestReadyReceiptEncodesEverySemanticField(t *testing.T) {
	t.Parallel()

	command := usecase.ReadyCommand{
		Actor:                      usecase.Identity{PlayerID: uuid.MustParse("7d000000-0000-4000-8000-000000000001")},
		TournamentID:               uuid.MustParse("7d000000-0000-4000-8000-000000000002"),
		WaveID:                     uuid.MustParse("7d000000-0000-4000-8000-000000000003"),
		CommandID:                  uuid.MustParse("7d000000-0000-4000-8000-000000000004"),
		ExpectedProjectionRevision: 8,
		Ready:                      true,
	}

	first, err := readyReceipt(command)
	require.NoError(t, err)
	second, err := readyReceipt(command)
	require.NoError(t, err)
	require.Equal(t, first, second)

	changed := command
	changed.WaveID = uuid.MustParse("7d000000-0000-4000-8000-000000000005")
	third, err := readyReceipt(changed)
	require.NoError(t, err)
	require.NotEqual(t, first.PayloadDigest, third.PayloadDigest)
}

func TestSubmissionReceiptUsesLengthPrefixedFlag(t *testing.T) {
	t.Parallel()

	command := usecase.SubmissionCommand{
		Actor:                      usecase.Identity{PlayerID: uuid.MustParse("7e000000-0000-4000-8000-000000000001")},
		TournamentID:               uuid.MustParse("7e000000-0000-4000-8000-000000000002"),
		SeriesID:                   uuid.MustParse("7e000000-0000-4000-8000-000000000003"),
		GameID:                     uuid.MustParse("7e000000-0000-4000-8000-000000000004"),
		CommandID:                  uuid.MustParse("7e000000-0000-4000-8000-000000000005"),
		ExpectedProjectionRevision: 9,
		SubmittedFlag:              "ab",
	}

	first, err := submissionReceipt(command)
	require.NoError(t, err)
	changed := command
	changed.SubmittedFlag = "a"
	second, err := submissionReceipt(changed)
	require.NoError(t, err)
	require.NotEqual(t, first.PayloadDigest, second.PayloadDigest)
}
