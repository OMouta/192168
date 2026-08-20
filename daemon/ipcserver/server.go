// Package ipcserver serves the local control channel the Windows client talks
// to.
//
// The client sends intents and renders what it is told. This package is the
// transport for that: newline-delimited JSON over a named pipe, requests
// answered in order, events pushed as they happen.
//
// Message volume here is a few per second at most, so JSON costs nothing worth
// counting and being able to read a session in a log is worth a lot.
package ipcserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/OMouta/192168/protocol/ipc"
)

// eventBuffer is how many events a client can fall behind by before it is cut
// loose. A UI that cannot keep up with presence changes is not going to catch
// up, and reconnecting gets it a whole State to redraw from.
const eventBuffer = 32

// Handler is the daemon behind the pipe. Every method is a user intent, not a
// networking primitive: the client asks to connect to a group, and what that
// takes is none of its business.
type Handler interface {
	GetState(ctx context.Context) (ipc.State, error)
	GetGroups(ctx context.Context) ([]ipc.Group, error)
	CreateGroup(ctx context.Context, params ipc.CreateGroupParams) (ipc.Group, error)
	JoinGroup(ctx context.Context, params ipc.JoinGroupParams) (ipc.Group, error)
	LeaveGroup(ctx context.Context, groupID string) error
	Connect(ctx context.Context, groupID string) error
	Disconnect(ctx context.Context) error
	SetNickname(ctx context.Context, params ipc.SetNicknameParams) error
	GetServer(ctx context.Context) (string, error)
	SetServer(ctx context.Context, url string) error
	TestServer(ctx context.Context, url string) (ipc.TestServerResult, error)
	ResetSettings(ctx context.Context) (string, error)
}

// Failure is an error with a code and a message fit to show a user. Anything
// else a handler returns is treated as a bug and reported as such, so socket
// numbers and SQL text stay in the log where they belong.
type Failure struct {
	Code    string
	Message string
}

func (f *Failure) Error() string { return f.Code + ": " + f.Message }

// Server accepts client connections and fans events out to them.
type Server struct {
	handler Handler
	log     *slog.Logger

	mu      sync.Mutex
	clients map[*client]struct{}
}

type client struct {
	events chan []byte

	mu     sync.Mutex
	closed bool
}

// New creates a server around a handler.
func New(handler Handler, log *slog.Logger) *Server {
	return &Server{handler: handler, log: log, clients: map[*client]struct{}{}}
}

// Serve accepts connections until ctx is cancelled or the listener fails.
//
// More than one client can be connected at once, which is what makes a tray
// process and a window able to coexist. They all see the same events.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("ipcserver: accept: %w", err)
		}

		wg.Go(func() { s.handleConnection(ctx, conn) })
	}
}

// Broadcast pushes an event to every connected client.
func (s *Server) Broadcast(name ipc.EventName, data any) {
	payload, err := encodeEvent(name, data)
	if err != nil {
		s.log.Error("cannot encode event", "event", name, "error", err)
		return
	}

	s.mu.Lock()
	targets := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		targets = append(targets, c)
	}
	s.mu.Unlock()

	for _, c := range targets {
		if !c.deliver(payload) {
			s.log.Warn("dropping a client that fell behind")
			s.remove(c)
		}
	}
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	c := &client{events: make(chan []byte, eventBuffer)}
	s.add(c)
	defer s.remove(c)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// One writer owns the connection, so a reply and an event cannot interleave
	// halfway through a line.
	writes := make(chan []byte, eventBuffer)
	var writer sync.WaitGroup
	writer.Go(func() {
		defer cancel()
		for payload := range writes {
			if _, err := conn.Write(payload); err != nil {
				return
			}
		}
	})

	// Events and replies both go to the writer.
	forwarding := make(chan struct{})
	go func() {
		defer close(forwarding)
		for {
			select {
			case <-ctx.Done():
				return
			case payload, ok := <-c.events:
				if !ok {
					return
				}
				select {
				case writes <- payload:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	scanner := bufio.NewScanner(conn)
	// A control message is small. This is the ceiling on one line, which keeps
	// a broken client from making the daemon allocate without bound.
	scanner.Buffer(make([]byte, 4096), 1<<20)

	for scanner.Scan() {
		var req ipc.Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			s.log.Warn("unreadable request", "error", err)
			continue
		}

		reply := s.dispatch(ctx, req)
		payload, err := json.Marshal(reply)
		if err != nil {
			s.log.Error("cannot encode response", "method", req.Method, "error", err)
			continue
		}
		payload = append(payload, '\n')
		select {
		case writes <- payload:
		case <-ctx.Done():
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
		s.log.Debug("client connection ended", "error", err)
	}

	cancel()
	<-forwarding
	close(writes)
	writer.Wait()
}

// dispatch runs one request and always produces a response, since a client
// waiting on an id it never hears about would hang.
func (s *Server) dispatch(ctx context.Context, req ipc.Request) ipc.Response {
	result, err := s.call(ctx, req)
	if err != nil {
		var failure *Failure
		if errors.As(err, &failure) {
			return ipc.Response{ID: req.ID, Err: &ipc.Error{Code: failure.Code, Message: failure.Message}}
		}
		// Nothing here is fit for a user to read, so the log gets the detail
		// and the client gets something it can show.
		s.log.Error("request failed", "method", req.Method, "error", err)
		return ipc.Response{ID: req.ID, Err: &ipc.Error{
			Code:    "internal",
			Message: "Something went wrong. Check the logs for details.",
		}}
	}

	res := ipc.Response{ID: req.ID, OK: true}
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			s.log.Error("cannot encode result", "method", req.Method, "error", err)
			return ipc.Response{ID: req.ID, Err: &ipc.Error{Code: "internal", Message: "Something went wrong."}}
		}
		res.Result = encoded
	}
	return res
}

func (s *Server) call(ctx context.Context, req ipc.Request) (any, error) {
	switch req.Method {
	case ipc.MethodGetState:
		return s.handler.GetState(ctx)

	case ipc.MethodGetGroups:
		groups, err := s.handler.GetGroups(ctx)
		if err != nil {
			return nil, err
		}
		return ipc.GetGroupsResult{Groups: groups}, nil

	case ipc.MethodCreateGroup:
		var params ipc.CreateGroupParams
		if err := req.UnmarshalParams(&params); err != nil {
			return nil, badParams(err)
		}
		group, err := s.handler.CreateGroup(ctx, params)
		if err != nil {
			return nil, err
		}
		return ipc.GroupResult{Group: group}, nil

	case ipc.MethodJoinGroup:
		var params ipc.JoinGroupParams
		if err := req.UnmarshalParams(&params); err != nil {
			return nil, badParams(err)
		}
		group, err := s.handler.JoinGroup(ctx, params)
		if err != nil {
			return nil, err
		}
		return ipc.GroupResult{Group: group}, nil

	case ipc.MethodLeaveGroup:
		var params ipc.GroupParams
		if err := req.UnmarshalParams(&params); err != nil {
			return nil, badParams(err)
		}
		return nil, s.handler.LeaveGroup(ctx, params.GroupID)

	case ipc.MethodConnect:
		var params ipc.GroupParams
		if err := req.UnmarshalParams(&params); err != nil {
			return nil, badParams(err)
		}
		return nil, s.handler.Connect(ctx, params.GroupID)

	case ipc.MethodDisconnect:
		return nil, s.handler.Disconnect(ctx)

	case ipc.MethodSetNickname:
		var params ipc.SetNicknameParams
		if err := req.UnmarshalParams(&params); err != nil {
			return nil, badParams(err)
		}
		return nil, s.handler.SetNickname(ctx, params)

	case ipc.MethodGetServer:
		url, err := s.handler.GetServer(ctx)
		if err != nil {
			return nil, err
		}
		return ipc.GetServerResult{URL: url}, nil

	case ipc.MethodSetServer:
		var params ipc.ServerParams
		if err := req.UnmarshalParams(&params); err != nil {
			return nil, badParams(err)
		}
		return nil, s.handler.SetServer(ctx, params.URL)

	case ipc.MethodTestServer:
		var params ipc.ServerParams
		if err := req.UnmarshalParams(&params); err != nil {
			return nil, badParams(err)
		}
		return s.handler.TestServer(ctx, params.URL)

	case ipc.MethodResetSettings:
		url, err := s.handler.ResetSettings(ctx)
		if err != nil {
			return nil, err
		}
		return ipc.GetServerResult{URL: url}, nil

	default:
		// A newer client against an older daemon lands here, so it has to be a
		// clear answer rather than silence.
		return nil, &Failure{
			Code:    "unknown_method",
			Message: fmt.Sprintf("This version of the app does not support %q.", req.Method),
		}
	}
}

func badParams(err error) error {
	return &Failure{Code: "bad_request", Message: "The request could not be read: " + err.Error()}
}

func (s *Server) add(c *client) {
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) remove(c *client) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
	c.close()
}

func (c *client) deliver(payload []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.events <- payload:
		return true
	default:
		return false
	}
}

func (c *client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.events)
	}
}

// encodeEvent returns a complete line, terminator included. Broadcast hands the
// same payload to every connected client, so the newline has to be baked in
// here rather than appended by each writer to a slice it does not own.
func encodeEvent(name ipc.EventName, data any) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	line, err := json.Marshal(ipc.Event{Event: name, Data: raw})
	if err != nil {
		return nil, err
	}
	return append(line, '\n'), nil
}
