//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type cpuRaw struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c cpuRaw) total() uint64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

func readCPU() (cpuRaw, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuRaw{}, err
	}

	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)

	if len(fields) < 8 || fields[0] != "cpu" {
		return cpuRaw{}, fmt.Errorf("unexpected /proc/stat format")
	}

	parse := func(i int) uint64 {
		if i >= len(fields) {
			return 0
		}
		v, _ := strconv.ParseUint(fields[i], 10, 64)
		return v
	}

	return cpuRaw{
		user:    parse(1),
		nice:    parse(2),
		system:  parse(3),
		idle:    parse(4),
		iowait:  parse(5),
		irq:     parse(6),
		softirq: parse(7),
		steal:   parse(8),
	}, nil
}

func calcPercentages(prev, curr cpuRaw) CPUSample {
	dUser := float64(curr.user + curr.nice - prev.user - prev.nice)
	dSystem := float64(curr.system + curr.irq + curr.softirq - prev.system - prev.irq - prev.softirq)
	dIOWait := float64(curr.iowait - prev.iowait)
	dSteal := float64(curr.steal - prev.steal)
	dIdle := float64(curr.idle - prev.idle)

	total := dUser + dSystem + dIOWait + dSteal + dIdle
	if total == 0 {
		return CPUSample{}
	}

	return CPUSample{
		User:   (dUser / total) * 100,
		System: (dSystem / total) * 100,
		IOWait: (dIOWait / total) * 100,
		Steal:  (dSteal / total) * 100,
	}
}
