# SPDX-License-Identifier: AGPL-3.0-or-later
# panda-notify.ps1 — Windows entry point for the panda-notify driver.
#
# Usage:
#   panda-notify.ps1 [--title TEXT] [--body TEXT]
#
# Same CLI, output and exit codes as the POSIX sibling (drivers/panda-notify),
# whose Windows branch needs git-bash. A stock Windows node resolves THIS file
# instead (carddetect.driverFileNames) and runs it as `pwsh/powershell -File`.
# Title and body arrive as literal -File arguments and are handed to the
# NotifyIcon as plain strings — a body carrying quotes, $() or & stays text.
#
# Exit codes: 0 ok · 2 bad arguments · 3 no notification backend · 1 delivery error.

$ErrorActionPreference = 'Stop'

function Write-Usage {
    [Console]::Error.WriteLine(@'
panda-notify [--title TEXT] [--body TEXT]
  --title TEXT   notification title (default "OpenPanda")
  --body TEXT    notification body
'@)
}

$title = 'OpenPanda'
$body = ''
$i = 0
while ($i -lt $args.Count) {
    $a = [string]$args[$i]
    if ($a -eq '--title' -or $a -eq '-title') {
        $i++
        if ($i -ge $args.Count) { Write-Usage; exit 2 }
        $title = [string]$args[$i]
    }
    elseif ($a -eq '--body' -or $a -eq '-body') {
        $i++
        if ($i -ge $args.Count) { Write-Usage; exit 2 }
        $body = [string]$args[$i]
    }
    elseif ($a -eq '-h' -or $a -eq '--help' -or $a -eq '-help') {
        Write-Usage; exit 0
    }
    else {
        Write-Usage; exit 2
    }
    $i++
}

# No WinForms (Server Core without Desktop Experience) means no notification
# surface — exit 3, the driver contract for "no backend on this host".
try {
    Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop
    Add-Type -AssemblyName System.Drawing -ErrorAction Stop
}
catch {
    [Console]::Error.WriteLine("panda-notify: no notification backend on this host — System.Windows.Forms unavailable: $($_.Exception.Message)")
    exit 3
}

try {
    $n = New-Object System.Windows.Forms.NotifyIcon
    $n.Icon = [System.Drawing.SystemIcons]::Information
    $n.Visible = $true
    $n.ShowBalloonTip(3000, $title, $body, [System.Windows.Forms.ToolTipIcon]::Info)
    # The icon must outlive the call long enough for the shell to render the
    # balloon; disposing immediately swallows it (the git-bash branch sleeps
    # for the same reason).
    Start-Sleep -Seconds 1
    $n.Visible = $false
    $n.Dispose()
}
catch {
    [Console]::Error.WriteLine("panda-notify: notification failed: $($_.Exception.Message)")
    exit 1
}

[Console]::Out.WriteLine("notified: $title")
