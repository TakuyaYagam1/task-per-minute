//go:build media_integration

package ffmpeg

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestProcessorCanonicalizesLocalH264Fixture(t *testing.T) {
	t.Parallel()

	processor := newIntegrationProcessor(t, t.TempDir())
	input, err := os.ReadFile("testdata/avatar.mp4")
	require.NoError(t, err)

	canonical, err := processor.CanonicalizeMP4(t.Context(), input)
	require.NoError(t, err)
	require.NotEmpty(t, canonical)
	require.LessOrEqual(t, len(canonical), maxInputBytes)
	require.True(t, isMP4(canonical))

	outputPath := filepath.Join(t.TempDir(), "canonical.mp4")
	require.NoError(t, os.WriteFile(outputPath, canonical, 0o600))
	document, err := processor.probe(t.Context(), outputPath, false)
	require.NoError(t, err)
	require.NoError(t, validateCanonical(document))
	require.Len(t, document.Streams, 1)
	require.Equal(t, "video", document.Streams[0].CodecType)

	entries, err := os.ReadDir(processor.tempDir)
	require.NoError(t, err)
	require.Empty(t, entries, "processor must remove private work files after success")
}

func TestProcessorStripsAudioAndMetadataFromMP4(t *testing.T) {
	t.Parallel()

	processor := newIntegrationProcessor(t, t.TempDir())
	input, err := os.ReadFile("testdata/avatar-audio.mp4")
	require.NoError(t, err)
	inputPath := filepath.Join(t.TempDir(), "input.mp4")
	require.NoError(t, os.WriteFile(inputPath, input, 0o600))
	inputInfo, err := processor.probe(t.Context(), inputPath, false)
	require.NoError(t, err)
	require.Len(t, inputInfo.Streams, 2)

	canonical, err := processor.CanonicalizeMP4(t.Context(), input)
	require.NoError(t, err)
	outputPath := filepath.Join(t.TempDir(), "canonical.mp4")
	require.NoError(t, os.WriteFile(outputPath, canonical, 0o600))
	outputInfo, err := processor.probe(t.Context(), outputPath, false)
	require.NoError(t, err)
	require.Len(t, outputInfo.Streams, 1)
	require.Equal(t, "video", outputInfo.Streams[0].CodecType)

	metadataCmd := exec.CommandContext(t.Context(), processor.ffprobePath,
		"-hide_banner", "-v", "error", "-f", "mov",
		"-protocol_whitelist", "file", "-show_entries", "format_tags:stream_tags",
		"-of", "json", outputPath,
	)
	metadataCmd.Env = privateProcessEnv(filepath.Dir(outputPath))
	metadata, err := metadataCmd.Output()
	require.NoError(t, err)
	require.NotContains(t, string(metadata), "synthetic avatar metadata")
	require.NotContains(t, string(metadata), `"title"`)
}

func TestProcessorRejectsCorruptMP4AndRemovesWorkFiles(t *testing.T) {
	t.Parallel()

	processor := newIntegrationProcessor(t, t.TempDir())
	_, err := processor.CanonicalizeMP4(t.Context(), []byte("not an mp4"))
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)

	entries, err := os.ReadDir(processor.tempDir)
	require.NoError(t, err)
	require.Empty(t, entries, "processor must remove private work files after failure")
}

func TestProcessorCancellationRemovesWorkDirectory(t *testing.T) {
	t.Parallel()

	ffmpegPath, err := exec.LookPath("ffmpeg")
	require.NoError(t, err)
	root := t.TempDir()
	fakeFFprobe := filepath.Join(root, "ffprobe")
	startedPath := fakeFFprobe + ".started"
	script := []byte("#!/bin/sh\n: > \"$0.started\"\nwhile :; do :; done\n")
	require.NoError(t, os.WriteFile(fakeFFprobe, script, 0o700))
	processor, err := NewProcessor(Config{FFmpegPath: ffmpegPath, FFprobePath: fakeFFprobe, TempDir: root})
	require.NoError(t, err)
	input, err := os.ReadFile("testdata/avatar.mp4")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, processErr := processor.CanonicalizeMP4(ctx, input)
		result <- processErr
	}()
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(startedPath)
		return statErr == nil
	}, time.Second, 5*time.Millisecond)
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled ffprobe process did not stop")
	}

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Len(t, entries, 2, "only the fake probe and its marker should remain")
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), "avatar-video-")
	}
}

func TestProcessorRejectsInputOverFiveMiBWithoutRunningTools(t *testing.T) {
	t.Parallel()

	var processor *Processor
	_, err := processor.CanonicalizeMP4(t.Context(), bytes.Repeat([]byte{0}, maxInputBytes+1))
	require.ErrorIs(t, err, domain.ErrAvatarTooLarge)
}

func newIntegrationProcessor(t *testing.T, tempDir string) *Processor {
	t.Helper()
	processor, err := NewProcessor(Config{TempDir: tempDir})
	require.NoError(t, err, "media_integration requires ffmpeg and ffprobe on PATH")
	return processor
}

func isMP4(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	boxSize := binary.BigEndian.Uint32(data[:4])
	return boxSize >= 12 && string(data[4:8]) == "ftyp"
}
