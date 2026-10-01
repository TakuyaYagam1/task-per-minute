package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	avatarusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/avatar"
)

const (
	maxInputBytes    = avatarusecase.MaxAvatarBytes
	maxDuration      = 10 * time.Second
	maxInputWidth    = 1920
	maxInputHeight   = 1920
	maxInputPixels   = 2_073_600
	maxInputFPS      = 60.0
	maxInputFrames   = 600
	maxOutputWidth   = 512
	maxOutputHeight  = 512
	maxOutputFPS     = 30.0
	maxOutputFrames  = 300
	maxAllocBytes    = 64 << 20
	maxProbeBytes    = 32 << 10
	processSlots     = 1
	probeTimeout     = 8 * time.Second
	transcodeTimeout = 20 * time.Second
	processTimeout   = 40 * time.Second
	maxCPUSeconds    = 12
)

var _ avatarusecase.VideoProcessor = (*Processor)(nil)

// Config names the local FFmpeg binaries. Empty paths are resolved through
// PATH at construction time, then stored as absolute paths.
type Config struct {
	FFmpegPath  string
	FFprobePath string
	TempDir     string
}

type Processor struct {
	ffmpegPath  string
	ffprobePath string
	tempDir     string
	slots       chan struct{}
}

func NewProcessor(cfg Config) (*Processor, error) {
	ffmpegPath, err := resolveExecutable(cfg.FFmpegPath, "ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("resolve ffmpeg executable: %w", err)
	}
	ffprobePath, err := resolveExecutable(cfg.FFprobePath, "ffprobe")
	if err != nil {
		return nil, fmt.Errorf("resolve ffprobe executable: %w", err)
	}
	tempDir := cfg.TempDir
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	info, err := os.Stat(tempDir)
	if err != nil {
		return nil, fmt.Errorf("avatar media temp directory is unavailable: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("avatar media temp path is not a directory")
	}
	return &Processor{
		ffmpegPath:  ffmpegPath,
		ffprobePath: ffprobePath,
		tempDir:     tempDir,
		slots:       make(chan struct{}, processSlots),
	}, nil
}

func (p *Processor) CanonicalizeMP4(ctx context.Context, input []byte) (result []byte, resultErr error) {
	if err := validateRequest(ctx, p, input); err != nil {
		return nil, err
	}
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-p.slots }()
	processCtx, cancel := context.WithTimeout(ctx, processTimeout)
	defer cancel()

	workDir, err := os.MkdirTemp(p.tempDir, "avatar-video-")
	if err != nil {
		return nil, domain.ErrInternal
	}
	defer func() {
		if err := os.RemoveAll(workDir); err != nil {
			result = nil
			if resultErr == nil {
				resultErr = domain.ErrInternal
			}
		}
	}()

	inputPath := filepath.Join(workDir, "input.mp4")
	if err := writePrivateFile(inputPath, input); err != nil {
		return nil, domain.ErrInternal
	}
	if err := p.validateInputFile(processCtx, inputPath); err != nil {
		return nil, err
	}

	outputPath := filepath.Join(workDir, "canonical.mp4")
	canonical, err := p.transcodeCanonical(processCtx, inputPath, outputPath)
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func validateRequest(ctx context.Context, processor *Processor, input []byte) error {
	if ctx == nil || len(input) == 0 {
		return domain.ErrAvatarInvalid
	}
	if len(input) > maxInputBytes {
		return domain.ErrAvatarTooLarge
	}
	if processor == nil || processor.slots == nil {
		return domain.ErrAvatarVideoUnavailable
	}
	return nil
}

func (p *Processor) validateInputFile(ctx context.Context, inputPath string) error {
	inputInfo, err := p.probe(ctx, inputPath, false)
	if err != nil {
		return err
	}
	if err := validateInputMetadata(inputInfo); err != nil {
		return err
	}
	frameInfo, err := p.probe(ctx, inputPath, true)
	if err != nil {
		return err
	}
	if len(frameInfo.Streams) != 1 || frameInfo.Streams[0].CodecType != "video" {
		return domain.ErrAvatarInvalid
	}
	for index := range inputInfo.Streams {
		if inputInfo.Streams[index].CodecType == "video" {
			inputInfo.Streams[index].NBReadFrames = frameInfo.Streams[0].NBReadFrames
			break
		}
	}
	return validateInput(inputInfo)
}

func (p *Processor) transcodeCanonical(ctx context.Context, inputPath, outputPath string) ([]byte, error) {
	if err := createPrivateFile(outputPath); err != nil {
		return nil, domain.ErrInternal
	}
	if err := p.transcode(ctx, inputPath, outputPath); err != nil {
		if info, statErr := os.Stat(outputPath); statErr == nil && info.Size() > maxInputBytes {
			return nil, domain.ErrAvatarTooLarge
		}
		return nil, err
	}
	outputInfo, err := os.Stat(outputPath)
	if err != nil || outputInfo.Size() <= 0 {
		return nil, domain.ErrAvatarInvalid
	}
	if outputInfo.Size() > maxInputBytes {
		return nil, domain.ErrAvatarTooLarge
	}
	if err := os.Chmod(outputPath, 0o600); err != nil {
		return nil, domain.ErrInternal
	}
	canonical, err := os.ReadFile(outputPath)
	if err != nil || int64(len(canonical)) != outputInfo.Size() {
		return nil, domain.ErrInternal
	}
	if err := p.validateCanonicalFile(ctx, outputPath); err != nil {
		return nil, err
	}
	return canonical, nil
}

func (p *Processor) validateCanonicalFile(ctx context.Context, path string) error {
	canonicalMetadata, err := p.probe(ctx, path, false)
	if err != nil || len(canonicalMetadata.Streams) != 1 || canonicalMetadata.Streams[0].CodecType != "video" {
		return domain.ErrAvatarInvalid
	}
	canonicalInfo, err := p.probe(ctx, path, true)
	if err != nil || len(canonicalInfo.Streams) != 1 || canonicalInfo.Streams[0].CodecType != "video" {
		return domain.ErrAvatarInvalid
	}
	canonicalMetadata.Streams[0].NBReadFrames = canonicalInfo.Streams[0].NBReadFrames
	return validateCanonical(canonicalMetadata)
}

func (p *Processor) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case p.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return domain.ErrAvatarBusy
	}
}

func (p *Processor) probe(parent context.Context, path string, countFrames bool) (probeDocument, error) {
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()

	args := []string{
		"-hide_banner", "-v", "error", "-threads", "1", "-f", "mov",
		"-enable_drefs", "0", "-use_absolute_path", "0",
		"-protocol_whitelist", "file", "-max_alloc", strconv.Itoa(maxAllocBytes),
		"-max_streams", "4", "-max_pixels", strconv.Itoa(maxInputPixels),
	}
	if countFrames {
		args = append(args, "-count_frames", "-select_streams", "v:0")
	}
	args = append(args,
		"-show_entries", "format=format_name,duration:stream=codec_type,codec_name,width,height,pix_fmt,avg_frame_rate,r_frame_rate,duration,nb_frames,nb_read_frames",
		"-of", "json", path,
	)
	cmd := commandWithArgs(ctx, p.ffprobePath, args)
	cmd.Env = privateProcessEnv(filepath.Dir(path))
	stdout := &boundedCapture{limit: maxProbeBytes}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || stdout.exceeded {
		if parent.Err() != nil {
			return probeDocument{}, processingContextError(parent.Err())
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return probeDocument{}, domain.ErrAvatarBusy
		}
		return probeDocument{}, domain.ErrAvatarInvalid
	}
	var document probeDocument
	if err := json.Unmarshal(stdout.buffer.Bytes(), &document); err != nil {
		return probeDocument{}, domain.ErrAvatarInvalid
	}
	return document, nil
}

func (p *Processor) transcode(parent context.Context, inputPath, outputPath string) error {
	ctx, cancel := context.WithTimeout(parent, transcodeTimeout)
	defer cancel()

	filter := "fps=30,scale=w='min(512,iw)':h='min(512,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2"
	args := []string{
		"-hide_banner", "-v", "error", "-nostdin", "-y", "-xerror", "-err_detect", "explode",
		"-threads", "1", "-filter_threads", "1", "-f", "mov",
		"-enable_drefs", "0", "-use_absolute_path", "0", "-protocol_whitelist", "file",
		"-max_alloc", strconv.Itoa(maxAllocBytes), "-max_streams", "4", "-max_pixels", strconv.Itoa(maxInputPixels),
		"-i", inputPath, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", filter,
		"-c:v", "libx264", "-threads:v", "1", "-preset", "veryfast", "-crf", "24",
		"-profile:v", "baseline", "-pix_fmt", "yuv420p", "-map_metadata", "-1", "-map_chapters", "-1",
		"-movflags", "+faststart", "-fs", strconv.Itoa(maxInputBytes + 1), "-timelimit", strconv.Itoa(maxCPUSeconds),
		"-f", "mp4", outputPath,
	}
	cmd := commandWithArgs(ctx, p.ffmpegPath, args)
	cmd.Env = privateProcessEnv(filepath.Dir(inputPath))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if parent.Err() != nil {
			return processingContextError(parent.Err())
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return domain.ErrAvatarBusy
		}
		return domain.ErrAvatarInvalid
	}
	return nil
}

func validateInput(document probeDocument) error {
	if err := validateInputMetadata(document); err != nil {
		return err
	}
	var stream probeStream
	for _, candidate := range document.Streams {
		if candidate.CodecType == "video" {
			stream = candidate
			break
		}
	}
	frames, err := frameCount(stream.NBReadFrames)
	if err != nil || frames <= 0 || frames > maxInputFrames {
		return domain.ErrAvatarInvalid
	}
	return nil
}

func validateInputMetadata(document probeDocument) error {
	stream, err := inputVideoStream(document)
	if err != nil {
		return err
	}
	if err := validateDimensions(stream.Width, stream.Height, maxInputWidth, maxInputHeight, maxInputPixels); err != nil {
		return err
	}
	if err := validateDuration(document.Format.Duration, stream.Duration); err != nil {
		return err
	}
	if err := validateFrameRate(stream.AvgFrameRate, stream.RFrameRate, maxInputFPS); err != nil {
		return err
	}
	frames, err := frameCount(stream.NBReadFrames, stream.NBFrames)
	if err == nil && frames > maxInputFrames {
		return domain.ErrAvatarInvalid
	}
	return nil
}

func inputVideoStream(document probeDocument) (probeStream, error) {
	if !strings.Contains(document.Format.FormatName, "mp4") && !strings.Contains(document.Format.FormatName, "mov") {
		return probeStream{}, domain.ErrAvatarInvalid
	}
	if len(document.Streams) < 1 || len(document.Streams) > 2 {
		return probeStream{}, domain.ErrAvatarInvalid
	}
	videoStreams := 0
	audioStreams := 0
	var stream probeStream
	for _, candidate := range document.Streams {
		switch candidate.CodecType {
		case "video":
			videoStreams++
			stream = candidate
		case "audio":
			audioStreams++
		default:
			return probeStream{}, domain.ErrAvatarInvalid
		}
	}
	if videoStreams != 1 || audioStreams > 1 {
		return probeStream{}, domain.ErrAvatarInvalid
	}
	if !supportedInputVideoCodec(stream.CodecName) {
		return probeStream{}, domain.ErrAvatarInvalid
	}
	return stream, nil
}

func validateDimensions(width, height, maxWidth, maxHeight, maxPixels int) error {
	if width <= 0 || height <= 0 || width > maxWidth || height > maxHeight || int64(width)*int64(height) > int64(maxPixels) {
		return domain.ErrAvatarInvalid
	}
	return nil
}

func validateDuration(formatDuration, streamDuration string) error {
	duration, err := durationSeconds(formatDuration, streamDuration)
	if err != nil || duration <= 0 || duration > maxDuration.Seconds() {
		return domain.ErrAvatarInvalid
	}
	return nil
}

func validateFrameRate(average, nominal string, maximum float64) error {
	fps, err := maxFrameRate(average, nominal)
	if err != nil || fps <= 0 || fps > maximum {
		return domain.ErrAvatarInvalid
	}
	return nil
}

func supportedInputVideoCodec(codecName string) bool {
	switch codecName {
	case "h264", "hevc", "mpeg4":
		return true
	default:
		return false
	}
}

func validateCanonical(document probeDocument) error {
	stream, err := canonicalVideoStream(document)
	if err != nil {
		return err
	}
	if err := validateDimensions(stream.Width, stream.Height, maxOutputWidth, maxOutputHeight, maxOutputWidth*maxOutputHeight); err != nil {
		return err
	}
	if err := validateDuration(document.Format.Duration, stream.Duration); err != nil {
		return err
	}
	if err := validateFrameRate(stream.AvgFrameRate, stream.RFrameRate, maxOutputFPS); err != nil {
		return err
	}
	frames, err := frameCount(stream.NBReadFrames, stream.NBFrames)
	if err != nil || frames <= 0 || frames > maxOutputFrames {
		return domain.ErrAvatarInvalid
	}
	return nil
}

func canonicalVideoStream(document probeDocument) (probeStream, error) {
	if len(document.Streams) != 1 || document.Streams[0].CodecType != "video" {
		return probeStream{}, domain.ErrAvatarInvalid
	}
	stream := document.Streams[0]
	if stream.CodecName != "h264" || stream.PixelFormat != "yuv420p" {
		return probeStream{}, domain.ErrAvatarInvalid
	}
	return stream, nil
}

func durationSeconds(values ...string) (float64, error) {
	maximum := 0.0
	for _, value := range values {
		if value == "" || value == "N/A" {
			continue
		}
		seconds, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return 0, domain.ErrAvatarInvalid
		}
		if seconds > maximum {
			maximum = seconds
		}
	}
	if maximum == 0 {
		return 0, domain.ErrAvatarInvalid
	}
	return maximum, nil
}

func maxFrameRate(values ...string) (float64, error) {
	maximum := 0.0
	for _, value := range values {
		if value == "" || value == "N/A" {
			continue
		}
		parts := strings.Split(value, "/")
		if len(parts) != 2 {
			return 0, domain.ErrAvatarInvalid
		}
		numerator, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, domain.ErrAvatarInvalid
		}
		denominator, err := strconv.ParseFloat(parts[1], 64)
		if err != nil || denominator <= 0 {
			return 0, domain.ErrAvatarInvalid
		}
		rate := numerator / denominator
		if math.IsNaN(rate) || math.IsInf(rate, 0) {
			return 0, domain.ErrAvatarInvalid
		}
		if rate > maximum {
			maximum = rate
		}
	}
	return maximum, nil
}

func frameCount(values ...string) (int, error) {
	for _, value := range values {
		if value == "" || value == "N/A" {
			continue
		}
		count, err := strconv.Atoi(value)
		if err != nil || count < 0 {
			return 0, domain.ErrAvatarInvalid
		}
		return count, nil
	}
	return 0, domain.ErrAvatarInvalid
}

func resolveExecutable(path, name string) (string, error) {
	if path == "" {
		resolved, err := exec.LookPath(name)
		if err != nil {
			return "", err
		}
		path = resolved
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("media executable is not executable")
	}
	return absPath, nil
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, writeErr := file.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func createPrivateFile(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func privateProcessEnv(tempDir string) []string {
	return []string{
		"HOME=" + tempDir,
		"TMPDIR=" + tempDir,
		"PATH=/usr/bin:/bin",
		"LANG=C",
		"LC_ALL=C",
	}
}

func commandWithArgs(ctx context.Context, executable string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable)
	cmd.Args = append(cmd.Args, args...)
	return cmd
}

func processingContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.ErrAvatarBusy
	}
	return err
}

type probeDocument struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
	Streams []probeStream `json:"streams"`
}

type probeStream struct {
	CodecType    string `json:"codec_type"`
	CodecName    string `json:"codec_name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	PixelFormat  string `json:"pix_fmt"`
	AvgFrameRate string `json:"avg_frame_rate"`
	RFrameRate   string `json:"r_frame_rate"`
	Duration     string `json:"duration"`
	NBFrames     string `json:"nb_frames"`
	NBReadFrames string `json:"nb_read_frames"`
}

type boundedCapture struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (c *boundedCapture) Write(data []byte) (int, error) {
	remaining := c.limit - c.buffer.Len()
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		_, _ = c.buffer.Write(data[:remaining])
	}
	if remaining < len(data) {
		c.exceeded = true
	}
	return len(data), nil
}
