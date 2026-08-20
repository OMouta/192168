package core

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/OMouta/192168/daemon/control"
	"github.com/OMouta/192168/daemon/ipcserver"
	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/ipc"
)

// heartbeatInterval keeps the session alive well inside the server's timeout,
// so one lost heartbeat does not look like a machine that went away.
const heartbeatInterval = 30 * time.Second

// Connect joins a group's virtual network.
//
// It returns as soon as the attempt has started rather than when it finishes.
// Discovery, registration, and the session call all involve a server that might
// be slow, and a UI that blocks on that has nothing to show for the wait. The
// result arrives as an event.
func (c *Core) Connect(ctx context.Context, groupID string) error {
	if groupID == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "Choose a group to connect to."}
	}

	c.mu.Lock()
	switch {
	case c.state.Connection == ipc.StateConnecting:
		c.mu.Unlock()
		return &ipcserver.Failure{Code: "busy", Message: "Already connecting. Give it a moment."}
	case c.session != nil && c.session.groupID == groupID:
		c.mu.Unlock()
		return nil
	}
	alreadyConnected := c.session != nil
	c.mu.Unlock()

	// Only one group can be active, so switching means a full teardown first.
	// Two virtual networks at once would fight over the local routes.
	if alreadyConnected {
		if err := c.Disconnect(ctx); err != nil {
			return err
		}
	}

	c.mu.Lock()
	c.state.Connection = ipc.StateConnecting
	c.state.GroupID = groupID
	c.state.Message = ""
	state := c.snapshot()
	c.mu.Unlock()
	c.emit(ipc.EventStateChanged, state)

	go c.runConnect(groupID)
	return nil
}

func (c *Core) runConnect(groupID string) {
	// This work belongs to the daemon, not to the request that asked for it.
	ctx, stop := context.WithCancel(c.ctx)

	session, err := withClient(c, ctx, func(client *control.Client) (api.CreateSessionResponse, error) {
		return client.Connect(ctx, groupID)
	})
	if err != nil {
		stop()
		c.failConnect(groupID, err)
		return
	}

	groupName, nickname := c.describeGroup(ctx, groupID)

	c.mu.Lock()
	c.session = &activeSession{
		sessionID: session.SessionID,
		groupID:   groupID,
		groupName: groupName,
		nickname:  nickname,
		virtualIP: session.VirtualIP,
		stop:      stop,
		done:      make(chan struct{}),
	}
	c.state.Connection = ipc.StateConnected
	c.state.GroupID = groupID
	c.state.GroupName = groupName
	c.state.Nickname = nickname
	c.state.VirtualIP = session.VirtualIP
	c.state.Message = ""

	// Peers start as connecting and are in the state before it is announced, so
	// the first thing the UI draws already has the rows. The mesh moves each one
	// to direct or failed as its link resolves.
	c.peers = map[string]*ipc.PeerView{}
	for _, peer := range session.Peers {
		c.peers[peer.DeviceID] = &ipc.PeerView{
			DeviceID:  peer.DeviceID,
			Nickname:  peer.Nickname,
			VirtualIP: peer.VirtualIP,
			State:     ipc.PeerConnecting,
		}
	}
	active := c.session
	done := c.session.done
	snapshot := c.snapshot()
	c.mu.Unlock()

	c.log.Info("connected", "groupId", groupID, "sessionId", session.SessionID, "virtualIp", session.VirtualIP)

	c.emit(ipc.EventGroupConnected, ipc.GroupConnectedData{
		GroupID:   groupID,
		GroupName: groupName,
		VirtualIP: session.VirtualIP,
	})
	c.emit(ipc.EventStateChanged, snapshot)

	go c.keepAlive(ctx, session.SessionID, done)

	// The socket, our public address, and a link attempt toward everyone who is
	// already here. A group is joined whether or not any of that works.
	c.startNetwork(ctx, active, session.Peers)
}

// failConnect reports a connect that did not happen and puts the state back.
func (c *Core) failConnect(groupID string, err error) {
	message := "Could not connect to that group."
	var failure *ipcserver.Failure
	if errors.As(err, &failure) {
		message = failure.Message
	}

	c.mu.Lock()
	c.state.Connection = ipc.StateDisconnected
	c.state.GroupID = ""
	c.state.GroupName = ""
	c.state.VirtualIP = ""
	c.state.Message = message
	c.peers = map[string]*ipc.PeerView{}
	state := c.snapshot()
	c.mu.Unlock()

	c.log.Warn("connect failed", "groupId", groupID, "error", err)
	c.emit(ipc.EventStateChanged, state)
}

// keepAlive tells the server this session is still here. Missing enough of
// these is how the server decides a machine went away, so losing the session
// means the connection is over.
func (c *Core) keepAlive(ctx context.Context, sessionID string, done chan struct{}) {
	defer close(done)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
				return struct{}{}, client.Heartbeat(ctx, sessionID)
			})
			if err == nil {
				continue
			}
			if ctx.Err() != nil {
				return
			}

			var failure *ipcserver.Failure
			if errors.As(err, &failure) && failure.Code == api.ErrSessionInvalid {
				c.log.Warn("the session is gone, disconnecting", "sessionId", sessionID)
				go c.dropSession("The connection to the group was lost.")
				return
			}
			// Anything else is the server being unreachable, which is not a
			// reason to tear down. Established tunnels outlive a server outage,
			// so this keeps trying.
			c.log.Info("heartbeat failed, will retry", "sessionId", sessionID, "error", err)
		}
	}
}

// Disconnect leaves the active group. It is safe to call when nothing is
// connected, which matters because it runs on the way into several other things.
func (c *Core) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	session := c.session
	c.session = nil
	if session == nil {
		c.mu.Unlock()
		return nil
	}
	c.state.Connection = ipc.StateDisconnecting
	c.mu.Unlock()

	// Stop the heartbeat before telling the server, so it cannot revive a
	// session that is being closed.
	session.stop()
	<-session.done

	// Best effort. The server expires a session nobody heartbeats anyway, so a
	// failure here costs a delay rather than a stuck session.
	if _, err := withClient(c, ctx, func(client *control.Client) (struct{}, error) {
		return struct{}{}, client.Disconnect(ctx, session.sessionID)
	}); err != nil {
		c.log.Info("could not close the session cleanly", "sessionId", session.sessionID, "error", err)
	}

	c.finishDisconnect(session, "")
	return nil
}

// dropSession ends a session the daemon decided is over, as opposed to one the
// user asked to leave.
func (c *Core) dropSession(reason string) {
	c.mu.Lock()
	session := c.session
	c.session = nil
	c.mu.Unlock()

	if session == nil {
		return
	}
	session.stop()
	c.finishDisconnect(session, reason)
}

func (c *Core) finishDisconnect(session *activeSession, reason string) {
	c.mu.Lock()
	links := c.mesh
	c.mesh = nil
	c.state.Connection = ipc.StateDisconnected
	c.state.GroupID = ""
	c.state.GroupName = ""
	c.state.VirtualIP = ""
	c.state.Message = reason
	c.peers = map[string]*ipc.PeerView{}
	state := c.snapshot()
	c.mu.Unlock()

	// The socket goes with the session. Leaving it open would keep NAT
	// mappings alive for a group this device has left.
	if links != nil {
		links.Close()
	}

	c.log.Info("disconnected", "groupId", session.groupID, "reason", reason)
	c.emit(ipc.EventGroupDisconnected, ipc.GroupDisconnectedData{
		GroupID: session.groupID,
		Reason:  reason,
	})
	c.emit(ipc.EventStateChanged, state)
}

// describeGroup finds the name and nickname for a group. The session response
// does not carry them, and the UI needs something to put in its title.
func (c *Core) describeGroup(ctx context.Context, groupID string) (name, nickname string) {
	memberships, err := withClient(c, ctx, func(client *control.Client) ([]api.Membership, error) {
		return client.Groups(ctx)
	})
	if err != nil {
		return groupID, ""
	}
	for _, m := range memberships {
		if m.GroupID == groupID {
			return m.GroupName, m.Nickname
		}
	}
	return groupID, ""
}

// sortPeers keeps the list in a stable order, so a UI redraw does not shuffle
// rows under someone's cursor.
func sortPeers(peers []ipc.PeerView) {
	slices.SortFunc(peers, func(a, b ipc.PeerView) int {
		if a.VirtualIP != b.VirtualIP {
			if a.VirtualIP < b.VirtualIP {
				return -1
			}
			return 1
		}
		return 0
	})
}
