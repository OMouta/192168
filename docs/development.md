# Development

Requires Go 1.26+ and the .NET 10 SDK. The client only builds on Windows.

## Running the whole thing

The three pieces normally live on different machines. This builds them, starts a
local server, points the daemon at it, and opens the window:

```powershell
.\scripts\dev.ps1
```

The server and the daemon each get a console showing their log. Add `-Reset` to
clear the local database and this device's identity, which is the difference
between testing a returning user and a new one. `-Stop` kills everything.

Logs land in `%LOCALAPPDATA%\192168-dev\logs`. If the client vanishes without a
window or a message, read `%ProgramData%\192168\logs\client.log`. An unhandled
exception in WinUI closes the app silently, so that file is usually the only
evidence.

## Building pieces on their own

```sh
go test ./...
go build ./daemon/cmd/192168-service
go build ./server/cmd/192168-server

dotnet build client/windows/192168.slnx -p:Platform=x64
```

Plain HTTP works against localhost and nowhere else. Both the daemon and the
server refuse anything else that is not HTTPS.

## Where things live

`protocol` holds everything that crosses a process or machine boundary, so a
message shape changes in one place instead of three. `transport` is the binary
peer wire format, `ipc` is the local client to daemon protocol, `api` is the
control plane, `auth` is passwords and device signatures, and `session` is the
Noise handshake.

`daemon` runs on a player's machine. `server` is the coordination server.
`client/windows` is the WinUI 3 app. `deploy` has the Docker and Railway setups.

## Naming

A name cannot start with a digit in an environment variable, a C# namespace, or
a Go identifier. Those read `Net192168` and `NET192168_`. Everything a user sees
is `192168`: the app, the well-known path, the virtual adapter.

The client assembly is `Net192168.Client` for the same reason. The XAML and WinRT
source generators emit broken code from an assembly name starting with a digit,
and a `TargetName` override fails the same way. The product name comes from the
window title, the shortcut, and the installer instead.
