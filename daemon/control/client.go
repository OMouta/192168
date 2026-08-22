// Package control talks to the coordination server.
//
// It is the only part of the daemon that knows the server exists. Everything
// above it works in terms of groups, sessions, and peers.
package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OMouta/192168/protocol"
	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/auth"
)

// requestTimeout bounds a control-plane call. Peer traffic does not go through
// here, so a slow server delays a connect attempt and nothing else.
const requestTimeout = 15 * time.Second

// Client is a connection to one server. Pointing the app at a different server
// means a new Client, since the discovery document and the token both belong to
// the one that issued them.
type Client struct {
	http      *http.Client
	discovery api.Discovery
	baseURL   string
	token     string
}

// Error is a failure the server described. Code is stable, so callers decide
// what to do from that rather than from the message or the status.
type Error struct {
	Status  int
	Code    string
	Message string

	// Err is the transport failure underneath, when there was one. It never
	// reaches the user, who gets Message. It exists so the log can say which
	// of a name that would not resolve, a refused connection, a timeout, or a
	// certificate it was, because all four read as "could not reach" and only
	// one of them is worth changing a setting over.
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("control: %s (%d): %s: %v", e.Code, e.Status, e.Message, e.Err)
	}
	return fmt.Sprintf("control: %s (%d): %s", e.Code, e.Status, e.Message)
}

// Unwrap exposes the transport failure to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// IsUnauthorized reports whether an error means the token is no longer good, so
// the daemon knows to register again rather than retrying.
func IsUnauthorized(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == api.ErrUnauthorized
}

// Discover fetches a server's capabilities and checks this client can talk to
// it. Everything else in this package needs the result, so it comes first.
func Discover(ctx context.Context, baseURL string) (*Client, error) {
	c := &Client{
		http:    &http.Client{Timeout: requestTimeout},
		baseURL: strings.TrimRight(baseURL, "/"),
	}

	var doc api.Discovery
	if err := c.do(ctx, http.MethodGet, c.baseURL+protocol.WellKnownPath, nil, &doc); err != nil {
		return nil, err
	}
	if doc.Version != protocol.DiscoveryVersion {
		return nil, &Error{
			Code: api.ErrVersionUnsupported,
			Message: fmt.Sprintf("That server speaks version %d and this app speaks version %d.",
				doc.Version, protocol.DiscoveryVersion),
		}
	}
	if doc.API == "" || doc.Realtime == "" {
		return nil, &Error{Code: api.ErrVersionUnsupported, Message: "That server did not describe itself properly."}
	}

	c.discovery = doc
	return c, nil
}

// Discovery is what the server said about itself, including the STUN servers to
// use and which optional features it has.
func (c *Client) Discovery() api.Discovery { return c.discovery }

// BaseURL is the address this client was built from.
func (c *Client) BaseURL() string { return c.baseURL }

// SetToken supplies the credential for authenticated calls.
func (c *Client) SetToken(token string) { c.token = token }

// Register creates or refreshes this device on the server and returns its
// token. The request is signed with the key it registers, which is what the
// server checks.
func (c *Client) Register(ctx context.Context, deviceID, name, publicKey, transportKey string, sign func(publicKey, transportKey string, issuedAt time.Time, nonce string) string) (string, error) {
	nonce, err := newNonce()
	if err != nil {
		return "", err
	}
	issuedAt := time.Now()

	var res api.RegisterDeviceResponse
	err = c.do(ctx, http.MethodPost, c.discovery.API+"/devices/register", api.RegisterDeviceRequest{
		DeviceID:     deviceID,
		PublicKey:    publicKey,
		TransportKey: transportKey,
		DeviceName:   name,
		IssuedAt:     issuedAt.Unix(),
		Nonce:        nonce,
		Signature:    sign(publicKey, transportKey, issuedAt, nonce),
	}, &res)
	if err != nil {
		return "", err
	}
	return res.DeviceToken, nil
}

// NewGroup is everything creating one takes. A struct because three strings in
// a row are easy to pass in the wrong order.
type NewGroup struct {
	Name  string
	Icon  string
	Color string
}

// CreateGroup creates a group and joins it. What comes back carries the invite
// code.
func (c *Client) CreateGroup(ctx context.Context, g NewGroup) (api.Membership, error) {
	var m api.Membership
	err := c.do(ctx, http.MethodPost, c.discovery.API+"/groups", api.CreateGroupRequest{
		Name:  g.Name,
		Icon:  g.Icon,
		Color: g.Color,
	}, &m)
	return m, err
}

// JoinByCode joins whichever group an invite opens.
func (c *Client) JoinByCode(ctx context.Context, code string) (api.Membership, error) {
	var m api.Membership
	err := c.do(ctx, http.MethodPost, c.discovery.API+"/groups/join-by-code", api.JoinByCodeRequest{
		Code: code,
	}, &m)
	return m, err
}

// Invite says what a code opens, without joining it. Needs no token.
func (c *Client) Invite(ctx context.Context, code string) (api.Invite, error) {
	var out api.Invite
	err := c.do(ctx, http.MethodGet, c.discovery.API+"/invites/"+url.PathEscape(code), nil, &out)
	return out, err
}

// ResetInvite replaces a group's code. The old one stops working.
func (c *Client) ResetInvite(ctx context.Context, groupID string) (string, error) {
	var out api.InviteCodeResponse
	err := c.do(ctx, http.MethodPost, c.discovery.API+"/groups/"+groupID+"/invite/reset", nil, &out)
	return out.Code, err
}

// Groups lists the groups this device belongs to.
func (c *Client) Groups(ctx context.Context) ([]api.Membership, error) {
	var out []api.Membership
	err := c.do(ctx, http.MethodGet, c.discovery.API+"/groups", nil, &out)
	return out, err
}

// Members lists everyone in a group, whether or not they are connected.
func (c *Client) Members(ctx context.Context, groupID string) ([]api.Member, error) {
	var out api.MembersResponse
	err := c.do(ctx, http.MethodGet, c.discovery.API+"/groups/"+groupID+"/members", nil, &out)
	return out.Members, err
}

// LeaveGroup gives up membership of a group.
func (c *Client) LeaveGroup(ctx context.Context, groupID string) error {
	return c.do(ctx, http.MethodDelete, c.discovery.API+"/groups/"+groupID+"/membership", nil, nil)
}

// Me is what the server says this device is called.
func (c *Client) Me(ctx context.Context) (api.Me, error) {
	var me api.Me
	err := c.do(ctx, http.MethodGet, c.discovery.API+"/me", nil, &me)
	return me, err
}

// SetNickname changes what this device is called, in every group at once.
func (c *Client) SetNickname(ctx context.Context, nickname string) error {
	return c.do(ctx, http.MethodPut, c.discovery.API+"/me/nickname", api.SetNicknameRequest{
		Nickname: nickname,
	}, nil)
}

// Connect opens a session in a group and returns the virtual IP and the peers
// already online.
func (c *Client) Connect(ctx context.Context, groupID string) (api.CreateSessionResponse, error) {
	var res api.CreateSessionResponse
	err := c.do(ctx, http.MethodPost, c.discovery.API+"/groups/"+groupID+"/sessions", nil, &res)
	return res, err
}

// PublishEndpoint tells the group where this device can be reached.
func (c *Client) PublishEndpoint(ctx context.Context, sessionID string, endpoint api.Endpoint) error {
	endpoint.Protocol = "udp"
	return c.do(ctx, http.MethodPut, c.discovery.API+"/sessions/"+sessionID+"/endpoint", endpoint, nil)
}

// Heartbeat keeps a session from expiring.
func (c *Client) Heartbeat(ctx context.Context, sessionID string) error {
	return c.do(ctx, http.MethodPost, c.discovery.API+"/sessions/"+sessionID+"/heartbeat", nil, nil)
}

// Disconnect ends a session and frees its virtual IP.
func (c *Client) Disconnect(ctx context.Context, sessionID string) error {
	return c.do(ctx, http.MethodDelete, c.discovery.API+"/sessions/"+sessionID, nil, nil)
}

func (c *Client) do(ctx context.Context, method, url string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("control: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("control: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", auth.TokenScheme+" "+c.token)
	}

	res, err := c.http.Do(req)
	if err != nil {
		// A network failure is not the server's fault and has no code, so it
		// gets one the UI can recognise. The cause is carried along rather
		// than replaced, since the friendly sentence is true of every way this
		// can fail and so says nothing about which one happened.
		return &Error{
			Code:    "unreachable",
			Message: "Could not reach the server. Check your connection or server settings.",
			Err:     err,
		}
	}
	defer res.Body.Close()

	// Bodies are small. Capping the read stops a hostile or broken server from
	// filling memory.
	payload, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("control: read response: %w", err)
	}

	if res.StatusCode >= 300 {
		return toError(res.StatusCode, payload)
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("control: parse response: %w", err)
	}
	return nil
}

func toError(status int, payload []byte) error {
	var body api.Error
	if err := json.Unmarshal(payload, &body); err != nil || body.Code == "" {
		// A server that does not speak our error shape still has to produce
		// something the UI can show.
		return &Error{Status: status, Code: api.ErrInternal, Message: "The server returned an unexpected response."}
	}
	return &Error{Status: status, Code: body.Code, Message: body.Message}
}

func newNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("control: new nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// RemoveMember takes someone out of a group and keeps them out.
func (c *Client) RemoveMember(ctx context.Context, groupID, deviceID string) error {
	return c.do(ctx, http.MethodDelete, c.discovery.API+"/groups/"+groupID+"/members/"+deviceID, nil, nil)
}

// RenameGroup changes what a group is called.
func (c *Client) RenameGroup(ctx context.Context, groupID, name string) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.do(ctx, http.MethodPut, c.discovery.API+"/groups/"+groupID+"/name", body, nil)
}

// SetGroupAppearance changes the icon and colour the group is shown with.
func (c *Client) SetGroupAppearance(ctx context.Context, groupID, icon, color string) error {
	body := api.SetGroupAppearanceRequest{Icon: icon, Color: color}
	return c.do(ctx, http.MethodPut, c.discovery.API+"/groups/"+groupID+"/appearance", body, nil)
}

// TransferOwnership hands a group to another member.
func (c *Client) TransferOwnership(ctx context.Context, groupID, deviceID string) error {
	return c.do(ctx, http.MethodPut, c.discovery.API+"/groups/"+groupID+"/owner/"+deviceID, nil, nil)
}

// DeleteGroup removes a group for everyone in it.
func (c *Client) DeleteGroup(ctx context.Context, groupID string) error {
	return c.do(ctx, http.MethodDelete, c.discovery.API+"/groups/"+groupID, nil, nil)
}
