package core

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"

	"github.com/OMouta/192168/daemon/mesh"
	"github.com/OMouta/192168/daemon/tun"
	"github.com/OMouta/192168/protocol"
	"github.com/OMouta/192168/protocol/ipc"
	"github.com/OMouta/192168/protocol/transport"
)

// startAdapter brings up the virtual adapter and moves packets between it and
// the peer links.
//
// Without it the group is joined, addresses are assigned, and links open, but
// nothing carries a game. With it, a game sees an ordinary network interface.
func (c *Core) startAdapter(ctx context.Context, links *mesh.Mesh, virtualIP, subnet string) {
	address, err := adapterAddress(virtualIP, subnet)
	if err != nil {
		c.log.Error("cannot work out the adapter address", "virtualIp", virtualIP, "subnet", subnet, "error", err)
		return
	}

	device, err := tun.Open(protocol.Name, address, transport.TunnelMTU, c.log)
	if err != nil {
		// The group still works as far as the server is concerned, so this is
		// reported rather than treated as a failed connect. The user has to be
		// told, because a missing adapter looks like nothing working at all.
		c.log.Error("cannot open the virtual adapter", "error", err)
		c.setMessage(adapterProblem(err))
		return
	}

	// Windows blocks unsolicited inbound traffic on a network it does not know,
	// and this adapter is always one of those. Without this the tunnel carries
	// packets perfectly and the machine at the other end still cannot ping this
	// one or join a game hosted on it.
	if err := tun.AllowSubnet(subnet, c.log); err != nil {
		c.log.Warn("cannot open the firewall to the virtual network", "subnet", subnet, "error", err)
	}

	c.mu.Lock()
	c.device = device
	lanDiscovery := c.settings.LanDiscovery
	c.mu.Unlock()

	if err := device.PreferForMulticast(lanDiscovery); err != nil {
		// Games that find each other by scanning may not, and everything else
		// works. Not worth failing a connect over.
		c.log.Warn("cannot set the adapter's multicast preference", "error", err)
	}

	go c.pumpOut(ctx, device, links, address)
	go c.pumpIn(ctx, device, links)
}

// pumpOut takes what Windows wants to send and gives it to whoever owns the
// destination address, or to everybody when it is addressed to the whole LAN.
func (c *Core) pumpOut(ctx context.Context, device *tun.Device, links *mesh.Mesh, address netip.Prefix) {
	everyone := broadcastOf(address)

	for {
		packet, err := device.Read(ctx)
		if err != nil {
			// Disconnecting closes the adapter under this loop on purpose, so
			// only an unexpected end is worth a line.
			if ctx.Err() == nil && !errors.Is(err, tun.ErrClosed) {
				c.log.Info("stopped reading the adapter", "error", err)
			}
			return
		}

		destination, ok := destinationOf(packet)
		if !ok {
			// IPv6, and anything else that is not an IPv4 packet. The overlay
			// is IPv4 only, so there is nowhere for it to go.
			continue
		}

		if forEveryone(destination, everyone) {
			// Read per packet rather than captured, so turning the switch off
			// stops the copies at once instead of at the next connect.
			//
			// No delivery report either way. On a real LAN nobody is owed one,
			// and a group with nobody else in it would otherwise log a line
			// for every announcement a game makes.
			//
			// One packet left this device however many copies went out.
			if c.lanDiscovery() && links.Broadcast(packet) > 0 {
				c.packetsOut.Add(1)
			}
			continue
		}

		if err := links.Send(destination, packet); err != nil {
			// A peer whose link is not open yet, or an address in the subnet
			// that nobody holds. Both are ordinary, and both look like packet
			// loss to the game, which is what they are.
			c.log.Debug("dropped an outgoing packet", "to", destination.String(), "error", err)
			continue
		}
		c.packetsOut.Add(1)
	}
}

// pumpIn takes what peers sent and hands it to Windows.
func (c *Core) pumpIn(ctx context.Context, device *tun.Device, links *mesh.Mesh) {
	for {
		select {
		case <-ctx.Done():
			return
		case packet, ok := <-links.Inbound():
			if !ok {
				return
			}
			if err := device.Write(packet); err != nil {
				c.log.Info("cannot write to the adapter", "error", err)
				return
			}
			c.packetsIn.Add(1)
		}
	}
}

// adapterAddress puts this device's address together with the group's prefix
// length, which is what tells Windows to route the whole subnet here.
func adapterAddress(virtualIP, subnet string) (netip.Prefix, error) {
	addr, err := netip.ParseAddr(virtualIP)
	if err != nil {
		return netip.Prefix{}, err
	}

	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return netip.Prefix{}, err
	}
	if !prefix.Contains(addr) {
		return netip.Prefix{}, errors.New("the assigned address is outside the group's subnet")
	}

	return netip.PrefixFrom(addr, prefix.Bits()), nil
}

// lanDiscovery reports whether packets addressed to the whole LAN should be
// copied to the group.
func (c *Core) lanDiscovery() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings.LanDiscovery
}

// limitedBroadcast is 255.255.255.255, which means everyone reachable without
// a router. Games that predate multicast discovery shout at this.
var limitedBroadcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})

// forEveryone reports whether a destination is addressed to the whole LAN
// rather than to one machine.
//
// A layer 3 tunnel carries none of these on its own: there is no shared segment
// for a broadcast to reach and no switch to flood a multicast to, so a game
// that finds servers by scanning finds nobody and the LAN list stays empty.
// Copying the packet onto every link is what the missing segment would have
// done.
func forEveryone(destination, broadcast netip.Addr) bool {
	return destination == broadcast || destination == limitedBroadcast || destination.IsMulticast()
}

// broadcastOf is the address that means everyone on a subnet: the network with
// every host bit set, so 10.69.0.255 for 10.69.0.0/24.
func broadcastOf(subnet netip.Prefix) netip.Addr {
	if !subnet.Addr().Is4() {
		return netip.Addr{}
	}

	raw := subnet.Masked().Addr().As4()
	host := ^uint32(0) >> subnet.Bits()
	binary.BigEndian.PutUint32(raw[:], binary.BigEndian.Uint32(raw[:])|host)
	return netip.AddrFrom4(raw)
}

// destinationOf reads where an IPv4 packet is headed. Bytes 16 to 20 of the
// header, and nothing else in the packet is looked at.
func destinationOf(packet []byte) (netip.Addr, bool) {
	const minimumHeader = 20

	if len(packet) < minimumHeader || packet[0]>>4 != 4 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte(packet[16:20])), true
}

// adapterProblem turns an adapter failure into something a person can act on.
func adapterProblem(err error) string {
	switch {
	case errors.Is(err, tun.ErrNeedsAdmin):
		return "Creating the network adapter needs administrator rights. Restart 192168 as an administrator."
	case errors.Is(err, tun.ErrMissingDriver):
		return "The network driver is missing. Reinstalling 192168 should put it back."
	default:
		return "Could not create the network adapter, so games will not see the other players."
	}
}

// setMessage puts a line in front of the user without changing the connection.
func (c *Core) setMessage(message string) {
	c.mu.Lock()
	c.state.Message = message
	state := c.snapshot()
	c.mu.Unlock()
	c.emit(ipc.EventStateChanged, state)
}
