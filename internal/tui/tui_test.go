package tui

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"qrcheck/internal/report"
)

func TestInitialView(t *testing.T) {
	m := model{
		agg:       report.NewAggregator("go"),
		width:     120,
		height:    20,
		startedAt: time.Now(),
	}
	view := m.View()
	if !strings.Contains(view, "No results yet") {
		t.Fatalf("expected empty table message, got %q", view)
	}
}

func TestFilterAndDetailToggle(t *testing.T) {
	m := model{
		agg:       report.NewAggregator("go"),
		width:     120,
		height:    20,
		startedAt: time.Now(),
		rows: []report.Result{
			{Path: "found.png", Found: true, PagesScanned: 1},
			{Path: "miss.png", Found: false, PagesScanned: 1},
		},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(model)
	if m.filter != 1 {
		t.Fatalf("expected filter index 1, got %d", m.filter)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if !m.detailOpen {
		t.Fatalf("expected detail panel to open")
	}
}

func TestFinishedSummaryIsFrozen(t *testing.T) {
	m := model{
		agg:       report.NewAggregator("go"),
		width:     120,
		height:    20,
		startedAt: time.Now().Add(-5 * time.Second),
	}
	m.agg.Apply(report.Event{Type: report.EventDiscovered, Path: "a.png"})
	m.agg.Apply(report.Event{Type: report.EventResult, Result: &report.Result{Path: "a.png", Found: true}})

	updated, cmd := m.Update(scanDoneMsg{})
	m = updated.(model)
	if cmd != nil {
		t.Fatalf("expected no further tick command after finish")
	}
	frozen := m.summary()
	time.Sleep(150 * time.Millisecond)
	after := m.summary()
	if after.Duration != frozen.Duration {
		t.Fatalf("expected frozen duration, got %v then %v", frozen.Duration, after.Duration)
	}
}

func TestFinishedHeaderFilesPerSecondIsFrozen(t *testing.T) {
	m := model{
		agg:       report.NewAggregator("go"),
		width:     120,
		height:    20,
		startedAt: time.Now().Add(-5 * time.Second),
	}
	for i := 0; i < 10; i++ {
		path := "a.png"
		m.agg.Apply(report.Event{Type: report.EventDiscovered, Path: path})
		m.agg.Apply(report.Event{Type: report.EventResult, Result: &report.Result{Path: path, Found: true}})
	}

	updated, _ := m.Update(scanDoneMsg{})
	m = updated.(model)
	header1 := m.renderHeader()
	time.Sleep(150 * time.Millisecond)
	header2 := m.renderHeader()
	if header1 != header2 {
		t.Fatalf("expected frozen header after finish, got %q then %q", header1, header2)
	}
}

func TestOpenSelectedFileLocationKey(t *testing.T) {
	var gotName string
	var gotArgs []string
	startCommand = func(name string, args ...string) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	}
	defer func() {
		startCommand = func(name string, args ...string) error { return exec.Command(name, args...).Start() }
	}()

	m := model{
		agg:       report.NewAggregator("go"),
		width:     120,
		height:    20,
		startedAt: time.Now(),
		rows: []report.Result{
			{Path: filepath.Join("testdata", "found.png"), Found: true, PagesScanned: 1},
		},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = updated.(model)
	if m.statusMsg != "opened file location" {
		t.Fatalf("expected success status, got %q", m.statusMsg)
	}
	if gotName == "" {
		t.Fatalf("expected launcher to be called")
	}
	switch runtime.GOOS {
	case "windows":
		if gotName != "explorer.exe" || len(gotArgs) != 2 || gotArgs[0] != "/select," {
			t.Fatalf("unexpected windows launcher call: %q %#v", gotName, gotArgs)
		}
	case "darwin":
		if gotName != "open" || len(gotArgs) != 2 || gotArgs[0] != "-R" {
			t.Fatalf("unexpected macOS launcher call: %q %#v", gotName, gotArgs)
		}
	default:
		if gotName != "xdg-open" || len(gotArgs) != 1 {
			t.Fatalf("unexpected linux launcher call: %q %#v", gotName, gotArgs)
		}
	}
}
