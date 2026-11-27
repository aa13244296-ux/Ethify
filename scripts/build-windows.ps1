$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
$Version = "1.1.1"
$OutputDir = Join-Path $RepoRoot "dist\Ethify-$Version-windows-x64"
$OutputExe = Join-Path $OutputDir "Ethify.exe"
$ResourceOutput = Join-Path $RepoRoot "cmd\ethify-client\resource.syso"
$Icon = Join-Path $RepoRoot "assets\icon.ico"
$Manifest = Join-Path $RepoRoot "assets\Ethifi.manifest"
$Screenshot = Join-Path $RepoRoot "docs\screenshots\ethifi-dashboard.png"
$Rsrc = Join-Path $env:USERPROFILE "go\bin\rsrc.exe"

if (-not (Test-Path $Rsrc)) {
    throw "Windows resource compiler not found. Run: go install github.com/akavel/rsrc@v0.10.2"
}
if (-not (Test-Path $Icon)) {
    throw "Application icon not found: $Icon"
}
if (-not (Test-Path $Screenshot)) {
    throw "Dashboard screenshot not found: $Screenshot"
}

& $Rsrc "-arch" "amd64" "-ico" $Icon "-manifest" $Manifest "-o" $ResourceOutput
if ($LASTEXITCODE -ne 0) {
    throw "Failed to compile Windows resources."
}

Push-Location $RepoRoot
try {
    New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    go build -trimpath "-ldflags=-s -w -H=windowsgui -X main.version=$Version" -o $OutputExe ./cmd/ethify-client
    if ($LASTEXITCODE -ne 0) {
        throw "Ethify build failed."
    }

    Copy-Item (Join-Path $RepoRoot "README.md") (Join-Path $OutputDir "README.txt") -Force
    Copy-Item (Join-Path $RepoRoot "LICENSE") $OutputDir -Force
    Copy-Item $Icon $OutputDir -Force
    $ExtensionOutput = Join-Path $OutputDir "browser-extension"
    if (Test-Path $ExtensionOutput) {
        Remove-Item $ExtensionOutput -Recurse -Force
    }
    New-Item -ItemType Directory -Path $ExtensionOutput -Force | Out-Null
    Copy-Item (Join-Path $RepoRoot "browser-extension\*") $ExtensionOutput -Recurse -Force
    $ScreenshotDir = Join-Path $OutputDir "docs\screenshots"
    New-Item -ItemType Directory -Path $ScreenshotDir -Force | Out-Null
    Copy-Item $Screenshot $ScreenshotDir -Force

    $Zip = Join-Path $RepoRoot "dist\Ethify-$Version-windows-x64.zip"
    if (Test-Path $Zip) {
        Remove-Item $Zip -Force
    }
    Compress-Archive -Path $OutputDir -DestinationPath $Zip -Force
}
finally {
    Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
    Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}

$Hash = (Get-FileHash $OutputExe -Algorithm SHA256).Hash
Write-Host "Built: $OutputExe"
Write-Host "Package: $Zip"
Write-Host "SHA256: $Hash"
