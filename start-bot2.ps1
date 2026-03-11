# 一键启动多个 bot2 实例
# 用法: .\start-bot2.ps1          # 默认 5 个
#       .\start-bot2.ps1 3        # 启动 3 个

param(
    [int]$Count = 2,
    [string]$Room = "lobby",
    [string]$Addr = "127.0.0.1:3250"
)

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $scriptDir

Write-Host "正在启动 $Count 个 bot2 实例..." -ForegroundColor Cyan

1..$Count | ForEach-Object {
    $name = "Bot$_"
    Start-Process powershell -ArgumentList "-NoExit", "-Command", "cd '$scriptDir'; go run cmd/bot2/bot2.go -name $name -room $Room -addr $Addr"
    Start-Sleep -Milliseconds 500
}

Write-Host "已启动 $Count 个 bot2 窗口。" -ForegroundColor Green
