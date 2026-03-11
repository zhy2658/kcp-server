@echo off
chcp 65001 >nul
setlocal

:: 一键启动多个 bot2 实例
:: 默认启动 5 个，可通过参数修改: start-bot2.bat 3

set BOT_COUNT=2
if not "%~1"=="" set BOT_COUNT=%~1

set ROOM=lobby
set ADDR=127.0.0.1:3250

cd /d "%~dp0"

echo 正在启动 %BOT_COUNT% 个 bot2 实例...
echo.

for /L %%i in (1,1,%BOT_COUNT%) do (
    start "Bot%%i" cmd /k "go run cmd/bot2/bot2.go -name Bot%%i -room %ROOM% -addr %ADDR%"
    timeout /t 1 /nobreak >nul
)

echo 已启动 %BOT_COUNT% 个 bot2 窗口。
pause
