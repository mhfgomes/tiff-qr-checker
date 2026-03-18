package cli

import "testing"

func TestParseFlags(t *testing.T) {
	opts, err := Parse([]string{
		"--format", "json",
		"--engine", "go",
		"--workers", "3",
		"--include", "*.png",
		"--exclude", "*.tmp",
		"--no-recursive",
		"fixtures",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if opts.OutputFormat != OutputJSON {
		t.Fatalf("expected json output, got %s", opts.OutputFormat)
	}
	if opts.EngineMode != EngineGo {
		t.Fatalf("expected go engine, got %s", opts.EngineMode)
	}
	if opts.Workers != 3 {
		t.Fatalf("expected 3 workers, got %d", opts.Workers)
	}
	if opts.Recursive {
		t.Fatalf("expected recursion disabled")
	}
	if len(opts.Includes) != 1 || opts.Includes[0] != "*.png" {
		t.Fatalf("unexpected includes: %#v", opts.Includes)
	}
	if len(opts.Excludes) != 1 || opts.Excludes[0] != "*.tmp" {
		t.Fatalf("unexpected excludes: %#v", opts.Excludes)
	}
}

func TestShouldUseTUIForced(t *testing.T) {
	if !ShouldUseTUI(Options{
		ForceTUI:     true,
		OutputFormat: OutputText,
		Inputs:       []string{"single.png"},
	}) {
		t.Fatalf("expected forced tui to win")
	}
}

func TestShouldUseTUIDisabledForJSON(t *testing.T) {
	if ShouldUseTUI(Options{
		ForceTUI:     true,
		OutputFormat: OutputJSON,
		Inputs:       []string{"single.png"},
	}) {
		t.Fatalf("json mode should disable tui")
	}
}

