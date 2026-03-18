package pipeline

import (
	"context"
	"testing"

	"qrcheck/internal/cli"
	"qrcheck/internal/engine"
	"qrcheck/internal/report"
	testfiles "qrcheck/internal/testdata"
)

func TestPipelineScansMixedInputs(t *testing.T) {
	root := t.TempDir()
	testfiles.WriteQRPNG(t, root, "found.png")
	testfiles.WriteQRJPEG(t, root, "found.jpg")
	testfiles.WriteBlankPNG(t, root, "miss.png")
	testfiles.WriteUnsupported(t, root, "skip.txt")

	eng, err := engine.Select(cli.EngineGo)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	handle, err := Start(context.Background(), Config{
		Inputs:    []string{root},
		Recursive: true,
		Workers:   2,
		Engine:    eng,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	results := collectResults(t, handle)
	if len(results) != 3 {
		t.Fatalf("expected 3 image results, got %d", len(results))
	}

	var found, miss int
	for _, res := range results {
		if res.Found {
			found++
		}
		if !res.Found && res.Error == "" {
			miss++
		}
	}
	if found != 2 || miss != 1 {
		t.Fatalf("unexpected result mix: found=%d miss=%d", found, miss)
	}
}

func TestPipelineMultiPageTIFFFirstHitLaterPage(t *testing.T) {
	path := testfiles.FixturePath(t, "multi-hit-page2.tiff")

	eng, err := engine.Select(cli.EngineGo)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	handle, err := Start(context.Background(), Config{
		Inputs:    []string{path},
		Recursive: false,
		Workers:   1,
		Engine:    eng,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	results := collectResults(t, handle)
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if !results[0].Found {
		t.Fatalf("expected TIFF QR to be found")
	}
	if results[0].PagesScanned != 2 {
		t.Fatalf("expected two pages scanned, got %d", results[0].PagesScanned)
	}
	if results[0].FirstHitPage == nil || *results[0].FirstHitPage != 2 {
		t.Fatalf("expected first hit on page 2, got %#v", results[0].FirstHitPage)
	}
}

func TestPipelineStrictUnsupportedExplicit(t *testing.T) {
	root := t.TempDir()
	path := testfiles.WriteUnsupported(t, root, "bad.txt")

	eng, err := engine.Select(cli.EngineGo)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	handle, err := Start(context.Background(), Config{
		Inputs:       []string{path},
		Recursive:    false,
		Workers:      1,
		StrictErrors: true,
		Engine:       eng,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	results := collectResults(t, handle)
	if len(results) != 1 || results[0].Error == "" {
		t.Fatalf("expected unsupported explicit file to error, got %#v", results)
	}
}

func collectResults(t *testing.T, handle *Handle) []report.Result {
	t.Helper()
	var results []report.Result
	for event := range handle.Events {
		if event.Type == report.EventResult && event.Result != nil {
			results = append(results, *event.Result)
		}
	}
	if err := handle.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	return results
}
