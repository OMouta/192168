# Development

Requires Go 1.26+ and the .NET 10 SDK. The client only builds on Windows.

```sh
go test ./...
go build ./daemon/cmd/192168-daemon
go build ./server/cmd/192168-server

dotnet build client/windows/192168.slnx -p:Platform=x64
```

Run a server locally:

```sh
NET192168_PUBLIC_URL=http://localhost:8080 go run ./server/cmd/192168-server
curl http://localhost:8080/.well-known/192168
```

Plain HTTP only works against localhost. Everything else has to be HTTPS, in the
daemon and in the server.

## Where things live

`protocol` holds everything that crosses a process or machine boundary, so a
message shape changes in one place instead of three. `transport` is the binary
peer-to-peer wire format, `ipc` is the local client to daemon protocol, and
`api` is the control plane.

`daemon` is the Go networking process that runs on a player's machine. `server`
is the coordination server. `client/windows` is the WinUI 3 app. `deploy/docker`
builds and runs the server.

## Naming

A name cannot start with a digit in an environment variable, a C# namespace, or
a Go identifier. Those read `Net192168` and `NET192168_`. Everything a user sees
is `192168`, including the app, the well-known path, and the virtual adapter.

The client assembly is `Net192168.Client` for the same reason. The XAML and
WinRT source generators produce broken code from an assembly name starting with
a digit, and a `TargetName` override hits the same failure, so the product name
comes from the window title, the shortcut, and the installer.
