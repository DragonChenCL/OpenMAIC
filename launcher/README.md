# Native Windows launcher acceptance preflight

This Go module builds the Windows launcher and has host-runnable tests for the pure child-environment policy. From `launcher/`:

```sh
gofmt -w *.go
go test -race ./...
GOOS=windows GOARCH=amd64 go test -c -o /dev/null .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-H windowsgui -s -w' -o /dev/null .
```

The first command executes platform-independent tests on the current host; the two `GOOS=windows` commands only compile Windows binaries and do not run them. Actual HWND creation, DPAPI, bundled Node runtime, health route, static assets, screenshot, and final archive checks must run in `.github/workflows/windows-launcher-acceptance.yml` on `windows-latest`.
