// SPDX-License-Identifier: AGPL-3.0-or-later

// Package carddetect probes host hardware, discovers installed agent CLIs,
// and assembles or initializes capability cards (capabilities.yaml).
package carddetect

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Xustalis/OpenPanda/internal/agents"
	"github.com/Xustalis/OpenPanda/internal/hwinfo"
	"github.com/Xustalis/OpenPanda/internal/install"
	"github.com/Xustalis/OpenPanda/internal/ledger"
	"gopkg.in/yaml.v3"
)

// DetectCard probes the host and assembles a Card draft.
func DetectCard() ledger.Card {
	cores := runtime.NumCPU()
	ramGB := hwinfo.RAMGB()
	vram := hwinfo.GPUVRAMGB()

	// Resource class from RAM: <8GB = Micro, <32GB = Standard, else Full.
	class := "Standard"
	switch {
	case ramGB > 0 && ramGB < 8:
		class = "Micro"
	case ramGB >= 32:
		class = "Full"
	}
	maxConcurrent := 2
	if cores >= 8 {
		maxConcurrent = 4
	}

	card := ledger.Card{
		Device:        hwinfo.Hostname(),
		ResourceClass: class,
		Chip:          hwinfo.CPUModel(),
		Native:        DetectNative(),
		Capacity: ledger.Capacity{
			CPUCores:      cores,
			RAMGB:         ramGB,
			MaxConcurrent: maxConcurrent,
		},
		ResourceProfile: ledger.ResourceProfile{
			CPU:          cores,
			RAMGB:        ramGB,
			GPUVRAMGB:    vram,
			DurationHint: DurationHint(cores, ramGB, vram),
		},
	}

	// Agents: probe every known agent CLI from the registry (internal/agents)
	// and prewire the ones present.
	card.Agents = CardAgents()
	// Actuators: declare the bundled reference drivers that exist — the
	// shipped drivers/ dir does nothing on its own until the card advertises
	// it. Command is the resolved path, so PruneUnavailableActuators cannot
	// drop what this just found.
	card.Actuators = DetectActuators()
	return card
}

// NativeProbe defines one candidate native ability to probe on the host.
type NativeProbe struct {
	ID          string
	Command     string
	Args        []string
	Tier        int
	Description string
}

var defaultNativeProbes = []NativeProbe{
	{
		ID:          "sys:info",
		Command:     "uname",
		Args:        []string{"-a"},
		Tier:        1,
		Description: "system info",
	},
	{
		ID:          "disk:usage",
		Command:     "df",
		Args:        []string{"-h", "."},
		Tier:        1,
		Description: "disk usage of the work directory",
	},
	{
		ID:          "git",
		Command:     "git",
		Args:        []string{"status", "--short"},
		Tier:        1,
		Description: "git repository inspection",
	},
	{
		ID:          "media:ffmpeg",
		Command:     "ffmpeg",
		Args:        []string{"-version"},
		Tier:        1,
		Description: "media processing and transcoding",
	},
	{
		ID:          "docker:ps",
		Command:     "docker",
		Args:        []string{"ps"},
		Tier:        1,
		Description: "docker container inspection",
	},
	{
		ID:          "net:curl",
		Command:     "curl",
		Args:        []string{"--version"},
		Tier:        1,
		Description: "network requests and HTTP fetch",
	},
	{
		ID:          "runtime:python3",
		Command:     "python3",
		Args:        []string{"--version"},
		Tier:        1,
		Description: "python3 runtime",
	},
	{
		ID:          "runtime:node",
		Command:     "node",
		Args:        []string{"--version"},
		Tier:        1,
		Description: "nodejs runtime",
	},
	// Screen capture is deliberately NOT a native probe: a fixed-argv one-shot
	// ("screencapture -x /tmp/screencap.png") writes one shared path — tasks
	// collide, the file is world-readable, and there is no Linux/Windows
	// equivalent. The routed form lives on DetectActuators'
	// hardware:screen_capture (panda-screen driver): unique output per task
	// work dir, cross-platform, advertised to peers.
}

// DetectNative probes common command-line utilities present on the host
// and returns corresponding NativeAbility entries.
func DetectNative() []ledger.NativeAbility {
	var out []ledger.NativeAbility
	for _, p := range defaultNativeProbes {
		if _, err := exec.LookPath(p.Command); err == nil {
			out = append(out, ledger.NativeAbility{
				ID:          p.ID,
				Command:     p.Command,
				Args:        p.Args,
				Tier:        p.Tier,
				Description: p.Description,
			})
		}
	}
	return out
}

// driversDirEnv lets an operator point the driver probe at a nonstandard
// drivers/ root (tests use the same seam).
const driversDirEnv = "OPENPANDA_DRIVERS_DIR"

// resolveDriver finds one reference actuator driver (drivers/panda-*): the
// env override first — an operator who sets OPENPANDA_DRIVERS_DIR means
// "drivers live here", so it is the only directory consulted (tests rely on
// the same exclusivity for isolation) — then PATH (operator-installed
// copies win), then the shipped drivers/ tree: repo checkout, the packaged
// layout beside the executable, the dirs install copies into. Returns the
// command to put on the card: the bare name when PATH resolves it, the
// absolute driver path otherwise; "" when absent. An absolute result
// survives PruneUnavailableActuators for the same reason an on-PATH name
// does — both resolve through exec.LookPath at exec time.
func resolveDriver(name string) string {
	if d := strings.TrimSpace(os.Getenv(driversDirEnv)); d != "" {
		if cand := filepath.Join(d, name); executableFile(cand) {
			return cand
		}
		return ""
	}
	if _, err := exec.LookPath(name); err == nil {
		return name
	}
	if d := install.BundledDir("drivers"); d != "" {
		if cand := filepath.Join(d, name); executableFile(cand) {
			return cand
		}
	}
	return ""
}

// executableFile reports whether path names an existing non-directory file
// with any execute bit set.
func executableFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// DetectActuators declares actuators for the bundled reference drivers that
// exist — the shipped drivers/ dir is dead weight until the card advertises
// it. camera/mic/notify sense or signal; screen_capture + gui_input are the
// computer-use pair (eyes and hands): capture the screen, reason over it in
// an agent task, click/type back. servo/mcu are deliberately absent — they
// need wiring knowledge (pin, port) a probe cannot guess, so they stay
// manual declarations.
//
// gui_input is tier 2 on purpose: a click or keystroke cannot be undone —
// input synthesis is irreversible work and asks for consent like any other
// irreversible command. screen_capture stays tier 1 (reading the screen
// changes nothing) with the caveat a desktop screenshot can carry secrets —
// same exposure class as reading files, which is tier 1 too.
//
// Desktop-interface actuators additionally require guiSessionLikely: a card
// must not advertise eyes and hands on a node where no desktop session can
// exist (a headless server, or panda running as a system-domain daemon),
// because peers would route GUI work here and every run would fail.
func DetectActuators() []ledger.ActuatorProfile {
	var out []ledger.ActuatorProfile
	desktop := guiSessionLikely()
	add := func(name string, act ledger.ActuatorProfile) {
		if act.Interface == "desktop" && !desktop {
			return
		}
		if cmd := resolveDriver(name); cmd != "" {
			act.Command = cmd
			out = append(out, act)
		}
	}
	add("panda-camera", ledger.ActuatorProfile{
		ID:           "hardware:camera_snap",
		Type:         "hardware",
		Category:     "video_sensing",
		Interface:    "usb_camera",
		Capabilities: []string{"camera", "snapshot", "photo", "拍照", "摄像头"},
		Tier:         1,
		Args:         []string{"--out", "{param:out}"},
	})
	add("panda-mic", ledger.ActuatorProfile{
		ID:           "hardware:mic_record",
		Type:         "hardware",
		Category:     "audio_sensing",
		Interface:    "alsa",
		Capabilities: []string{"microphone", "record", "audio", "麦克风", "录音"},
		Tier:         1,
		Args:         []string{"--seconds", "{param:seconds}"},
	})
	add("panda-notify", ledger.ActuatorProfile{
		ID:           "hardware:notify",
		Type:         "software",
		Category:     "ui_feedback",
		Interface:    "desktop",
		Capabilities: []string{"notify", "notification", "alert", "通知", "提醒"},
		Tier:         1,
		Args:         []string{"--title", "{param:title}", "--body", "{param:body}"},
	})
	// The computer-use pair. Both drivers carry the platform fallback chain
	// (screencapture/grim/scrot/powershell; cliclick/xdotool/ydotool/
	// powershell) and fail loudly with "no backend" at exec time; the
	// desktop gate above keeps the card honest about whether a session
	// even exists.
	add("panda-screen", ledger.ActuatorProfile{
		ID:           "hardware:screen_capture",
		Type:         "software",
		Category:     "screen_sensing",
		Interface:    "desktop",
		Capabilities: []string{"screen", "screenshot", "capture", "gui", "desktop", "computer-use", "截屏", "屏幕", "桌面"},
		Tier:         1,
		// No placeholder args: the driver writes a unique timestamped file
		// into the task's own work dir and prints its path — nothing to
		// substitute, nothing for concurrent tasks to collide on.
	})
	add("panda-input", ledger.ActuatorProfile{
		ID:           "hardware:gui_input",
		Type:         "software",
		Category:     "gui_control",
		Interface:    "desktop",
		Capabilities: []string{"gui", "click", "type", "key", "scroll", "mouse", "keyboard", "desktop", "computer-use", "点击", "输入", "按键", "鼠标", "键盘"},
		Tier:         2,
		// Contract: {"action": "<verb>", "args": "<verb args>"} — e.g.
		// click "640,400", type "hello", key "ctrl+c", scroll "down,5".
		Args: []string{"{action}", "{param:args}"},
	})
	return out
}

// DurationHint classifies what this machine is for.
func DurationHint(cores, ramGB, vram int) string {
	if vram > 0 || vram == ledger.GPUVRAMUnknown || cores >= 8 || ramGB >= 32 {
		return "long"
	}
	return "short"
}

// CardAgents scans the agent registry and returns a card.Agents map with one
// entry per installed CLI.
func CardAgents() map[string]ledger.Agent {
	out := map[string]ledger.Agent{}
	for _, k := range agents.Registry() {
		bin := InstalledBinary(k)
		if bin == "" {
			continue
		}
		caps := k.DefaultCapabilities
		if len(caps) == 0 {
			caps = []string{"coding", "shell", "file_edit"}
		}
		bestAt := k.DefaultBestAt
		if len(bestAt) == 0 {
			bestAt = []string{"multi_file_edits", "code_search", "running_tests"}
		}
		costTier := k.DefaultCostTier
		if costTier == "" {
			costTier = "medium"
		}
		tier := k.DefaultTier
		if tier == 0 {
			// A generated card must not silently opt every agent into the
			// approval gate: commander.Route defaults an undeclared tier to 1
			// ("delegating to an agent is auto-approved"), and the shipped
			// example cards say tier: 1 too. Writing 2 here would win over that
			// default and make unattended work impossible on every detected
			// node — which is exactly what it did.
			tier = agents.TierAutoApproved
		}
		out[k.Name] = ledger.Agent{
			Adapter: k.Adapter,
			// The generic-adapter argv template (e.g. zcode's
			// "zcode --prompt {prompt}") rides the registry entry so a
			// detected agent lands on the card runnable, not half-wired.
			Command:      k.Command,
			InstallCheck: InstallCheckFor(bin),
			Capabilities: caps,
			BestAt:       bestAt,
			NotFor:       []string{"hardware_io", "realtime_control"},
			CostTier:     costTier,
			Tier:         tier,
		}
	}
	return out
}

// InstalledBinary returns the first of an agent's known binary names that is on
// PATH (or conventional user binary directories), or "".
func InstalledBinary(k agents.Known) string {
	for _, bin := range k.Binaries {
		if bin == "" {
			continue
		}
		if _, err := exec.LookPath(bin); err == nil {
			return bin
		}
		home, _ := os.UserHomeDir()
		candidates := []string{
			filepath.Join(home, ".local", "bin", bin),
			filepath.Join("/opt/homebrew/bin", bin),
			filepath.Join("/usr/local/bin", bin),
			filepath.Join(home, ".grok", "bin", bin),
			filepath.Join(home, ".cargo", "bin", bin),
		}
		for _, cand := range candidates {
			if st, err := os.Stat(cand); err == nil && !st.IsDir() && (st.Mode()&0o111 != 0) {
				return bin
			}
		}
	}
	return ""
}

// InstallCheckFor renders the shell one-liner a card carries so a human (or a
// remote node reading the card) can verify the CLI is really installed.
func InstallCheckFor(bin string) string {
	if runtime.GOOS == "windows" {
		return "where " + bin
	}
	return "which " + bin
}

// EnsureCard checks if the card file at path exists. If it exists, it loads
// and returns the card (created=false). If it does not exist, it runs DetectCard(),
// writes the generated YAML to path with a header, and returns the card (created=true).
func EnsureCard(path string) (ledger.Card, bool, error) {
	if path == "" {
		return ledger.Card{}, false, fmt.Errorf("ensure card: empty path")
	}
	if _, err := os.Stat(path); err == nil {
		c, err := ledger.LoadCard(path)
		return c, false, err
	}

	card := DetectCard()
	data, err := yaml.Marshal(card)
	if err != nil {
		return ledger.Card{}, false, fmt.Errorf("render capability card: %w", err)
	}

	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return ledger.Card{}, false, fmt.Errorf("create card dir: %w", err)
		}
	}

	header := fmt.Sprintf("# capabilities.yaml — generated by OpenPanda on %s\n# hardware fields are auto-detected; modify or add native/agents as needed.\n\n",
		time.Now().Format("2006-01-02 15:04"))

	content := append([]byte(header), data...)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return ledger.Card{}, false, fmt.Errorf("write capability card %s: %w", path, err)
	}

	return card, true, nil
}

// guiSessionEnv forces the desktop-session verdict — "1"/"true" claims a
// session, "0"/"false" claims none. It exists for two crowds: operators
// debugging why a desktop node is not advertising eyes+hands, and tests
// running on machines whose session reality varies (CI is headless).
const guiSessionEnv = "OPENPANDA_GUI_SESSION"

// guiSessionLikely reports whether this process could plausibly reach a
// desktop GUI session. It is a session check, not a permission check —
// macOS TCC grants (screen recording, accessibility) are prompts only the
// user can answer at exec time, so the drivers still fail honestly when the
// OS refuses. What this prevents is the card advertising desktop abilities
// on a node where no screen exists at all: peers would route GUI work here
// and every run would fail the same way.
func guiSessionLikely() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(guiSessionEnv))) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		// The GUI belongs to whoever owns /dev/console. A user-domain
		// launchd agent or terminal session running as that user sees the
		// screen; a system daemon (uid 0, or a different user) does not.
		out, err := exec.Command("stat", "-f", "%u", "/dev/console").Output()
		if err != nil {
			return false
		}
		uid, err := strconv.Atoi(strings.TrimSpace(string(out)))
		return err == nil && uid == os.Getuid()
	case "windows":
		// Session 0 ("Services") has no desktop. Git-bash and similar may
		// not set SESSIONNAME at all — unknown counts as present, since
		// silently disarming a working desktop is worse than letting a
		// driver report its own failure.
		name := os.Getenv("SESSIONNAME")
		return name == "" || !strings.EqualFold(name, "Services")
	default:
		// X11 or Wayland export the display socket name when a session
		// exists. Headless Linux/BSD sets neither.
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
}
