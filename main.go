package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Block characters for rendering
var blocks = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// Styles - Datadog-inspired colors
var (
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))  // bright green
	systemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // bright red
	iowaitStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("226")) // bright yellow
	stealStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("201")) // magenta
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // gray
)

// CPUSample holds calculated CPU percentages
type CPUSample struct {
	User   float64
	System float64
	IOWait float64
	Steal  float64
}

// model is the Bubbletea model
type model struct {
	history []CPUSample
	prev    cpuRaw
	width   int
	height  int
	err     error
}

type tickMsg time.Time

func main() {
	p := tea.NewProgram(model{}, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (m model) Init() tea.Cmd {
	return tick()
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		curr, err := readCPU()
		if err != nil {
			m.err = err
			return m, tick()
		}

		if m.prev.total() > 0 {
			sample := calcPercentages(m.prev, curr)
			m.history = append(m.history, sample)

			maxHist := m.width
			if maxHist < 10 {
				maxHist = 80
			}
			if len(m.history) > maxHist {
				m.history = m.history[len(m.history)-maxHist:]
			}
		}
		m.prev = curr
		return m, tick()
	}
	return m, nil
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	if m.err != nil {
		return fmt.Sprintf("Error: %v", m.err)
	}

	var latest CPUSample
	if len(m.history) > 0 {
		latest = m.history[len(m.history)-1]
	}
	total := latest.User + latest.System + latest.IOWait + latest.Steal

	// Header
	header := fmt.Sprintf(" CPU %3.0f%% ", total) +
		userStyle.Render("■") + dimStyle.Render("usr ") +
		systemStyle.Render("■") + dimStyle.Render("sys ") +
		iowaitStyle.Render("■") + dimStyle.Render("io ") +
		stealStyle.Render("■") + dimStyle.Render("stl")

	// Chart
	chartHeight := m.height - 2
	if chartHeight < 1 {
		chartHeight = 1
	}
	chart := renderChart(m.history, m.width, chartHeight)

	// Footer
	footer := dimStyle.Render(" q:quit")
	pad := m.width - 7 - 3
	if pad > 0 {
		footer += strings.Repeat(" ", pad)
	}
	footer += dimStyle.Render("1s ")

	return header + "\n" + chart + "\n" + footer
}

func renderChart(history []CPUSample, width, height int) string {
	if height < 1 {
		return ""
	}

	padded := make([]CPUSample, width)
	start := width - len(history)
	if start < 0 {
		copy(padded, history[len(history)-width:])
	} else {
		copy(padded[start:], history)
	}

	rows := make([]string, height)
	for y := 0; y < height; y++ {
		var row strings.Builder
		for x := 0; x < width; x++ {
			char, style := getCell(padded[x], y, height)
			row.WriteString(style.Render(string(char)))
		}
		rows[y] = row.String()
	}

	return strings.Join(rows, "\n")
}

func getCell(s CPUSample, row, totalRows int) (rune, lipgloss.Style) {
	maxUnits := totalRows * 8

	userUnits := int(s.User * float64(maxUnits) / 100)
	sysUnits := int(s.System * float64(maxUnits) / 100)
	ioUnits := int(s.IOWait * float64(maxUnits) / 100)
	stealUnits := int(s.Steal * float64(maxUnits) / 100)

	userTop := userUnits
	sysTop := userTop + sysUnits
	ioTop := sysTop + ioUnits
	stealTop := ioTop + stealUnits

	rowBottom := (totalRows - 1 - row) * 8

	// Empty if row is above the entire stack
	if rowBottom >= stealTop {
		return ' ', lipgloss.NewStyle()
	}

	// Fill extends to top of entire stack (not just current layer)
	fill := min(stealTop-rowBottom, 8)

	// Color based on which layer this row starts in
	var style lipgloss.Style
	switch {
	case rowBottom < userTop:
		style = userStyle
	case rowBottom < sysTop:
		style = systemStyle
	case rowBottom < ioTop:
		style = iowaitStyle
	default:
		style = stealStyle
	}

	return blocks[fill], style
}
