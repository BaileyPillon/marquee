<#
.SYNOPSIS
    Builds Marquee: the web UI, brotli precompression, the Windows exe, the
    Linux binary, and the Linux release archive. Exits non-zero on any failure.

.PARAMETER SkipUI
    Skip "npm ci" / "npm run build" and embed whatever is already in web\dist.
    Useful for backend-only work.

.EXAMPLE
    .\scripts\build.ps1
#>
[CmdletBinding()]
param(
    [switch]$SkipUI
)

$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'env.ps1')
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Invoke-Step {
    # Runs a native command and fails the build if it exits non-zero.
    param([string]$Label, [scriptblock]$Command)
    Write-Host "==> $Label" -ForegroundColor Cyan
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "$Label failed (exit code $LASTEXITCODE)" }
}

function Format-Mib([long]$Bytes) { '{0,8:N2} MiB' -f ($Bytes / 1MB) }

try {
    $v = (Get-Content -LiteralPath (Join-Path $root 'VERSION') -Raw).Trim()
    if (-not $v) { throw 'VERSION is empty' }
    Write-Host "Marquee $v" -ForegroundColor Green

    # --- web UI -------------------------------------------------------------
    if ($SkipUI) {
        Write-Host '==> UI build skipped (-SkipUI); embedding the existing web\dist' -ForegroundColor Yellow
    } else {
        Push-Location (Join-Path $root 'web')
        try {
            if (-not (Test-Path -LiteralPath 'node_modules')) {
                Invoke-Step 'npm ci' { npm ci }
            }
            Invoke-Step 'npm run build' { npm run build }
        } finally {
            Pop-Location
        }
    }
    if (-not (Test-Path -LiteralPath (Join-Path $root 'web\dist\index.html'))) {
        throw 'web\dist\index.html is missing: build the UI first (or put a placeholder there)'
    }

    # --- precompress --------------------------------------------------------
    Invoke-Step 'precompress web\dist' { go run ./tools/precompress web/dist }

    # --- binaries -----------------------------------------------------------
    $bin = Join-Path $root 'bin'
    New-Item -ItemType Directory -Force -Path $bin | Out-Null
    $ldflags = "-s -w -X main.version=$v"

    $exe   = Join-Path $bin 'marquee.exe'
    $linux = Join-Path $bin 'marquee-linux-amd64'
    $tar   = Join-Path $bin "marquee_${v}_linux_amd64.tar.gz"

    Invoke-Step 'go build (windows/amd64)' {
        go build -trimpath -ldflags $ldflags -o $exe ./cmd/marquee
    }

    $savedOS, $savedArch = $env:GOOS, $env:GOARCH
    try {
        $env:GOOS = 'linux'
        $env:GOARCH = 'amd64'
        Invoke-Step 'go build (linux/amd64)' {
            go build -trimpath -ldflags $ldflags -o $linux ./cmd/marquee
        }
    } finally {
        if ($null -eq $savedOS)   { Remove-Item Env:GOOS   -ErrorAction SilentlyContinue } else { $env:GOOS = $savedOS }
        if ($null -eq $savedArch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $savedArch }
    }

    # --- release archive (Windows built-in tar.exe) -------------------------
    $stage = Join-Path $bin '.stage'
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    try {
        Copy-Item -LiteralPath $linux -Destination (Join-Path $stage 'marquee')
        Copy-Item -LiteralPath (Join-Path $root 'LICENSE')   -Destination $stage
        Copy-Item -LiteralPath (Join-Path $root 'README.md') -Destination $stage
        if (Test-Path -LiteralPath $tar) { Remove-Item -LiteralPath $tar -Force }
        $tarExe = Join-Path $env:SystemRoot 'System32\tar.exe'
        if (-not (Test-Path -LiteralPath $tarExe)) { throw "tar.exe not found at $tarExe" }
        # Files on NTFS carry no Unix mode, so a plain "tar -c" would store the
        # binary as 0666 (not executable on Linux). bsdtar can read an mtree
        # manifest as its input, which lets us state the modes explicitly.
        $mtree = "#mtree`n/set type=file uid=0 gid=0`n" +
                 "marquee mode=0755 contents=marquee`n" +
                 "LICENSE mode=0644 contents=LICENSE`n" +
                 "README.md mode=0644 contents=README.md`n"
        [System.IO.File]::WriteAllText((Join-Path $stage 'manifest.mtree'), $mtree)
        Push-Location $stage   # "contents=" paths in the manifest are relative to here
        try {
            Invoke-Step 'tar.gz (linux/amd64)' { & $tarExe -czf $tar '@manifest.mtree' }
        } finally {
            Pop-Location
        }
    } finally {
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    }

    # --- size table ---------------------------------------------------------
    $rows = @(
        [pscustomobject]@{ Artifact = 'bin\marquee.exe (windows)';       Bytes = (Get-Item -LiteralPath $exe).Length;   Target = 25MB },
        [pscustomobject]@{ Artifact = 'bin\marquee-linux-amd64';         Bytes = (Get-Item -LiteralPath $linux).Length; Target = $null },
        [pscustomobject]@{ Artifact = "bin\marquee_${v}_linux_amd64.tar.gz"; Bytes = (Get-Item -LiteralPath $tar).Length;   Target = 17MB }
    )
    Write-Host ''
    Write-Host 'Sizes' -ForegroundColor Green
    Write-Host ('{0,-46} {1,12} {2,14}  {3}' -f 'artifact', 'bytes', 'size', 'target')
    foreach ($r in $rows) {
        $t = ''
        if ($r.Target) { $t = if ($r.Bytes -le $r.Target) { 'ok (<= {0} MB)' -f ($r.Target / 1MB) } else { 'OVER {0} MB' -f ($r.Target / 1MB) } }
        Write-Host ('{0,-46} {1,12:N0} {2,14}  {3}' -f $r.Artifact, $r.Bytes, (Format-Mib $r.Bytes), $t)
    }
    Write-Host ''
    Write-Host 'Build OK' -ForegroundColor Green
    exit 0
} catch {
    Write-Host ''
    Write-Host "Build FAILED: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
