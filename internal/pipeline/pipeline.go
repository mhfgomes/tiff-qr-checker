package pipeline

import (
	"context"
	"errors"
	"os"
	"sync"

	"qrcheck/internal/discovery"
	"qrcheck/internal/engineapi"
	"qrcheck/internal/imageio"
	"qrcheck/internal/report"
)

type Config struct {
	Inputs       []string
	Recursive    bool
	Includes     []string
	Excludes     []string
	Workers      int
	Thorough     bool
	StrictErrors bool
	Engine       engineapi.Engine
}

type Handle struct {
	Events  <-chan report.Event
	control *discovery.Control
	cancel  context.CancelFunc
	done    chan struct{}
	errMu   sync.Mutex
	err     error
}

func (h *Handle) Wait() error {
	<-h.done
	h.errMu.Lock()
	defer h.errMu.Unlock()
	return h.err
}

func (h *Handle) Cancel() {
	h.cancel()
}

func (h *Handle) Pause() {
	h.control.Pause()
}

func (h *Handle) Resume() {
	h.control.Resume()
}

func (h *Handle) Paused() bool {
	return h.control.Paused()
}

type fileJob struct {
	path     string
	explicit bool
}

type scanTask struct {
	path   string
	format imageio.Format
}

func Start(parent context.Context, cfg Config) (*Handle, error) {
	if cfg.Engine == nil {
		return nil, errors.New("scan engine is required")
	}
	if cfg.Workers <= 0 {
		return nil, errors.New("workers must be greater than zero")
	}

	ctx, cancel := context.WithCancel(parent)
	control := discovery.NewControl()
	events := make(chan report.Event, cfg.Workers*4)
	discovered := make(chan fileJob, cfg.Workers*2)
	scanTasks := make(chan scanTask, cfg.Workers*2)

	handle := &Handle{
		Events:  events,
		control: control,
		cancel:  cancel,
		done:    make(chan struct{}),
	}

	go func() {
		defer close(handle.done)
		defer close(events)
		defer cancel()

		var once sync.Once
		setErr := func(err error) {
			if err == nil {
				return
			}
			once.Do(func() {
				handle.errMu.Lock()
				handle.err = err
				handle.errMu.Unlock()
			})
		}

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(discovered)
			err := discovery.Walk(
				ctx,
				cfg.Inputs,
				cfg.Recursive,
				discovery.Filters{Includes: cfg.Includes, Excludes: cfg.Excludes},
				control,
				func(candidate discovery.Candidate) error {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case discovered <- fileJob{path: candidate.Path, explicit: candidate.Explicit}:
						return nil
					}
				},
			)
			if err != nil && !errors.Is(err, context.Canceled) {
				setErr(err)
			}
		}()

		preflightWorkers := min(4, max(2, cfg.Workers/2))
		var preflightWG sync.WaitGroup
		preflightWG.Add(preflightWorkers)
		for i := 0; i < preflightWorkers; i++ {
			go func() {
				defer preflightWG.Done()
				for job := range discovered {
					if ctx.Err() != nil {
						return
					}

					info, err := os.Stat(job.path)
					if err != nil {
						emitDiscovered(ctx, events, job.path)
						emitResult(ctx, events, report.Result{
							Path:  job.path,
							Error: err.Error(),
						})
						continue
					}
					if info.Size() == 0 {
						emitDiscovered(ctx, events, job.path)
						emitResult(ctx, events, report.Result{
							Path:  job.path,
							Error: "empty file",
						})
						continue
					}

					format, err := imageio.DetectFormat(job.path)
					if err != nil {
						if errors.Is(err, imageio.ErrUnsupportedFormat) && !job.explicit && !cfg.StrictErrors {
							continue
						}
						emitDiscovered(ctx, events, job.path)
						emitResult(ctx, events, report.Result{
							Path:   job.path,
							Format: string(format),
							Error:  err.Error(),
						})
						continue
					}
					emitDiscovered(ctx, events, job.path)

					select {
					case <-ctx.Done():
						return
					case scanTasks <- scanTask{path: job.path, format: format}:
					}
				}
			}()
		}

		go func() {
			preflightWG.Wait()
			close(scanTasks)
		}()

		var detectWG sync.WaitGroup
		detectWG.Add(cfg.Workers)
		for i := 0; i < cfg.Workers; i++ {
			go func() {
				defer detectWG.Done()
				for task := range scanTasks {
					if ctx.Err() != nil {
						return
					}
					select {
					case <-ctx.Done():
						return
					case events <- report.Event{Type: report.EventProcessing, Path: task.path}:
					}
					res := imageio.ScanFile(ctx, task.path, task.format, cfg.Engine, imageio.ScanOptions{
						Thorough:     cfg.Thorough,
						StrictErrors: cfg.StrictErrors,
					})
					emitResult(ctx, events, res)
				}
			}()
		}

		detectWG.Wait()
		wg.Wait()
	}()

	return handle, nil
}

func emitResult(ctx context.Context, events chan<- report.Event, res report.Result) {
	select {
	case <-ctx.Done():
	case events <- report.Event{
		Type:   report.EventResult,
		Path:   res.Path,
		Result: &res,
	}:
	}
}

func emitDiscovered(ctx context.Context, events chan<- report.Event, path string) {
	select {
	case <-ctx.Done():
	case events <- report.Event{Type: report.EventDiscovered, Path: path}:
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
