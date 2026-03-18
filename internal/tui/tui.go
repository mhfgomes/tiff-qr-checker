package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"qrcheck/internal/cli"
	"qrcheck/internal/pipeline"
	"qrcheck/internal/report"
)

type scanEventMsg struct {
	event report.Event
}

type scanDoneMsg struct{}
type tickMsg time.Time
type promptDoneMsg struct {
	inputs []string
	err    error
}

type model struct {
	handle       *pipeline.Handle
	opts         cli.Options
	agg          *report.Aggregator
	runLog       *report.RunLog
	engineName   string
	width        int
	height       int
	startedAt    time.Time
	rows         []report.Result
	filter       int
	selected     int
	detailOpen   bool
	finalSummary report.Summary
	hasFinal     bool
	finished     bool
	quitting     bool
	summaryDone  bool
	filterInput  textinput.Model
	filtering    bool
	promptMode   bool
	promptText   string
	promptErr    string
	promptDone   []string
	statusMsg    string
}

var (
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	foundStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	missStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	selectStyle  = lipgloss.NewStyle().Reverse(true)
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	startCommand = func(name string, args ...string) error {
		return exec.Command(name, args...).Start()
	}
)

func Run(_ context.Context, handle *pipeline.Handle, opts cli.Options, engineName string, runLog *report.RunLog) (report.Summary, error) {
	input := textinput.New()
	input.Placeholder = "filter path"
	input.CharLimit = 256
	input.Width = 32

	m := model{
		handle:      handle,
		opts:        opts,
		agg:         report.NewAggregator(engineName),
		runLog:      runLog,
		engineName:  engineName,
		startedAt:   time.Now(),
		filterInput: input,
	}

	program := tea.NewProgram(m, tea.WithAltScreen())
	result, err := program.Run()
	if err != nil {
		return report.Summary{}, err
	}
	finalModel := result.(model)
	return finalModel.summary(), nil
}

func PromptForInputs(opts cli.Options) ([]string, error) {
	input := textinput.New()
	input.Placeholder = `Enter file/folder path(s), separated by ;`
	input.CharLimit = 1024
	input.Width = 80
	input.Focus()

	m := model{
		opts:        opts,
		agg:         report.NewAggregator("pending"),
		filterInput: input,
		promptMode:  true,
		promptText:  "Enter file or folder path(s). Use ';' to separate multiple entries.",
		width:       100,
		height:      12,
	}

	program := tea.NewProgram(m)
	result, err := program.Run()
	if err != nil {
		return nil, err
	}
	finalModel := result.(model)
	if len(finalModel.promptDone) == 0 {
		if finalModel.promptErr != "" {
			return nil, errors.New(finalModel.promptErr)
		}
		return nil, errors.New("input cancelled")
	}
	return finalModel.promptDone, nil
}

func (m model) Init() tea.Cmd {
	if m.promptMode {
		return textinput.Blink
	}
	return tea.Batch(waitForEvent(m.handle.Events), tick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case scanEventMsg:
		m.agg.Apply(msg.event)
		if msg.event.Type == report.EventResult && msg.event.Result != nil {
			m.rows = append(m.rows, *msg.event.Result)
			if m.runLog != nil {
				_ = m.runLog.WriteResult(*msg.event.Result)
			}
			if m.selected >= len(m.filteredRows()) {
				m.selected = max(0, len(m.filteredRows())-1)
			}
		}
		return m, waitForEvent(m.handle.Events)
	case scanDoneMsg:
		m.finished = true
		m.finalSummary = m.agg.Summary()
		m.hasFinal = true
		if !m.summaryDone && m.runLog != nil {
			_ = m.runLog.WriteSummary(m.finalSummary, nil)
			m.summaryDone = true
		}
		return m, nil
	case tickMsg:
		if m.finished {
			return m, nil
		}
		return m, tick()
	case promptDoneMsg:
		m.promptDone = msg.inputs
		if msg.err != nil {
			m.promptErr = msg.err.Error()
		}
		return m, tea.Quit
	case tea.KeyMsg:
		if m.promptMode {
			switch msg.String() {
			case "ctrl+c", "esc":
				m.promptErr = "input cancelled"
				return m, tea.Quit
			case "enter":
				inputs := splitInputPaths(m.filterInput.Value())
				if len(inputs) == 0 {
					m.promptErr = "enter at least one path"
					return m, nil
				}
				m.promptDone = inputs
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.promptErr = ""
			return m, cmd
		}
		if m.filtering {
			switch msg.String() {
			case "esc":
				m.filtering = false
				m.filterInput.Blur()
				return m, nil
			case "enter":
				m.filtering = false
				m.filterInput.Blur()
				m.selected = 0
				return m, nil
			}
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.selected = 0
			return m, cmd
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			if m.handle != nil {
				m.handle.Cancel()
			}
			return m, tea.Quit
		case "p":
			if m.handle != nil {
				if m.handle.Paused() {
					m.handle.Resume()
				} else {
					m.handle.Pause()
				}
			}
			return m, nil
		case "tab":
			m.filter = (m.filter + 1) % 4
			m.selected = 0
			return m, nil
		case "/":
			m.filtering = true
			m.filterInput.Focus()
			return m, textinput.Blink
		case "l":
			rows := m.filteredRows()
			if len(rows) == 0 || m.selected >= len(rows) {
				m.statusMsg = "no selected file"
				return m, nil
			}
			if err := openFileLocation(rows[m.selected].Path); err != nil {
				m.statusMsg = "open failed: " + err.Error()
				return m, nil
			}
			m.statusMsg = "opened file location"
			return m, nil
		case "enter":
			m.detailOpen = !m.detailOpen
			return m, nil
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case "down", "j":
			if m.selected < len(m.filteredRows())-1 {
				m.selected++
			}
			return m, nil
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Starting qrcheck..."
	}
	if m.promptMode {
		return m.renderPrompt()
	}

	rows := m.filteredRows()
	if m.selected >= len(rows) {
		m.selected = max(0, len(rows)-1)
	}

	header := m.renderHeader()
	filterBar := m.renderFilterBar()
	table := m.renderTable(rows)
	footer := m.renderFooter(rows)
	return lipgloss.JoinVertical(lipgloss.Left, header, filterBar, table, footer)
}

func (m model) renderHeader() string {
	summary := m.summary()
	state := "running"
	if m.handle != nil && m.handle.Paused() {
		state = "paused"
	}
	if m.finished {
		state = "end"
	}
	fps := 0.0
	if elapsed := summary.Duration.Seconds(); elapsed > 0 {
		fps = float64(summary.FilesTotal) / elapsed
	}

	line1 := headerStyle.Render("qrcheck") + "  " + mutedStyle.Render(fmt.Sprintf("[%s | %s]", m.engineName, state))
	line2 := fmt.Sprintf(
		"elapsed %s  discovered %d  scanned %d  found %d  miss %d  error %d  %.1f files/s",
		summary.Duration.Round(100*time.Millisecond),
		summary.FilesTotal,
		summary.FilesScanned,
		summary.FilesFound,
		summary.FilesMiss,
		summary.FilesError,
		fps,
	)
	return lipgloss.JoinVertical(lipgloss.Left, line1, line2)
}

func (m model) summary() report.Summary {
	if m.hasFinal {
		return m.finalSummary
	}
	return m.agg.Summary()
}

func (m model) renderFilterBar() string {
	labels := []string{"All", "Found", "Miss", "Error"}
	parts := make([]string, 0, len(labels)+1)
	for idx, label := range labels {
		if idx == m.filter {
			parts = append(parts, selectStyle.Render(label))
		} else {
			parts = append(parts, mutedStyle.Render(label))
		}
	}
	if m.filtering {
		parts = append(parts, "filter: "+m.filterInput.View())
	} else if value := strings.TrimSpace(m.filterInput.Value()); value != "" {
		parts = append(parts, "filter: "+value)
	}
	return strings.Join(parts, "  ")
}

func (m model) renderTable(rows []report.Result) string {
	tableHeight := m.height - 8
	if tableHeight < 4 {
		tableHeight = 4
	}

	statusWidth := 7
	formatWidth := 6
	pagesWidth := 5
	elapsedWidth := 8
	pathWidth := m.width - statusWidth - formatWidth - pagesWidth - elapsedWidth - 8
	if pathWidth < 20 {
		pathWidth = 20
	}

	header := fmt.Sprintf("%-7s %-*s %-6s %-5s %-8s", "STATUS", pathWidth, "PATH", "FORMAT", "PAGES", "TIME")
	lines := []string{header}

	start := 0
	if m.selected >= tableHeight-1 {
		start = m.selected - tableHeight + 2
	}
	end := min(len(rows), start+tableHeight-1)
	for idx := start; idx < end; idx++ {
		row := rows[idx]
		path := truncateMiddle(row.Path, pathWidth)
		line := fmt.Sprintf(
			"%-7s %-*s %-6s %-5d %-8s",
			row.Status(),
			pathWidth,
			path,
			row.Format,
			row.PagesScanned,
			row.Duration().Round(time.Millisecond),
		)
		line = styleStatus(row.Status(), line)
		if idx == m.selected {
			line = selectStyle.Render(line)
		}
		lines = append(lines, line)
	}
	if len(rows) == 0 {
		lines = append(lines, mutedStyle.Render("No results yet"))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderFooter(rows []report.Result) string {
	current := m.agg.CurrentPath()
	if current == "" {
		current = "waiting for files..."
	}
	lines := []string{
		"current: " + truncateMiddle(current, max(20, m.width-10)),
		"keys: q quit  p pause/resume  tab filter  / path filter  l open folder  enter toggle detail",
	}
	if m.finished {
		lines = append(lines, "end")
	}
	if m.statusMsg != "" {
		lines = append(lines, m.statusMsg)
	}
	if m.detailOpen && len(rows) > 0 && m.selected < len(rows) {
		row := rows[m.selected]
		detail := fmt.Sprintf("selected: %s | status=%s | pages=%d", row.Path, row.Status(), row.PagesScanned)
		if row.FirstHitPage != nil {
			detail += fmt.Sprintf(" | first_hit_page=%d", *row.FirstHitPage)
		}
		if row.Error != "" {
			detail += " | error=" + row.Error
		}
		lines = append(lines, detail)
	}
	return mutedStyle.Render(strings.Join(lines, "\n"))
}

func (m model) renderPrompt() string {
	lines := []string{
		headerStyle.Render("qrcheck"),
		m.promptText,
		"",
		m.filterInput.View(),
		"",
		"keys: enter start  esc quit",
	}
	if m.promptErr != "" {
		lines = append(lines, errorStyle.Render(m.promptErr))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m model) filteredRows() []report.Result {
	rows := make([]report.Result, 0, len(m.rows))
	needle := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	for _, row := range m.rows {
		if !matchesStatusFilter(m.filter, row) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(row.Path), needle) {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func waitForEvent(events <-chan report.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return scanDoneMsg{}
		}
		return scanEventMsg{event: event}
	}
}

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func styleStatus(status report.Status, line string) string {
	switch status {
	case report.StatusFound:
		return foundStyle.Render(line)
	case report.StatusError:
		return errorStyle.Render(line)
	default:
		return missStyle.Render(line)
	}
}

func matchesStatusFilter(filter int, row report.Result) bool {
	switch filter {
	case 1:
		return row.Status() == report.StatusFound
	case 2:
		return row.Status() == report.StatusMiss
	case 3:
		return row.Status() == report.StatusError
	default:
		return true
	}
}

func truncateMiddle(value string, width int) string {
	if width <= 0 || len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	head := (width - 3) / 2
	tail := width - 3 - head
	return value[:head] + "..." + value[len(value)-tail:]
}

func splitInputPaths(value string) []string {
	parts := strings.Split(value, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func openFileLocation(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	switch runtime.GOOS {
	case "windows":
		return startCommand("explorer.exe", "/select,", abs)
	case "darwin":
		return startCommand("open", "-R", abs)
	default:
		return startCommand("xdg-open", filepath.Dir(abs))
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
