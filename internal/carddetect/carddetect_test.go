// SPDX-License-Identifier: AGPL-3.0-or-later

package carddetect_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Xustalis/OpenPanda/internal/agents"
	"github.com/Xustalis/OpenPanda/internal/carddetect"
	"github.com/Xustalis/OpenPanda/internal/ledger"
)

// stubAgentOnPATH creates a fake agent executable in a temp dir and prepends it
// to PATH so that exec.LookPath / InstalledBinary finds at least one known
// agent even on CI runners that have no real agent CLIs installed.
func stubAgentOnPATH(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho stub\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))
}

func TestDetectCard(t *testing.T) {
	card := carddetect.DetectCard()
	if card.Device == "" {
		t.Error("expected non-empty Device in detected card")
	}
	if card.ResourceClass == "" {
		t.Error("expected non-empty ResourceClass in detected card")
	}
	if card.Capacity.CPUCores <= 0 {
		t.Errorf("expected positive CPUCores, got %d", card.Capacity.CPUCores)
	}
}

func TestEnsureCard(t *testing.T) {
	tmpDir := t.TempDir()
	cardPath := filepath.Join(tmpDir, "sub", "capabilities.yaml")

	// 1. File does not exist -> should create it
	card, created, err := carddetect.EnsureCard(cardPath)
	if err != nil {
		t.Fatalf("EnsureCard create failed: %v", err)
	}
	if !created {
		t.Error("expected created=true on first call")
	}
	if card.Device == "" {
		t.Error("expected valid card device")
	}
	if _, err := os.Stat(cardPath); err != nil {
		t.Fatalf("card file was not written: %v", err)
	}

	// 2. File exists -> should load without re-creating
	card2, created2, err := carddetect.EnsureCard(cardPath)
	if err != nil {
		t.Fatalf("EnsureCard load failed: %v", err)
	}
	if created2 {
		t.Error("expected created=false on second call")
	}
	if card2.Device != card.Device {
		t.Errorf("got device %q, want %q", card2.Device, card.Device)
	}
}

// TestCardAgentsDoNotPreSelectTheApprovalGate is the generator half of the same
// contract: whatever the registry says, the map a card is built from must not
// declare tier 2 for an agent. The literal that used to be here (2 for every
// agent, and again as the 0-fallback) overrode commander.Route's documented
// default of 1, so every `panda ask` that classified as an agent task was
// refused by defense.Authorize and waited for a human.
func TestCardAgentsDoNotPreSelectTheApprovalGate(t *testing.T) {
	stubAgentOnPATH(t)

	card := carddetect.CardAgents()
	if len(card) == 0 {
		t.Fatal("no agents to check")
	}
	for name, ag := range card {
		if ag.Tier != agents.TierAutoApproved {
			t.Errorf("generated card declares agent %q tier %d, want %d",
				name, ag.Tier, agents.TierAutoApproved)
		}
	}
}

// driverFileName is the file name a driver resolves under on this platform:
// the bare script on unix, the PowerShell entry point on Windows (the POSIX
// script needs git-bash there and CreateProcess cannot exec a shebang).
func driverFileName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".ps1"
	}
	return name
}

// stubDriverDir builds a fake drivers/ dir with the named drivers (under this
// platform's file name) and points the probe at it via OPENPANDA_DRIVERS_DIR —
// the env seam resolveDriver honors before PATH and the bundled layout.
func stubDriverDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, driverFileName(n)), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OPENPANDA_DRIVERS_DIR", dir)
	return dir
}

// requireDriverHost skips driver-resolution tests on a Windows host with no
// PowerShell: a .ps1 driver is deliberately not advertised there (the card
// would name an interpreter the node lacks), so there is nothing to assert.
func requireDriverHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	if _, err := exec.LookPath("pwsh"); err == nil {
		return
	}
	if _, err := exec.LookPath("powershell"); err == nil {
		return
	}
	t.Skip("no PowerShell host on this Windows runner — .ps1 drivers are not advertised by design")
}

// assertDriverInvocation checks that a detected actuator carries an invocation
// that can actually run on this platform: the driver file itself on unix, a
// PowerShell host with `-File <driver>` on Windows.
func assertDriverInvocation(t *testing.T, act ledger.ActuatorProfile, dir, name string) {
	t.Helper()
	driver := filepath.Join(dir, driverFileName(name))
	if runtime.GOOS != "windows" {
		if act.Command != driver {
			t.Errorf("%s command = %q, want the resolved driver path %q", act.ID, act.Command, driver)
		}
		return
	}
	if strings.EqualFold(filepath.Ext(act.Command), ".ps1") {
		t.Errorf("%s command = %q — a .ps1 is not directly exec'able on Windows", act.ID, act.Command)
	}
	found := false
	for i, a := range act.Args {
		if a == "-File" && i+1 < len(act.Args) && act.Args[i+1] == driver {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%s args = %v, want `-File %s` among them", act.ID, act.Args, driver)
	}
}

func actuatorIDs(acts []ledger.ActuatorProfile) map[string]ledger.ActuatorProfile {
	out := make(map[string]ledger.ActuatorProfile, len(acts))
	for _, a := range acts {
		out[a.ID] = a
	}
	return out
}

// guiSession pins the desktop-session verdict so tests are deterministic on
// both CI (headless) and a dev machine (console user).
func guiSession(t *testing.T, present bool) {
	t.Helper()
	if present {
		t.Setenv("OPENPANDA_GUI_SESSION", "1")
	} else {
		t.Setenv("OPENPANDA_GUI_SESSION", "0")
	}
}

func TestDetectActuatorsFromDriversDir(t *testing.T) {
	requireDriverHost(t)
	guiSession(t, true)
	dir := stubDriverDir(t, "panda-screen", "panda-input")

	acts := actuatorIDs(carddetect.DetectActuators())
	screen, ok := acts["hardware:screen_capture"]
	if !ok {
		t.Fatal("panda-screen present but hardware:screen_capture not declared")
	}
	if screen.Tier != 1 {
		t.Errorf("screen_capture tier = %d, want 1 (observation is reversible)", screen.Tier)
	}
	assertDriverInvocation(t, screen, dir, "panda-screen")

	input, ok := acts["hardware:gui_input"]
	if !ok {
		t.Fatal("panda-input present but hardware:gui_input not declared")
	}
	// Input synthesis is irreversible — a click that sent a message cannot be
	// unsent. The generated card must gate it at tier 2 without being told.
	if input.Tier != 2 {
		t.Errorf("gui_input tier = %d, want 2 (clicks/keys are irreversible)", input.Tier)
	}
	// No panda-camera/mic/notify in the stub dir → no phantom actuators.
	for _, absent := range []string{"hardware:camera_snap", "hardware:mic_record", "hardware:notify"} {
		if _, found := acts[absent]; found {
			t.Errorf("actuator %s declared although its driver is absent", absent)
		}
	}
}

func TestDetectActuatorsEmptyWhenNoDrivers(t *testing.T) {
	guiSession(t, true)
	stubDriverDir(t) // empty dir — resolves nothing
	if acts := carddetect.DetectActuators(); len(acts) != 0 {
		t.Fatalf("expected no actuators with no drivers present, got %v", acts)
	}
}

// TestDesktopActuatorsRequireGuiSession pins the card's honesty rule: on a
// headless host (or a system-domain daemon context) the computer-use pair —
// and any other Interface:"desktop" actuator — must not be advertised even
// when the driver scripts exist, because every routed task would fail there.
// Hardware actuators are unaffected: a camera needs no display.
func TestDesktopActuatorsRequireGuiSession(t *testing.T) {
	requireDriverHost(t)
	guiSession(t, false)
	stubDriverDir(t, "panda-screen", "panda-input", "panda-notify", "panda-camera")

	acts := actuatorIDs(carddetect.DetectActuators())
	for _, id := range []string{"hardware:screen_capture", "hardware:gui_input", "hardware:notify"} {
		if _, found := acts[id]; found {
			t.Errorf("%s advertised with no GUI session", id)
		}
	}
	if _, found := acts["hardware:camera_snap"]; !found {
		t.Error("hardware:camera_snap must not be gated on a desktop session")
	}
}

// TestDetectedActuatorsSurvivePrune pins the contract that makes the pair
// useful: PruneUnavailableActuators runs at load/execution and must keep what
// detection just declared — a detected path that prunes away is an actuator
// the card advertises but can never run.
func TestDetectedActuatorsSurvivePrune(t *testing.T) {
	requireDriverHost(t)
	guiSession(t, true)
	stubDriverDir(t, "panda-screen")
	card := ledger.Card{Actuators: carddetect.DetectActuators()}
	if len(card.Actuators) == 0 {
		t.Skip("no drivers resolved on this host — nothing to prune-check")
	}
	if dropped := card.PruneUnavailableActuators(); len(dropped) != 0 {
		t.Fatalf("detected actuator(s) pruned: %v — resolveDriver must return a LookPath-resolvable command", dropped)
	}
}
