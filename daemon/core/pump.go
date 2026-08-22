package core

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"

	"github.com/OMouta/192168/daemon/mesh"
	"github.com/OMouta/192168/daemon/netlog"
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
	go c.pumpIn(ctx, device, links, address)
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
			c.packets.Drop(netlog.NotIPv4)
			continue
		}

		if forEveryone(destination, everyone) {
			// Read per packet rather than captured, so turning the switch off
			// stops the copies at once instead of at the next connect.
			if !c.lanDiscovery() {
				continue
			}
			// One packet left this device however many copies went out. The
			// count of peers it reached is the thing worth knowing: zero means
			// the announcement was made and nobody was there to hear it, which
			// is a different problem from never having made it.
			reached := links.Broadcast(packet)
			c.packets.Shouted(destination, reached, len(packet))
			if reached > 0 {
				c.packetsOut.Add(1)
				c.packets.Sent()
			}
			continue
		}

		if err := links.Send(destination, packet); err != nil {
			// A peer whose link is not open yet, or an address in the subnet
			// that nobody holds. Both are ordinary, and both look like packet
			// loss to the game, which is what they are. Send has already
			// counted it against the reason it failed for.
			continue
		}
		c.packetsOut.Add(1)
		c.packets.Sent()
	}
}

// pumpIn takes what peers sent and hands it to Windows.
func (c *Core) pumpIn(ctx context.Context, device *tun.Device, links *mesh.Mesh, address netip.Prefix) {
	local, everyone := address.Addr(), broadcastOf(address)

	for {
		select {
		case <-ctx.Done():
			return
		case packet, ok := <-links.Inbound():
			if !ok {
				return
			}

			if destination, ok := destinationOf(packet); ok && forEveryone(destination, everyone) {
				// The other end of what pumpOut replicated. One line at each
				// end is what says whether an announcement crossed, which is
				// the whole question behind a LAN list that stays empty.
				c.packets.Overheard(sourceOf(packet), destination, len(packet))

				// Read per packet for the same reason the outgoing copies are:
				// turning the switch off stops discovery in both directions at
				// once rather than at the next connect.
				if c.lanDiscovery() {
					localiseMulticast(packet, local)
				}
			}

			switch err := device.Write(packet); {
			case err == nil:
				c.packetsIn.Add(1)
				c.packets.Received()
			case errors.Is(err, tun.ErrDropped):
				// Windows is not draining the adapter. The next packet may
				// well fit, so this is a lost packet rather than a reason to
				// stop carrying them.
				c.packets.Drop(netlog.AdapterFull)
			default:
				c.log.Info("cannot write to the adapter", "error", err)
				return
			}
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

const (
	// protocolUDP is the IPv4 protocol number for UDP, which every game's
	// discovery traffic is.
	protocolUDP = 17

	// udpHeaderSize is source(2) | destination(2) | length(2) | checksum(2).
	udpHeaderSize = 8

	// fragmentBits are the more-fragments flag and the fragment offset, taken
	// together because either one being set means this is part of a datagram
	// rather than all of it.
	fragmentBits = 0x3fff
)

// localiseMulticast readdresses a packet meant for a multicast group to this
// machine, so Windows hands it to the game instead of dropping it.
//
// Copying the packet onto every link is only half of what a shared segment
// would have done. The other half is delivery, and that is where a layer 3
// tunnel runs out: a program listening for a group tells Windows to join it,
// Windows binds that membership to whichever single interface wins the route
// for 224.0.0.0/4, and even when the tunnel wins there is no segment beneath
// it and so no flooded frame for the membership to catch. The packet reaches
// the adapter and goes nowhere. This is why the LAN list stays empty against a
// tunnel and fills against Radmin or Hamachi, which put a real Ethernet
// segment underneath and get the flooding for free.
//
// Addressing the copy to this machine steps around the whole thing. A program
// listening for discovery binds the wildcard address, so an ordinary unicast
// datagram on the right port reaches it with no membership involved, and what
// a game reads to find the host is the source address, which is untouched.
//
// Only whole UDP datagrams are rewritten. Anything else is left as it is:
// without the transport header there is no checksum to correct, and moving the
// address on one fragment of a datagram would leave it unreassemblable.
func localiseMulticast(packet []byte, local netip.Addr) {
	const minimumHeader = 20

	if len(packet) < minimumHeader || packet[0]>>4 != 4 || !local.Is4() {
		return
	}
	if !netip.AddrFrom4([4]byte(packet[16:20])).IsMulticast() {
		return
	}
	if packet[9] != protocolUDP || binary.BigEndian.Uint16(packet[6:8])&fragmentBits != 0 {
		return
	}

	// Options are rare and legal, so the transport header is found through the
	// header length rather than assumed to be at twenty bytes.
	headerLen := int(packet[0]&0x0f) * 4
	if headerLen < minimumHeader || len(packet) < headerLen+udpHeaderSize {
		return
	}

	was, now := [4]byte(packet[16:20]), local.As4()
	copy(packet[16:20], now[:])

	header := packet[10:12]
	binary.BigEndian.PutUint16(header, adjustChecksum(binary.BigEndian.Uint16(header), was, now))

	// A UDP checksum is optional over IPv4, and one that was never computed is
	// left as it is rather than turned into a wrong one.
	body := packet[headerLen+6 : headerLen+8]
	if sum := binary.BigEndian.Uint16(body); sum != 0 {
		updated := adjustChecksum(sum, was, now)
		if updated == 0 {
			// Zero on the wire means there is no checksum, so a checksum that
			// really is zero goes out as its other representation.
			updated = 0xffff
		}
		binary.BigEndian.PutUint16(body, updated)
	}
}

// adjustChecksum folds an address change into a one's complement checksum
// instead of recomputing it over the packet. RFC 1624.
//
// The IP header checksum and the UDP checksum both cover the destination
// address and nothing else that moves here, so the same correction applies to
// each of them.
func adjustChecksum(checksum uint16, was, now [4]byte) uint16 {
	sum := uint32(^checksum)
	for i := 0; i < len(was); i += 2 {
		sum += uint32(^binary.BigEndian.Uint16(was[i : i+2]))
		sum += uint32(binary.BigEndian.Uint16(now[i : i+2]))
	}
	for sum > 0xffff {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// sourceOf reads who sent an IPv4 packet. Only meaningful for one that has
// already passed destinationOf, which is what checks it is long enough.
func sourceOf(packet []byte) netip.Addr {
	return netip.AddrFrom4([4]byte(packet[12:16]))
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
