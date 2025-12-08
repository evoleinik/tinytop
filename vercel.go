package main

import (
	"bufio"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DeploymentState represents a Vercel deployment state
type DeploymentState int

const (
	DeployNone DeploymentState = iota
	DeployQueued
	DeployBuilding
	DeployReady
	DeployError
	DeployCanceled
)

// DeploymentEvent represents a state change
type DeploymentEvent struct {
	Time  time.Time
	State DeploymentState
}

// VercelPoller polls Vercel CLI for deployment status
type VercelPoller struct {
	mu           sync.RWMutex
	project      string
	events       []DeploymentEvent
	lastState    DeploymentState
	lastDeployID string
	pollInterval time.Duration
	stopCh       chan struct{}
}

// NewVercelPoller creates a new Vercel poller
func NewVercelPoller(project string) *VercelPoller {
	return &VercelPoller{
		project:      project,
		pollInterval: 10 * time.Second,
		stopCh:       make(chan struct{}),
	}
}

// Start begins polling in background
func (v *VercelPoller) Start() {
	go v.pollLoop()
}

// Stop stops polling
func (v *VercelPoller) Stop() {
	close(v.stopCh)
}

func (v *VercelPoller) pollLoop() {
	ticker := time.NewTicker(v.pollInterval)
	defer ticker.Stop()

	// Poll immediately on start
	v.poll()

	for {
		select {
		case <-ticker.C:
			v.poll()
		case <-v.stopCh:
			return
		}
	}
}

func (v *VercelPoller) poll() {
	// Execute: vercel ls <project> -y
	// Use CombinedOutput because vercel sends the table to stderr
	cmd := exec.Command("vercel", "ls", v.project, "-y")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return // Silently ignore errors (optional feature)
	}

	v.parseOutput(string(output))
}

func (v *VercelPoller) parseOutput(output string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	scanner := bufio.NewScanner(strings.NewReader(output))
	inData := false

	for scanner.Scan() {
		line := scanner.Text()

		// Skip header lines
		if strings.Contains(line, "Age") && strings.Contains(line, "Status") {
			inData = true
			continue
		}
		if !inData || strings.TrimSpace(line) == "" {
			continue
		}

		// Parse deployment line
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		// Find status and URL
		var state DeploymentState
		var deployID string

		for _, f := range fields {
			if strings.HasPrefix(f, "https://") {
				deployID = extractDeployID(f)
			}
			switch strings.ToUpper(f) {
			case "BUILDING":
				state = DeployBuilding
			case "READY":
				state = DeployReady
			case "ERROR":
				state = DeployError
			case "QUEUED":
				state = DeployQueued
			case "CANCELED":
				state = DeployCanceled
			}
		}

		// Only track the most recent deployment (first data line)
		if deployID != "" {
			if deployID != v.lastDeployID || state != v.lastState {
				v.events = append(v.events, DeploymentEvent{
					Time:  time.Now(),
					State: state,
				})
				v.lastDeployID = deployID
				v.lastState = state
			}
			break // Only care about the most recent
		}
	}
}

// GetEvents returns copy of all events
func (v *VercelPoller) GetEvents() []DeploymentEvent {
	v.mu.RLock()
	defer v.mu.RUnlock()
	result := make([]DeploymentEvent, len(v.events))
	copy(result, v.events)
	return result
}

func extractDeployID(url string) string {
	// Extract unique deployment ID from URL
	// e.g., "https://airshelf-gbb82lz07-eugeneoteam.vercel.app"
	parts := strings.Split(url, "-")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return url
}
