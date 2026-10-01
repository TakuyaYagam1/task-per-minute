package avatar

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	maxGIFFrames       = 120
	maxGIFTotalPixels  = 32 << 20
	maxGIFCanvasPixels = 16 << 20
	jpegQuality        = 88
)

var errCanonicalImageTooLarge = errors.New("canonical avatar exceeds size limit")

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (w *boundedBuffer) Write(data []byte) (int, error) {
	if len(data) > w.limit-w.buffer.Len() {
		return 0, errCanonicalImageTooLarge
	}
	return w.buffer.Write(data)
}

func encodeCanonicalImage(data []byte, contentType string) (string, []byte, error) {
	switch contentType {
	case "image/jpeg", "image/png":
		return encodeStaticImage(data, contentType)
	case "image/gif":
		return encodeCanonicalGIF(data)
	default:
		return "", nil, domain.ErrAvatarUnsupportedMediaType
	}
}

func encodeStaticImage(data []byte, contentType string) (string, []byte, error) {
	config, format, err := decodeStaticConfig(data, contentType)
	if err != nil {
		return "", nil, err
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decoded == nil || decodedFormat != format {
		return "", nil, domain.ErrAvatarInvalid
	}
	if bounds := decoded.Bounds(); bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		return "", nil, domain.ErrAvatarInvalid
	}
	writer := &boundedBuffer{limit: MaxAvatarBytes}
	if format == "jpeg" {
		err = jpeg.Encode(writer, decoded, &jpeg.Options{Quality: jpegQuality})
	} else {
		err = png.Encode(writer, decoded)
	}
	if err != nil {
		return "", nil, canonicalEncodeError(err)
	}
	return contentType, writer.buffer.Bytes(), nil
}

func decodeStaticConfig(data []byte, contentType string) (image.Config, string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return image.Config{}, "", domain.ErrAvatarInvalid
	}
	if (format != "jpeg" && format != "png") ||
		(format == "jpeg" && contentType != "image/jpeg") ||
		(format == "png" && contentType != "image/png") {
		return image.Config{}, "", domain.ErrAvatarInvalid
	}
	if err := validateDimensions(config.Width, config.Height, maxStaticPixels); err != nil {
		return image.Config{}, "", err
	}
	return config, format, nil
}

func encodeCanonicalGIF(data []byte) (string, []byte, error) {
	if err := scanGIF(data); err != nil {
		return "", nil, err
	}
	decoded, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil || decoded == nil || len(decoded.Image) == 0 || len(decoded.Image) > maxGIFFrames {
		return "", nil, domain.ErrAvatarInvalid
	}
	if err := validateDimensions(decoded.Config.Width, decoded.Config.Height, maxGIFCanvasPixels); err != nil {
		return "", nil, err
	}
	writer := &boundedBuffer{limit: MaxAvatarBytes}
	if err := gif.EncodeAll(writer, decoded); err != nil {
		return "", nil, canonicalEncodeError(err)
	}
	return "image/gif", writer.buffer.Bytes(), nil
}

func canonicalEncodeError(err error) error {
	if errors.Is(err, errCanonicalImageTooLarge) {
		return domain.ErrAvatarTooLarge
	}
	return domain.ErrAvatarInvalid
}

func validateDimensions(width, height int, maxPixels uint64) error {
	if width <= 0 || height <= 0 || width > maxAvatarWidth || height > maxAvatarHeight {
		return domain.ErrAvatarInvalid
	}
	pixels := uint64(width) * uint64(height)
	if pixels > maxPixels {
		return domain.ErrAvatarInvalid
	}
	return nil
}

func scanGIF(data []byte) error {
	scanner, err := newGIFScanner(data)
	if err != nil {
		return err
	}
	for scanner.offset < len(data) {
		introducer := data[scanner.offset]
		scanner.offset++
		switch introducer {
		case 0x3b:
			if scanner.frames == 0 || scanner.offset != len(data) {
				return domain.ErrAvatarInvalid
			}
			return nil
		case 0x21:
			if err := scanner.skipExtension(); err != nil {
				return err
			}
		case 0x2c:
			if err := scanner.skipFrame(); err != nil {
				return err
			}
		default:
			return domain.ErrAvatarInvalid
		}
	}
	return domain.ErrAvatarInvalid
}

type gifScanner struct {
	data         []byte
	offset       int
	canvasWidth  int
	canvasHeight int
	frames       int
	totalPixels  uint64
}

func newGIFScanner(data []byte) (*gifScanner, error) {
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return nil, domain.ErrAvatarInvalid
	}
	canvasWidth := int(binary.LittleEndian.Uint16(data[6:8]))
	canvasHeight := int(binary.LittleEndian.Uint16(data[8:10]))
	if err := validateDimensions(canvasWidth, canvasHeight, maxGIFCanvasPixels); err != nil {
		return nil, err
	}
	scanner := &gifScanner{
		data:         data,
		offset:       13,
		canvasWidth:  canvasWidth,
		canvasHeight: canvasHeight,
	}
	packed := data[10]
	if packed&0x80 != 0 {
		colorTableBytes := 3 * (1 << (int(packed&0x07) + 1))
		if !hasBytes(data, scanner.offset, colorTableBytes) {
			return nil, domain.ErrAvatarInvalid
		}
		scanner.offset += colorTableBytes
	}
	return scanner, nil
}

func (s *gifScanner) skipExtension() error {
	if !hasBytes(s.data, s.offset, 1) {
		return domain.ErrAvatarInvalid
	}
	s.offset++ // extension label
	offset, err := skipGIFSubBlocks(s.data, s.offset)
	if err != nil {
		return err
	}
	s.offset = offset
	return nil
}

func (s *gifScanner) skipFrame() error {
	if !hasBytes(s.data, s.offset, 9) {
		return domain.ErrAvatarInvalid
	}
	left := int(binary.LittleEndian.Uint16(s.data[s.offset : s.offset+2]))
	top := int(binary.LittleEndian.Uint16(s.data[s.offset+2 : s.offset+4]))
	frameWidth := int(binary.LittleEndian.Uint16(s.data[s.offset+4 : s.offset+6]))
	frameHeight := int(binary.LittleEndian.Uint16(s.data[s.offset+6 : s.offset+8]))
	framePacked := s.data[s.offset+8]
	s.offset += 9
	if err := s.validateFrameBounds(left, top, frameWidth, frameHeight); err != nil {
		return err
	}
	if framePacked&0x80 != 0 {
		colorTableBytes := 3 * (1 << (int(framePacked&0x07) + 1))
		if !hasBytes(s.data, s.offset, colorTableBytes) {
			return domain.ErrAvatarInvalid
		}
		s.offset += colorTableBytes
	}
	if !hasBytes(s.data, s.offset, 1) || s.data[s.offset] < 2 || s.data[s.offset] > 8 {
		return domain.ErrAvatarInvalid
	}
	s.offset++ // LZW minimum code size
	offset, err := skipGIFSubBlocks(s.data, s.offset)
	if err != nil {
		return err
	}
	s.offset = offset
	return nil
}

func (s *gifScanner) validateFrameBounds(left, top, width, height int) error {
	if width <= 0 || height <= 0 || left+width > s.canvasWidth || top+height > s.canvasHeight {
		return domain.ErrAvatarInvalid
	}
	if width > maxAvatarWidth || height > maxAvatarHeight {
		return domain.ErrAvatarInvalid
	}
	s.frames++
	if s.frames > maxGIFFrames {
		return domain.ErrAvatarInvalid
	}
	s.totalPixels += uint64(width) * uint64(height)
	if s.totalPixels > maxGIFTotalPixels {
		return domain.ErrAvatarInvalid
	}
	return nil
}

func skipGIFSubBlocks(data []byte, offset int) (int, error) {
	for {
		if !hasBytes(data, offset, 1) {
			return 0, domain.ErrAvatarInvalid
		}
		size := int(data[offset])
		offset++
		if size == 0 {
			return offset, nil
		}
		if !hasBytes(data, offset, size) {
			return 0, domain.ErrAvatarInvalid
		}
		offset += size
	}
}

func hasBytes(data []byte, offset, count int) bool {
	return offset >= 0 && count >= 0 && offset <= len(data) && count <= len(data)-offset
}
