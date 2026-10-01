package avatar

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestCanonicalAvatarReencodesSupportedFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		input       []byte
	}{
		{name: "jpeg", contentType: "image/jpeg", input: encodedJPEG(t, 32, 24)},
		{name: "png", contentType: "image/png", input: encodedPNG(t, 32, 24)},
		{name: "animated gif", contentType: "image/gif", input: encodedGIF(t)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contentType, encoded, err := canonicalAvatar(test.input, test.contentType)
			require.NoError(t, err)
			require.Equal(t, test.contentType, contentType)
			require.NotEmpty(t, encoded)
			require.LessOrEqual(t, len(encoded), MaxAvatarBytes)
			require.Equal(t, contentType, detectAvatarType(encoded))
			if contentType == "image/gif" {
				decoded, err := gif.DecodeAll(bytes.NewReader(encoded))
				require.NoError(t, err)
				require.Len(t, decoded.Image, 2)
				require.Equal(t, []int{5, 7}, decoded.Delay)
				require.Equal(t, 0, decoded.LoopCount)
			}
		})
	}
}

func TestCanonicalAvatarRejectsUnsupportedMismatchAndCorruptBytes(t *testing.T) {
	t.Parallel()

	validPNG := encodedPNG(t, 8, 8)
	_, _, err := canonicalAvatar(validPNG, "image/webp")
	require.ErrorIs(t, err, domain.ErrAvatarUnsupportedMediaType)

	_, _, err = canonicalAvatar(validPNG, "image/jpeg")
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)

	_, _, err = canonicalAvatar([]byte("not an image"), "image/png")
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)
}

func TestScanGIFRejectsOversizedCanvasAndFramePixelBudgetBeforeDecode(t *testing.T) {
	t.Parallel()

	tooWide := testGIF(5000, 1, 1)
	err := scanGIF(tooWide)
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)

	tooManyPixels := testGIF(4096, 4096, 3)
	err = scanGIF(tooManyPixels)
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)
}

func TestCanonicalAvatarRejectsImageDimensionsBeforeDecode(t *testing.T) {
	t.Parallel()

	tooLarge := testGIF(4097, 1, 1)
	_, _, err := canonicalAvatar(tooLarge, "image/gif")
	require.ErrorIs(t, err, domain.ErrAvatarInvalid)
}

func encodedJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	imageData := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			imageData.Set(x, y, color.RGBA{R: uint8(x * 5), G: uint8(y * 7), B: 80, A: 255})
		}
	}
	require.NoError(t, jpeg.Encode(&output, imageData, &jpeg.Options{Quality: 90}))
	return output.Bytes()
}

func encodedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	imageData := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			imageData.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 5), G: uint8(y * 7), B: 80, A: 220})
		}
	}
	require.NoError(t, png.Encode(&output, imageData))
	return output.Bytes()
}

func encodedGIF(t *testing.T) []byte {
	t.Helper()
	palette := color.Palette{color.RGBA{A: 255}, color.RGBA{R: 220, G: 80, B: 60, A: 255}}
	first := image.NewPaletted(image.Rect(0, 0, 8, 8), palette)
	second := image.NewPaletted(image.Rect(0, 0, 8, 8), palette)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if (x+y)%2 == 0 {
				first.SetColorIndex(x, y, 1)
			}
			if x == y {
				second.SetColorIndex(x, y, 1)
			}
		}
	}
	animated := &gif.GIF{
		Image:     []*image.Paletted{first, second},
		Delay:     []int{5, 7},
		Disposal:  []byte{gif.DisposalNone, gif.DisposalBackground},
		LoopCount: 0,
		Config:    image.Config{ColorModel: palette, Width: 8, Height: 8},
	}
	var output bytes.Buffer
	require.NoError(t, gif.EncodeAll(&output, animated))
	return output.Bytes()
}

func testGIF(width, height, frames int) []byte {
	data := []byte("GIF89a")
	data = binary.LittleEndian.AppendUint16(data, uint16(width))
	data = binary.LittleEndian.AppendUint16(data, uint16(height))
	data = append(data, 0, 0, 0)
	for range frames {
		data = append(data, 0x2c)
		data = binary.LittleEndian.AppendUint16(data, 0)
		data = binary.LittleEndian.AppendUint16(data, 0)
		data = binary.LittleEndian.AppendUint16(data, uint16(width))
		data = binary.LittleEndian.AppendUint16(data, uint16(height))
		data = append(data, 0, 2, 0)
	}
	return append(data, 0x3b)
}

func detectAvatarType(data []byte) string {
	return http.DetectContentType(data)
}
