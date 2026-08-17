#!/usr/bin/env pwsh
# pre-commit — 提交门禁：go vet + staticcheck + gochecks 告警非零即阻止提交。
# 启用：git config core.hooksPath .githooks
#
# gochecks 为仓库内聚合检查器（scripts/gochecks），覆盖 gopls 编辑器分析器：
#   writestring / unusedparams / simplifycompositelit / modernize (b.Loop)
#
# 全部检查统一在仓库根目录执行，避免在子目录提交时 ./... 只覆盖子树而漏检。

$ErrorActionPreference = "Continue"

$pass = $true

$root = git rev-parse --show-toplevel
if (-not $root) {
    Write-Host "[pre-commit] FAIL: cannot locate repo root" -ForegroundColor Red
    exit 1
}

Push-Location $root
try {
    Write-Host "[pre-commit] running go vet ./... ..."
    & go vet ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[pre-commit] FAIL: go vet reported issues" -ForegroundColor Red
        $pass = $false
    }

    Write-Host "[pre-commit] running staticcheck ./... ..."
    & staticcheck ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[pre-commit] FAIL: staticcheck reported issues" -ForegroundColor Red
        $pass = $false
    }

    Write-Host "[pre-commit] running gochecks (writestring/unusedparams/simplifycompositelit/modernize) ..."
    & go run ./scripts/gochecks
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[pre-commit] FAIL: gochecks reported issues" -ForegroundColor Red
        $pass = $false
    }
}
finally {
    Pop-Location
}

if (-not $pass) {
    Write-Host "[pre-commit] COMMIT BLOCKED: fix the issues above before committing." -ForegroundColor Red
    exit 1
}

Write-Host "[pre-commit] OK: vet + staticcheck + gochecks clean." -ForegroundColor Green
exit 0