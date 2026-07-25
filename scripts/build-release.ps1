$ErrorActionPreference = "Stop"

# 切换到项目根目录
Set-Location -Path $PSScriptRoot\..

$VERSION = "dev"
if ($args.Count -gt 0 -and -not [string]::IsNullOrWhiteSpace($args[0])) {
    $VERSION = $args[0]
}

$OUT = "dist"

$COMMIT = "unknown"
try {
    $gitOutput = git rev-parse --short HEAD 2>$null
    if ($LASTEXITCODE -eq 0 -and $gitOutput) {
        $COMMIT = ($gitOutput -join "").Trim()
    }
} catch {
}

$BUILD_TIME = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$LDFLAGS = "-s -w -X main.version=$VERSION -X main.buildCommit=$COMMIT -X main.buildTime=$BUILD_TIME"

if (Test-Path $OUT) {
    Remove-Item -Recurse -Force $OUT
}
New-Item -ItemType Directory -Force -Path $OUT | Out-Null
function Build-Release {
    param (
        [string]$goos,
        [string]$goarch,
        [string]$bin,
        [string]$pkg,
        [string[]]$files
    )

    $stage = Join-Path $OUT $pkg
    Write-Host "==> 编译 $goos/$goarch"

    $env:CGO_ENABLED = "0"
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    go build -trimpath -tags with_utls -ldflags="$LDFLAGS" -o (Join-Path $stage $bin) ./cmd/vproxy
    Remove-Item Env:\CGO_ENABLED
    Remove-Item Env:\GOOS
    Remove-Item Env:\GOARCH

    $configDir = Join-Path $stage "config"
    New-Item -ItemType Directory -Force -Path $configDir | Out-Null
    
    if (Test-Path "config\config.example.json") { Copy-Item "config\config.example.json" -Destination $configDir }
    if (Test-Path "config\api_keys.example.txt") { Copy-Item "config\api_keys.example.txt" -Destination $configDir }
    if (Test-Path "config\models.json") { Copy-Item "config\models.json" -Destination $configDir }
    if (Test-Path "部署指南.md") { Copy-Item "部署指南.md" -Destination $stage }
    if (Test-Path "cmd\vproxy\rules.txt") { Copy-Item "cmd\vproxy\rules.txt" -Destination $stage }

    foreach ($f in $files) {
        if (Test-Path $f) {
            Copy-Item $f -Destination $stage
        }
    }

    $zipPath = Join-Path $OUT "$pkg.zip"
    
    # 优先用 7z（压缩率更高，且解决长路径问题），没有则用自带的 Compress-Archive
    if (Get-Command 7z -ErrorAction SilentlyContinue) {
        $pwd = Get-Location
        Set-Location $stage
        7z a -tzip -mx=9 "..\$pkg.zip" * > $null
        Set-Location $pwd
        Remove-Item -Recurse -Force $stage
    } else {
        Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $zipPath -Force
        Remove-Item -Recurse -Force $stage
    }

    Write-Host "    -> $zipPath"
}

# Windows
Build-Release -goos windows -goarch amd64 -bin vertex-proxy.exe -pkg vertex-proxy-windows-amd64 -files @("scripts\启动.bat", "scripts\setup.bat")

# Linux
Build-Release -goos linux -goarch amd64 -bin vertex-proxy -pkg vertex-proxy-linux-amd64 -files @("scripts\start.sh", "scripts\vertex-proxy.service", "scripts\setup.sh")
Build-Release -goos linux -goarch 386   -bin vertex-proxy -pkg vertex-proxy-linux-386   -files @("scripts\start.sh", "scripts\vertex-proxy.service", "scripts\setup.sh")
Build-Release -goos linux -goarch arm64 -bin vertex-proxy -pkg vertex-proxy-linux-arm64 -files @("scripts\start.sh", "scripts\vertex-proxy.service", "scripts\setup.sh")
Build-Release -goos linux -goarch arm -bin vertex-proxy -pkg vertex-proxy-linux-arm -files @("scripts\start.sh", "scripts\vertex-proxy.service", "scripts\setup.sh")

# Android
Build-Release -goos android -goarch arm64 -bin vertex-proxy -pkg vertex-proxy-android-arm64 -files @("scripts\start.sh", "scripts\setup.sh")

# macOS
Build-Release -goos darwin -goarch amd64 -bin vertex-proxy -pkg vertex-proxy-darwin-amd64 -files @("scripts\start.sh", "scripts\setup.sh")
Build-Release -goos darwin -goarch arm64 -bin vertex-proxy -pkg vertex-proxy-darwin-arm64 -files @("scripts\start.sh", "scripts\setup.sh")

Write-Host "完成。产物："
Get-ChildItem -Path $OUT | Select-Object Name
