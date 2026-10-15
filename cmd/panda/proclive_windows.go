// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package main

import (
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// daemonProcStats returns a one-line "rss=… cpu=… uptime=…" detail for pid
// when the process is alive — `panda metrics --runtime` reports the daemon's
// load through its pid file because the daemon keeps no introspection
// endpoint. The unix twin shells out to ps; here the same numbers come from
// the process API: WorkingSetSize for RSS (GetProcessMemoryInfo), kernel+user
// time for CPU, and the creation time for uptime.
//
// One honest difference: the unix line prints an instantaneous cpu% from ps.
// Windows has no cheap equivalent without sampling the counter twice, so the
// line prints total CPU time consumed instead — labelled ("cpu=4.2s"), never
// dressed up as a percentage.
func daemonProcStats(pid int) (string, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h)

	var parts []string

	var mc processMemoryCounters
	mc.Cb = uint32(unsafe.Sizeof(mc))
	if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mc)), uintptr(mc.Cb)); r != 0 {
		parts = append(parts, "rss="+strconv.FormatFloat(float64(mc.WorkingSetSize)/1048576, 'f', 1, 64)+"MiB")
	}

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err == nil {
		cpu := filetimeDuration(kernel) + filetimeDuration(user)
		parts = append(parts, "cpu="+strconv.FormatFloat(cpu.Seconds(), 'f', 1, 64)+"s")
		// creation is a timestamp (Nanoseconds subtracts the 1601→1970 epoch
		// offset, which is right here); kernel/user are durations and must not
		// go through it — see filetimeDuration.
		if up := time.Since(time.Unix(0, creation.Nanoseconds())).Round(time.Second); up > 0 {
			parts = append(parts, "uptime="+up.String())
		}
	}

	if len(parts) == 0 {
		return "alive", true
	}
	return strings.Join(parts, " "), true
}

// filetimeDuration converts a FILETIME used as a duration (kernel/user time)
// into a Go duration. Filetime.Nanoseconds is for timestamps only: it subtracts
// the 1601→1970 epoch offset, which would turn a 4-second CPU time into a
// negative number of centuries.
func filetimeDuration(ft windows.Filetime) time.Duration {
	return time.Duration((uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)) * 100)
}

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS (psapi.h).
type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}
