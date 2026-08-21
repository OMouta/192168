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

	"github.com/OMouta/192168/daemon/control"
	"github.com/OMouta/192168/daemon/identity"
	"github.com/OMouta/192168/daemon/ipcserver"
	"github.com/OMouta/192168/daemon/mesh"
	"github.com/OMouta/192168/daemon/tun"
	"github.com/OMouta/192168/protocol/api"
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

	mu      sync.Mutex
	client  *control.Client
	state   ipc.State
	peers   map[string]*ipc.PeerView
	session *activeSession

	// mesh is the peer-to-peer socket, alive only while a group is connected.
	mesh *mesh.Mesh

	// device is the virtual adapter, alive for the same span.
	device *tun.Device
}

// activeSession is the group connection. There is at most one, because two
// virtual networks at once would fight over local routes.
type activeSession struct {
	sessionID string
	groupID   string
	groupName string
	nickname  string
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
	return &Core{
		log:           log,
		identity:      id,
		settings:      set,
		events:        nopEvents{},
		defaultServer: defaultServer,
		ctx:           ctx,
		cancel:        cancel,
		peers:         map[string]*ipc.PeerView{},
		state: ipc.State{
			Connection: ipc.StateDisconnected,
			ServerURL:  set.ServerURL,
			Peers:      []ipc.PeerView{},
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
		groups = append(groups, toGroup(m, c.session != nil && c.session.groupID == m.GroupID))
	}
	return groups, nil
}

// CreateGroup makes a group and joins it. It does not connect: the user may
// want to invite people before anyone is online.
func (c *Core) CreateGroup(ctx context.Context, params ipc.CreateGroupParams) (ipc.Group, error) {
	if params.Name == "" || params.Password == "" || params.Nickname == "" {
		return ipc.Group{}, &ipcserver.Failure{
			Code:    "bad_request",
			Message: "A group needs a name, a password, and a nickname.",
		}
	}

	membership, err := withClient(c, ctx, func(client *control.Client) (api.Membership, error) {
		return client.CreateGroup(ctx, control.NewGroup{
			Name:     params.Name,
			Password: params.Password,
			Nickname: params.Nickname,
			Icon:     params.Icon,
			Color:    params.Color,
		})
	})
	if err != nil {
		return ipc.Group{}, err
	}
	c.log.Info("group created", "groupId", membership.GroupID)
	return toGroup(membership, false), nil
}

// JoinGroup joins an existing group. The password is used once here and never
// stored, since the device token is what gets it back in afterwards.
func (c *Core) JoinGroup(ctx context.Context, params ipc.JoinGroupParams) (ipc.Group, error) {
	if params.Group == "" || params.Password == "" || params.Nickname == "" {
		return ipc.Group{}, &ipcserver.Failure{
			Code:    "bad_request",
			Message: "Joining needs a group, a password, and a nickname.",
		}
	}

	membership, err := withClient(c, ctx, func(client *control.Client) (api.Membership, error) {
		return client.JoinGroup(ctx, params.Group, params.Password, params.Nickname)
	})
	if err != nil {
		return ipc.Group{}, err
	}
	c.log.Info("group joined", "groupId", membership.GroupID)
	return toGroup(membership, false), nil
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

// SetNickname changes this device's name in one group.
func (c *Core) SetNickname(ctx context.Context, params ipc.SetNicknameParams) error {
	if params.Nickname == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "That nickname will not work."}
	}

	_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.SetNickname(ctx, params.GroupID, params.Nickname)
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.session != nil && c.session.groupID == params.GroupID {
		c.session.nickname = params.Nickname
		c.state.Nickname = params.Nickname
	}
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

func toGroup(m api.Membership, active bool) ipc.Group {
	return ipc.Group{
		GroupID:  m.GroupID,
		Name:     m.GroupName,
		Icon:     m.GroupIcon,
		Color:    m.GroupColor,
		Nickname: m.Nickname,
		Active:   active,
		IsOwner:  m.Role == api.RoleOwner,
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
// everyone in it.
//
// The keys are passed through rather than checked against a list. What an icon
// looks like is the app's business, and a daemon that had to know every one of
// them would have to be updated before the app could offer a new one.
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

// SetGroupPassword changes the password a new member joins with.
//
// It removes nobody, and is not meant to. Once somebody is in, the device token
// is what proves it, and the password is only ever checked at the door.
func (c *Core) SetGroupPassword(ctx context.Context, params ipc.SetGroupPasswordParams) error {
	if params.GroupID == "" || params.Password == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "That password will not work."}
	}

	// The proof is derived from the group's name, so the name has to be the one
	// the server has rather than whatever the screen last showed.
	name := c.describeGroup(ctx, params.GroupID).GroupName

	_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.SetGroupPassword(ctx, params.GroupID, name, params.Password)
	})
	if err != nil {
		return err
	}

	c.log.Info("group password changed", "groupId", params.GroupID)
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
