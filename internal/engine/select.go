package engine

import (
	"qrcheck/internal/cli"
	"qrcheck/internal/engine/gozxing"
	"qrcheck/internal/engine/native"
	"qrcheck/internal/engineapi"
)

func Select(mode cli.EngineMode) (engineapi.Engine, error) {
	switch mode {
	case cli.EngineGo:
		return gozxing.New(), nil
	case cli.EngineNative:
		return native.New()
	case cli.EngineAuto:
		if native.Available() {
			if eng, err := native.New(); err == nil {
				return eng, nil
			}
		}
		return gozxing.New(), nil
	default:
		return nil, native.ErrUnsupportedMode(mode)
	}
}

