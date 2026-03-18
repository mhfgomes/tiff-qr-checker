package testdata

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	andelingtiff "github.com/Andeling/tiff"
	qrcode "github.com/skip2/go-qrcode"
)

func WriteQRPNG(tb testing.TB, dir, name string) string {
	tb.Helper()
	qr, err := qrcode.New("qrcheck-test", qrcode.Medium)
	if err != nil {
		tb.Fatalf("create qr: %v", err)
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, qr.Image(256)); err != nil {
		tb.Fatalf("encode png: %v", err)
	}
	return path
}

func WriteQRJPEG(tb testing.TB, dir, name string) string {
	tb.Helper()
	qr, err := qrcode.New("qrcheck-test", qrcode.Medium)
	if err != nil {
		tb.Fatalf("create qr: %v", err)
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, qr.Image(256), &jpeg.Options{Quality: 100}); err != nil {
		tb.Fatalf("encode jpeg: %v", err)
	}
	return path
}

func WriteBlankPNG(tb testing.TB, dir, name string) string {
	tb.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, BlankImage(256)); err != nil {
		tb.Fatalf("encode blank png: %v", err)
	}
	return path
}

func WriteUnsupported(tb testing.TB, dir, name string) string {
	tb.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("not an image"), 0o644); err != nil {
		tb.Fatalf("write %s: %v", path, err)
	}
	return path
}

func WriteSinglePageTIFF(tb testing.TB, dir, name string, img image.Image) string {
	tb.Helper()
	return WriteMultiPageTIFF(tb, dir, name, []image.Image{img})
}

func WriteMultiPageTIFF(tb testing.TB, dir, name string, pages []image.Image) string {
	tb.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	enc := andelingtiff.NewEncoder(f)
	for _, page := range pages {
		gray := toGray(page)
		im := enc.NewImage()
		im.SetWidthHeight(gray.Bounds().Dx(), gray.Bounds().Dy())
		im.SetPixelFormat(andelingtiff.PhotometricBlackIsZero, 1, []int{8})
		im.SetCompression(andelingtiff.CompressionNone)
		im.SetRowsPerStrip(gray.Bounds().Dy())
		if err := im.EncodeImage(gray.Pix); err != nil {
			tb.Fatalf("encode tiff page: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		tb.Fatalf("close tiff encoder: %v", err)
	}
	return path
}

func QRImage(tb testing.TB) image.Image {
	tb.Helper()
	qr, err := qrcode.New("qrcheck-test", qrcode.Medium)
	if err != nil {
		tb.Fatalf("create qr: %v", err)
	}
	return qr.Image(256)
}

func BlankImage(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	return img
}

func EncodePNGBytes(tb testing.TB, img image.Image) []byte {
	tb.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		tb.Fatalf("encode png bytes: %v", err)
	}
	return buf.Bytes()
}

func FixturePath(tb testing.TB, name string) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatalf("resolve fixture path")
	}
	return filepath.Join(filepath.Dir(file), "fixtures", name)
}

func toGray(src image.Image) *image.Gray {
	if gray, ok := src.(*image.Gray); ok {
		return gray
	}
	bounds := src.Bounds()
	dst := image.NewGray(bounds)
	draw.Draw(dst, bounds, src, bounds.Min, draw.Src)
	return dst
}
