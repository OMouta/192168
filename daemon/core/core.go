// Package core is the daemon's brain.
//
// It owns the connection state and is the only thing that decides what
// connecting means. The client above it sends intents and draws what it is
// told; the control client below it moves bytes to the server. This is where the
// two meet.
package core

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/OMouta/192168/daemon/config"
	"github.com/OMouta/192168/daemon/control"
	"github.com/OMouta/192168/daemon/identity"
	"github.com/OMouta/192168/daemon/ipcserver"
	"github.com/OMouta/192168/daemon/mesh"
	"github.com/OMouta/192168/daemon/netlog"
	"github.com/OMouta/192168/daemon/tun"
	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/invite"
	"github.com/OMouta/192168/protocol/ipc"
)

// Events is how the core tells connected clients what changed. The IPC server
// satisfies it.
type Events interface {
	Broadcast(name ipc.EventName, data any)
}

// Core implements ipcserver.Handler.
type Core struct {
	log      *slog.Logger
	identity *identity.Identity
	settings *settings
	events   Events

	// defaultServer is what the app shipped with, kept so settings can be put
	// back to it.
	defaultServer string

	// ctx is the daemon's lifetime. Work started by a request outlives the
	// request, so it hangs off this rather than off a caller's context.
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	client *control.Client
	// inviteBase is where the current server says its invite links live. Kept
	// here because the app is handed links, and the group list is built without
	// a server call.
	inviteBase string
	state      ipc.State
	peers      map[string]*ipc.PeerView
	session    *activeSession

	// mesh is the peer-to-peer socket, alive only while a group is connected.
	mesh *mesh.Mesh

	// device is the virtual adapter, alive for the same span.
	device *tun.Device

	// packetsOut and packetsIn count at the adapter rather than per link, so
	// they keep counting across peers coming and going. A link is thrown away
	// with the peer that left, and its count goes too.
	packetsOut atomic.Uint64
	packetsIn  atomic.Uint64

	// packets is the account of what happened to traffic, as opposed to the
	// two counters above, which are what the UI draws.
	packets *netlog.Recorder

	// mainLog is the daemon's own log file, kept so it can be emptied on
	// request. Nil in the foreground, where the log is a console.
	mainLog *config.RollingLog
}

// activeSession is the group connection. There is at most one, because two
// virtual networks at once would fight over local routes.
type activeSession struct {
	sessionID string
	groupID   string
	groupName string
	virtualIP string

	// subnet is the group's range, which the adapter needs so Windows routes
	// every peer here rather than only the one address.
	subnet string

	// stop ends the work that belongs to this session, so a disconnect does
	// not leave a heartbeat running against a session that is gone.
	stop context.CancelFunc
	done chan struct{}
}

// New creates a core around an identity and its data directory. defaultServer
// is used only when the user has not chosen one yet.
func New(ctx context.Context, id *identity.Identity, dataDir, defaultServer string, log *slog.Logger) (*Core, error) {
	set, err := loadSettings(dataDir, defaultServer)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	packets := netlog.New(log, config.PacketLogFile(dataDir))
	if set.PacketLog {
		// A log that was left on stays on across a restart, which is what
		// somebody chasing something intermittent wants. Failing to open it is
		// not worth refusing to start over.
		if err := packets.SetEnabled(true); err != nil {
			log.Warn("cannot open the packet log", "error", err)
		}
	}
	go packets.Run(ctx)

	return &Core{
		log:           log,
		identity:      id,
		settings:      set,
		events:        nopEvents{},
		defaultServer: defaultServer,
		ctx:           ctx,
		cancel:        cancel,
		packets:       packets,
		peers:         map[string]*ipc.PeerView{},
		state: ipc.State{
			Connection: ipc.StateDisconnected,
			ServerURL:  set.ServerURL,
			// Available before anything is connected, so the app can show it and
			// let you change it.
			Nickname: id.Nickname,
			Peers:    []ipc.PeerView{},
		},
	}, nil
}

// SetEvents wires up where state changes are announced. It exists because the
// core and the IPC server need each other, and one of them has to be built
// first.
func (c *Core) SetEvents(events Events) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = events
}

// emit reads the sink under the lock, so wiring it up at startup cannot race
// with an event going out. It must not be called while holding the lock.
func (c *Core) emit(name ipc.EventName, data any) {
	c.mu.Lock()
	sink := c.events
	c.mu.Unlock()
	sink.Broadcast(name, data)
}

// nopEvents drops everything, so a core with no client attached still runs.
type nopEvents struct{}

func (nopEvents) Broadcast(ipc.EventName, any) {}

// Close disconnects and stops any background work.
func (c *Core) Close() {
	_ = c.Disconnect(context.Background())
	c.cancel()
	_ = c.packets.Close()
}

// SetLogs tells the core which log files it is allowed to empty.
//
// It is a setter rather than an argument because the log is opened before there
// is a core to give it to: the first thing a daemon needs is somewhere to
// report that it could not start.
func (c *Core) SetLogs(main *config.RollingLog) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mainLog = main
}

// GetState returns everything the UI needs to draw itself.
func (c *Core) GetState(context.Context) (ipc.State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot(), nil
}

// GetGroups lists saved memberships. It asks the server, because membership is
// server-side state and another device may have joined or left since last time.
func (c *Core) GetGroups(ctx context.Context) ([]ipc.Group, error) {
	memberships, err := withClient(c, ctx, func(client *control.Client) ([]api.Membership, error) {
		return client.Groups(ctx)
	})
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	groups := make([]ipc.Group, 0, len(memberships))
	for _, m := range memberships {
		groups = append(groups, toGroup(m, c.session != nil && c.session.groupID == m.GroupID, c.inviteBase))
	}
	return groups, nil
}

// CreateGroup makes a group and joins it. It does not connect: the user may
// want to invite people before anyone is online.
func (c *Core) CreateGroup(ctx context.Context, params ipc.CreateGroupParams) (ipc.Group, error) {
	if params.Name == "" {
		return ipc.Group{}, &ipcserver.Failure{Code: "bad_request", Message: "A group needs a name."}
	}

	membership, err := withClient(c, ctx, func(client *control.Client) (api.Membership, error) {
		return client.CreateGroup(ctx, control.NewGroup{
			Name:  params.Name,
			Icon:  params.Icon,
			Color: params.Color,
		})
	})
	if err != nil {
		return ipc.Group{}, err
	}
	c.log.Info("group created", "groupId", membership.GroupID)
	return toGroup(membership, false, c.inviteLinkBase()), nil
}

// JoinGroup joins whichever group an invite opens. The code is whatever was
// pasted, so a link works too. It is used once and never stored; the device
// token is what gets back in afterwards.
func (c *Core) JoinGroup(ctx context.Context, params ipc.JoinGroupParams) (ipc.Group, error) {
	code := invite.Parse(params.Code)
	if code == "" {
		return ipc.Group{}, &ipcserver.Failure{
			Code:    "bad_request",
			Message: "That does not look like an invite.",
		}
	}

	membership, err := withClient(c, ctx, func(client *control.Client) (api.Membership, error) {
		return client.JoinByCode(ctx, code)
	})
	if err != nil {
		return ipc.Group{}, err
	}
	c.log.Info("group joined", "groupId", membership.GroupID)
	return toGroup(membership, false, c.inviteLinkBase()), nil
}

// GetInvite says what a code opens, so the screen can name the group first.
//
// A code that opens nothing comes back Found false rather than as an error. A
// half-typed code is invalid and that is not worth reporting.
func (c *Core) GetInvite(ctx context.Context, params ipc.InviteParams) (ipc.InviteResult, error) {
	code := invite.Parse(params.Code)
	if code == "" {
		return ipc.InviteResult{}, nil
	}

	found, err := withClient(c, ctx, func(client *control.Client) (api.Invite, error) {
		return client.Invite(ctx, code)
	})
	if err != nil {
		var failure *ipcserver.Failure
		if errors.As(err, &failure) && failure.Code == api.ErrInviteInvalid {
			return ipc.InviteResult{}, nil
		}
		return ipc.InviteResult{}, err
	}

	return ipc.InviteResult{
		Found:      true,
		Code:       found.Code,
		GroupName:  found.GroupName,
		GroupIcon:  found.GroupIcon,
		GroupColor: found.GroupColor,
		Members:    found.Members,
	}, nil
}

// ResetInvite replaces a group's code. Owner only, which the server decides.
func (c *Core) ResetInvite(ctx context.Context, params ipc.GroupParams) (ipc.InviteCodeResult, error) {
	if params.GroupID == "" {
		return ipc.InviteCodeResult{}, &ipcserver.Failure{Code: "bad_request", Message: "Choose a group."}
	}

	code, err := withClient(c, ctx, func(client *control.Client) (string, error) {
		return client.ResetInvite(ctx, params.GroupID)
	})
	if err != nil {
		return ipc.InviteCodeResult{}, err
	}
	c.log.Info("invite code reset", "groupId", params.GroupID)
	return ipc.InviteCodeResult{Code: code, Link: invite.Link(c.inviteLinkBase(), code)}, nil
}

// LeaveGroup gives up a membership, disconnecting first if that group is the
// active one.
func (c *Core) LeaveGroup(ctx context.Context, groupID string) error {
	c.mu.Lock()
	active := c.session != nil && c.session.groupID == groupID
	c.mu.Unlock()

	if active {
		if err := c.Disconnect(ctx); err != nil {
			return err
		}
	}

	_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.LeaveGroup(ctx, groupID)
	})
	return err
}

// SetNickname changes what this device is called, in every group at once.
func (c *Core) SetNickname(ctx context.Context, params ipc.SetNicknameParams) error {
	nickname := strings.TrimSpace(params.Nickname)
	if nickname == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "That nickname will not work."}
	}

	_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.SetNickname(ctx, nickname)
	})
	if err != nil {
		return err
	}
	if err := c.identity.SetNickname(nickname); err != nil {
		return err
	}

	c.mu.Lock()
	c.state.Nickname = nickname
	state := c.snapshot()
	c.mu.Unlock()

	c.emit(ipc.EventStateChanged, state)
	return nil
}

// GetServer reports the configured server.
func (c *Core) GetServer(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings.ServerURL, nil
}

// SetServer points the daemon at a different server.
//
// A device token is only good where it was issued, so switching servers means
// registering again. Disconnecting first keeps the old server from holding a
// session nobody is going to close.
func (c *Core) SetServer(ctx context.Context, url string) error {
	if err := validateServerURL(url); err != nil {
		return err
	}

	c.mu.Lock()
	unchanged := c.settings.ServerURL == url
	c.mu.Unlock()
	if unchanged {
		return nil
	}

	if err := c.Disconnect(ctx); err != nil {
		return err
	}

	c.mu.Lock()
	c.settings.ServerURL = url
	c.client = nil
	c.state.ServerURL = url
	c.state.ServerOnline = false
	err := c.settings.save()
	state := c.snapshot()
	c.mu.Unlock()

	if err != nil {
		return err
	}
	c.log.Info("server changed", "serverUrl", url)
	c.emit(ipc.EventStateChanged, state)
	return nil
}

// GetLanDiscovery reports whether the group is treated as a real LAN for the
// purpose of games that find each other by scanning one.
func (c *Core) GetLanDiscovery(context.Context) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings.LanDiscovery, nil
}

// SetLanDiscovery turns LAN discovery on or off, and applies it now rather than
// at the next connect. Somebody who turns it off because their speakers went
// missing wants them back without disconnecting first.
func (c *Core) SetLanDiscovery(_ context.Context, enabled bool) error {
	c.mu.Lock()
	if c.settings.LanDiscovery == enabled {
		c.mu.Unlock()
		return nil
	}
	c.settings.LanDiscovery = enabled
	device := c.device
	err := c.settings.save()
	c.mu.Unlock()

	if err != nil {
		return err
	}

	// Replication reads the setting on every packet, so it is already in
	// effect. The multicast route is a property of the adapter, and only there
	// is one to change.
	if device != nil {
		if err := device.PreferForMulticast(enabled); err != nil {
			c.log.Warn("cannot change the adapter's multicast preference", "error", err)
		}
	}

	c.log.Info("lan discovery changed", "enabled", enabled)
	return nil
}

// GetPacketLog reports whether the packet log is on.
func (c *Core) GetPacketLog(context.Context) (bool, error) {
	return c.packets.Enabled(), nil
}

// SetPacketLog turns the packet log on or off, and reports what it settled on.
//
// A file that cannot be opened leaves the switch off rather than half on, and
// says so, because a user who thinks they are recording and is not will come
// back with the same problem and no more information than before.
func (c *Core) SetPacketLog(_ context.Context, enabled bool) (bool, error) {
	if err := c.packets.SetEnabled(enabled); err != nil {
		c.log.Error("cannot open the packet log", "error", err)
		return false, &ipcserver.Failure{
			Code:    "log_unavailable",
			Message: "Could not open the packet log, so it stays off.",
		}
	}

	c.mu.Lock()
	c.settings.PacketLog = enabled
	err := c.settings.save()
	c.mu.Unlock()
	if err != nil {
		return enabled, err
	}

	c.log.Info("packet log changed", "enabled", enabled)
	return enabled, nil
}

// ClearLogs empties the logs the daemon owns and names what it emptied.
//
// The app cannot do this itself. These files are held open by a service running
// as LocalSystem, and deleting one from outside appears to work while leaving
// the daemon writing into a file that no longer has a name.
//
// The app's own log is the app's to clear, and is not touched here.
func (c *Core) ClearLogs(context.Context) ([]string, error) {
	c.mu.Lock()
	main := c.mainLog
	c.mu.Unlock()

	cleared := make([]string, 0, 2)
	if main != nil {
		if err := main.Clear(); err != nil {
			return nil, err
		}
		cleared = append(cleared, "daemon.log")
	}
	if err := c.packets.Clear(); err != nil {
		return nil, err
	}
	cleared = append(cleared, "packets.log")

	// After the truncation, so it is the first line in the empty file and the
	// log says where it starts.
	c.log.Info("logs cleared", "cleared", cleared)
	return cleared, nil
}

// TestServer checks whether an address is a server this app can use. A server
// that cannot be reached is an answer rather than an error, because the user
// asked a question.
func (c *Core) TestServer(ctx context.Context, url string) (ipc.TestServerResult, error) {
	if err := validateServerURL(url); err != nil {
		return ipc.TestServerResult{}, err
	}

	client, err := control.Discover(ctx, url)
	if err != nil {
		// The user gets a sentence they can act on; the log gets the cause,
		// which is the only place the difference between a bad address and a
		// resolver having a bad day is visible.
		c.log.Info("server test failed", "serverUrl", url, "error", err)

		var e *control.Error
		if errors.As(err, &e) {
			return ipc.TestServerResult{Reachable: false, Message: e.Message}, nil
		}
		return ipc.TestServerResult{Reachable: false, Message: "That server could not be reached."}, nil
	}

	doc := client.Discovery()
	return ipc.TestServerResult{
		Reachable: true,
		Version:   doc.Version,
		Features:  doc.Features,
	}, nil
}

// snapshot builds the state the UI renders. Callers hold the lock.
func (c *Core) snapshot() ipc.State {
	state := c.state
	state.Peers = make([]ipc.PeerView, 0, len(c.peers))
	for _, peer := range c.peers {
		state.Peers = append(state.Peers, *peer)
	}
	sortPeers(state.Peers)
	return state
}

// inviteLinkBase reads where links live, for callers not holding the lock.
func (c *Core) inviteLinkBase() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inviteBase
}

func toGroup(m api.Membership, active bool, inviteBase string) ipc.Group {
	return ipc.Group{
		GroupID:       m.GroupID,
		Name:          m.GroupName,
		Icon:          m.GroupIcon,
		Color:         m.GroupColor,
		Active:        active,
		OnlineMembers: m.OnlineMembers,
		InviteCode:    m.InviteCode,
		InviteLink:    invite.Link(inviteBase, m.InviteCode),
		IsOwner:       m.Role == api.RoleOwner,
	}
}

// Core has to satisfy the IPC server's handler, and a missing method should
// fail the build rather than at the first click.
var _ ipcserver.Handler = (*Core)(nil)

// ResetSettings puts the daemon back to how it shipped: the default server,
// LAN discovery on, and no active connection. The device keeps its identity,
// since forgetting that would drop every group this machine belongs to.
func (c *Core) ResetSettings(ctx context.Context) (string, error) {
	c.mu.Lock()
	fallback := c.defaultServer
	c.mu.Unlock()

	if err := c.SetServer(ctx, fallback); err != nil {
		return "", err
	}
	if err := c.SetLanDiscovery(ctx, true); err != nil {
		return "", err
	}
	c.log.Info("settings reset", "serverUrl", fallback)
	return fallback, nil
}

// RemoveMember takes someone out of a group and keeps them out. The owner's
// alone, which the server decides.
func (c *Core) RemoveMember(ctx context.Context, params ipc.MemberParams) error {
	if params.GroupID == "" || params.DeviceID == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "Choose who to remove."}
	}

	_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.RemoveMember(ctx, params.GroupID, params.DeviceID)
	})
	if err != nil {
		return err
	}

	// They are gone from the group, so they are gone from the list. The peer
	// event says they went offline, which on its own would leave them showing
	// as somebody who is merely away.
	c.removePeer(params.DeviceID)
	c.log.Info("member removed", "groupId", params.GroupID, "deviceId", params.DeviceID)
	return nil
}

// RenameGroup changes what a group is called, for everyone in it.
func (c *Core) RenameGroup(ctx context.Context, params ipc.RenameGroupParams) error {
	name := strings.TrimSpace(params.Name)
	if params.GroupID == "" || name == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "A group needs a name."}
	}

	_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.RenameGroup(ctx, params.GroupID, name)
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.session != nil && c.session.groupID == params.GroupID {
		c.session.groupName = name
		c.state.GroupName = name
	}
	state := c.snapshot()
	c.mu.Unlock()

	c.log.Info("group renamed", "groupId", params.GroupID, "name", name)
	c.emit(ipc.EventStateChanged, state)
	return nil
}

// SetGroupAppearance changes the icon and colour a group is shown with, for
// everyone in it. The keys are passed through: what an icon looks like is the
// app's business, not the daemon's.
func (c *Core) SetGroupAppearance(ctx context.Context, params ipc.SetGroupAppearanceParams) error {
	if params.GroupID == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "Choose a group to change."}
	}

	_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.SetGroupAppearance(ctx, params.GroupID, params.Icon, params.Color)
	})
	if err != nil {
		return err
	}

	// The connected screen wears the group's look too, so changing it while
	// that group is up has to show there rather than at the next connect.
	c.mu.Lock()
	if c.session != nil && c.session.groupID == params.GroupID {
		c.state.GroupIcon = params.Icon
		c.state.GroupColor = params.Color
	}
	state := c.snapshot()
	c.mu.Unlock()

	c.log.Info("group appearance changed", "groupId", params.GroupID, "icon", params.Icon, "color", params.Color)
	c.emit(ipc.EventStateChanged, state)
	return nil
}

// TransferOwnership hands a group to another member. The device that does it
// stops being the owner, so it is not undone without the other one agreeing.
func (c *Core) TransferOwnership(ctx context.Context, params ipc.MemberParams) error {
	if params.GroupID == "" || params.DeviceID == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "Choose who to hand the group to."}
	}

	_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.TransferOwnership(ctx, params.GroupID, params.DeviceID)
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.session != nil && c.session.groupID == params.GroupID {
		c.state.IsOwner = false
	}
	state := c.snapshot()
	c.mu.Unlock()

	c.log.Info("group ownership transferred", "groupId", params.GroupID, "to", params.DeviceID)
	c.emit(ipc.EventStateChanged, state)

	// Who owns it is on the member list, and the list is what says which row
	// wears the mark.
	go c.loadMembers(c.ctx, params.GroupID)
	return nil
}

// DeleteGroup removes a group for everyone in it.
//
// Disconnecting first is what makes the local teardown orderly. The other
// members are told by the server and disconnect the same way, but this device
// is the one holding an adapter and a socket for a group about to stop
// existing.
func (c *Core) DeleteGroup(ctx context.Context, groupID string) error {
	if groupID == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "Choose a group to delete."}
	}

	c.mu.Lock()
	active := c.session != nil && c.session.groupID == groupID
	c.mu.Unlock()

	if active {
		if err := c.Disconnect(ctx); err != nil {
			return err
		}
	}

	_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.DeleteGroup(ctx, groupID)
	})
	if err != nil {
		return err
	}

	c.log.Info("group deleted", "groupId", groupID)
	return nil
}
