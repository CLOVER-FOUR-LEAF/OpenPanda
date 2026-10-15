# SPDX-License-Identifier: AGPL-3.0-or-later
# panda-input.ps1 — Windows entry point for the panda-input driver.
#
# Usage:
#   panda-input.ps1 <verb> [args...]
#
# Verbs (same contract as the POSIX sibling drivers/panda-input):
#   move x,y                    absolute pointer move
#   click x,y[,button[,times]]  move + click (button left|middle|right; times n)
#   dblclick x,y[,button]
#   type <text>                 type literal text
#   key <spec>                  key or combo: Return | Escape | ctrl+c |
#                               alt+tab | shift+F5 …
#                               (mods: ctrl, alt|option, shift, cmd|meta|win)
#   scroll dir[,n]              wheel: up|down, n steps (default 3)
#   pos                         print current pointer position "x,y"
#
# Backend: System.Windows.Forms (SendKeys for the keyboard, Cursor for the
# pointer) plus user32 mouse_event for clicks and the wheel. Arguments arrive
# as literal -File strings, so typed text is never parsed as code. This is the
# "hands" half of computer use: declare the actuator tier 2.
#
# Exit codes: 0 ok · 2 bad arguments · 3 no input backend · 4 verb
# unsupported on this backend · 1 action error.

$ErrorActionPreference = 'Stop'

function Fail([int]$code, [string]$msg) {
    [Console]::Error.WriteLine("panda-input: $msg")
    exit $code
}

function Write-Usage {
    [Console]::Error.WriteLine(@'
panda-input <verb> [args]
  move x,y                     click x,y[,button[,times]]    dblclick x,y[,button]
  type <text>                  key <combo>                   scroll dir[,n]
  pos
buttons: left (default) | middle | right      scroll dirs: up down left right
'@)
}

# --- backend ----------------------------------------------------------------

function Ensure-WinForms {
    try { Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop }
    catch { Fail 3 "no input backend on this host — System.Windows.Forms unavailable: $($_.Exception.Message)" }
}

# user32!mouse_event, declared once per process. dwData is int (not the DWORD
# the API signature says) so a negative wheel delta survives the marshalling.
function Ensure-MouseApi {
    if (-not ('Panda.Mouse' -as [type])) {
        Add-Type -Namespace Panda -Name Mouse -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("user32.dll")] public static extern void mouse_event(uint dwFlags, uint dx, uint dy, int dwData, System.IntPtr dwExtraInfo);
'@
    }
}

function Move-Cursor([int]$x, [int]$y) {
    Ensure-WinForms
    [System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point($x, $y)
}

function Invoke-Click([int]$x, [int]$y, [string]$button, [int]$times) {
    Ensure-WinForms
    Ensure-MouseApi
    $down = 0x0002; $up = 0x0004
    switch ($button) {
        'right'  { $down = 0x0008; $up = 0x0010 }
        'middle' { $down = 0x0020; $up = 0x0040 }
    }
    Move-Cursor $x $y
    for ($n = 0; $n -lt $times; $n++) {
        [Panda.Mouse]::mouse_event($down, 0, 0, 0, [System.IntPtr]::Zero)
        [Panda.Mouse]::mouse_event($up, 0, 0, 0, [System.IntPtr]::Zero)
    }
}

function Send-Keys([string]$sk) {
    Ensure-WinForms
    try { [System.Windows.Forms.SendKeys]::SendWait($sk) }
    catch { Fail 1 "SendKeys failed: $($_.Exception.Message)" }
}

# --- argument parsing -------------------------------------------------------

function Parse-XY([string]$s) {
    $parts = $s.Split(',')
    if ($parts.Count -ne 2) { Fail 2 "want x,y got '$s'" }
    [int]$x = 0; [int]$y = 0
    if (-not [int]::TryParse($parts[0], [ref]$x) -or -not [int]::TryParse($parts[1], [ref]$y)) {
        Fail 2 "bad coords '$s'"
    }
    return [pscustomobject]@{ X = $x; Y = $y }
}

function Parse-Click([string]$s) {
    $parts = $s.Split(',')
    if ($parts.Count -lt 2 -or $parts.Count -gt 4) { Fail 2 "want x,y[,button[,times]] got '$s'" }
    [int]$x = 0; [int]$y = 0
    if (-not [int]::TryParse($parts[0], [ref]$x) -or -not [int]::TryParse($parts[1], [ref]$y)) {
        Fail 2 "bad coords '$s'"
    }
    $btn = 'left'
    if ($parts.Count -ge 3) {
        if ($parts[2] -eq '') { Fail 2 "bad button ''" }
        $btn = $parts[2].ToLowerInvariant()
    }
    $times = 1
    if ($parts.Count -ge 4) {
        if (-not [int]::TryParse($parts[3], [ref]$times) -or $times -lt 0) { Fail 2 "bad times '$($parts[3])'" }
    }
    if (@('left', 'middle', 'right') -notcontains $btn) { Fail 2 "bad button '$btn'" }
    return [pscustomobject]@{ X = $x; Y = $y; Button = $btn; Times = $times }
}

# Normalize-KeySpec <spec> -> object with Mods (ctrl/alt/shift/cmd) and Key
# (a canonical name: Return, Escape, space, Delete, Insert, Page_Up, Page_Down,
# Up/Down/Left/Right, Tab, Home, End, BackSpace, F1..F12, or a literal char).
function Normalize-KeySpec([string]$spec) {
    $s = ($spec -replace '\s', '').ToLowerInvariant()
    $mods = New-Object System.Collections.Generic.List[string]
    while ($true) {
        if ($s -match '^(ctrl|control)\+(.*)$') { $mods.Add('ctrl'); $s = $Matches[2] }
        elseif ($s -match '^(alt|option)\+(.*)$') { $mods.Add('alt'); $s = $Matches[2] }
        elseif ($s -match '^shift\+(.*)$') { $mods.Add('shift'); $s = $Matches[1] }
        elseif ($s -match '^(cmd|meta|win|super)\+(.*)$') { $mods.Add('cmd'); $s = $Matches[2] }
        else { break }
    }
    $key = switch ($s) {
        'return'    { 'Return' }
        'enter'     { 'Return' }
        'esc'       { 'Escape' }
        'space'     { 'space' }
        'del'       { 'Delete' }
        'ins'       { 'Insert' }
        'pgup'      { 'Page_Up' }
        'pgdn'      { 'Page_Down' }
        'up'        { 'Up' }
        'down'      { 'Down' }
        'left'      { 'Left' }
        'right'     { 'Right' }
        'tab'       { 'Tab' }
        'home'      { 'Home' }
        'end'       { 'End' }
        'backspace' { 'BackSpace' }
        default {
            if ($s -match '^f([1-9]|1[0-2])$') { 'F' + $Matches[1] } else { $s }
        }
    }
    if ([string]::IsNullOrEmpty($key)) { Fail 2 'empty key spec' }
    return [pscustomobject]@{ Mods = @($mods); Key = $key }
}

# SendKeys metacharacters in literal text: + ^ % ~ ( ) { } [ ]
function Escape-SendKeys([string]$text) {
    return [regex]::Replace($text, '([+^%~(){}\[\]])', '{$1}')
}

# Key spec -> SendKeys string (single app focus; no cmd/win modifier).
function Get-SendKeysString($nk) {
    if ($nk.Mods -contains 'cmd') {
        Fail 4 "Windows 'cmd/win' modifier needs SendInput, unsupported by SendKeys path"
    }
    $sk = ''
    foreach ($m in $nk.Mods) {
        switch ($m) {
            'ctrl'  { $sk += '^' }
            'alt'   { $sk += '%' }
            'shift' { $sk += '+' }
        }
    }
    switch ($nk.Key) {
        'Return'    { $sk += '{ENTER}' }
        'Tab'       { $sk += '{TAB}' }
        'Escape'    { $sk += '{ESC}' }
        'space'     { $sk += ' ' }
        'Delete'    { $sk += '{DEL}' }
        'Insert'    { $sk += '{INS}' }
        'Up'        { $sk += '{UP}' }
        'Down'      { $sk += '{DOWN}' }
        'Left'      { $sk += '{LEFT}' }
        'Right'     { $sk += '{RIGHT}' }
        'Home'      { $sk += '{HOME}' }
        'End'       { $sk += '{END}' }
        'Page_Up'   { $sk += '{PGUP}' }
        'Page_Down' { $sk += '{PGDN}' }
        'BackSpace' { $sk += '{BS}' }
        default {
            if ($nk.Key -match '^F([1-9]|1[0-2])$') { $sk += '{' + $nk.Key + '}' }
            else { $sk += $nk.Key }
        }
    }
    return $sk
}

# --- verbs ------------------------------------------------------------------

if ($args.Count -lt 1) { Write-Usage; exit 2 }
$verb = [string]$args[0]
$rest = @()
if ($args.Count -gt 1) { $rest = @($args[1..($args.Count - 1)]) }

# The dispatch runs under one catch: an unexpected failure inside an action
# (a denied SendInput, a broken assembly) reports as exit 1 — the driver's
# "action error" — while usage/argument refusals exit 2/4 through Fail, whose
# `exit` terminates the process without being caught here.
try {
    switch ($verb) {
        'move' {
            if ($rest.Count -ne 1) { Write-Usage; exit 2 }
            $p = Parse-XY ([string]$rest[0])
            Move-Cursor $p.X $p.Y
        }
        'click' {
            if ($rest.Count -ne 1) { Write-Usage; exit 2 }
            $c = Parse-Click ([string]$rest[0])
            Invoke-Click $c.X $c.Y $c.Button $c.Times
        }
        'dblclick' {
            if ($rest.Count -ne 1) { Write-Usage; exit 2 }
            $c = Parse-Click ([string]$rest[0])
            Invoke-Click $c.X $c.Y $c.Button 2
        }
        'type' {
            if ($rest.Count -lt 1) { Write-Usage; exit 2 }
            Send-Keys (Escape-SendKeys ($rest -join ' '))
        }
        'key' {
            if ($rest.Count -ne 1) { Write-Usage; exit 2 }
            $nk = Normalize-KeySpec ([string]$rest[0])
            Send-Keys (Get-SendKeysString $nk)
        }
        'scroll' {
            if ($rest.Count -ne 1) { Write-Usage; exit 2 }
            $parts = ([string]$rest[0]).Split(',')
            $dir = $parts[0]
            $n = 3
            if ($parts.Count -gt 2) { Fail 2 "bad scroll count '$($rest[0])'" }
            if ($parts.Count -ge 2) {
                if (-not [int]::TryParse($parts[1], [ref]$n) -or $n -lt 0) { Fail 2 "bad scroll count '$($parts[1])'" }
            }
            if (@('up', 'down', 'left', 'right') -notcontains $dir) { Fail 2 "bad scroll dir '$dir'" }
            if ($dir -eq 'left' -or $dir -eq 'right') {
                Fail 4 'horizontal scroll unsupported on Windows backend'
            }
            Ensure-MouseApi
            $delta = 120 * $n
            if ($dir -eq 'down') { $delta = -120 * $n }
            [Panda.Mouse]::mouse_event(0x0800, 0, 0, $delta, [System.IntPtr]::Zero)
        }
        'pos' {
            Ensure-WinForms
            $p = [System.Windows.Forms.Cursor]::Position
            [Console]::Out.WriteLine(("{0},{1}" -f $p.X, $p.Y))
        }
        '-h' { Write-Usage; exit 0 }
        '--help' { Write-Usage; exit 0 }
        'help' { Write-Usage; exit 0 }
        default {
            Write-Usage
            Fail 2 "unknown verb '$verb'"
        }
    }
}
catch {
    Fail 1 "action failed: $($_.Exception.Message)"
}
