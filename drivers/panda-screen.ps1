# SPDX-License-Identifier: AGPL-3.0-or-later
# panda-screen.ps1 — Windows entry point for the panda-screen driver.
#
# Usage:
#   panda-screen.ps1 [--out FILE] [--display N]
#
# Same CLI, output and exit codes as the POSIX sibling (drivers/panda-screen):
# capture the desktop to a PNG and print its path. The POSIX script reaches its
# PowerShell branch only under git-bash; a stock Windows node has no MSYS, so
# the capability card resolves THIS file instead (carddetect.driverFileNames)
# and runs it as `pwsh/powershell -File <this script>`. -File hands every
# argument to the script as a literal string, so task-supplied text can never
# execute as PowerShell code.
#
# --display is accepted and ignored: screencapture -D is macOS-only.
#
# Exit codes: 0 ok · 2 bad arguments · 3 no capture backend · 1 capture error.

$ErrorActionPreference = 'Stop'

function Write-Usage {
    [Console]::Error.WriteLine(@'
panda-screen [--out FILE] [--display N]
  --out FILE    PNG output path (default ./screen-<ts>-<pid>.png)
  --display N   display index (macOS screencapture -D; ignored here)
'@)
}

$out = ''
$i = 0
while ($i -lt $args.Count) {
    $a = [string]$args[$i]
    if ($a -eq '--out' -or $a -eq '-out') {
        $i++
        if ($i -ge $args.Count) { Write-Usage; exit 2 }
        $out = [string]$args[$i]
    }
    elseif ($a -eq '--display' -or $a -eq '-display') {
        $i++
        if ($i -ge $args.Count) { Write-Usage; exit 2 }
    }
    elseif ($a -eq '-h' -or $a -eq '--help' -or $a -eq '-help') {
        Write-Usage; exit 0
    }
    else {
        Write-Usage; exit 2
    }
    $i++
}

if ([string]::IsNullOrEmpty($out)) {
    $out = "./screen-$(Get-Date -Format 'yyyyMMdd-HHmmss')-$PID.png"
}

# Resolve against the process cwd (the task's work dir) once, so the save and
# the existence check cannot disagree about what a relative --out means.
$full = [System.IO.Path]::GetFullPath($out)
$dir = [System.IO.Path]::GetDirectoryName($full)
if ($dir -and -not (Test-Path -LiteralPath $dir)) {
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
}

# A host without the desktop assemblies (Server Core without Desktop
# Experience) has no capture backend at all — exit 3, the driver contract for
# "nothing here can take this picture", so the failure reads as a missing
# backend rather than a broken capture.
try {
    Add-Type -AssemblyName System.Drawing -ErrorAction Stop
    Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop
}
catch {
    [Console]::Error.WriteLine("panda-screen: no capture backend on this host — System.Drawing/System.Windows.Forms unavailable: $($_.Exception.Message)")
    exit 3
}

try {
    # Full virtual screen: multi-monitor setups capture as one desktop, the
    # same way the git-bash branch does.
    $b = [System.Windows.Forms.SystemInformation]::VirtualScreen
    $bmp = New-Object System.Drawing.Bitmap $b.Width, $b.Height
    try {
        $g = [System.Drawing.Graphics]::FromImage($bmp)
        try {
            $g.CopyFromScreen($b.Left, $b.Top, 0, 0, $bmp.Size)
        }
        finally { $g.Dispose() }
        $bmp.Save($full, [System.Drawing.Imaging.ImageFormat]::Png)
    }
    finally { $bmp.Dispose() }
}
catch {
    [Console]::Error.WriteLine("panda-screen: capture failed: $($_.Exception.Message)")
    exit 1
}

if (-not (Test-Path -LiteralPath $full) -or (Get-Item -LiteralPath $full).Length -le 0) {
    [Console]::Error.WriteLine("panda-screen: produced empty file: $out")
    exit 1
}

[Console]::Out.WriteLine($out)
