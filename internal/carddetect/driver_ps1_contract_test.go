// SPDX-License-Identifier: AGPL-3.0-or-later

package carddetect_test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPS1DriversHonorUsageExitCode runs each shipped Windows driver through
// the exact host invocation the card carries (`pwsh/powershell -File <script>`)
// and asserts the documented usage refusal: an unknown flag exits 2.
//
// It is the one slice of the .ps1 contract a host without the Windows desktop
// APIs can still check — everything past argument parsing needs
// System.Windows.Forms — and it keeps the scripts honest on the CI legs that
// have a PowerShell host (ubuntu and windows runners both ship pwsh): a syntax
// error, a renamed flag or a broken exit path fails here instead of on a
// user's desktop node.
func TestPS1DriversHonorUsageExitCode(t *testing.T) {
	ps := powerShellForTest(t)
	for _, script := range []string{"panda-screen.ps1", "panda-input.ps1", "panda-notify.ps1"} {
		t.Run(script, func(t *testing.T) {
			path := filepath.Join("..", "..", "drivers", script)
			cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path, "--bogus-flag")
			out, err := cmd.CombinedOutput()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("run %s: %v (output: %s)", script, err, out)
			}
			if code != 2 {
				t.Fatalf("%s with an unknown flag exited %d, want 2 (usage refusal); output: %s", script, code, out)
			}
		})
	}
}

// powerShellForTest returns a PowerShell executable on PATH, or skips — the
// same probe order driverInvocation uses.
func powerShellForTest(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh", "powershell"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("no PowerShell host on this machine — .ps1 driver contract not checkable here")
	return ""
}
