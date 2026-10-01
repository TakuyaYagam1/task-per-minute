package ffmpeg

import (
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestValidateInputAcceptsOneVideoAndOneAudioTrack(t *testing.T) {
	t.Parallel()

	document := validProbeDocument()
	document.Streams = append(document.Streams, probeStream{CodecType: "audio", CodecName: "aac"})
	require.NoError(t, validateInput(document))
}

func TestValidateInputAcceptsMaximumDeclaredDurationAndFrameBudget(t *testing.T) {
	t.Parallel()

	document := validProbeDocument()
	document.Format.Duration = "10"
	document.Streams[0].Duration = "10"
	document.Streams[0].AvgFrameRate = "60/1"
	document.Streams[0].RFrameRate = "60/1"
	document.Streams[0].NBFrames = "600"
	document.Streams[0].NBReadFrames = "600"
	document.Streams[0].Width = 1920
	document.Streams[0].Height = 1080
	require.NoError(t, validateInput(document))
}

func TestValidateInputRejectsUnsafeStreamShapesAndLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*probeDocument)
	}{
		{
			name: "multiple video tracks",
			mutate: func(document *probeDocument) {
				document.Streams = append(document.Streams, document.Streams[0])
			},
		},
		{
			name:   "unsupported video codec",
			mutate: func(document *probeDocument) { document.Streams[0].CodecName = "vp9" },
		},
		{
			name: "multiple audio tracks",
			mutate: func(document *probeDocument) {
				document.Streams = append(document.Streams,
					probeStream{CodecType: "audio", CodecName: "aac"},
					probeStream{CodecType: "audio", CodecName: "aac"},
				)
			},
		},
		{
			name: "non audio video stream",
			mutate: func(document *probeDocument) {
				document.Streams = append(document.Streams, probeStream{CodecType: "subtitle"})
			},
		},
		{
			name:   "duration exceeds bound",
			mutate: func(document *probeDocument) { document.Format.Duration = "10.01" },
		},
		{
			name: "stream duration exceeds container duration",
			mutate: func(document *probeDocument) {
				document.Format.Duration = "1.0"
				document.Streams[0].Duration = "10.01"
			},
		},
		{
			name:   "oversized dimensions",
			mutate: func(document *probeDocument) { document.Streams[0].Width = 1921 },
		},
		{
			name: "pixel budget exceeded",
			mutate: func(document *probeDocument) {
				document.Streams[0].Width = 1920
				document.Streams[0].Height = 1081
			},
		},
		{
			name:   "frame rate exceeds bound",
			mutate: func(document *probeDocument) { document.Streams[0].AvgFrameRate = "61/1" },
		},
		{
			name:   "frame count exceeds bound",
			mutate: func(document *probeDocument) { document.Streams[0].NBReadFrames = "601" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := validProbeDocument()
			test.mutate(&document)
			require.ErrorIs(t, validateInput(document), domain.ErrAvatarInvalid)
		})
	}
}

func TestValidateCanonicalRequiresSilentBoundedH264(t *testing.T) {
	t.Parallel()

	document := validProbeDocument()
	document.Streams[0].CodecName = "h264"
	document.Streams[0].PixelFormat = "yuv420p"
	require.NoError(t, validateCanonical(document))

	document.Streams = append(document.Streams, probeStream{CodecType: "audio", CodecName: "aac"})
	require.ErrorIs(t, validateCanonical(document), domain.ErrAvatarInvalid)
}

func TestDurationAndFrameRateRejectMalformedProbeValues(t *testing.T) {
	t.Parallel()

	_, err := durationSeconds("NaN")
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)
	_, err = maxFrameRate("24/0")
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)
	_, err = frameCount("N/A")
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)
	duration, err := durationSeconds("1.0", "10.01")
	require.NoError(t, err)
	require.InDelta(t, 10.01, duration, 0.0001)
}

func TestBoundedCaptureRetainsOnlyConfiguredBytes(t *testing.T) {
	t.Parallel()

	capture := &boundedCapture{limit: 5}
	n, err := capture.Write([]byte("123456789"))
	require.NoError(t, err)
	require.Equal(t, 9, n)
	require.Equal(t, "12345", capture.buffer.String())
	require.True(t, capture.exceeded)
}

func TestNewProcessorRejectsUnresolvableExecutable(t *testing.T) {
	t.Parallel()

	_, err := NewProcessor(Config{FFmpegPath: "/missing/ffmpeg", FFprobePath: "/missing/ffprobe"})
	require.Error(t, err)
}

func validProbeDocument() probeDocument {
	var document probeDocument
	document.Format.FormatName = "mov,mp4,m4a,3gp,3g2,mj2"
	document.Format.Duration = "1.0"
	document.Streams = []probeStream{{
		CodecType: "video", CodecName: "h264", Width: 128, Height: 128,
		PixelFormat: "yuv420p", AvgFrameRate: "24/1", RFrameRate: "24/1",
		Duration: "1.0", NBFrames: "24", NBReadFrames: "24",
	}}
	return document
}
