package engineapi

import (
	"context"
	"image"
	"time"
)

type Engine interface {
	Name() string
	Detect(ctx context.Context, frame Frame, opts DetectOptions) (DetectResult, error)
}

type Frame struct {
	Path      string
	PageIndex int
	Image     image.Image
}

type DetectOptions struct {
	Thorough bool
}

type DetectResult struct {
	Found   bool
	Elapsed time.Duration
}

