package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Status string

const (
	StatusFound Status = "FOUND"
	StatusMiss  Status = "MISS"
	StatusError Status = "ERROR"
)

type Result struct {
	Path         string `json:"path"`
	Format       string `json:"format"`
	Found        bool   `json:"found"`
	PagesScanned int    `json:"pages_scanned"`
	FirstHitPage *int   `json:"first_hit_page"`
	Error        string `json:"error"`
	DurationMS   int64  `json:"-"`
}

func (r Result) Status() Status {
	if r.Error != "" {
		return StatusError
	}
	if r.Found {
		return StatusFound
	}
	return StatusMiss
}

func (r Result) Duration() time.Duration {
	return time.Duration(r.DurationMS) * time.Millisecond
}

func (r Result) WithDuration(d time.Duration) Result {
	r.DurationMS = d.Milliseconds()
	return r
}

type Summary struct {
	FilesTotal   int           `json:"files_total"`
	FilesScanned int           `json:"files_scanned"`
	FilesFound   int           `json:"files_found"`
	FilesMiss    int           `json:"files_miss"`
	FilesError   int           `json:"files_error"`
	DurationMS   int64         `json:"duration_ms"`
	Engine       string        `json:"engine"`
	Duration     time.Duration `json:"-"`
}

func (s Summary) WithDuration(d time.Duration) Summary {
	s.Duration = d
	s.DurationMS = d.Milliseconds()
	return s
}

type EventType string

const (
	EventDiscovered EventType = "discovered"
	EventProcessing EventType = "processing"
	EventResult     EventType = "result"
)

type Event struct {
	Type   EventType
	Path   string
	Result *Result
}

type Aggregator struct {
	engine      string
	results     []Result
	summary     Summary
	currentPath string
	startedAt   time.Time
}

func NewAggregator(engine string) *Aggregator {
	return &Aggregator{
		engine:    engine,
		startedAt: time.Now(),
		summary: Summary{
			Engine: engine,
		},
	}
}

func (a *Aggregator) Apply(event Event) {
	switch event.Type {
	case EventDiscovered:
		a.summary.FilesTotal++
	case EventProcessing:
		a.currentPath = event.Path
	case EventResult:
		if event.Result == nil {
			return
		}
		res := *event.Result
		a.results = append(a.results, res)
		switch res.Status() {
		case StatusFound:
			a.summary.FilesFound++
			a.summary.FilesScanned++
		case StatusMiss:
			a.summary.FilesMiss++
			a.summary.FilesScanned++
		case StatusError:
			a.summary.FilesError++
		}
	}
}

func (a *Aggregator) Results() []Result {
	out := make([]Result, len(a.results))
	copy(out, a.results)
	return out
}

func (a *Aggregator) Summary() Summary {
	return a.summary.WithDuration(time.Since(a.startedAt))
}

func (a *Aggregator) CurrentPath() string {
	return a.currentPath
}

func ExitCode(summary Summary, strictErrors bool, runErr error) int {
	if runErr != nil {
		return 2
	}
	if strictErrors && summary.FilesError > 0 {
		return 2
	}
	if summary.FilesFound > 0 {
		return 0
	}
	return 1
}

type JSONEnvelope struct {
	Summary Summary  `json:"summary"`
	Results []Result `json:"results"`
}

func WriteJSON(w io.Writer, summary Summary, results []Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(JSONEnvelope{
		Summary: summary,
		Results: results,
	})
}

func WriteTextLine(w io.Writer, result Result) error {
	switch result.Status() {
	case StatusFound:
		_, err := fmt.Fprintf(w, "FOUND  %s\n", result.Path)
		return err
	case StatusMiss:
		_, err := fmt.Fprintf(w, "MISS   %s\n", result.Path)
		return err
	default:
		_, err := fmt.Fprintf(w, "ERROR  %s  %s\n", result.Path, result.Error)
		return err
	}
}

func WriteSummaryLine(w io.Writer, summary Summary) error {
	_, err := fmt.Fprintf(
		w,
		"Scanned %d files: %d found, %d miss, %d error in %.2fs\n",
		summary.FilesTotal,
		summary.FilesFound,
		summary.FilesMiss,
		summary.FilesError,
		summary.Duration.Seconds(),
	)
	return err
}

type RunLog struct {
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
	path   string
}

func NewRunLog() (*RunLog, error) {
	name := fmt.Sprintf("log_%s.txt", time.Now().Format("20060102_150405"))
	path := filepath.Join(".", name)
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	writer := bufio.NewWriterSize(f, 64*1024)
	log := &RunLog{file: f, writer: writer, path: path}
	if _, err := fmt.Fprintf(writer, "qrcheck log started %s\n", time.Now().Format(time.RFC3339)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return log, nil
}

func (l *RunLog) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *RunLog) WriteResult(result Result) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return WriteTextLine(l.writer, result)
}

func (l *RunLog) WriteSummary(summary Summary, runErr error) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := WriteSummaryLine(l.writer, summary); err != nil {
		return err
	}
	if runErr != nil {
		if _, err := fmt.Fprintf(l.writer, "RUN ERROR  %v\n", runErr); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(l.writer, "END"); err != nil {
		return err
	}
	return l.writer.Flush()
}

func (l *RunLog) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	if l.writer != nil {
		if err := l.writer.Flush(); err != nil {
			_ = l.file.Close()
			return err
		}
	}
	return l.file.Close()
}
