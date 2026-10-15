// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package carddetect_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xustalis/OpenPanda/internal/agents"
	"github.com/Xustalis/OpenPanda/internal/carddetect"
)

// TestInstalledBinaryFindsFallbackDir pins the fallback half of agent
// detection: a CLI that is not on PATH but sits in a conventional per-user
// bin dir (npm/pip/cargo installs) is still found, under this platform's
// runnability rule. The Windows mirror of this check lives in the Windows
// test file, where the file shape is a .cmd/.exe.
func TestInstalledBinaryFindsFallbackDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // empty: LookPath must miss, fallback must hit

	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := carddetect.InstalledBinary(agents.Known{Binaries: []string{"claude"}}); got != "claude" {
		t.Fatalf("InstalledBinary = %q, want claude from ~/.local/bin", got)
	}
}

// TestUnixIgnoresPS1Driver pins the platform split from the other side: the
// Windows entry point (.ps1) must not resolve on unix even when the release
// lays it down executable (package.sh chmods every drivers/panda-*), because
// unix runs the POSIX script and a .ps1 here is a file with no interpreter.
func TestUnixIgnoresPS1Driver(t *testing.T) {
	guiSession(t, true)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "panda-screen.ps1"), []byte("# windows entry point\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENPANDA_DRIVERS_DIR", dir)

	for _, a := range carddetect.DetectActuators() {
		if a.ID == "hardware:screen_capture" {
			t.Fatalf("a .ps1 resolved on unix as %q — the POSIX driver is the unix entry point", a.Command)
		}
	}
}
