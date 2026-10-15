// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package carddetect_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xustalis/OpenPanda/internal/agents"
	"github.com/Xustalis/OpenPanda/internal/carddetect"
	"github.com/Xustalis/OpenPanda/internal/ledger"
)

// stubRawDriverDir writes exactly the given file names into a temp dir and
// points the driver probe at it — no platform naming applied, so a test can
// stage the file shapes Windows must and must not accept.
func stubRawDriverDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OPENPANDA_DRIVERS_DIR", dir)
	return dir
}

func screenActuator(t *testing.T) (ledger.ActuatorProfile, bool) {
	t.Helper()
	guiSession(t, true)
	for _, a := range carddetect.DetectActuators() {
		if a.ID == "hardware:screen_capture" {
			return a, true
		}
	}
	return ledger.ActuatorProfile{}, false
}

// TestWindowsResolvesPS1Driver pins the fix for the platform gap: before this,
// the bundled POSIX driver (an extensionless sh script) was the only shape
// Windows looked for — os.Stat never reports an execute bit there and LookPath
// cannot match an extensionless file — so a Windows node advertised no
// hardware:* actuators at all despite shipping PowerShell-capable drivers.
func TestWindowsResolvesPS1Driver(t *testing.T) {
	requireDriverHost(t)
	dir := stubRawDriverDir(t, "panda-screen.ps1")

	act, ok := screenActuator(t)
	if !ok {
		t.Fatal("panda-screen.ps1 present but hardware:screen_capture not declared")
	}
	assertDriverInvocation(t, act, dir, "panda-screen")
}

// TestWindowsPrefersPS1OverExe pins the preference order: the shipped entry
// point wins over a native binary dropped beside it.
func TestWindowsPrefersPS1OverExe(t *testing.T) {
	requireDriverHost(t)
	dir := stubRawDriverDir(t, "panda-screen.ps1", "panda-screen.exe")

	act, ok := screenActuator(t)
	if !ok {
		t.Fatal("drivers present but hardware:screen_capture not declared")
	}
	assertDriverInvocation(t, act, dir, "panda-screen")
	if filepath.Ext(act.Command) == ".exe" {
		t.Errorf("command = %q — the .exe shadowed the shipped .ps1", act.Command)
	}
}

// TestWindowsIgnoresExtensionlessDriver documents the deliberate refusal: the
// POSIX script cannot be exec'd by CreateProcess (no shebang handling), so
// resolving it would advertise an actuator that dies at exec with 127.
func TestWindowsIgnoresExtensionlessDriver(t *testing.T) {
	stubRawDriverDir(t, "panda-screen")
	if act, ok := screenActuator(t); ok {
		t.Fatalf("extensionless POSIX driver resolved as %q — CreateProcess cannot run it", act.Command)
	}
}

// TestWindowsIgnoresBatchWrappers documents the other deliberate refusal:
// cmd.exe parses its command line by its own rules, so a .cmd wrapper turns an
// action_spec parameter carrying &, % or a quote into a possible second
// command (golang/go#27199). The shipped .ps1 exists so arguments stay data.
func TestWindowsIgnoresBatchWrappers(t *testing.T) {
	stubRawDriverDir(t, "panda-screen.cmd", "panda-screen.bat")
	if act, ok := screenActuator(t); ok {
		t.Fatalf("batch wrapper resolved as %q — its cmd.exe parsing is not Go-escaping compatible", act.Command)
	}
}

// TestWindowsInstalledBinaryFindsFallbackDir mirrors the unix fallback test:
// an agent shim in %APPDATA%\npm — where npm's per-user global installs land —
// is found even when it is not on PATH. The old stat-based check required a
// unix execute bit, which Windows never reports, so this whole path was dead
// code there.
func TestWindowsInstalledBinaryFindsFallbackDir(t *testing.T) {
	home := t.TempDir()
	appdata := filepath.Join(home, "AppData", "Roaming")
	if err := os.MkdirAll(filepath.Join(appdata, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appdata, "npm", "claude.cmd"), []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", appdata)
	t.Setenv("PATH", t.TempDir()) // empty: LookPath must miss, fallback must hit

	if got := carddetect.InstalledBinary(agents.Known{Binaries: []string{"claude"}}); got != "claude" {
		t.Fatalf("InstalledBinary = %q, want claude from %%APPDATA%%\\npm", got)
	}
}
