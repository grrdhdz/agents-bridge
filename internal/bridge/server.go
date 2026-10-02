// Package bridge implements one isolated, RAM-only bridge instance.
package bridge

import (
	"bufio"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

var (
	ErrClosed           = errors.New("bridge instance is closed")
	ErrAlreadyBound     = errors.New("role is already connected")
	ErrWorkerConnected  = errors.New("cannot rotate pairing while executor is connected")
	ErrInvalidPairing   = errors.New("invalid or expired pairing token")
	ErrInstanceMismatch = errors.New("instance_id does not match this bridge instance")
	ErrBufferFull       = errors.New("instance memory limit reached")
	ErrMessageConflict  = errors.New("message_id already exists with different body")
)

type Options struct {
	MaxMessages       int
	MaxBytes          int
	MaxFrame          int
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
}

func DefaultOptions() Options {
	return Options{
		MaxMessages:       protocol.MaxHistoryMessages,
		MaxBytes:          protocol.MaxHistoryBytes,
		MaxFrame:          protocol.MaxFrameBytes,
		HeartbeatInterval: 5 * time.Second,
		HeartbeatTimeout:  15 * time.Second,
	}
}

type Server struct {
	mu sync.Mutex

	listener net.Listener
	options  Options

	instanceID  string
	ownerToken  string
	joinToken   string
	resumeToken string

	owner  *peer
	worker *peer

	history   []protocol.Envelope
	byMessage map[string]protocol.Envelope
	delivered map[string]map[protocol.Role]bool
	nextSeq   uint64
	closed    bool
	closeOnce sync.Once
	done      chan struct{}
}

type peer struct {
	role         protocol.Role
	conn         net.Conn
	out          chan protocol.Frame
	done         chan struct{}
	once         sync.Once
	writeMu      sync.Mutex
	writeTimeout time.Duration
	activityMu   sync.Mutex
	lastActivity time.Time
}

func NewServer(bindHost string, options Options) (*Server, error) {
	defaults := DefaultOptions()
	if options.MaxMessages <= 0 {
		options.MaxMessages = defaults.MaxMessages
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = defaults.MaxBytes
	}
	if options.MaxFrame <= 0 {
		options.MaxFrame = defaults.MaxFrame
	}
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = defaults.HeartbeatInterval
	}
	if options.HeartbeatTimeout <= options.HeartbeatInterval {
		options.HeartbeatTimeout = defaults.HeartbeatTimeout
		if options.HeartbeatTimeout <= options.HeartbeatInterval {
			options.HeartbeatTimeout = options.HeartbeatInterval * 3
		}
	}
	instanceID, err := randomToken(16)
	if err != nil {
		return nil, fmt.Errorf("create instance_id: %w", err)
	}
	ownerToken, err := randomToken(20)
	if err != nil {
		return nil, fmt.Errorf("create owner token: %w", err)
	}
	joinToken, err := randomToken(20)
	if err != nil {
		return nil, fmt.Errorf("create pairing token: %w", err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(bindHost, "0"))
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", bindHost, err)
	}
	s := &Server{
		listener:   listener,
		options:    options,
		instanceID: instanceID,
		ownerToken: ownerToken,
		joinToken:  joinToken,
		byMessage:  make(map[string]protocol.Envelope),
		delivered:  make(map[string]map[protocol.Role]bool),
		done:       make(chan struct{}),
	}
	go s.acceptLoop()
	return s, nil
}

func randomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

func (s *Server) InstanceID() string { return s.instanceID }

func (s *Server) OwnerToken() string { return s.ownerToken }

func (s *Server) JoinToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.joinToken
}

// RegeneratePairingToken invalidates the previous one-use token without
// changing the instance or its in-memory history.
func (s *Server) RegeneratePairingToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrClosed
	}
	if s.worker != nil {
		return "", ErrWorkerConnected
	}
	token, err := randomToken(20)
	if err != nil {
		return "", err
	}
	s.joinToken = token
	// A new pairing must never inherit a reconnect credential from a prior
	// executor. The next successful join receives a fresh resume token.
	s.resumeToken = ""
	return token, nil
}

func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// WorkerConnected reports whether an executor is currently connected to this
// server. In tailscale-host mode the orchestrator's own client.Connected()
// only reflects its local loopback connection to this same server, never
// whether the remote executor joined (§4.1); this is the correct signal for
// that endpoint's PeerConnected.
func (s *Server) WorkerConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.worker != nil
}

func (s *Server) Done() <-chan struct{} { return s.done }

func (s *Server) Stats() (messages int, bytes int, closed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.history {
		bytes += len([]byte(e.Body))
	}
	return len(s.history), bytes, s.closed
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	p := &peer{conn: conn, out: make(chan protocol.Frame, s.options.MaxMessages+16), done: make(chan struct{}), writeTimeout: s.options.HeartbeatTimeout, lastActivity: time.Now()}
	go p.writeLoop()
	defer func() {
		s.detach(p)
		p.close()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), s.options.MaxFrame)
	_ = conn.SetReadDeadline(time.Now().Add(s.options.HeartbeatTimeout))
	if !scanner.Scan() {
		return
	}
	var hello protocol.Frame
	if err := json.Unmarshal(scanner.Bytes(), &hello); err != nil {
		p.closeWith(protocol.Frame{Type: protocol.FrameError, Code: "BAD_JSON", Detail: "invalid hello frame"})
		return
	}
	if err := s.register(p, hello); err != nil {
		p.closeWith(protocol.Frame{Type: protocol.FrameError, Code: errorCode(err), Detail: err.Error()})
		return
	}
	go s.heartbeatLoop(p)

	for {
		_ = conn.SetReadDeadline(time.Now().Add(s.options.HeartbeatTimeout))
		if !scanner.Scan() {
			break
		}
		p.touch()
		var frame protocol.Frame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			p.send(protocol.Frame{Type: protocol.FrameError, Code: "BAD_JSON", Detail: "invalid frame"})
			continue
		}
		s.handleFrame(p, frame)
	}
}

func (s *Server) register(p *peer, hello protocol.Frame) error {
	if hello.Type != protocol.FrameHello || hello.ProtocolVersion != protocol.Version {
		return fmt.Errorf("invalid hello")
	}
	if hello.InstanceID != s.instanceID {
		return ErrInstanceMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	p.role = hello.Role
	switch hello.Role {
	case protocol.RoleOrchestrator:
		if hello.Token != s.ownerToken {
			return ErrInvalidPairing
		}
		if s.owner != nil && s.owner != p {
			return ErrAlreadyBound
		}
		s.owner = p
	case protocol.RoleExecutor:
		if hello.Token == s.joinToken {
			if s.worker != nil && s.worker != p {
				return ErrAlreadyBound
			}
			s.joinToken = ""
			var err error
			// Every accepted pairing gets a fresh resume token. This also
			// invalidates credentials from a previous executor session.
			s.resumeToken, err = randomToken(20)
			if err != nil {
				return fmt.Errorf("create reconnect token: %w", err)
			}
		} else if hello.Token != s.resumeToken || s.resumeToken == "" {
			return ErrInvalidPairing
		}
		if s.worker != nil && s.worker != p {
			return ErrAlreadyBound
		}
		s.worker = p
	default:
		return fmt.Errorf("unsupported role %q", hello.Role)
	}

	welcome := protocol.Frame{
		Type:            protocol.FrameWelcome,
		ProtocolVersion: protocol.Version,
		InstanceID:      s.instanceID,
		State:           protocol.StateRunning,
		CurrentSeq:      s.nextSeq,
		ReconnectToken:  s.resumeToken,
		LimitMessages:   s.options.MaxMessages,
		LimitBytes:      s.options.MaxBytes,
	}
	p.send(welcome)
	for _, e := range s.history {
		// Replay the complete in-memory history so a newly paired peer can
		// render the conversation from the beginning. The client recognizes
		// its own envelopes and confirms them locally instead of ACKing them.
		if e.ServerSeq > hello.LastServerSeq {
			p.send(protocol.Frame{Type: protocol.FrameMessage, InstanceID: s.instanceID, Envelope: &e})
		}
	}
	return nil
}

func (s *Server) handleFrame(p *peer, frame protocol.Frame) {
	if frame.InstanceID != "" && frame.InstanceID != s.instanceID {
		p.send(protocol.Frame{Type: protocol.FrameError, Code: "INSTANCE_MISMATCH", Detail: "frame belongs to another instance"})
		return
	}
	switch frame.Type {
	case protocol.FramePublish:
		s.publish(p, frame.Envelope)
	case protocol.FrameAck:
		s.ack(p, frame.MessageID, frame.ServerSeq)
	case protocol.FramePing:
		p.send(protocol.Frame{Type: protocol.FramePong, InstanceID: s.instanceID, PingID: frame.PingID})
	case protocol.FramePong:
		p.touch()
	case protocol.FrameClose:
		if p.role == protocol.RoleOrchestrator {
			s.Close()
		} else {
			p.close()
		}
	default:
		p.send(protocol.Frame{Type: protocol.FrameError, Code: "UNKNOWN_FRAME", Detail: "unsupported frame type"})
	}
}

func (s *Server) publish(p *peer, input *protocol.Envelope) {
	if input == nil {
		p.send(protocol.Frame{Type: protocol.FrameError, Code: "MISSING_ENVELOPE", Detail: "publish requires an envelope"})
		return
	}
	e := *input
	if e.InstanceID != s.instanceID || e.SenderRole != p.role || e.SenderID != protocol.ExpectedSenderID(p.role) {
		p.send(protocol.Frame{Type: protocol.FrameError, Code: "SENDER_MISMATCH", Detail: "sender does not match authenticated connection"})
		return
	}
	if err := protocol.ValidateEnvelope(e); err != nil {
		p.send(protocol.Frame{Type: protocol.FrameError, MessageID: e.MessageID, Code: "INVALID_MESSAGE", Detail: err.Error()})
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		p.send(protocol.Frame{Type: protocol.FrameError, Code: "INSTANCE_CLOSED", Detail: ErrClosed.Error()})
		return
	}
	if existing, ok := s.byMessage[e.MessageID]; ok {
		s.mu.Unlock()
		if existing.Body == e.Body && existing.SenderID == e.SenderID {
			p.send(protocol.Frame{Type: protocol.FrameAccepted, InstanceID: s.instanceID, MessageID: existing.MessageID, ServerSeq: existing.ServerSeq, Duplicate: true})
			return
		}
		p.send(protocol.Frame{Type: protocol.FrameError, MessageID: e.MessageID, Code: "ID_CONFLICT", Detail: ErrMessageConflict.Error()})
		return
	}
	if len(s.history) >= s.options.MaxMessages || s.historyBytesLocked()+len([]byte(e.Body)) > s.options.MaxBytes {
		s.mu.Unlock()
		p.send(protocol.Frame{Type: protocol.FrameError, MessageID: e.MessageID, Code: "BUFFER_FULL", Detail: ErrBufferFull.Error()})
		return
	}
	s.nextSeq++
	e.ServerSeq = s.nextSeq
	e.AcceptedAt = time.Now().UTC()
	s.history = append(s.history, e)
	s.byMessage[e.MessageID] = e
	s.delivered[e.MessageID] = make(map[protocol.Role]bool)
	receiver := s.receiverLocked(p.role)
	s.mu.Unlock()

	p.send(protocol.Frame{Type: protocol.FrameAccepted, InstanceID: s.instanceID, MessageID: e.MessageID, ServerSeq: e.ServerSeq})
	if receiver != nil {
		if !receiver.send(protocol.Frame{Type: protocol.FrameMessage, InstanceID: s.instanceID, Envelope: &e}) {
			p.send(protocol.Frame{Type: protocol.FrameError, Code: "PEER_BACKPRESSURE", Detail: "message accepted in RAM; peer must reconnect to replay it"})
		}
	}
}

func (s *Server) historyBytesLocked() int {
	total := 0
	for _, e := range s.history {
		total += len([]byte(e.Body))
	}
	return total
}

func (s *Server) receiverLocked(sender protocol.Role) *peer {
	if sender == protocol.RoleOrchestrator {
		return s.worker
	}
	return s.owner
}

func (s *Server) ack(p *peer, messageID string, serverSeq uint64) {
	s.mu.Lock()
	e, ok := s.byMessage[messageID]
	if !ok || e.ServerSeq != serverSeq || e.SenderRole == p.role {
		s.mu.Unlock()
		p.send(protocol.Frame{Type: protocol.FrameError, Code: "INVALID_ACK", Detail: "ack does not match a delivered peer message"})
		return
	}
	if s.delivered[messageID] == nil {
		s.delivered[messageID] = make(map[protocol.Role]bool)
	}
	// ACKs are intentionally idempotent. A replayed message can be ACKed
	// again after a lost confirmation without becoming INVALID_ACK.
	s.delivered[messageID][p.role] = true
	sender := s.peerForRoleLocked(e.SenderRole)
	s.mu.Unlock()
	p.send(protocol.Frame{Type: protocol.FrameAckConfirmed, InstanceID: s.instanceID, MessageID: messageID, ServerSeq: serverSeq})
	if sender != nil {
		sender.send(protocol.Frame{Type: protocol.FrameDelivered, InstanceID: s.instanceID, MessageID: messageID, ServerSeq: serverSeq})
	}
}

func (s *Server) heartbeatLoop(p *peer) {
	ticker := time.NewTicker(s.options.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if time.Since(p.lastTouch()) > s.options.HeartbeatTimeout {
				p.close()
				return
			}
			pingID, err := randomToken(8)
			if err != nil || !p.send(protocol.Frame{Type: protocol.FramePing, InstanceID: s.instanceID, PingID: pingID}) {
				p.close()
				return
			}
		case <-p.done:
			return
		case <-s.done:
			return
		}
	}
}

func (s *Server) peerForRoleLocked(role protocol.Role) *peer {
	if role == protocol.RoleOrchestrator {
		return s.owner
	}
	return s.worker
}

func (s *Server) detach(p *peer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == p {
		s.owner = nil
	}
	if s.worker == p {
		s.worker = nil
	}
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		owner, worker := s.owner, s.worker
		s.owner, s.worker = nil, nil
		s.joinToken, s.ownerToken, s.resumeToken = "", "", ""
		s.history = nil
		s.byMessage = nil
		s.delivered = nil
		close(s.done)
		s.mu.Unlock()

		_ = s.listener.Close()
		closeFrame := protocol.Frame{Type: protocol.FrameClose, InstanceID: s.instanceID, State: protocol.StateClosed, Detail: "orchestrator closed the instance"}
		if owner != nil {
			owner.closeWith(closeFrame)
		}
		if worker != nil {
			worker.closeWith(closeFrame)
		}
	})
}

func (p *peer) writeLoop() {
	encoder := json.NewEncoder(p.conn)
	for {
		select {
		case frame := <-p.out:
			p.writeMu.Lock()
			_ = p.conn.SetWriteDeadline(time.Now().Add(p.writeTimeout))
			if err := encoder.Encode(frame); err != nil {
				p.writeMu.Unlock()
				p.close()
				return
			}
			p.writeMu.Unlock()
		case <-p.done:
			return
		}
	}
}

func (p *peer) send(frame protocol.Frame) bool {
	select {
	case p.out <- frame:
		return true
	case <-p.done:
		return false
	default:
		return false
	}
}

func (p *peer) close() {
	p.once.Do(func() {
		close(p.done)
		_ = p.conn.Close()
	})
}

func (p *peer) closeWith(frame protocol.Frame) {
	p.once.Do(func() {
		p.writeMu.Lock()
		_ = p.conn.SetWriteDeadline(time.Now().Add(p.writeTimeout))
		_ = json.NewEncoder(p.conn).Encode(frame)
		p.writeMu.Unlock()
		close(p.done)
		_ = p.conn.Close()
	})
}

func (p *peer) touch() {
	p.activityMu.Lock()
	p.lastActivity = time.Now()
	p.activityMu.Unlock()
}

func (p *peer) lastTouch() time.Time {
	p.activityMu.Lock()
	defer p.activityMu.Unlock()
	return p.lastActivity
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidPairing):
		return "PAIRING_INVALID"
	case errors.Is(err, ErrInstanceMismatch):
		return "INSTANCE_MISMATCH"
	case errors.Is(err, ErrAlreadyBound):
		return "ROLE_ALREADY_BOUND"
	case errors.Is(err, ErrWorkerConnected):
		return "WORKER_CONNECTED"
	case errors.Is(err, ErrClosed):
		return "INSTANCE_CLOSED"
	default:
		return "HELLO_REJECTED"
	}
}

func IsTemporaryNetworkError(err error) bool {
	return err != nil && !errors.Is(err, io.EOF)
}
