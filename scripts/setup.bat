@echo off
chcp 65001 >nul 2>&1
title Vertex AI Proxy — 交互式部署向导

echo.
echo ╔══════════════════════════════════════════════════╗
echo ║   Vertex AI Proxy — 交互式部署向导              ║
echo ╚══════════════════════════════════════════════════╝
echo.

set "SCRIPT_DIR=%~dp0"
set "BINARY=%SCRIPT_DIR%vertex-proxy.exe"

if not exist "%BINARY%" (
    echo [X] 未找到 vertex-proxy.exe
    echo     请确保 vertex-proxy.exe 与本脚本在同一目录。
    pause
    exit /b 1
)
echo [OK] 找到主程序：vertex-proxy.exe
echo.

:: ---- 基本配置 ----
echo ── 基本配置 ──
set "PORT=2156"
set /p "PORT=监听端口 [2156]: "
if "%PORT%"=="" set "PORT=2156"

:: 生成随机 API Key
set "API_KEY=sk-%RANDOM%%RANDOM%%RANDOM%"
set /p "API_KEY=API 密钥（sk- 开头，留空自动生成）[%API_KEY%]: "
if "%API_KEY%"=="" set "API_KEY=sk-%RANDOM%%RANDOM%%RANDOM%"

:: 生成管理员密码
set "ADMIN_PASS=%RANDOM%%RANDOM%%RANDOM%"
echo   管理员密码：%%ADMIN_PASS%%
echo.

:: ---- 网络配置 ----
echo ── 网络配置 ──
echo   如果你的网络能直接访问 Google，输入 n。
echo   如果在国内需要代理，输入 y。
set "USE_PROXY=n"
set /p "USE_PROXY=是否需要配置代理？(y/n) [n]: "
if "%USE_PROXY%"=="" set "USE_PROXY=n"

set "PROXY_URL="
if /i "%USE_PROXY%"=="y" (
    set /p "PROXY_URL=代理地址（如 socks5://127.0.0.1:1080）: "
)

echo.
echo ── 高级选项 ──
set "MAX_RETRIES=2"
set /p "MAX_RETRIES=请求失败重试次数 [2]: "
if "%MAX_RETRIES%"=="" set "MAX_RETRIES=2"


:: ---- 创建配置 ----
echo.
echo ── 正在创建配置 ──

if not exist "%SCRIPT_DIR%config" mkdir "%SCRIPT_DIR%config"

:: config.json
(
echo {
echo   "port_api": %PORT%,
echo   "max_retries": %MAX_RETRIES%,
echo   "admin_password": "%ADMIN_PASS%",
echo   "proxy_url": "%PROXY_URL%"
echo }
) > "%SCRIPT_DIR%config\config.json"
echo [OK] config\config.json

:: api_keys.txt
(
echo # 格式: 名称:密钥:备注
echo mykey:%API_KEY%:部署脚本自动生成
) > "%SCRIPT_DIR%config\api_keys.txt"
echo [OK] config\api_keys.txt

:: models.json（如果不存在）
if not exist "%SCRIPT_DIR%config\models.json" (
    if exist "%SCRIPT_DIR%config\models.json" (
        copy "%SCRIPT_DIR%config\models.json" "%SCRIPT_DIR%config\models.json" >nul
    ) else if exist "%SCRIPT_DIR%..\config\models.json" (
        copy "%SCRIPT_DIR%..\config\models.json" "%SCRIPT_DIR%config\models.json" >nul
    )
    echo [OK] config\models.json
) else (
    echo [=] config\models.json 已存在，跳过
)

if not exist "%SCRIPT_DIR%config\state" mkdir "%SCRIPT_DIR%config\state"
if not exist "%SCRIPT_DIR%config\state\.rules_agreed" (
    echo.
    echo ── 使用规则 ──
    echo   ⚠️ 重要声明：本软件完全免费（PolyForm Noncommercial License 1.0.0）。
    echo   仅供个人非商业学习与研究使用，严禁转售、收费代搭或用于商业营利。
    if exist "%SCRIPT_DIR%rules.txt" echo   完整规则文件：%SCRIPT_DIR%rules.txt
    (
    echo 2026-08-17T00:00:00Z	36800adeec862126
    ) > "%SCRIPT_DIR%config\state\.rules_agreed"
    echo [OK] 已记录规则同意凭证
)

:: ---- 开机自启 ----
echo.
set "AUTOSTART=n"
set /p "AUTOSTART=是否设置开机自启？(y/n) [n]: "
if "%AUTOSTART%"=="" set "AUTOSTART=n"

if /i "%AUTOSTART%"=="y" (
    echo 正在创建开机自启快捷方式...
    set "STARTUP=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup"
    :: 创建 VBS 启动脚本（静默启动，不弹窗）
    (
    echo Set WshShell = CreateObject("WScript.Shell"^)
    echo WshShell.CurrentDirectory = "%SCRIPT_DIR%"
    echo WshShell.Run """%BINARY%""", 0, False
    ) > "%STARTUP%\vertex-proxy.vbs"
    echo [OK] 已创建开机自启快捷方式
)

:: ---- 完成 ----
echo.
echo ════════════════════════════════════════════════════
echo   ✓ 部署完成！
echo.
echo   API 地址：http://127.0.0.1:%PORT%/v1
echo   API 密钥：%API_KEY%
echo   管理面板：http://127.0.0.1:%PORT%/admin/
echo   管理密码：%ADMIN_PASS%
echo.
echo   在客户端（Cherry Studio / SillyTavern 等）中：
echo     API Key：%API_KEY%
echo     Base URL：http://你的IP:%PORT%/v1
echo ════════════════════════════════════════════════════
echo.

set "START_NOW=y"
set /p "START_NOW=现在启动服务？(y/n) [y]: "
if "%START_NOW%"=="" set "START_NOW=y"

if /i "%START_NOW%"=="y" (
    echo.
    echo 正在启动...
    cd /d "%SCRIPT_DIR%"
    start "" "%BINARY%"
    echo 服务已在后台启动。
    echo 管理面板：http://127.0.0.1:%PORT%/admin/
)

pause
