<p align="center">
  <img src="assets/github_banner.png" alt="192168.lol" width="400">
  <h2 align="center">Fake LANs for real friends.</h2>
</p>

192168 is a Windows app that puts you and your friends on the same LAN from
different networks, so you can play LAN games together.

Create a private group and give your friends its name and password. Joining gets
you a virtual LAN IP like `10.69.0.2`, the same one every time, and any game that
lets you type in a host address works from there.

Traffic goes straight between players, encrypted. The server hands out the
addresses and says who is online. Game packets never reach it.

## Download

The installer is on the [releases page](https://github.com/OMouta/192168/releases).

It is not signed, so Windows may warn before running it: choose More info, then
Run anyway.

## What you need

Windows 10 or newer, 64-bit. Installing asks for administrator rights once, to
set up the virtual network adapter. Nothing after that does.

A game that can connect to a host by IP. Games that find servers by scanning the
local network also work: your group is treated as one. While that is on, and it
is by default, discovery goes to your group instead of the room you are in, so
nearby speakers and printers may not be found until you disconnect. There is a
switch for it in Settings.

## Groups

A group is a private LAN that sticks around between sessions. You join with its
name and password once. After that the app remembers you and connecting is one
click.

You can belong to any number of groups, and one of them can be connected at a
time. While you are connected you see everyone else in the group and the address
each of them has, online or not.

## Servers

The app talks to `https://api.192168.lol`. To use another one, type its URL into
Settings. To run your own, see [docs/self-hosting.md](docs/self-hosting.md).

## Hacking on it

[docs/development.md](docs/development.md) covers building and running the
pieces. [docs/architecture.md](docs/architecture.md) covers how the networking
works.

## License

MIT. See [LICENSE](LICENSE).
