package imageio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	andelingtiff "github.com/Andeling/tiff"
	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"qrcheck/internal/engineapi"
	"qrcheck/internal/report"
)

type Format string

const (
	FormatUnknown Format = "unknown"
	FormatPNG     Format = "png"
	FormatJPEG    Format = "jpeg"
	FormatTIFF    Format = "tiff"
	FormatGIF     Format = "gif"
	FormatBMP     Format = "bmp"
	FormatWEBP    Format = "webp"
)

var ErrUnsupportedFormat = errors.New("unsupported image format")

var (
	magickOnce sync.Once
	magickPath string
)

type ScanOptions struct {
	Thorough     bool
	StrictErrors bool
}

func DetectFormat(path string) (Format, error) {
	f, err := os.Open(path)
	if err != nil {
		return FormatUnknown, err
	}
	defer f.Close()

	header := make([]byte, 16)
	n, err := io.ReadFull(f, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return FormatUnknown, err
	}
	header = header[:n]

	switch {
	case len(header) >= 8 && string(header[:8]) == "\x89PNG\r\n\x1a\n":
		return FormatPNG, nil
	case len(header) >= 3 && header[0] == 0xFF && header[1] == 0xD8 && header[2] == 0xFF:
		return FormatJPEG, nil
	case len(header) >= 4 && (string(header[:4]) == "II*\x00" || string(header[:4]) == "MM\x00*"):
		return FormatTIFF, nil
	case len(header) >= 6 && (string(header[:6]) == "GIF87a" || string(header[:6]) == "GIF89a"):
		return FormatGIF, nil
	case len(header) >= 2 && header[0] == 'B' && header[1] == 'M':
		return FormatBMP, nil
	case len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP":
		return FormatWEBP, nil
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return FormatPNG, nil
	case ".jpg", ".jpeg":
		return FormatJPEG, nil
	case ".tif", ".tiff":
		return FormatTIFF, nil
	case ".gif":
		return FormatGIF, nil
	case ".bmp":
		return FormatBMP, nil
	case ".webp":
		return FormatWEBP, nil
	default:
		return FormatUnknown, ErrUnsupportedFormat
	}
}

func ScanFile(ctx context.Context, path string, format Format, eng engineapi.Engine, opts ScanOptions) report.Result {
	started := time.Now()
	result := report.Result{
		Path:   path,
		Format: string(format),
	}

	if format == FormatTIFF {
		scanTIFF(ctx, &result, path, eng, opts)
		return result.WithDuration(time.Since(started))
	}

	img, _, err := decodeSingle(path)
	if err != nil {
		result.Error = err.Error()
		return result.WithDuration(time.Since(started))
	}
	result.PagesScanned = 1

	detect, err := eng.Detect(ctx, engineapi.Frame{Path: path, PageIndex: 0, Image: img}, engineapi.DetectOptions{Thorough: opts.Thorough})
	if err != nil && ctx.Err() != nil {
		result.Error = ctx.Err().Error()
		return result.WithDuration(time.Since(started))
	}
	result.Found = detect.Found
	return result.WithDuration(time.Since(started))
}

func decodeSingle(path string) (image.Image, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	img, format, err := image.Decode(f)
	if err != nil {
		return nil, "", err
	}
	return img, format, nil
}

func scanTIFF(ctx context.Context, result *report.Result, path string, eng engineapi.Engine, opts ScanOptions) {
	f, err := os.Open(path)
	if err != nil {
		result.Error = err.Error()
		return
	}
	defer f.Close()

	dec, err := andelingtiff.NewDecoder(f)
	if err != nil {
		result.Error = err.Error()
		return
	}

	iter := dec.Iter()
	for iter.Next() {
		if ctx.Err() != nil {
			result.Error = ctx.Err().Error()
			return
		}

		img, err := decodeTIFFImage(iter.Image())
		if err != nil {
			if result.Error == "" {
				result.Error = err.Error()
			}
			if opts.StrictErrors {
				return
			}
			continue
		}

		result.PagesScanned++
		detect, detectErr := eng.Detect(ctx, engineapi.Frame{
			Path:      path,
			PageIndex: iter.Index(),
			Image:     img,
		}, engineapi.DetectOptions{Thorough: opts.Thorough})
		if detectErr != nil && ctx.Err() != nil {
			result.Error = ctx.Err().Error()
			return
		}
		if detect.Found {
			result.Found = true
			hit := iter.Index() + 1
			result.FirstHitPage = &hit
			if !opts.Thorough {
				break
			}
		}
	}

	if err := iter.Err(); err != nil {
		if result.Error == "" {
			result.Error = err.Error()
		}
	}

	if !result.Found && opts.Thorough && result.PagesScanned <= 1 {
		if img, _, err := decodeSingle(path); err == nil {
			detect, detectErr := eng.Detect(ctx, engineapi.Frame{
				Path:      path,
				PageIndex: 0,
				Image:     img,
			}, engineapi.DetectOptions{Thorough: opts.Thorough})
			if detectErr != nil && ctx.Err() != nil {
				result.Error = ctx.Err().Error()
				return
			}
			if detect.Found {
				result.Found = true
				hit := 1
				result.FirstHitPage = &hit
				if result.PagesScanned == 0 {
					result.PagesScanned = 1
				}
			}
		}
	}

	if !result.Found && opts.Thorough && result.PagesScanned <= 1 {
		if img, err := rasterizeWithMagick(path, 0); err == nil {
			detect, detectErr := eng.Detect(ctx, engineapi.Frame{
				Path:      path,
				PageIndex: 0,
				Image:     img,
			}, engineapi.DetectOptions{Thorough: opts.Thorough})
			if detectErr != nil && ctx.Err() != nil {
				result.Error = ctx.Err().Error()
				return
			}
			if detect.Found {
				result.Found = true
				hit := 1
				result.FirstHitPage = &hit
				if result.PagesScanned == 0 {
					result.PagesScanned = 1
				}
			}
		}
	}

	if result.Error != "" && result.Found && !opts.StrictErrors {
		result.Error = ""
	}
}

func decodeTIFFImage(im *andelingtiff.Image) (image.Image, error) {
	width, height := im.WidthHeight()
	samples := im.SamplesPerPixel()
	photometric := photometricOf(im)
	if width <= 0 || height <= 0 || samples <= 0 {
		return nil, fmt.Errorf("invalid TIFF page dimensions")
	}

	switch im.DataType() {
	case andelingtiff.Uint8:
		buf := make([]uint8, width*height*samples)
		if err := im.DecodeImage(buf); err != nil {
			return nil, err
		}
		return imageFromUint8(width, height, samples, photometric, buf), nil
	case andelingtiff.Uint16:
		buf := make([]uint16, width*height*samples)
		if err := im.DecodeImage(buf); err != nil {
			return nil, err
		}
		return imageFromUint16(width, height, samples, photometric, buf), nil
	default:
		return nil, fmt.Errorf("unsupported TIFF pixel data type")
	}
}

func imageFromUint8(width, height, samples, photometric int, buf []uint8) image.Image {
	if photometric == andelingtiff.PhotometricYCbCr && samples >= 3 {
		img := image.NewGray(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			src := y * width * samples
			dst := y * img.Stride
			for x := 0; x < width; x++ {
				img.Pix[dst+x] = buf[src+x*samples]
			}
		}
		return img
	}
	switch samples {
	case 1:
		img := image.NewGray(image.Rect(0, 0, width, height))
		copy(img.Pix, buf)
		return img
	case 3:
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		i := 0
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				offset := img.PixOffset(x, y)
				img.Pix[offset] = buf[i]
				img.Pix[offset+1] = buf[i+1]
				img.Pix[offset+2] = buf[i+2]
				img.Pix[offset+3] = 0xFF
				i += 3
			}
		}
		return img
	case 4:
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		copy(img.Pix, buf)
		return img
	default:
		gray := image.NewGray(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			dst := y * gray.Stride
			for x := 0; x < width; x++ {
				base := (y*width + x) * samples
				var sum int
				for i := 0; i < samples; i++ {
					sum += int(buf[base+i])
				}
				gray.Pix[dst+x] = uint8(sum / samples)
			}
		}
		return gray
	}
}

func imageFromUint16(width, height, samples, photometric int, buf []uint16) image.Image {
	gray := image.NewGray(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		dst := y * gray.Stride
		for x := 0; x < width; x++ {
			base := (y*width + x) * samples
			if photometric == andelingtiff.PhotometricYCbCr && samples >= 3 {
				gray.Pix[dst+x] = uint8(buf[base] >> 8)
				continue
			}
			var sum uint32
			for i := 0; i < samples; i++ {
				sum += uint32(buf[base+i] >> 8)
			}
			gray.Pix[dst+x] = uint8(sum / uint32(samples))
		}
	}
	return gray
}

func ResizeForThorough(img image.Image, maxDim int) image.Image {
	return resizeToMaxDim(img, maxDim, xdraw.ApproxBiLinear)
}

func ResizeForFastRetry(img image.Image, maxDim int) image.Image {
	if gray, ok := img.(*image.Gray); ok {
		return resizeGrayNearest(gray, maxDim)
	}
	if ycbcr, ok := img.(*image.YCbCr); ok {
		return resizeYCbCrLumaNearest(ycbcr, maxDim)
	}
	return resizeToMaxDim(img, maxDim, xdraw.NearestNeighbor)
}

func resizeToMaxDim(img image.Image, maxDim int, scaler xdraw.Scaler) image.Image {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= maxDim && height <= maxDim {
		return img
	}

	targetW := width
	targetH := height
	if width >= height {
		targetW = maxDim
		targetH = int(float64(height) * float64(maxDim) / float64(width))
	} else {
		targetH = maxDim
		targetW = int(float64(width) * float64(maxDim) / float64(height))
	}
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	scaler.Scale(dst, dst.Bounds(), img, bounds, xdraw.Over, nil)
	return dst
}

func resizeGrayNearest(src *image.Gray, maxDim int) image.Image {
	targetW, targetH, ok := targetSize(src.Bounds(), maxDim)
	if !ok {
		return src
	}
	dst := image.NewGray(image.Rect(0, 0, targetW, targetH))
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	for y := 0; y < targetH; y++ {
		srcY := srcBounds.Min.Y + y*srcH/targetH
		dstOffset := y * dst.Stride
		for x := 0; x < targetW; x++ {
			srcX := srcBounds.Min.X + x*srcW/targetW
			dst.Pix[dstOffset+x] = src.Pix[src.PixOffset(srcX, srcY)]
		}
	}
	return dst
}

func resizeYCbCrLumaNearest(src *image.YCbCr, maxDim int) image.Image {
	targetW, targetH, ok := targetSize(src.Bounds(), maxDim)
	if !ok {
		return src
	}
	dst := image.NewGray(image.Rect(0, 0, targetW, targetH))
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	for y := 0; y < targetH; y++ {
		srcY := srcBounds.Min.Y + y*srcH/targetH
		dstOffset := y * dst.Stride
		for x := 0; x < targetW; x++ {
			srcX := srcBounds.Min.X + x*srcW/targetW
			dst.Pix[dstOffset+x] = src.Y[src.YOffset(srcX, srcY)]
		}
	}
	return dst
}

func targetSize(bounds image.Rectangle, maxDim int) (int, int, bool) {
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= maxDim && height <= maxDim {
		return width, height, false
	}

	targetW := width
	targetH := height
	if width >= height {
		targetW = maxDim
		targetH = int(float64(height) * float64(maxDim) / float64(width))
	} else {
		targetH = maxDim
		targetW = int(float64(width) * float64(maxDim) / float64(height))
	}
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}
	return targetW, targetH, true
}

func rasterizeWithMagick(path string, page int) (image.Image, error) {
	exe, ok := magickBinary()
	if !ok {
		return nil, errors.New("magick not available")
	}

	pageSpec := fmt.Sprintf("%s[%d]", path, page)
	cmd := exec.Command(exe, pageSpec, "png:-")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("magick rasterize failed: %s", strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}

	img, _, err := image.Decode(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return nil, err
	}
	return img, nil
}

func magickBinary() (string, bool) {
	magickOnce.Do(func() {
		if path, err := exec.LookPath("magick"); err == nil {
			magickPath = path
		}
	})
	return magickPath, magickPath != ""
}

func photometricOf(im *andelingtiff.Image) int {
	tag := im.Tag[andelingtiff.TagPhotometric]
	if tag == nil {
		return andelingtiff.PhotometricBlackIsZero
	}
	value, ok := tag.Uint()
	if !ok {
		return andelingtiff.PhotometricBlackIsZero
	}
	return int(value)
}
