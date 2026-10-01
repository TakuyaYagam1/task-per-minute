package avatar

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCanonicalizeAvatarDelegatesMP4AndChecksCanonicalOutput(t *testing.T) {
	t.Parallel()

	input := testMP4Box()
	output := testMP4Box()
	processor := &videoProcessorFake{output: output}
	service := &Service{video: processor}

	contentType, canonical, err := service.canonicalizeAvatar(context.Background(), input, "video/mp4")
	require.NoError(t, err)
	require.Equal(t, "video/mp4", contentType)
	require.Equal(t, output, canonical)
	require.Equal(t, input, processor.input)

	service.video = nil
	_, _, err = service.canonicalizeAvatar(context.Background(), input, "video/mp4")
	require.ErrorIs(t, err, domain.ErrAvatarVideoUnavailable)

	service.video = &videoProcessorFake{output: []byte("not an mp4")}
	_, _, err = service.canonicalizeAvatar(context.Background(), input, "video/mp4")
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)
}

func TestCanonicalizeAvatarRejectsMP4ContentTypeMismatch(t *testing.T) {
	t.Parallel()

	service := &Service{video: &videoProcessorFake{output: testMP4Box()}}
	_, _, err := service.canonicalizeAvatar(context.Background(), testMP4Box(), "image/png")
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)
}

func TestMP4AvatarObjectKeyUsesPrivateMP4Extension(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	objectID := uuid.New()
	require.Equal(t, "avatars/"+playerID.String()+"/"+objectID.String()+".mp4", avatarObjectKey(playerID, objectID, "video/mp4"))
}

func TestVideoProcessorRejectsOversizedInputBeforeProcessing(t *testing.T) {
	t.Parallel()

	service := &Service{}
	err := service.ReplaceAvatar(context.Background(), uuid.New(), uuid.New(), "video/mp4", make([]byte, MaxAvatarBytes+1))
	require.ErrorIs(t, err, domain.ErrAvatarTooLarge)
}

func testMP4Box() []byte {
	data := make([]byte, 20)
	binary.BigEndian.PutUint32(data[:4], uint32(len(data)))
	copy(data[4:8], "ftyp")
	copy(data[8:12], "isom")
	return data
}

type videoProcessorFake struct {
	input  []byte
	output []byte
	err    error
}

func (processor *videoProcessorFake) CanonicalizeMP4(_ context.Context, input []byte) ([]byte, error) {
	processor.input = append([]byte(nil), input...)
	if processor.err != nil {
		return nil, processor.err
	}
	return append([]byte(nil), processor.output...), nil
}

var _ VideoProcessor = (*videoProcessorFake)(nil)

func TestCanonicalizeAvatarPropagatesProcessorErrors(t *testing.T) {
	t.Parallel()

	service := &Service{video: &videoProcessorFake{err: errors.New("test processing failure")}}
	_, _, err := service.canonicalizeAvatar(context.Background(), testMP4Box(), "video/mp4")
	require.EqualError(t, err, "test processing failure")
}
