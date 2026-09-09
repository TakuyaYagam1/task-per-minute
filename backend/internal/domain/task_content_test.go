package domain_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTaskKind(t *testing.T) {
	t.Parallel()

	require.True(t, domain.TaskKindNormal.IsValid())
	require.True(t, domain.TaskKindGolden.IsValid())
	require.False(t, domain.TaskKind("archive").IsValid())
}

func TestTaskVersion_ValidatesServerDigest(t *testing.T) {
	t.Parallel()

	digest := sha256.Sum256([]byte("server content"))
	version := domain.TaskVersion{
		TaskID:        uuid.New(),
		Version:       1,
		Title:         "task",
		Description:   "description",
		Category:      domain.CategoryWeb,
		Difficulty:    domain.DifficultyEasy,
		TimeLimit:     60,
		Flag:          "FLAG{task}",
		Hints:         []string{"", "", ""},
		ContentDigest: digest,
		CreatedAt:     time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
	}

	require.NoError(t, version.Validate())

	version.ContentDigest = [sha256.Size]byte{}
	require.Error(t, version.Validate())
}

func TestCurrentTaskVersionHealth(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	unknown, err := domain.CurrentTaskVersionHealth(taskID, 1, nil)
	require.NoError(t, err)
	require.False(t, unknown.Attested)
	require.False(t, unknown.Healthy)

	first := domain.TaskVersionHealthAttestation{
		TaskID: taskID, Version: 1, Revision: 1,
		Healthy: false, Source: domain.TaskHealthAttestationSourceContentValidation,
		AttestedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
	}
	recovered := first
	recovered.Revision = 2
	recovered.Healthy = true
	recovered.Source = domain.TaskHealthAttestationSourceProbe
	recovered.AttestedAt = recovered.AttestedAt.Add(time.Minute)

	state, err := domain.CurrentTaskVersionHealth(taskID, 1, []domain.TaskVersionHealthAttestation{recovered, first})
	require.NoError(t, err)
	require.True(t, state.Attested)
	require.True(t, state.Healthy)
	require.Equal(t, int64(2), state.Revision)

	_, err = domain.CurrentTaskVersionHealth(taskID, 1, []domain.TaskVersionHealthAttestation{first, first})
	require.Error(t, err)
}
