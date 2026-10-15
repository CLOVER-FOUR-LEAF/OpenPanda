// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package carddetect

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// driverFileNames lists the file names a driver may live under on Windows, in
// preference order. The release ships PowerShell entry points
// (drivers/panda-screen.ps1): a stock Windows node has no MSYS shell, and the
// POSIX scripts' Windows branches need git-bash — CreateProcess cannot exec a
// shebang script in the first place. Native executables are accepted after.
//
// Batch wrappers (.cmd/.bat) are deliberately absent even though Windows
// treats them as executable: cmd.exe parses its command line by its own rules,
// not the CommandLineToArgvW rules Go's exec escaping follows, so an argument
// carrying &, % or a quote can split into a second command (golang/go#27199).
// The shipped .ps1 exists precisely so driver arguments stay data.
func driverFileNames(name string) []string {
	return []string{name + ".ps1", name + ".exe", name + ".com"}
}

// driverInvocation maps a resolved driver file to the argv a card must carry.
// A .ps1 is not directly exec'able: the command becomes a PowerShell host and
// the script rides the argv prefix (-File <path>). -File passes the remaining
// arguments to the script as literal strings, so hostile text in an
// action_spec parameter can mangle an argument at worst — never execute as
// code, which is what a cmd.exe wrapper would allow.
//
// The host is probed (pwsh first, the order hwinfo uses) because a card naming
// an interpreter this node lacks would prune away at load or die at exec with
// 127. Everything else — a .exe, or a bare name LookPath resolved — runs
// directly.
func driverInvocation(path string) (string, []string, bool) {
	if strings.EqualFold(filepath.Ext(path), ".ps1") {
		ps := powerShellHost()
		if ps == "" {
			return "", nil, false
		}
		return ps, []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path}, true
	}
	return path, nil, true
}

// powerShellHost returns the PowerShell executable to run scripts with —
// "pwsh" (7+, actively maintained) when present, else the inbox
// "powershell" (5.1) — or "" when neither resolves.
func powerShellHost() string {
	for _, name := range []string{"pwsh", "powershell"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// executableFile reports whether path names an existing non-directory file
// Windows can launch. There is no execute bit on Windows — os.Stat reports
// 0666/0444 for every file — so runnability is decided by extension, over the
// same set LookPath/PATHEXT treats as executable.
func executableFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ps1", ".exe", ".com":
		return true
	}
	return false
}

// binaryFileNames lists the file names an installed agent CLI may live under
// in a fallback directory: Windows installers land .exe binaries and npm-style
// .cmd shims. Detection only needs the file to exist — how the CLI is later
// invoked is LookPath's business, the same as for a PATH-resolved name.
func binaryFileNames(name string) []string {
	return []string{name + ".exe", name + ".cmd", name + ".bat", name + ".com"}
}

// fallbackBinDirs lists the per-user directories Windows agent-CLI installers
// land in outside PATH: npm's per-user prefix, winget's shim dir, scoop,
// cargo, and the unix-style ~/.local/bin that some cross-platform installers
// (and this project's own install.sh under git-bash) still use.
func fallbackBinDirs(home string) []string {
	dirs := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, "scoop", "shims"),
	}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		dirs = append(dirs, filepath.Join(appdata, "npm"))
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs, filepath.Join(local, "Microsoft", "WinGet", "Links"))
	}
	return dirs
}
