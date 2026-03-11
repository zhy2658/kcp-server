$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -ldflags "-s -w" -o kcp-server-linux main.go
Write-Host "Build complete: kcp-server-linux"
