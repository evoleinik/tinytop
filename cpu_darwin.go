//go:build darwin

package main

/*
#include <mach/mach_host.h>
#include <mach/host_info.h>
*/
import "C"
import "unsafe"

// macOS CPU states: user, system, idle, nice (no iowait/steal)
type cpuRaw struct {
	user, system, idle, nice uint64
}

func (c cpuRaw) total() uint64 {
	return c.user + c.system + c.idle + c.nice
}

func readCPU() (cpuRaw, error) {
	var cpuLoad C.host_cpu_load_info_data_t
	var count C.mach_msg_type_number_t = C.HOST_CPU_LOAD_INFO_COUNT

	host := C.mach_host_self()
	ret := C.host_statistics(C.host_t(host), C.HOST_CPU_LOAD_INFO,
		(C.host_info_t)(unsafe.Pointer(&cpuLoad)), &count)

	if ret != C.KERN_SUCCESS {
		return cpuRaw{}, nil
	}

	return cpuRaw{
		user:   uint64(cpuLoad.cpu_ticks[C.CPU_STATE_USER]),
		system: uint64(cpuLoad.cpu_ticks[C.CPU_STATE_SYSTEM]),
		idle:   uint64(cpuLoad.cpu_ticks[C.CPU_STATE_IDLE]),
		nice:   uint64(cpuLoad.cpu_ticks[C.CPU_STATE_NICE]),
	}, nil
}

func calcPercentages(prev, curr cpuRaw) CPUSample {
	dUser := float64(curr.user + curr.nice - prev.user - prev.nice)
	dSystem := float64(curr.system - prev.system)
	dIdle := float64(curr.idle - prev.idle)

	total := dUser + dSystem + dIdle
	if total == 0 {
		return CPUSample{}
	}

	return CPUSample{
		User:   (dUser / total) * 100,
		System: (dSystem / total) * 100,
		IOWait: 0,
		Steal:  0,
	}
}
