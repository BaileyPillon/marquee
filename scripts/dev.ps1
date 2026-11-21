<#
.SYNOPSIS
    Runs the Marquee server for development: config in .dev\config, listening on
    127.0.0.1:6464. Extra arguments are passed through to marquee.

.EXAMPLE
    .\scripts\dev.ps1
    .\scripts\dev.ps1 -addr 127.0.0.1:7000
#>
$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'env.ps1')
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$v = (Get-Content -LiteralPath (Join-Path $root 'VERSION') -Raw).Trim()

# The binary embeds web\dist, so it has to exist for the code to compile.
$index = Join-Path $root 'web\dist\index.html'
if (-not (Test-Path -LiteralPath $index)) {
    Write-Warning 'web\dist is missing; writing a placeholder so the server compiles. Run "npm run build" in web\ for the real UI.'
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $index) | Out-Null
    Set-Content -LiteralPath $index -Value '<!doctype html><title>Marquee</title><p>UI not built yet.</p>'
}

New-Item -ItemType Directory -Force -Path (Join-Path $root '.dev\config') | Out-Null

go run -ldflags "-X main.version=$v" ./cmd/marquee -config .dev\config -addr 127.0.0.1:6464 @args
exit $LASTEXITCODE
