package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"qrcheck/internal/cli"
	"qrcheck/internal/engine"
	"qrcheck/internal/pipeline"
	"qrcheck/internal/report"
	"qrcheck/internal/tui"
)

func Execute(ctx context.Context, opts cli.Options) (int, error) {
	if len(opts.Inputs) == 0 {
		if cli.ShouldUseTUI(opts) {
			inputs, err := tui.PromptForInputs(opts)
			if err != nil {
				return 2, err
			}
			opts.Inputs = inputs
		} else {
			return 2, errors.New("no input paths provided")
		}
	}

	selected, err := engine.Select(opts.EngineMode)
	if err != nil {
		return 2, err
	}

	runLog, err := report.NewRunLog()
	if err != nil {
		return 2, err
	}
	defer runLog.Close()

	cfg := pipeline.Config{
		Inputs:       opts.Inputs,
		Recursive:    opts.Recursive,
		Includes:     opts.Includes,
		Excludes:     opts.Excludes,
		Workers:      opts.Workers,
		Thorough:     opts.Thorough,
		StrictErrors: opts.StrictErrors,
		Engine:       selected,
	}

	handle, err := pipeline.Start(ctx, cfg)
	if err != nil {
		return 2, err
	}

	if cli.ShouldUseTUI(opts) {
		summary, runErr := tui.Run(ctx, handle, opts, selected.Name(), runLog)
		if waitErr := handle.Wait(); waitErr != nil && runErr == nil {
			runErr = waitErr
		}
		return report.ExitCode(summary, opts.StrictErrors, runErr), runErr
	}

	agg := report.NewAggregator(selected.Name())
	for event := range handle.Events {
		agg.Apply(event)
		if event.Type == report.EventResult && event.Result != nil && !opts.Quiet && opts.OutputFormat == cli.OutputText {
			if err := report.WriteTextLine(os.Stdout, *event.Result); err != nil {
				return 2, err
			}
		}
		if event.Type == report.EventResult && event.Result != nil {
			if err := runLog.WriteResult(*event.Result); err != nil {
				return 2, err
			}
		}
	}

	runErr := handle.Wait()
	summary := agg.Summary()
	if err := runLog.WriteSummary(summary, runErr); err != nil {
		return 2, err
	}

	switch opts.OutputFormat {
	case cli.OutputJSON:
		if err := report.WriteJSON(os.Stdout, summary, agg.Results()); err != nil {
			return 2, err
		}
	default:
		if err := report.WriteSummaryLine(os.Stdout, summary); err != nil {
			return 2, err
		}
	}

	if runErr != nil {
		return report.ExitCode(summary, opts.StrictErrors, runErr), fmt.Errorf("scan failed: %w", runErr)
	}
	return report.ExitCode(summary, opts.StrictErrors, nil), nil
}
