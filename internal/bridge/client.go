package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

var ErrClientQueueFull = errors.New("client RAM queue limit reached")

const (
	clientMaxQueueMessages  = 256
	clientMaxQueueBytes     = 2 * 1024 * 1024
	clientHeartbeatInterval = 5 * time.Second
	clientHeartbeatTimeout  = 15 * time.Second
)

type Client struct {
	endpoint   string
	instanceID string
	role       protocol.Role
	senderID   string

	mu            sync.Mutex
	writeMu       sync.Mutex
	conn          net.Conn
	resumeToken   string
	initialToken  string
	clientSeq     uint64
	lastServerSeq uint64
	confirmedSeq  map[uint64]bool
	// ackedPending tracks remote (message_id -> server_seq) pairs this client
	// already sent an ACK for but has not yet seen ack_confirmed. The server
	// replays an unconfirmed remote message on every reconnect; if the
	// connection drops between our ACK and the server's confirmation, the
	// EventHub dedupes the replayed FrameMessage and no new "received" event
	// fires, so nothing would re-ACK it without this.
	ackedPending map[string]uint64
	closed       bool
	pending      map[string]protocol.Envelope
	pendingOrder []string
	pendingBytes int
	closedCh     chan struct{}
	// publishMu makes the message_id idempotency check and its registration
	// one step, so concurrent retries cannot fork one report into two.
	publishMu sync.Mutex

	hub *EventHub
}

func Dial(ctx context.Context, endpoint, instanceID string, role protocol.Role, token string) (*Client, protocol.Frame, error) {
	c := &Client{
		endpoint:     endpoint,
		instanceID:   instanceID,
		role:         role,
		senderID:     protocol.ExpectedSenderID(role),
		initialToken: token,
		pending:      make(map[string]protocol.Envelope),
		confirmedSeq: make(map[uint64]bool),
		ackedPending: make(map[string]uint64),
		closedCh:     make(chan struct{}),
		hub:          NewEventHub(DefaultEventHubOptions()),
	}
	welcome, err := c.connect(ctx, token)
	if err != nil {
		return nil, protocol.Frame{}, err
	}
	return c, welcome, nil
}

// EventHub is the single multiplexed event source shared by the TUI and local
// control subscribers. A subscriber never steals frames from another consumer.
func (c *Client) EventHub() *EventHub { return c.hub }

func (c *Client) Subscribe(afterEventSeq uint64) (*Subscription, error) {
	return c.hub.Subscribe(afterEventSeq)
}

func (c *Client) ReadEvents(afterEventSeq uint64, limit int) ([]Event, uint64, bool, error) {
	return c.hub.Read(afterEventSeq, limit)
}

// Done closes when the client is explicitly closed, allowing UI consumers to
// stop waiting without leaving a goroutine blocked on a subscription.
func (c *Client) Done() <-chan struct{} { return c.closedCh }

func (c *Client) Role() protocol.Role { return c.role }

func (c *Client) InstanceID() string { return c.instanceID }

func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && !c.closed
}

func (c *Client) LastServerSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastServerSeq
}

func (c *Client) connect(ctx context.Context, token string) (protocol.Frame, error) {
	dialer := net.Dialer{Timeout: 8 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", c.endpoint)
	if err != nil {
		return protocol.Frame{}, err
	}
	encoder := json.NewEncoder(conn)
	hello := protocol.Frame{
		Type:            protocol.FrameHello,
		ProtocolVersion: protocol.Version,
		InstanceID:      c.instanceID,
		Role:            c.role,
		Token:           token,
		LastServerSeq:   c.LastServerSeq(),
	}
	if err := encoder.Encode(hello); err != nil {
		_ = conn.Close()
		return protocol.Frame{}, err
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), protocol.MaxFrameBytes)
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	if !scanner.Scan() {
		_ = conn.Close()
		return protocol.Frame{}, errors.New("server closed during handshake")
	}
	var welcome protocol.Frame
	if err := json.Unmarshal(scanner.Bytes(), &welcome); err != nil {
		_ = conn.Close()
		return protocol.Frame{}, fmt.Errorf("decode welcome: %w", err)
	}
	if welcome.Type == protocol.FrameError {
		_ = conn.Close()
		detail := welcome.Detail
		if welcome.Code != "" {
			detail = welcome.Code + ": " + detail
		}
		return protocol.Frame{}, fmt.Errorf("server rejected connection: %s", detail)
	}
	if welcome.Type != protocol.FrameWelcome || welcome.InstanceID != c.instanceID {
		_ = conn.Close()
		return protocol.Frame{}, errors.New("invalid welcome frame")
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return protocol.Frame{}, errors.New("client is closed")
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = conn
	_ = conn.SetReadDeadline(time.Time{})
	if welcome.ReconnectToken != "" {
		c.resumeToken = welcome.ReconnectToken
	}
	c.mu.Unlock()

	go c.readLoop(scanner, conn)
	go c.heartbeatLoop(conn)
	c.emit(welcome)
	c.flushPending()
	return welcome, nil
}

func (c *Client) readLoop(scanner *bufio.Scanner, conn net.Conn) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(clientHeartbeatTimeout))
		if !scanner.Scan() {
			break
		}
		var frame protocol.Frame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			c.emit(protocol.Frame{Type: protocol.FrameTransportError, Detail: "invalid server frame"})
			continue
		}
		if frame.Type == protocol.FramePing {
			_ = c.writeOnConn(conn, protocol.Frame{Type: protocol.FramePong, InstanceID: c.instanceID, PingID: frame.PingID})
			continue
		}
		if frame.Type == protocol.FramePong {
			continue
		}
		// A raw FrameMessage is not a delivery confirmation. The replay cursor
		// advances only on a server-confirmed acceptance (for our own message)
		// or a server-confirmed ACK (for a remote message), and only across
		// contiguous server sequences so an earlier unacked message is never
		// skipped.
		if frame.Type == protocol.FrameMessage && frame.Envelope != nil && frame.Envelope.SenderRole == c.role {
			// Replayed own messages were already accepted by this instance. They
			// are rendered locally without sending an ACK to the server.
			c.confirmServerSeq(frame.Envelope.ServerSeq)
		}
		if frame.Type == protocol.FrameMessage && frame.Envelope != nil && frame.Envelope.SenderRole != c.role {
			// A replayed remote message we already ACKed (its ack_confirmed was
			// lost with the previous connection) gets re-ACKed automatically, so
			// the sender still reaches "delivered" without a second manual Ack.
			c.resendAckIfPending(conn, *frame.Envelope)
		}
		if (frame.Type == protocol.FrameAccepted || frame.Type == protocol.FrameAckConfirmed) && frame.ServerSeq > 0 {
			c.confirmServerSeq(frame.ServerSeq)
		}
		if frame.Type == protocol.FrameAckConfirmed {
			c.mu.Lock()
			delete(c.ackedPending, frame.MessageID)
			c.mu.Unlock()
		}
		if frame.Type == protocol.FrameAccepted {
			c.clearPending(frame.MessageID)
		}
		if frame.Type == protocol.FrameError && frame.MessageID != "" {
			c.clearPending(frame.MessageID)
		}
		c.emit(frame)
		if frame.Type == protocol.FrameClose {
			c.mu.Lock()
			if c.conn == conn {
				c.conn = nil
			}
			c.mu.Unlock()
			return
		}
	}

	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		c.emit(protocol.Frame{Type: protocol.FrameTransportError, Detail: "connection lost; retrying"})
	}
	_ = conn.Close()
}

func (c *Client) confirmServerSeq(serverSeq uint64) {
	c.mu.Lock()
	if c.confirmedSeq == nil {
		c.confirmedSeq = make(map[uint64]bool)
	}
	c.confirmedSeq[serverSeq] = true
	for c.confirmedSeq[c.lastServerSeq+1] {
		delete(c.confirmedSeq, c.lastServerSeq+1)
		c.lastServerSeq++
	}
	c.mu.Unlock()
}

func (c *Client) heartbeatLoop(conn net.Conn) {
	ticker := time.NewTicker(clientHeartbeatInterval)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		active := !c.closed && c.conn == conn
		c.mu.Unlock()
		if !active {
			return
		}
		pingID, err := randomToken(8)
		if err != nil || c.writeOnConn(conn, protocol.Frame{Type: protocol.FramePing, InstanceID: c.instanceID, PingID: pingID}) != nil {
			_ = conn.Close()
			return
		}
	}
}

// emit never blocks the TCP reader: the EventHub fans out through bounded,
// per-subscriber queues instead of one shared channel.
func (c *Client) emit(frame protocol.Frame) {
	c.hub.PublishFrame(frame)
}

func (c *Client) Reconnect(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("client is closed")
	}
	if c.conn != nil {
		c.mu.Unlock()
		return nil
	}
	token := c.resumeToken
	if c.role == protocol.RoleOrchestrator {
		token = c.initialToken
	}
	c.mu.Unlock()
	_, err := c.connect(ctx, token)
	return err
}

func (c *Client) Publish(body string) (protocol.Envelope, error) {
	messageID, err := randomToken(16)
	if err != nil {
		return protocol.Envelope{}, err
	}
	return c.PublishWithID(messageID, body)
}

// PublishWithID is the idempotent local-control publishing path. Reusing a
// message_id with the same body returns the original envelope; changing the
// body is rejected so retries cannot silently fork a report.
func (c *Client) PublishWithID(messageID, body string) (protocol.Envelope, error) {
	if messageID == "" {
		return protocol.Envelope{}, errors.New("message_id is required")
	}
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	if existing, ok := c.hub.Envelope(messageID); ok {
		if existing.Body == body {
			return existing, nil
		}
		return protocol.Envelope{}, errors.New("message_id already exists with different body")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return protocol.Envelope{}, errors.New("client is closed")
	}
	c.clientSeq++
	seq := c.clientSeq
	conn := c.conn
	c.mu.Unlock()
	e, err := protocol.NewEnvelope(c.instanceID, messageID, seq, c.senderID, c.role, body, time.Now())
	if err != nil {
		return protocol.Envelope{}, err
	}
	c.mu.Lock()
	if len(c.pending) >= clientMaxQueueMessages || c.pendingBytes+len([]byte(body)) > clientMaxQueueBytes {
		c.mu.Unlock()
		return protocol.Envelope{}, ErrClientQueueFull
	}
	c.pending[e.MessageID] = e
	c.pendingOrder = append(c.pendingOrder, e.MessageID)
	c.pendingBytes += len([]byte(body))
	c.mu.Unlock()
	c.hub.RememberEnvelope(e)
	c.hub.PublishFrame(protocol.Frame{Type: protocol.FrameMessage, InstanceID: c.instanceID, Envelope: &e})
	if conn != nil {
		_ = c.write(protocol.Frame{Type: protocol.FramePublish, InstanceID: c.instanceID, Envelope: &e})
	}
	return e, nil
}

func (c *Client) flushPending() {
	c.mu.Lock()
	ids := append([]string(nil), c.pendingOrder...)
	c.mu.Unlock()
	for _, id := range ids {
		c.mu.Lock()
		e, ok := c.pending[id]
		c.mu.Unlock()
		if !ok {
			continue
		}
		if err := c.write(protocol.Frame{Type: protocol.FramePublish, InstanceID: c.instanceID, Envelope: &e}); err != nil {
			return
		}
	}
}

func (c *Client) clearPending(messageID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.pending[messageID]
	if !ok {
		return
	}
	delete(c.pending, messageID)
	c.pendingBytes -= len([]byte(e.Body))
	for i, id := range c.pendingOrder {
		if id == messageID {
			c.pendingOrder = append(c.pendingOrder[:i], c.pendingOrder[i+1:]...)
			break
		}
	}
}

func (c *Client) QueueStats() (messages int, bytes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending), c.pendingBytes
}

func (c *Client) Ack(messageID string, serverSeq uint64) error {
	c.mu.Lock()
	if c.ackedPending == nil {
		c.ackedPending = make(map[string]uint64)
	}
	c.ackedPending[messageID] = serverSeq
	c.mu.Unlock()
	return c.write(protocol.Frame{Type: protocol.FrameAck, InstanceID: c.instanceID, MessageID: messageID, ServerSeq: serverSeq})
}

// resendAckIfPending re-sends the ACK for a remote message this client
// already acknowledged but has not yet seen confirmed, matching it strictly
// on (message_id, server_seq) so an unrelated message never gets ACKed.
func (c *Client) resendAckIfPending(conn net.Conn, e protocol.Envelope) {
	c.mu.Lock()
	seq, pending := c.ackedPending[e.MessageID]
	c.mu.Unlock()
	if !pending || seq != e.ServerSeq {
		return
	}
	_ = c.writeOnConn(conn, protocol.Frame{Type: protocol.FrameAck, InstanceID: c.instanceID, MessageID: e.MessageID, ServerSeq: seq})
}

func (c *Client) write(frame protocol.Frame) error {
	c.mu.Lock()
	conn := c.conn
	closed := c.closed
	c.mu.Unlock()
	if closed || conn == nil {
		return errors.New("not connected")
	}
	return c.writeOnConn(conn, frame)
}

func (c *Client) writeOnConn(conn net.Conn, frame protocol.Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(clientHeartbeatTimeout))
	if err := json.NewEncoder(conn).Encode(frame); err != nil {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
		_ = conn.Close()
		return err
	}
	return nil
}

func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	conn := c.conn
	c.conn = nil
	close(c.closedCh)
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	c.hub.Close()
}
