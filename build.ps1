param([switch]$SkipTests)

$ErrorActionPreference = 'Stop'
$originalOS = $env:GOOS
$originalArch = $env:GOARCH
$originalCGO = $env:CGO_ENABLED
Push-Location $PSScriptRoot
try {
    if (-not $SkipTests) {
        go test -count=1 ./...
        if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw 'go vet failed.' }
    }
    New-Item -ItemType Directory -Force -Path bin | Out-Null
    $env:CGO_ENABLED = '0'
    foreach ($target in @('windows/amd64', 'windows/arm64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64')) {
        $parts = $target.Split('/')
        $env:GOOS = $parts[0]
        $env:GOARCH = $parts[1]
        $suffix = if ($env:GOOS -eq 'windows') { '.exe' } else { '' }
        $output = Join-Path bin ('resload-' + $env:GOOS + '-' + $env:GOARCH + $suffix)
        Write-Host "Building $target"
        go build -trimpath -ldflags '-s -w' -o $output .
        if ($LASTEXITCODE -ne 0) { throw "Build failed: $target" }
    }
} finally {
    $env:GOOS = $originalOS
    $env:GOARCH = $originalArch
    $env:CGO_ENABLED = $originalCGO
    Pop-Location
}
