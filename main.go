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

// Styles - CPU colors (Plan 9-inspired)
var (
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("28"))  // dark green (#006600)
	systemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("124")) // dark red (#AA0000)
	iowaitStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("186")) // pale yellow (#EEEE9E)
	stealStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("104")) // purple-blue (#8888CC)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // gray
)

// Styles - Token colors (Plan 9-inspired)
var (
	inputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("130")) // muted brown (prompt)
	outputStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("73"))  // muted cyan (response)
	cacheRStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245")) // gray
	cacheWStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252")) // light gray
)

// CPUSample holds calculated CPU percentages
type CPUSample struct {
	User   float64
	System float64
	IOWait float64
	Steal  float64
}

// Global OTEL receiver
var otelReceiver *OTELReceiver

// model is the Bubbletea model
type model struct {
	cpuHistory   []CPUSample
	tokenHistory []TokenSample
	prevCPU      cpuRaw
	prevToken    TokenSample
	width        int
	height       int
	err          error
}

type tickMsg time.Time

func main() {
	// Start OTEL receiver in background (gRPC on default OTLP port)
	otelReceiver = NewOTELReceiver(":4317")
	go func() {
		otelReceiver.Start() // Ignore error, optional feature
	}()

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
		// Update CPU
		curr, err := readCPU()
		if err != nil {
			m.err = err
			return m, tick()
		}

		if m.prevCPU.total() > 0 {
			sample := calcPercentages(m.prevCPU, curr)
			m.cpuHistory = append(m.cpuHistory, sample)

			maxHist := m.width
			if maxHist < 10 {
				maxHist = 80
			}
			if len(m.cpuHistory) > maxHist {
				m.cpuHistory = m.cpuHistory[len(m.cpuHistory)-maxHist:]
			}
		}
		m.prevCPU = curr

		// Update token history (deltas from cumulative OTEL values)
		if otelReceiver != nil {
			curr := otelReceiver.GetSample()
			delta := TokenSample{
				Input:      curr.Input - m.prevToken.Input,
				Output:     curr.Output - m.prevToken.Output,
				CacheRead:  curr.CacheRead - m.prevToken.CacheRead,
				CacheWrite: curr.CacheWrite - m.prevToken.CacheWrite,
			}
			// Add delta to history (even if zero - shows no activity)
			m.tokenHistory = append(m.tokenHistory, delta)
			maxHist := m.width
			if maxHist < 10 {
				maxHist = 80
			}
			if len(m.tokenHistory) > maxHist {
				m.tokenHistory = m.tokenHistory[len(m.tokenHistory)-maxHist:]
			}
			m.prevToken = curr
		}

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

	// Calculate heights for stacked layout
	// CPU chart (header+footer embedded in top row) + Token header + Token chart
	// Minimum: 1 + 1 + 1 = 3 rows
	tokChartHeight := 1                              // tokens are discrete, one line enough
	cpuChartHeight := m.height - 1 - tokChartHeight // CPU gets the rest (-1 for tok header)
	if cpuChartHeight < 1 {
		cpuChartHeight = 1
	}

	// CPU section
	var latestCPU CPUSample
	if len(m.cpuHistory) > 0 {
		latestCPU = m.cpuHistory[len(m.cpuHistory)-1]
	}
	cpuTotal := latestCPU.User + latestCPU.System + latestCPU.IOWait + latestCPU.Steal

	cpuHeader := fmt.Sprintf(" CPU %3.0f%% ", cpuTotal) +
		userStyle.Render("■") + dimStyle.Render("usr ") +
		systemStyle.Render("■") + dimStyle.Render("sys ") +
		iowaitStyle.Render("■") + dimStyle.Render("io ") +
		stealStyle.Render("■") + dimStyle.Render("stl ")

	// Right info (was footer): q:quit and time span
	rightInfo := dimStyle.Render("q:quit " + formatDuration(m.width) + " ")

	cpuChart := renderCPUChart(m.cpuHistory, m.width, cpuChartHeight, cpuHeader, rightInfo)

	// Token section - show cumulative total, chart shows deltas over time
	var tokTotal uint64
	if otelReceiver != nil {
		curr := otelReceiver.GetSample()
		tokTotal = curr.Input + curr.Output + curr.CacheRead + curr.CacheWrite
	}
	tokHeader := fmt.Sprintf(" TOK %s ", formatCount(tokTotal)) +
		inputStyle.Render("■") + dimStyle.Render("in ") +
		outputStyle.Render("■") + dimStyle.Render("out ") +
		cacheRStyle.Render("■") + dimStyle.Render("chr ") +
		cacheWStyle.Render("■") + dimStyle.Render("chw")

	tokChart := renderTokenChart(m.tokenHistory, m.width, tokChartHeight)

	return cpuChart + "\n" + tokHeader + "\n" + tokChart
}

func formatCount(n uint64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func formatDuration(seconds int) string {
	if seconds >= 60 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}

func renderCPUChart(history []CPUSample, width, height int, header, rightInfo string) string {
	if height < 1 {
		return ""
	}
	headerWidth := lipgloss.Width(header)
	rightWidth := lipgloss.Width(rightInfo)

	padded := make([]CPUSample, width)
	start := width - len(history)
	if start < 0 {
		copy(padded, history[len(history)-width:])
	} else {
		copy(padded[start:], history)
	}

	// Calculate total CPU % for each column
	totals := make([]int, width)
	for x := 0; x < width; x++ {
		s := padded[x]
		totals[x] = int(s.User + s.System + s.IOWait + s.Steal)
	}

	// Find peaks: local maxima that are significant (>20% and higher than neighbors by 5%)
	peaks := findPeaks(totals, 20, 5)

	// Also always show current value (rightmost with data)
	lastIdx := -1
	for x := width - 1; x >= 0; x-- {
		if totals[x] > 0 {
			lastIdx = x
			break
		}
	}
	if lastIdx >= 0 && totals[lastIdx] >= 20 {
		// Add current value if not already a peak
		found := false
		for _, p := range peaks {
			if p == lastIdx {
				found = true
				break
			}
		}
		if !found {
			peaks = append(peaks, lastIdx)
		}
	}

	// Build annotation map: column -> label (avoid collisions)
	annotations := make(map[int]string)
	for _, p := range peaks {
		label := fmt.Sprintf("%d", totals[p])
		// Check for collision (need 2-3 chars space)
		collision := false
		for ox := p - len(label); ox <= p+len(label); ox++ {
			if _, exists := annotations[ox]; exists {
				collision = true
				break
			}
		}
		if !collision {
			annotations[p] = label
		}
	}

	// Calculate which row each peak label should appear in
	peakRows := make(map[int]int) // column -> row
	maxUnits := height * 8
	for col := range annotations {
		units := int(float64(totals[col]) * float64(maxUnits) / 100)
		// Find the row where the peak ends (top of the bar)
		peakRow := height - 1 - units/8
		if peakRow < 0 {
			peakRow = 0
		}
		peakRows[col] = peakRow
	}

	// Render rows
	rows := make([]string, height)
	for y := 0; y < height; y++ {
		var row strings.Builder
		x := 0
		// Embed header in top row (left side)
		if y == 0 && header != "" {
			row.WriteString(header)
			x = headerWidth
		}
		// Stop early on row 0 to leave room for right info
		rowEnd := width
		if y == 0 && rightInfo != "" {
			rowEnd = width - rightWidth
		}
		for x < rowEnd {
			// Check if there's an annotation starting here
			label, hasLabel := annotations[x]
			targetRow, inRow := peakRows[x]
			if hasLabel && inRow && y == targetRow {
				// Render the number
				row.WriteString(dimStyle.Render(label))
				x += len(label)
				continue
			}
			// Regular cell
			char, style := getCPUCell(padded[x], y, height)
			row.WriteString(style.Render(string(char)))
			x++
		}
		// Add right info at end of top row
		if y == 0 && rightInfo != "" {
			row.WriteString(rightInfo)
		}
		rows[y] = row.String()
	}

	return strings.Join(rows, "\n")
}

// findPeaks finds local maxima above minVal that are higher than neighbors by minDiff
func findPeaks(data []int, minVal, minDiff int) []int {
	var peaks []int
	for i := 1; i < len(data)-1; i++ {
		if data[i] >= minVal &&
			data[i] > data[i-1]+minDiff &&
			data[i] > data[i+1]+minDiff {
			peaks = append(peaks, i)
		}
	}
	return peaks
}

func getCPUCell(s CPUSample, row, totalRows int) (rune, lipgloss.Style) {
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

	if rowBottom >= stealTop {
		return ' ', lipgloss.NewStyle()
	}

	fill := min(stealTop-rowBottom, 8)

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

func renderTokenChart(history []TokenSample, width, height int) string {
	if height < 1 {
		return ""
	}

	padded := make([]TokenSample, width)
	start := width - len(history)
	if start < 0 {
		copy(padded, history[len(history)-width:])
	} else {
		copy(padded[start:], history)
	}

	// Find max for scaling
	var maxTokens uint64 = 1
	for _, s := range padded {
		total := s.Input + s.Output + s.CacheRead + s.CacheWrite
		if total > maxTokens {
			maxTokens = total
		}
	}

	rows := make([]string, height)
	for y := 0; y < height; y++ {
		var row strings.Builder
		for x := 0; x < width; x++ {
			char, style := getTokenCell(padded[x], y, height, maxTokens)
			row.WriteString(style.Render(string(char)))
		}
		rows[y] = row.String()
	}

	return strings.Join(rows, "\n")
}

func getTokenCell(s TokenSample, row, totalRows int, maxTokens uint64) (rune, lipgloss.Style) {
	maxUnits := totalRows * 8

	scale := func(v uint64) int {
		if maxTokens == 0 {
			return 0
		}
		return int(float64(v) * float64(maxUnits) / float64(maxTokens))
	}

	inputUnits := scale(s.Input)
	outputUnits := scale(s.Output)
	cacheRUnits := scale(s.CacheRead)
	cacheWUnits := scale(s.CacheWrite)

	inputTop := inputUnits
	outputTop := inputTop + outputUnits
	cacheRTop := outputTop + cacheRUnits
	cacheWTop := cacheRTop + cacheWUnits

	rowBottom := (totalRows - 1 - row) * 8

	if rowBottom >= cacheWTop {
		return ' ', lipgloss.NewStyle()
	}

	fill := min(cacheWTop-rowBottom, 8)

	var style lipgloss.Style
	switch {
	case rowBottom < inputTop:
		style = inputStyle
	case rowBottom < outputTop:
		style = outputStyle
	case rowBottom < cacheRTop:
		style = cacheRStyle
	default:
		style = cacheWStyle
	}

	return blocks[fill], style
}
