# Marquee dev environment (Go + Node), process-scoped only.
# Usage:  . D:\Projects\Marquee\scripts\env.ps1
# Does not touch the system/user PATH or the registry; it only changes the
# current PowerShell process. Everything lives on D:.

# --- Go (portable install, no MSI) ---------------------------------------
$env:GOROOT       = 'D:\Tools\Go'
$env:GOPATH       = 'D:\Tools\gopath'
$env:GOMODCACHE   = 'D:\Tools\gopath\pkg\mod'
$env:GOCACHE      = 'D:\Tools\gocache'
$env:GOTELEMETRY  = 'off'
$env:GOTOOLCHAIN  = 'local'
$env:CGO_ENABLED  = '0'

# --- npm ------------------------------------------------------------------
$env:npm_config_cache = 'D:\Tools\npm-cache'

# --- node / npm resolution ------------------------------------------------
# Prefer the real Node.js install (it holds node.exe next to npm). node.exe may
# otherwise only exist inside a WindowsApps Codex package, which is not on PATH
# in a fresh shell and whose folder name changes with each package version.
$marqueeNodeDir = $null
foreach ($c in @(
        (Join-Path $env:ProgramFiles 'nodejs\node.exe'),
        (Join-Path ${env:ProgramFiles(x86)} 'nodejs\node.exe'),
        (Join-Path $env:LOCALAPPDATA 'Programs\nodejs\node.exe'))) {
    if ($c -and (Test-Path -LiteralPath $c)) { $marqueeNodeDir = Split-Path -Parent $c; break }
}
if (-not $marqueeNodeDir) {
    $cmd = Get-Command node -ErrorAction SilentlyContinue
    if ($cmd) { $marqueeNodeDir = Split-Path -Parent $cmd.Source }
}
if (-not $marqueeNodeDir) {
    $hit = Get-ChildItem 'C:\Program Files\WindowsApps\OpenAI.CodexPrimaryRuntime*\dependencies\node\bin\node.exe' -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending | Select-Object -First 1
    if ($hit) { $marqueeNodeDir = Split-Path -Parent $hit.FullName }
}
if (-not $marqueeNodeDir) { Write-Warning 'env.ps1: no node.exe found; node/npm will not resolve.' }

# --- PATH (current process only). Prepend in reverse so the final order is:
#     Go\bin ; gopath\bin ; <node dir> ; <existing PATH>. Idempotent. -------
foreach ($d in @($marqueeNodeDir, 'D:\Tools\gopath\bin', 'D:\Tools\Go\bin')) {
    if (-not $d) { continue }
    $rest = ($env:Path -split ';') | Where-Object { $_ -and ($_.TrimEnd('\') -ine $d.TrimEnd('\')) }
    $env:Path = (@($d) + $rest) -join ';'
}

Remove-Variable marqueeNodeDir, c, cmd, hit, d, rest -ErrorAction SilentlyContinue
