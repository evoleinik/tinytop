# tinytop

Tiny terminal CPU monitor with Unicode graphics. Designed for small terminals over SSH.

![tinytop screenshot](screenshot.png)

## Features

- Stacked area chart: user (green), system (red), iowait (yellow), steal (magenta)
- Unicode block characters for sub-character resolution
- Adapts to terminal size
- 1-second refresh rate (SSH-friendly)
- Single static binary, no dependencies

## Build

```bash
# For current platform
go build -o tinytop

# For Linux (cross-compile from macOS)
GOOS=linux GOARCH=amd64 go build -o tinytop
```

## Usage

```bash
./tinytop
```

Press `q` or `Ctrl+C` to quit.

## Requirements

- Linux or macOS
- UTF-8 terminal

## Platform Notes

- **Linux**: Shows user, system, iowait, steal (reads `/proc/stat`)
- **macOS**: Shows user, system (reads `kern.cp_time` via sysctl)

## AI Notes

- `main.go` - UI and rendering (~180 lines)
- `cpu_linux.go` - Linux `/proc/stat` parsing
- `cpu_darwin.go` - macOS sysctl parsing
- Uses Bubbletea for TUI, Lipgloss for styling
- Chart uses `▁▂▃▄▅▆▇█` blocks for 8-level vertical resolution
