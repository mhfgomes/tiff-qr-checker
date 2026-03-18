//go:build native_zxing

package native

import (
	"errors"

	"qrcheck/internal/engineapi"
)

var errNativeScaffold = errors.New("native_zxing build tag enabled, but the ZXing-C++ bridge is not implemented yet")

func Available() bool {
	return false
}

func New() (engineapi.Engine, error) {
	return nil, errNativeScaffold
}
