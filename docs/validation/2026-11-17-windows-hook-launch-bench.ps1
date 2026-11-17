# Hook launch benchmark behind 2026-11-17-windows-hook-launch.md.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File docs\validation\2026-11-17-windows-hook-launch-bench.ps1 [-Recarto <path to recarto.exe>]
#
# Run from a Recarto checkout on Windows. Needs only git and Windows PowerShell
# 5.1 (built in) or pwsh 7. -Recarto defaults to recarto on PATH.
#
# Times each Claude Code hook through three launches, started the way Claude
# Code 2.1.326 starts them: PowerShell ("shell": "powershell"), Git Bash
# ("shell": "bash") and direct (exec form, "args"). The binary is copied to a
# scratch dir whose path has spaces, so every launch has to handle them. Every
# figure is the median of 10 timed runs after 3 warm-ups; each run gets a fresh
# session id. The scratch dir (store, replies) is left behind; its path is
# printed last.
param([string]$Recarto)

$ErrorActionPreference = 'Stop'

if (-not $Recarto) {
    $cmd = Get-Command recarto -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $cmd) { throw 'recarto not found on PATH: pass -Recarto <path to recarto.exe>' }
    $Recarto = $cmd.Source
}
$Recarto = (Resolve-Path -LiteralPath $Recarto).Path

$repo = git -C $PSScriptRoot rev-parse --show-toplevel
if ($LASTEXITCODE -ne 0) { throw "not inside a git checkout: $PSScriptRoot" }
$repo = [IO.Path]::GetFullPath($repo)

$scratch = Join-Path ([IO.Path]::GetTempPath()) ('recarto launch bench ' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path (Join-Path $scratch 'replies') | Out-Null
$bin = Join-Path $scratch (Split-Path -Leaf $Recarto)
Copy-Item -LiteralPath $Recarto -Destination $bin

# Keep the bench's sessions out of the owner's store and sync server.
$env:RECARTO_CACHE_DIR = Join-Path $scratch 'cache'
$env:RECARTO_CONFIG = Join-Path $scratch 'config.toml'
New-Item -ItemType File -Path $env:RECARTO_CONFIG | Out-Null
foreach ($v in 'RECARTO_REPOS', 'RECARTO_WORKSPACE', 'RECARTO_SYNC_URL', 'RECARTO_SYNC_KEY', 'RECARTO_SYNC_REPO') {
    Remove-Item -LiteralPath "Env:$v" -ErrorAction SilentlyContinue
}

$utf8 = New-Object System.Text.UTF8Encoding $false

# Invoke-Run starts $file with $arguments (one Windows command line) in the
# repo, writes $stdin to it, and returns its time, exit code, stdout and stderr.
function Invoke-Run([string]$file, [string]$arguments, [byte[]]$stdin) {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $file
    $psi.Arguments = $arguments
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.StandardOutputEncoding = $utf8
    $psi.StandardErrorEncoding = $utf8
    $psi.WorkingDirectory = $repo
    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi
    $stdinErr = ''
    $clock = [System.Diagnostics.Stopwatch]::StartNew()
    [void]$p.Start()
    # Both outputs are drained while stdin is written, so a large payload cannot
    # deadlock against a full output pipe.
    $errTask = $p.StandardError.ReadToEndAsync()
    $outTask = $p.StandardOutput.ReadToEndAsync()
    try {
        if ($stdin -and $stdin.Length -gt 0) {
            $p.StandardInput.BaseStream.Write($stdin, 0, $stdin.Length)
        }
        $p.StandardInput.Close()
    } catch [System.IO.IOException] {
        # The child exited without reading its input: the run is reported as
        # failed through its stderr.
        $stdinErr = 'stdin not read: ' + $_.Exception.Message
    }
    $out = $outTask.Result
    $err = $errTask.Result + $stdinErr
    $p.WaitForExit()
    $clock.Stop()
    New-Object PSObject -Property @{ Ms = $clock.Elapsed.TotalMilliseconds; Exit = $p.ExitCode; Out = $out; Err = $err }
}

# Join-Args makes one command line; an argument with a space is double-quoted.
function Join-Args([string[]]$words) {
    ($words | ForEach-Object { if ($_ -match ' ') { '"' + $_ + '"' } else { $_ } }) -join ' '
}

function Invoke-Seed([string[]]$words) {
    $r = Invoke-Run $bin (Join-Args $words) $null
    if ($r.Exit -ne 0) { throw ("recarto " + ($words -join ' ') + " exited " + $r.Exit + ": " + $r.Err) }
}

Invoke-Seed @('index')
Invoke-Seed @('remember', 'bug', '--title', 'bench: decode the hook payload first',
    '--body', 'Bench seed: a bug anchored to handle.go so read/edit/search surface it.',
    '--files', 'internal/agents/handle.go', '--symbols', 'RunHook')
Invoke-Seed @('remember', 'decision', '--title', 'bench: hooks stay exit-0',
    '--body', 'Bench seed: guidance anchored to handle.go.', '--files', 'internal/agents/handle.go')
Invoke-Seed @('remember', 'todo', '--title', 'bench: open todo on handle.go',
    '--body', 'Bench seed: a todo anchored to handle.go.', '--files', 'internal/agents/handle.go')

# PowerShell: Claude Code takes pwsh on PATH, else Windows PowerShell.
$pwshCmd = Get-Command pwsh -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if ($pwshCmd) {
    $powershell = $pwshCmd.Source
} elseif ($env:SystemRoot) {
    $powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
} else {
    throw 'neither pwsh on PATH nor SystemRoot set'
}

# Git Bash: Claude Code's lookup order.
$gitBash = $null
$candidates = @()
if ($env:CLAUDE_CODE_GIT_BASH_PATH -and (@('bash.exe', 'sh.exe', 'bash', 'sh') -contains (Split-Path -Leaf $env:CLAUDE_CODE_GIT_BASH_PATH))) {
    $candidates += $env:CLAUDE_CODE_GIT_BASH_PATH
}
if ($env:ProgramFiles) { $candidates += Join-Path $env:ProgramFiles 'Git\bin\bash.exe' }
if (${env:ProgramFiles(x86)}) { $candidates += Join-Path ${env:ProgramFiles(x86)} 'Git\bin\bash.exe' }
$gitCmd = Get-Command git -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if ($gitCmd) { $candidates += Join-Path (Split-Path -Parent $gitCmd.Source) '..\..\bin\bash.exe' }
foreach ($c in $candidates) {
    if (Test-Path -LiteralPath $c -PathType Leaf) { $gitBash = $c; break }
}

$launches = @('PowerShell', 'Git Bash', 'direct')

# Get-Launch returns the file and command line a launch starts for $words.
function Get-Launch([string]$launch, [string[]]$words) {
    switch ($launch) {
        'PowerShell' {
            $cmdText = "& '" + $bin.Replace("'", "''") + "' " + ($words -join ' ') + "`n" + '; if (-not $?) { if ($LASTEXITCODE -eq 2) { exit 2 } }'
            return @($powershell, ('-NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "' + $cmdText + '"'))
        }
        'Git Bash' {
            $cmdText = "'" + $bin.Replace("'", "'\''") + "' " + ($words -join ' ')
            return @($gitBash, ('-c "' + $cmdText + '"'))
        }
        default { return @($bin, (Join-Args $words)) }
    }
}

function To-Payload($obj) {
    $utf8.GetBytes(($obj | ConvertTo-Json -Compress -Depth 5) + "`n")
}

$file = Join-Path $repo 'internal\agents\handle.go'
$text = [IO.File]::ReadAllText($file)
$outside = Join-Path $scratch 'elsewhere\notes.go'

# Each row: name, hook words, payload builder (session id -> object), and
# whether a session-start opens the session first.
$rows = @(
    @{ Name = 'version'; Words = @('version'); Payload = $null; Prepare = $false },
    @{ Name = 'session-start'; Words = @('hook', 'session-start', '--agent', 'claude'); Prepare = $false
        Payload = { param($s) @{ session_id = $s; source = 'startup' } } },
    @{ Name = 'post-read'; Words = @('hook', 'post-read', '--agent', 'claude'); Prepare = $false
        Payload = { param($s) @{ session_id = $s; tool_name = 'Read'; tool_input = @{ file_path = $file }
                tool_response = @{ type = 'text'; file = @{ filePath = $file; content = $text } } } } },
    @{ Name = 'post-edit'; Words = @('hook', 'post-edit', '--agent', 'claude'); Prepare = $false
        Payload = { param($s) @{ session_id = $s; tool_name = 'Edit'; tool_input = @{ file_path = $file } } } },
    @{ Name = 'post-edit outside the repo'; Words = @('hook', 'post-edit', '--agent', 'claude'); Prepare = $false
        Payload = { param($s) @{ session_id = $s; tool_name = 'Edit'; tool_input = @{ file_path = $outside } } } },
    @{ Name = 'augment'; Words = @('hook', 'augment', '--agent', 'claude'); Prepare = $false
        Payload = { param($s) @{ session_id = $s; tool_name = 'Grep'; tool_input = @{ pattern = 'RunHook' } } } },
    @{ Name = 'shell'; Words = @('hook', 'shell', '--agent', 'claude', '--cmd', 'grep'); Prepare = $false
        Payload = { param($s) @{ session_id = $s; tool_name = 'Bash'; tool_input = @{ command = 'grep -rn RunHook internal' }; cwd = $repo } } },
    @{ Name = 'stop'; Words = @('hook', 'stop', '--agent', 'claude'); Prepare = $true
        Payload = { param($s) @{ session_id = $s; stop_hook_active = $false } } }
)

$warmups = 3
$runs = 10
$results = @{}
$failures = @()
foreach ($row in $rows) {
    $times = @{}
    $replies = @{}
    $failed = @{}
    foreach ($l in $launches) { $times[$l] = New-Object System.Collections.Generic.List[double] }
    for ($round = 0; $round -lt $warmups + $runs; $round++) {
        foreach ($l in $launches) {
            if ($l -eq 'Git Bash' -and -not $gitBash) { continue }
            $session = 'bench-' + [guid]::NewGuid().ToString()
            if ($row.Prepare) {
                $r = Invoke-Run $bin 'hook session-start --agent claude' (To-Payload @{ session_id = $session; source = 'startup' })
                if ($r.Exit -ne 0) { throw ("preparing " + $row.Name + " exited " + $r.Exit + ": " + $r.Err) }
            }
            $stdin = $null
            if ($row.Payload) { $stdin = To-Payload (& $row.Payload $session) }
            $launch = Get-Launch $l $row.Words
            $r = Invoke-Run $launch[0] $launch[1] $stdin
            if ($round -eq 0) {
                $replies[$l] = $r.Out
                [IO.File]::WriteAllText((Join-Path $scratch ('replies\' + $row.Name + '-' + $l + '.txt')), $r.Out, $utf8)
            }
            if ($r.Exit -ne 0 -or $r.Err.Length -gt 0) {
                if (-not $failed[$l]) {
                    $failed[$l] = $true
                    $firstErr = ($r.Err -split "`r?`n")[0]
                    $failures += ('{0} via {1}: exit {2}: {3}' -f $row.Name, $l, $r.Exit, $firstErr)
                }
            }
            if ($round -ge $warmups) { $times[$l].Add($r.Ms) }
        }
    }
    $med = @{}
    foreach ($l in $launches) {
        if ($failed[$l] -or $times[$l].Count -eq 0) { continue }
        $sorted = $times[$l].ToArray()
        [Array]::Sort($sorted)
        $med[$l] = ($sorted[4] + $sorted[5]) / 2
    }
    $differs = @()
    foreach ($l in 'PowerShell', 'Git Bash') {
        if ($replies.ContainsKey($l) -and $replies[$l] -cne $replies['direct']) { $differs += $l }
    }
    $results[$row.Name] = @{ Med = $med; Failed = $failed; Differs = $differs }
}

$version = (Invoke-Run $bin 'version' $null).Out.Trim()
'Recarto:    ' + $version
'PowerShell: ' + $powershell
if ($gitBash) { 'Git Bash:   ' + $gitBash } else { 'Git Bash:   not found' }
'Binary:     ' + $bin
'Median of 10 runs after 3 warm-ups, ms'
''

function Format-Cell($res, [string]$l) {
    if ($l -eq 'Git Bash' -and -not $gitBash) { return '-' }
    if ($res.Failed[$l]) { return 'failed' }
    return $res.Med[$l].ToString('0')
}

$fmt = '{0,-28} {1,10} {2,10} {3,8} {4,22}  {5}'
$fmt -f 'hook', 'PowerShell', 'Git Bash', 'direct', 'direct vs PowerShell', 'reply'
foreach ($row in $rows) {
    $res = $results[$row.Name]
    $saved = '-'
    if ($res.Med.ContainsKey('PowerShell') -and $res.Med.ContainsKey('direct')) {
        $d = $res.Med['PowerShell'] - $res.Med['direct']
        $saved = ('{0} ms ({1}%)' -f $d.ToString('0'), (100 * $d / $res.Med['PowerShell']).ToString('0'))
    }
    $reply = 'same'
    if ($res.Differs.Count -gt 0) { $reply = 'differs: ' + ($res.Differs -join ', ') }
    $fmt -f $row.Name, (Format-Cell $res 'PowerShell'), (Format-Cell $res 'Git Bash'), (Format-Cell $res 'direct'), $saved, $reply
}
if ($failures.Count -gt 0) {
    ''
    'Failed runs:'
    $failures
}
''
'Scratch files (replies, store): ' + $scratch
