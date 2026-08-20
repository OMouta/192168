<p align="center">
  <img src="assets/github_banner.png" alt="192168.lol" width="400">
  <h1 align="center">Fake LANs for real friends.</h1>
</p>

192168 is a Windows app that puts you and your friends on the same LAN from
different networks, so you can play LAN games together.

Create a private group and give your friends its name and password. Everyone who
connects gets a virtual LAN IP like `10.69.0.2`, and any game that lets you type
in a host address works from there.

Traffic goes straight between players, encrypted. The server hands out the
addresses and says who is online. Game packets never reach it.

## What you need

Windows. Connecting installs a virtual network adapter, so the app asks for
administrator rights.

A game that can connect to a host by IP. Games that only find servers by
scanning the local network do not see anyone in your group.

## Groups

A group is a private LAN that sticks around between sessions. You join with its
name and password once. After that the app remembers you and connecting is one
click.

You can belong to any number of groups, and one of them can be connected at a
time. While you are connected you see everyone else who is online.

## Servers

The app talks to `https://api.192168.lol`. To use another one, type its URL into
Settings. To run your own, see [docs/self-hosting.md](docs/self-hosting.md).

## Hacking on it

[docs/development.md](docs/development.md) covers building and running the
pieces. [docs/architecture.md](docs/architecture.md) covers how the networking
works.

## License

MIT. See [LICENSE](LICENSE).
