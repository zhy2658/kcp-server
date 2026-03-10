
# Build
Write-Host "Building..."
go build -o server.exe main.go
go build -o bot.exe cmd/bot/main.go

# Start Server
$server = Start-Process -FilePath ".\server.exe" -PassThru -RedirectStandardOutput "server_out.log" -RedirectStandardError "server_err.log"
Write-Host "Server started with PID $($server.Id)"
Start-Sleep -Seconds 3

# Start Observer (Bot1) - This one will create the room if needed
# It will wait for events
$observer = Start-Process -FilePath ".\bot.exe" -ArgumentList "-role", "observer", "-room", "TestAuto", "-name", "Observer" -PassThru -RedirectStandardOutput "bot_obs_out.log" -RedirectStandardError "bot_obs_err.log"
Write-Host "Observer started with PID $($observer.Id)"
Start-Sleep -Seconds 2

# Start Actor (Bot2) - This one will join, move, chat, and leave
$actor = Start-Process -FilePath ".\bot.exe" -ArgumentList "-role", "actor", "-room", "TestAuto", "-name", "Actor" -PassThru -RedirectStandardOutput "bot_act_out.log" -RedirectStandardError "bot_act_err.log"
Write-Host "Actor started with PID $($actor.Id)"

# Wait for Observer to finish (it exits when tests pass or timeout)
$observer.WaitForExit()
$exitCode = $observer.ExitCode

Write-Host "Observer finished with ExitCode: $exitCode"

# Cleanup
Stop-Process -Id $actor.Id -ErrorAction SilentlyContinue
Stop-Process -Id $server.Id -ErrorAction SilentlyContinue

# Result Analysis
if ($exitCode -eq 0) {
    Write-Host "SUCCESS: All verification steps passed!" -ForegroundColor Green
} else {
    Write-Host "FAILURE: Verification failed!" -ForegroundColor Red
    Write-Host "--- Observer Log ---"
    Get-Content "bot_obs_err.log"
}
