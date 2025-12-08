package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const vercelProjectEnv = "TINYTOP_VERCEL_PROJECT"

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

// Styles - Vercel deployment colors
var (
	buildingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // yellow/orange
	readyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("28"))  // green
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("124")) // red
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

// Global Vercel poller (nil if not configured)
var vercelPoller *VercelPoller

// Available time scales (seconds per column)
var scales = []int{1, 2, 5, 10, 30, 60}

// model is the Bubbletea model
type model struct {
	cpuHistory    []CPUSample
	tokenHistory  []TokenSample
	deployHistory []DeploymentEvent
	prevCPU       cpuRaw
	prevToken     TokenSample
	width         int
	height        int
	scale         int // seconds per column (index into scales)
	err           error
}

type tickMsg time.Time

func main() {
	// Start OTEL receiver in background (gRPC on default OTLP port)
	otelReceiver = NewOTELReceiver(":4317")
	go func() {
		otelReceiver.Start() // Ignore error, optional feature
	}()

	// Start Vercel poller if project configured
	if project := os.Getenv(vercelProjectEnv); project != "" {
		vercelPoller = NewVercelPoller(project)
		vercelPoller.Start()
	}

	p := tea.NewProgram(model{scale: 60}, tea.WithAltScreen())
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
		case "+", "=":
			m.scale = nextScale(m.scale, 1) // zoom out
		case "-", "_":
			m.scale = nextScale(m.scale, -1) // zoom in
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

			// Keep enough history for max zoom (60s per column)
			maxHist := m.width * 60
			if maxHist < 600 {
				maxHist = 600
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
			// Keep enough history for max zoom (60s per column)
			maxHist := m.width * 60
			if maxHist < 600 {
				maxHist = 600
			}
			if len(m.tokenHistory) > maxHist {
				m.tokenHistory = m.tokenHistory[len(m.tokenHistory)-maxHist:]
			}
			m.prevToken = curr
		}

		// Update Vercel deployment events
		if vercelPoller != nil {
			m.deployHistory = vercelPoller.GetEvents()
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
	// CPU chart + (optional Vercel row) + Token header + Token chart
	tokChartHeight := 1 // tokens are discrete, one line enough
	vclRowHeight := 0
	if vercelPoller != nil {
		vclRowHeight = 1
	}
	cpuChartHeight := m.height - 1 - tokChartHeight - vclRowHeight // CPU gets the rest
	if cpuChartHeight < 1 {
		cpuChartHeight = 1
	}

	// Aggregate history based on current scale
	cpuAgg := aggregateCPU(m.cpuHistory, m.scale, m.width)
	tokAgg := aggregateTokens(m.tokenHistory, m.scale)

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

	// Right info: q:quit, scale indicator, and time span
	scaleInfo := fmt.Sprintf("%ds/col ", m.scale)
	rightInfo := dimStyle.Render("q:quit +/- " + scaleInfo + formatDuration(m.width*m.scale) + " ")

	cpuChart := renderCPUChart(cpuAgg, m.width, cpuChartHeight, cpuHeader, rightInfo)

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

	tokChart := renderTokenChart(tokAgg, m.width, tokChartHeight)

	// Vercel section (optional) - below tokens
	if vercelPoller != nil {
		vclRow := renderVercelEvents(m.deployHistory, m.width, m.scale)
		return cpuChart + "\n" + tokHeader + "\n" + tokChart + "\n" + vclRow
	}
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

func nextScale(current, dir int) int {
	idx := 0
	for i, s := range scales {
		if s == current {
			idx = i
			break
		}
	}
	idx += dir
	if idx < 0 {
		idx = 0
	}
	if idx >= len(scales) {
		idx = len(scales) - 1
	}
	return scales[idx]
}

func aggregateCPU(history []CPUSample, scale, width int) []CPUSample {
	if scale <= 1 {
		return history
	}
	result := make([]CPUSample, 0, len(history)/scale+1)
	for i := 0; i < len(history); i += scale {
		end := i + scale
		if end > len(history) {
			end = len(history)
		}
		chunk := history[i:end]
		var sum CPUSample
		for _, s := range chunk {
			sum.User += s.User
			sum.System += s.System
			sum.IOWait += s.IOWait
			sum.Steal += s.Steal
		}
		n := float64(len(chunk))
		result = append(result, CPUSample{
			User:   sum.User / n,
			System: sum.System / n,
			IOWait: sum.IOWait / n,
			Steal:  sum.Steal / n,
		})
	}
	return result
}

func aggregateTokens(history []TokenSample, scale int) []TokenSample {
	if scale <= 1 {
		return history
	}
	result := make([]TokenSample, 0, len(history)/scale+1)
	for i := 0; i < len(history); i += scale {
		end := i + scale
		if end > len(history) {
			end = len(history)
		}
		chunk := history[i:end]
		var sum TokenSample
		for _, s := range chunk {
			sum.Input += s.Input
			sum.Output += s.Output
			sum.CacheRead += s.CacheRead
			sum.CacheWrite += s.CacheWrite
		}
		result = append(result, sum)
	}
	return result
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

func renderVercelEvents(events []DeploymentEvent, width, scale int) string {
	header := " VCL "
	headerWidth := len(header)

	var row strings.Builder
	row.WriteString(dimStyle.Render(header))

	now := time.Now()
	for x := headerWidth; x < width; x++ {
		// Calculate time range for this column
		colAge := (width - 1 - x) * scale // seconds ago for this column
		colStart := now.Add(-time.Duration(colAge+scale) * time.Second)
		colEnd := now.Add(-time.Duration(colAge) * time.Second)

		// Find if any event falls in this time bucket
		var marker rune = ' '
		var style lipgloss.Style
		for _, e := range events {
			if (e.Time.After(colStart) && e.Time.Before(colEnd)) || e.Time.Equal(colEnd) {
				switch e.State {
				case DeployBuilding, DeployQueued:
					marker = '▌'
					style = buildingStyle
				case DeployReady:
					marker = '▌'
					style = readyStyle
				case DeployError:
					marker = '▌'
					style = errorStyle
				}
			}
		}

		if marker == ' ' {
			row.WriteRune(' ')
		} else {
			row.WriteString(style.Render(string(marker)))
		}
	}

	return row.String()
}
