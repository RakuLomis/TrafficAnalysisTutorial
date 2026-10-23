[CmdletBinding()]
param(
    [string]$Version = "0.1.0-dev"
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$packageName = "traffic-classroom-$Version"
$out = Join-Path $root "dist\$packageName"
$exe = Join-Path $root "bin\traffic-classroom.exe"

& (Join-Path $PSScriptRoot "build.ps1") -Version $Version
if ($LASTEXITCODE -ne 0) {
    throw "build failed"
}

if (Test-Path -LiteralPath $out) {
    Remove-Item -LiteralPath $out -Recurse -Force
}
New-Item -ItemType Directory -Force $out | Out-Null

Copy-Item -LiteralPath $exe -Destination (Join-Path $out "traffic-classroom.exe") -Force
Copy-Item -LiteralPath (Join-Path $root "README.md") -Destination $out -Force
Copy-Item -LiteralPath (Join-Path $root "THIRD_PARTY_NOTICES.md") -Destination $out -Force
Copy-Item -LiteralPath (Join-Path $root "docs\classroom-card.md") -Destination $out -Force
Copy-Item -LiteralPath (Join-Path $root "docs\licenses") -Destination $out -Recurse -Force

$commit = ""
$gitOutput = git rev-parse --short HEAD 2>$null
if ($LASTEXITCODE -eq 0 -and $gitOutput) {
    $commit = $gitOutput.Trim()
}
$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$goVersion = (go version)
$nodeVersion = (& node --version)

$versionText = @"
traffic-classroom $Version
protocol: 1
commit: $commit
build: $buildTime
go: $goVersion
node: $nodeVersion (build only)
websocket: github.com/coder/websocket v1.8.13
vite: 7.3.7 (build only)
typescript: 5.9.3 (build only)
tweetnacl: 1.0.3
qrcode: 1.5.4
"@
Set-Content -LiteralPath (Join-Path $out "VERSION.txt") -Value $versionText -Encoding utf8

$hash = (Get-FileHash -LiteralPath (Join-Path $out "traffic-classroom.exe") -Algorithm SHA256).Hash
Set-Content -LiteralPath (Join-Path $out "SHA256SUMS.txt") -Value "$hash  traffic-classroom.exe" -Encoding ascii

Write-Host ""
Write-Host "发布目录：$out"
Write-Host "SHA-256：$hash"
