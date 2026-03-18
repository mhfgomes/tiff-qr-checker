package gozxing

import (
	"context"
	"image"
	"sync"

	zxing "github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"qrcheck/internal/engineapi"
	"qrcheck/internal/imageio"
)

type QRCodeEngine struct {
	readerPool sync.Pool
}

func New() *QRCodeEngine {
	return &QRCodeEngine{
		readerPool: sync.Pool{
			New: func() any {
				return qrcode.NewQRCodeReader()
			},
		},
	}
}

func (e *QRCodeEngine) Name() string {
	return "go"
}

func (e *QRCodeEngine) Detect(ctx context.Context, frame engineapi.Frame, opts engineapi.DetectOptions) (engineapi.DetectResult, error) {
	if err := ctx.Err(); err != nil {
		return engineapi.DetectResult{}, err
	}

	reader := e.readerPool.Get().(zxing.Reader)
	defer e.readerPool.Put(reader)

	source := newLuminanceSource(frame.Image)
	if found := detectWithSource(reader, source); found {
		return engineapi.DetectResult{Found: true}, nil
	}
	if err := ctx.Err(); err != nil {
		return engineapi.DetectResult{}, err
	}
	if shouldTryFastResize(frame.Image) {
		resized := imageio.ResizeForFastRetry(frame.Image, 1800)
		if !sameImage(resized, frame.Image) {
			resizedSource := newLuminanceSource(resized)
			if found := detectWithSource(reader, resizedSource); found {
				return engineapi.DetectResult{Found: true}, nil
			}
			if found := detectWithSource(reader, resizedSource.Invert()); found {
				return engineapi.DetectResult{Found: true}, nil
			}
		}
	}
	if found := detectWithSource(reader, source.Invert()); found {
		return engineapi.DetectResult{Found: true}, nil
	}
	if opts.Thorough {
		resized := imageio.ResizeForThorough(frame.Image, 2048)
		if !sameImage(resized, frame.Image) {
			resizedSource := newLuminanceSource(resized)
			if found := detectWithSource(reader, resizedSource); found {
				return engineapi.DetectResult{Found: true}, nil
			}
			if found := detectWithSource(reader, resizedSource.Invert()); found {
				return engineapi.DetectResult{Found: true}, nil
			}
		}
	}

	return engineapi.DetectResult{Found: false}, nil
}

func detectWithSource(reader zxing.Reader, source zxing.LuminanceSource) bool {
	bitmap, err := zxing.NewBinaryBitmap(zxing.NewHybridBinarizer(source))
	if err != nil {
		return false
	}
	_, err = reader.Decode(bitmap, nil)
	return err == nil
}

func sameImage(a, b image.Image) bool {
	return a == b
}

func shouldTryFastResize(img image.Image) bool {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width == 0 || height == 0 {
		return false
	}
	maxDim := width
	if height > maxDim {
		maxDim = height
	}
	return maxDim >= 3000
}
