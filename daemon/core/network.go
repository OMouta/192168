package core

import (
	"context"
	"encoding/base64"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/OMouta/192168/daemon/control"
	"github.com/OMouta/192168/daemon/ipcserver"
	"github.com/OMouta/192168/daemon/mesh"
	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/ipc"
)

// startNetwork brings up the peer-to-peer side of a session: the socket, this
// machine's public address, and a link attempt toward everyone already online.
//
// A failure here does not fail the connect. The group is joined and the virtual
// IP is assigned either way, and a peer that cannot be reached is reported as
// such rather than taking the whole session down.
func (c *Core) startNetwork(ctx context.Context, session *activeSession, peers []api.Peer) {
	// The mesh needs this device's own address to tell a forwarded packet that
	// has arrived from one it is only carrying. An unparseable one is already
	// fatal to the adapter, so it is not worth failing the connect twice over.
	virtualIP, err := netip.ParseAddr(session.virtualIP)
	if err != nil {
		c.log.Error("the server gave us an address we cannot read", "virtualIp", session.virtualIP, "error", err)
	}

	links, err := mesh.New(c.identity.DeviceID, virtualIP, c.identity.Transport, mesh.Events{
		PeerStateChanged:   c.onPeerState,
		PeerLatencyChanged: c.onPeerLatency,
	}, c.log)
	if err != nil {
		c.log.Error("cannot open the peer socket", "error", err)
		return
	}

	c.mu.Lock()
	c.mesh = links
	c.mu.Unlock()

	go links.Run(ctx)

	for _, peer := range peers {
		c.linkPeer(peer)
	}

	go c.publishEndpoint(ctx, links, session.sessionID)
	go c.watchGroup(ctx, session)
	go c.watchTraffic(ctx, links)
	go c.startAdapter(ctx, links, session.virtualIP, session.subnet)
}

// trafficInterval is how often the counts on screen are brought up to date. A
// busy link moves thousands of packets a second, which nobody can read.
const trafficInterval = time.Second

// watchTraffic keeps the counts on screen in step with the links.
//
// A poll rather than an event, because the count changes once per packet. An
// event each time would be the game's own traffic again, in JSON, down a pipe.
func (c *Core) watchTraffic(ctx context.Context, links *mesh.Mesh) {
	ticker := time.NewTicker(trafficInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if state, moved := c.readTraffic(links); moved {
				c.emit(ipc.EventStateChanged, state)
			}
		}
	}
}

// readTraffic copies the counters into the state and reports whether anything
// moved, so a group sitting idle announces nothing.
func (c *Core) readTraffic(links *mesh.Mesh) (ipc.State, bool) {
	out, in := c.packetsOut.Load(), c.packetsIn.Load()

	c.mu.Lock()
	defer c.mu.Unlock()

	moved := c.state.PacketsSent != out || c.state.PacketsReceived != in
	c.state.PacketsSent, c.state.PacketsReceived = out, in

	for _, peer := range links.Peers() {
		view, known := c.peers[peer.DeviceID]
		if !known {
			continue
		}
		sent, received := peer.Traffic()
		if view.PacketsSent != sent || view.PacketsReceived != received {
			moved = true
		}
		view.PacketsSent, view.PacketsReceived = sent, received
	}

	if !moved {
		return ipc.State{}, false
	}
	return c.snapshot(), true
}

const (
	// endpointRecheckInterval is the slow path. A NAT can rebind without
	// anything on this machine changing, and the only way to find out is to ask
	// again.
	endpointRecheckInterval = 60 * time.Second

	// interfaceCheckInterval is the fast path. Moving between networks changes
	// a local address at once, and waiting out a slow cycle to notice would
	// leave a game dead for a minute for no reason. Reading the local
	// interfaces costs nothing, so it can be checked often.
	interfaceCheckInterval = 3 * time.Second
)

// publishEndpoint asks STUN where this socket appears from the outside, tells
// the group, and keeps watching for that answer to change.
//
// Until the first publish lands, peers know a device is online but have nowhere
// to send. After it, the address is still not permanent: switching from Wi-Fi
// to a hotspot, or a NAT rebinding on its own, leaves every peer sending to a
// mapping that reaches nobody. Nothing recovers from that on its own, because
// both sides keep punching at addresses that stopped being real, so this is
// what notices and republishes.
func (c *Core) publishEndpoint(ctx context.Context, links *mesh.Mesh, sessionID string) {
	servers := c.stunServers()
	if len(servers) == 0 {
		c.log.Warn("no STUN servers, peers will not be able to reach this device")
		return
	}

	var (
		published  netip.AddrPort
		interfaces = localAddresses()
		lastCheck  time.Time
	)

	ticker := time.NewTicker(interfaceCheckInterval)
	defer ticker.Stop()

	for {
		current := localAddresses()
		moved := current != interfaces
		interfaces = current

		if moved || time.Since(lastCheck) >= endpointRecheckInterval {
			lastCheck = time.Now()
			if moved && published.IsValid() {
				c.log.Info("local addresses changed, re-checking our public address")
			}
			c.republishEndpoint(ctx, links, sessionID, &published)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// republishEndpoint runs one STUN round and tells the group only when the
// answer is new, so a stable address costs one query and no writes.
//
// A changed address also means every existing link is pointed at a mapping that
// is gone, so they start over from the new one.
func (c *Core) republishEndpoint(ctx context.Context, links *mesh.Mesh, sessionID string, published *netip.AddrPort) {
	endpoint, err := links.PublicEndpoint(ctx, c.stunServers())
	if err != nil {
		c.log.Error("cannot find our public address", "error", err)
		return
	}
	if endpoint == *published {
		return
	}

	if _, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.PublishEndpoint(ctx, sessionID, api.Endpoint{
			Address: endpoint.Addr().String(),
			Port:    int(endpoint.Port()),
		})
	}); err != nil {
		c.log.Error("cannot publish our endpoint", "error", err)
		return
	}

	// Only after the group has been told. Restarting first would leave peers
	// punching toward the old address with no way to learn the new one.
	if published.IsValid() {
		c.log.Info("public address changed, restarting links", "was", published.String(), "now", endpoint.String())
		links.RestartLinks()
	}
	*published = endpoint
}

// localAddresses fingerprints this machine's own addresses. It is compared for
// equality rather than read, so the cheapest stable form will do.
//
// Loopback is left out because it is there on every network and never says
// anything about which one this is.
func localAddresses() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	found := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		prefix, ok := addr.(*net.IPNet)
		if !ok || prefix.IP.IsLoopback() {
			continue
		}
		found = append(found, prefix.String())
	}
	slices.Sort(found)
	return strings.Join(found, ",")
}

// watchGroup follows the group while it is connected, so a peer who joins after
// this device does is heard about rather than missed until the next connect.
func (c *Core) watchGroup(ctx context.Context, session *activeSession) {
	client, err := c.ensureClient(ctx)
	if err != nil {
		return
	}

	client.Realtime(ctx, session.sessionID, control.RealtimeHandler{
		PeerOnline: func(peer api.Peer) {
			c.log.Info("peer came online", "deviceId", peer.DeviceID, "virtualIp", peer.VirtualIP)
			c.addPeer(peer)
		},
		PeerOffline: func(deviceID string) {
			c.log.Info("peer went offline", "deviceId", deviceID)
			c.peerWentOffline(deviceID)
		},
		PeerEndpointUpdated: func(deviceID string, endpoint api.Endpoint) {
			addr, ok := parseEndpoint(endpoint)
			if !ok {
				return
			}
			c.mu.Lock()
			links := c.mesh
			c.mu.Unlock()
			if links != nil {
				links.SetPeerEndpoint(deviceID, addr)
			}
		},
		PeerRenamed: func(deviceID, nickname string) {
			c.renamePeer(deviceID, nickname)
		},
		GroupDeleted: func(groupID string) {
			c.log.Warn("the connected group was deleted", "groupId", groupID)
			go c.dropSession("That group was deleted.")
		},
		MembershipRevoked: func() {
			c.log.Warn("membership revoked while connected")
			go c.dropSession("You were removed from that group.")
		},
		SessionInvalidated: func() {
			go c.dropSession("The connection to the group was lost.")
		},
		Connected: c.setServerOnline,
	})
}

// addPeer puts someone on screen, then starts trying to reach them.
//
// The same peer arrives twice: once in the snapshot handed to a new subscriber,
// and again as an event when their session begins. Which order those land in is
// a race, and the second one used to replace the row.
//
// That is how a link that was already open ended up reading Connecting and
// staying there: the link had nothing further to report, so nothing ever put
// the row right again. Whoever lost the race saw it, which is why swapping who
// joined first swapped who saw it.
func (c *Core) addPeer(peer api.Peer) {
	c.mu.Lock()
	view, known := c.peers[peer.DeviceID]
	if !known {
		view = &ipc.PeerView{DeviceID: peer.DeviceID, State: ipc.PeerConnecting}
		c.peers[peer.DeviceID] = view
	}
	// What the server knows: a name and an address. The state and the latency
	// belong to the link and are left alone, except for someone who was away
	// and is back: there is a link to try again now.
	view.Nickname = peer.Nickname
	view.VirtualIP = peer.VirtualIP
	if view.State == ipc.PeerOffline {
		view.State = ipc.PeerConnecting
	}

	added := *view
	state := c.snapshot()
	c.mu.Unlock()

	if !known {
		c.emit(ipc.EventPeerAdded, ipc.PeerAddedData{Peer: added})
	}
	c.emit(ipc.EventStateChanged, state)

	c.linkPeer(peer)
}

// linkPeer starts the punch and handshake toward one peer.
//
// An endpoint is optional. A peer that has not published one yet is online but
// unreachable, and the realtime channel brings the address when it has one.
func (c *Core) linkPeer(peer api.Peer) {
	c.mu.Lock()
	links := c.mesh
	c.mu.Unlock()
	if links == nil {
		return
	}

	virtualIP, err := netip.ParseAddr(peer.VirtualIP)
	if err != nil {
		c.log.Warn("peer has an unusable address", "deviceId", peer.DeviceID, "virtualIp", peer.VirtualIP)
		return
	}

	key, err := base64.RawStdEncoding.DecodeString(peer.TransportKey)
	if err != nil {
		c.log.Warn("peer has an unusable key", "deviceId", peer.DeviceID)
		return
	}

	endpoint, _ := parseEndpoint(endpointOf(peer))
	if err := links.AddPeer(peer.DeviceID, peer.Nickname, virtualIP, key, endpoint); err != nil {
		c.log.Warn("cannot add a peer", "deviceId", peer.DeviceID, "error", err)
	}
}

// loadMembers brings the list in line with who belongs to the group, as opposed
// to who is currently reachable.
//
// The peer list only ever held people with a live session, so a group of six
// with one person online looked like a group of two. This adds the rest as
// offline, and drops anyone who is no longer a member: leaving a group and
// closing the app look identical from the outside, and this is what tells them
// apart.
//
// The state and latency of a link are left alone. They belong to the link.
func (c *Core) loadMembers(ctx context.Context, groupID string) {
	members, err := withClient(c, ctx, func(client *control.Client) ([]api.Member, error) {
		return client.Members(ctx, groupID)
	})
	if err != nil {
		c.log.Info("cannot list the group's members", "groupId", groupID, "error", err)
		return
	}

	c.mu.Lock()
	if c.session == nil || c.session.groupID != groupID {
		// Disconnected, or moved to another group, while this was in flight.
		c.mu.Unlock()
		return
	}

	belongs := make(map[string]struct{}, len(members))
	for _, member := range members {
		if member.DeviceID == c.identity.DeviceID {
			continue
		}
		belongs[member.DeviceID] = struct{}{}

		owner := member.Role == api.RoleOwner
		if peer, known := c.peers[member.DeviceID]; known {
			peer.Nickname = member.Nickname
			peer.VirtualIP = member.VirtualIP
			peer.IsOwner = owner
			continue
		}
		c.peers[member.DeviceID] = &ipc.PeerView{
			DeviceID:  member.DeviceID,
			Nickname:  member.Nickname,
			VirtualIP: member.VirtualIP,
			State:     ipc.PeerOffline,
			IsOwner:   owner,
		}
	}

	for deviceID := range c.peers {
		if _, ok := belongs[deviceID]; !ok {
			delete(c.peers, deviceID)
		}
	}

	state := c.snapshot()
	c.mu.Unlock()

	c.emit(ipc.EventStateChanged, state)
}

// RetryPeer attempts one link again, for someone who has changed something at
// their end and wants to find out whether it worked.
//
// Only the link is thrown away. The session, the adapter and every other link
// carry on, which is the difference between this and reconnecting.
func (c *Core) RetryPeer(_ context.Context, params ipc.PeerParams) error {
	if params.DeviceID == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "Choose who to try again."}
	}

	c.mu.Lock()
	links := c.mesh
	_, known := c.peers[params.DeviceID]
	c.mu.Unlock()

	if links == nil || !known {
		return &ipcserver.Failure{Code: "not_connected", Message: "That person is not on this network."}
	}
	if !links.RetryPeer(params.DeviceID) {
		// In the list but with no link behind it, which is what somebody who is
		// offline looks like. There is nothing to retry until they are back.
		return &ipcserver.Failure{Code: "peer_offline", Message: "They are not connected right now."}
	}
	return nil
}

// peerWentOffline marks someone as away rather than taking them off the list.
// They are still in the group, and a group is worth seeing whole.
func (c *Core) peerWentOffline(deviceID string) {
	c.mu.Lock()
	links := c.mesh
	groupID := ""
	if c.session != nil {
		groupID = c.session.groupID
	}
	peer, known := c.peers[deviceID]
	if known {
		peer.State = ipc.PeerOffline
		peer.LatencyMS = nil
		// The address is not cleared. It belongs to their membership rather
		// than to the session that just ended.
	}
	state := c.snapshot()
	c.mu.Unlock()

	// The link goes either way. Their session is gone, so there is nothing at
	// the other end of it.
	if links != nil {
		links.RemovePeer(deviceID)
	}
	if !known {
		return
	}

	c.emit(ipc.EventStateChanged, state)

	// Whether they closed the app or left the group entirely, the membership
	// list is what says which.
	if groupID != "" {
		go c.loadMembers(c.ctx, groupID)
	}
}

// renamePeer changes the name shown for someone who is already here.
func (c *Core) renamePeer(deviceID, nickname string) {
	c.mu.Lock()
	peer, ok := c.peers[deviceID]
	if ok {
		peer.Nickname = nickname
	}
	state := c.snapshot()
	c.mu.Unlock()
	if !ok {
		return
	}

	c.emit(ipc.EventStateChanged, state)
}

func (c *Core) removePeer(deviceID string) {
	c.mu.Lock()
	links := c.mesh
	delete(c.peers, deviceID)
	state := c.snapshot()
	c.mu.Unlock()

	if links != nil {
		links.RemovePeer(deviceID)
	}

	c.emit(ipc.EventPeerRemoved, ipc.PeerRemovedData{DeviceID: deviceID})
	c.emit(ipc.EventStateChanged, state)
}

// onPeerState carries a link's state up to the UI. This is where a row stops
// saying Connecting and starts saying Direct.
func (c *Core) onPeerState(deviceID string, state ipc.PeerState) {
	c.mu.Lock()
	peer, ok := c.peers[deviceID]
	if ok {
		peer.State = state
	}
	snapshot := c.snapshot()
	c.mu.Unlock()
	if !ok {
		return
	}

	c.emit(ipc.EventPeerStateChanged, ipc.PeerStateChangedData{DeviceID: deviceID, State: state})
	c.emit(ipc.EventStateChanged, snapshot)
}

func (c *Core) onPeerLatency(deviceID string, latency time.Duration) {
	milliseconds := int(latency.Milliseconds())

	c.mu.Lock()
	peer, ok := c.peers[deviceID]
	if ok {
		peer.LatencyMS = &milliseconds
	}
	snapshot := c.snapshot()
	c.mu.Unlock()
	if !ok {
		return
	}

	c.emit(ipc.EventPeerLatencyChanged, ipc.PeerLatencyChangedData{DeviceID: deviceID, LatencyMS: milliseconds})
	c.emit(ipc.EventStateChanged, snapshot)
}

// stunServers is whatever the current server advertises. Nothing is compiled
// in, so a deployment can move to its own STUN infrastructure without the app
// being rebuilt.
func (c *Core) stunServers() []string {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()

	if client == nil {
		return nil
	}
	return client.Discovery().STUN
}

func endpointOf(peer api.Peer) api.Endpoint {
	if peer.Endpoint == nil {
		return api.Endpoint{}
	}
	return *peer.Endpoint
}

func parseEndpoint(endpoint api.Endpoint) (netip.AddrPort, bool) {
	addr, err := netip.ParseAddr(endpoint.Address)
	if err != nil || endpoint.Port <= 0 || endpoint.Port > 65535 {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(addr, uint16(endpoint.Port)), true
}
