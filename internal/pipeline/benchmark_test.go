package pipeline

import (
	"context"
	"fmt"
	"testing"

	"qrcheck/internal/cli"
	"qrcheck/internal/engine"
	testfiles "qrcheck/internal/testdata"
)

func BenchmarkScanMixedCorpus(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 10; i++ {
		testfiles.WriteQRPNG(b, root, fmt.Sprintf("found-%d.png", i))
		testfiles.WriteBlankPNG(b, root, fmt.Sprintf("miss-%d.png", i))
	}

	eng, err := engine.Select(cli.EngineGo)
	if err != nil {
		b.Fatalf("engine: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handle, err := Start(context.Background(), Config{
			Inputs:    []string{root},
			Recursive: true,
			Workers:   2,
			Engine:    eng,
		})
		if err != nil {
			b.Fatalf("start: %v", err)
		}
		for range handle.Events {
		}
		if err := handle.Wait(); err != nil {
			b.Fatalf("wait: %v", err)
		}
	}
}
