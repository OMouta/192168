# 192168

192168 puts you and your friends on the same LAN from different networks, so you
can play LAN games together.

Pick a nickname, create a private group, share the name and password with your
friends. Everyone who connects gets a virtual LAN IP like `10.69.0.2`. Games
that let you type in a host address work from there.

Your traffic goes straight to the other players, encrypted. The server only
introduces you to each other and then stays out of the way, so it never sees a
game packet and never adds a hop to your latency.

Status: early development. There is nothing to download yet.

## What you need

Windows. The app installs a virtual network adapter, so it needs administrator
rights the first time it connects.

Games have to support connecting to a host by IP address. Anything that only
finds servers by scanning the local network will not see your friends yet.

## Groups

A group is a private LAN that sticks around. Make one for a game, or one for the
people you play with, whatever fits.

Joining takes the group name and its password, once. After that the app
remembers you, and connecting is one click. You can belong to as many groups as
you like, but only one can be connected at a time.

Everyone in a group sees who else is online, their nickname, and their virtual
IP.

## Servers

The app talks to `https://api.192168.lol` by default. To use a different one,
type its URL into Settings. Nothing gets rebuilt, and a friend running their own
server is as usable as the default one.

To run your own, see [docs/self-hosting.md](docs/self-hosting.md).

## Hacking on it

[docs/development.md](docs/development.md) covers building and running the
pieces. [docs/architecture.md](docs/architecture.md) covers how the networking
works.

## License

MIT. See [LICENSE](LICENSE).
