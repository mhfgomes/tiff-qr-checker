package native

import (
	"fmt"

	"qrcheck/internal/cli"
)

func ErrUnsupportedMode(mode cli.EngineMode) error {
	return fmt.Errorf("unsupported engine mode %q", mode)
}

