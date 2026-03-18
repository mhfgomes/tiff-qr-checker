//go:build !native_zxing

package native

import (
	"errors"

	"qrcheck/internal/engineapi"
)

var errNotCompiled = errors.New("native engine not compiled in; build with -tags native_zxing")

func Available() bool {
	return false
}

func New() (engineapi.Engine, error) {
	return nil, errNotCompiled
}
