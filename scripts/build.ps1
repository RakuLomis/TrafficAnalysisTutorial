[CmdletBinding()]
param(
    [string]$Version = "0.1.0-dev"
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$web = Join-Path $root "web"
$webDist = Join-Path $web "dist"
$embedDist = Join-Path $root "internal\web\dist"
$bin = Join-Path $root "bin"
$exe = Join-Path $bin "traffic-classroom.exe"

Push-Location $root
try {
    $env:GOTOOLCHAIN = "local"

    npm --prefix $web ci --prefer-offline --no-audit --no-fund
    if ($LASTEXITCODE -ne 0) {
        throw "npm ci failed"
    }

    npm --prefix $web run build
    if ($LASTEXITCODE -ne 0) {
        throw "frontend build failed"
    }

    New-Item -ItemType Directory -Force $embedDist | Out-Null
    Get-ChildItem -LiteralPath $embedDist -Force |
        Where-Object { $_.Name -ne ".gitkeep" } |
        Remove-Item -Recurse -Force
    Copy-Item -Path (Join-Path $webDist "*") -Destination $embedDist -Recurse -Force
    New-Item -ItemType File -Force (Join-Path $embedDist ".gitkeep") | Out-Null

    New-Item -ItemType Directory -Force $bin | Out-Null

    $commit = ""
    $gitOutput = git rev-parse --short HEAD 2>$null
    if ($LASTEXITCODE -eq 0 -and $gitOutput) {
        $commit = $gitOutput.Trim()
    }
    $buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    $ldflags = "-s -w -X main.version=$Version -X main.buildTime=$buildTime"
    if ($commit) {
        $ldflags += " -X main.commit=$commit"
    }

    go build -trimpath -ldflags $ldflags -o $exe ./cmd/traffic-classroom
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed"
    }

    $hash = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash
    Write-Host ""
    Write-Host "已生成：$exe"
    Write-Host "版本：$Version"
    Write-Host "SHA-256：$hash"
}
finally {
    Pop-Location
}
