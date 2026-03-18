package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/term"
)

type OutputFormat string

const (
	OutputText OutputFormat = "text"
	OutputJSON OutputFormat = "json"
)

type EngineMode string

const (
	EngineAuto   EngineMode = "auto"
	EngineGo     EngineMode = "go"
	EngineNative EngineMode = "native"
)

type Options struct {
	Inputs       []string
	ForceTUI     bool
	DisableTUI   bool
	OutputFormat OutputFormat
	EngineMode   EngineMode
	Workers      int
	Thorough     bool
	Includes     []string
	Excludes     []string
	Recursive    bool
	StrictErrors bool
	Quiet        bool
}

func Parse(args []string) (Options, error) {
	opts := Options{
		OutputFormat: OutputText,
		EngineMode:   EngineAuto,
		Workers:      runtime.NumCPU(),
		Recursive:    true,
	}

	fs := flag.NewFlagSet("qrcheck", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: qrcheck [flags] <path>...\n")
		fs.PrintDefaults()
	}

	format := fs.String("format", string(opts.OutputFormat), "Output format: text or json")
	engine := fs.String("engine", string(opts.EngineMode), "Detection engine: auto, go, native")
	fs.BoolVar(&opts.ForceTUI, "tui", false, "Force interactive TUI mode")
	fs.BoolVar(&opts.DisableTUI, "no-tui", false, "Disable interactive TUI mode")
	fs.IntVar(&opts.Workers, "workers", opts.Workers, "Number of detection workers")
	fs.BoolVar(&opts.Thorough, "thorough", false, "Enable slower fallback passes for hard images")
	noRecursive := fs.Bool("no-recursive", false, "Do not recurse into directories")
	fs.BoolVar(&opts.StrictErrors, "strict-errors", false, "Treat file-level errors as fatal")
	fs.BoolVar(&opts.Quiet, "quiet", false, "Suppress per-file lines outside the TUI")
	fs.Func("include", "Repeatable include glob", func(v string) error {
		opts.Includes = append(opts.Includes, v)
		return nil
	})
	fs.Func("exclude", "Repeatable exclude glob", func(v string) error {
		opts.Excludes = append(opts.Excludes, v)
		return nil
	})

	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}

	opts.Inputs = fs.Args()
	if opts.ForceTUI && opts.DisableTUI {
		return Options{}, errors.New("cannot use --tui and --no-tui together")
	}

	switch OutputFormat(strings.ToLower(*format)) {
	case OutputText, OutputJSON:
		opts.OutputFormat = OutputFormat(strings.ToLower(*format))
	default:
		return Options{}, fmt.Errorf("invalid --format %q", *format)
	}

	switch EngineMode(strings.ToLower(*engine)) {
	case EngineAuto, EngineGo, EngineNative:
		opts.EngineMode = EngineMode(strings.ToLower(*engine))
	default:
		return Options{}, fmt.Errorf("invalid --engine %q", *engine)
	}

	if opts.Workers <= 0 {
		return Options{}, fmt.Errorf("invalid --workers %d", opts.Workers)
	}

	opts.Recursive = !*noRecursive

	cleaned := make([]string, 0, len(opts.Inputs))
	for _, input := range opts.Inputs {
		cleaned = append(cleaned, filepath.Clean(input))
	}
	opts.Inputs = cleaned

	return opts, nil
}

func ShouldUseTUI(opts Options) bool {
	if opts.DisableTUI || opts.OutputFormat != OutputText {
		return false
	}
	if opts.ForceTUI {
		return true
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}
	if len(opts.Inputs) == 0 {
		return true
	}
	if len(opts.Inputs) > 1 {
		return true
	}
	for _, input := range opts.Inputs {
		info, err := os.Stat(input)
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
