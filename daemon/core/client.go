package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/OMouta/192168/daemon/config"
	"github.com/OMouta/192168/daemon/control"
	"github.com/OMouta/192168/daemon/ipcserver"
	"github.com/OMouta/192168/protocol/auth"
	"github.com/OMouta/192168/protocol/ipc"
)

// withClient runs an authenticated call, setting up the server connection if
// this is the first one.
//
// A token the server no longer accepts is retried once after registering again.
// That covers a self-hosted instance whose database was reset, which should not
// need a reinstall to recover from.
func withClient[T any](c *Core, ctx context.Context, call func(*control.Client) (T, error)) (T, error) {
	var zero T

	client, err := c.ensureClient(ctx)
	if err != nil {
		return zero, err
	}

	result, err := call(client)
	if err == nil {
		c.setServerOnline(true)
		return result, nil
	}

	if control.IsUnauthorized(err) {
		c.log.Info("the server rejected our token, registering again")
		if err := c.identity.ClearToken(); err != nil {
			return zero, err
		}
		c.mu.Lock()
		c.client = nil
		c.mu.Unlock()

		client, err = c.ensureClient(ctx)
		if err != nil {
			return zero, err
		}
		if result, err = call(client); err == nil {
			c.setServerOnline(true)
			return result, nil
		}
	}

	c.setServerOnline(!isUnreachable(err))
	return zero, toFailure(err)
}

// ensureClient returns a client for the configured server, discovering and
// registering as needed.
func (c *Core) ensureClient(ctx context.Context) (*control.Client, error) {
	c.mu.Lock()
	existing, url := c.client, c.settings.ServerURL
	c.mu.Unlock()

	if existing != nil && existing.BaseURL() == url {
		return existing, nil
	}
	if url == "" {
		return nil, &ipcserver.Failure{
			Code:    "no_server",
			Message: "No server is set. Choose one in Settings.",
		}
	}

	client, err := control.Discover(ctx, url)
	if err != nil {
		c.setServerOnline(false)
		return nil, toFailure(err)
	}

	token := c.identity.Token
	if !c.identity.RegisteredWith(url) {
		token, err = client.Register(ctx, c.identity.DeviceID, c.identity.Name,
			c.identity.PublicKey(), c.identity.TransportKey(), c.signRegistration)
		if err != nil {
			c.setServerOnline(!isUnreachable(err))
			return nil, toFailure(err)
		}
		if err := c.identity.SetToken(url, token); err != nil {
			return nil, err
		}
		c.log.Info("device registered", "serverUrl", url, "deviceId", c.identity.DeviceID)
	}
	client.SetToken(token)

	c.mu.Lock()
	c.client = client
	c.mu.Unlock()
	c.setServerOnline(true)

	return client, nil
}

func (c *Core) signRegistration(publicKey, transportKey string, issuedAt time.Time, nonce string) string {
	return auth.SignRegister(c.identity.Signing, c.identity.DeviceID, publicKey, transportKey, issuedAt, nonce)
}

// setServerOnline records whether the server is answering and tells the UI when
// that changes. Losing the server does not drop a connection, so this is a
// status line rather than a failure.
func (c *Core) setServerOnline(online bool) {
	c.mu.Lock()
	changed := c.state.ServerOnline != online
	c.state.ServerOnline = online
	c.mu.Unlock()

	if changed {
		c.emit(ipc.EventServerConnectionChanged, ipc.ServerConnectionChangedData{Online: online})
	}
}

// toFailure turns an error into something the UI can show. A control error
// already carries a stable code and a message written for a person, so it
// passes straight through.
func toFailure(err error) error {
	var e *control.Error
	if errors.As(err, &e) {
		return &ipcserver.Failure{Code: e.Code, Message: e.Message}
	}
	return err
}

func isUnreachable(err error) bool {
	var e *control.Error
	return errors.As(err, &e) && e.Code == "unreachable"
}

// validateServerURL rejects addresses the daemon will not talk to, with a
// reason the user can act on.
func validateServerURL(url string) error {
	if url == "" {
		return &ipcserver.Failure{Code: "bad_request", Message: "Enter a server address."}
	}
	if err := config.ValidateServerURL(url); err != nil {
		return &ipcserver.Failure{
			Code:    "bad_request",
			Message: fmt.Sprintf("That address will not work: %s", err),
		}
	}
	return nil
}
