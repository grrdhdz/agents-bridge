// Package api is a client of the engine core, serving the public stdio contract.
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	contract "github.com/grrdhdz/agents-bridge/engine/api"
	"github.com/grrdhdz/agents-bridge/engine/internal/bridgeexport"
	"github.com/grrdhdz/agents-bridge/engine/internal/bridges"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/integration"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const Version = 1
const MaxLineBytes = 2 * 1024 * 1024

// CreateLocal reuses the CLI's detached launcher. Empty timeout means default.
type Options struct {
	Root, Version string
	Integration   integration.EnsureOptions
	CreateLocal   func(context.Context, string) (string, error)
}
type Server struct {
	options   Options
	privateMu sync.Mutex
	private   []string
}

func New(o Options) *Server {
	if o.Integration.Version == "" {
		o.Integration.Version = o.Version
	}
	s := &Server{options: o}
	if o.Root != "" {
		s.private = append(s.private, o.Root)
	} else if root, err := control.RuntimeRoot(); err == nil {
		s.private = append(s.private, root)
	}
	return s
}

type Request struct {
	V    int            `json:"v"`
	ID   string         `json:"id"`
	Op   string         `json:"op"`
	Args map[string]any `json:"args"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string        { return e.Code + ": " + e.Message }
func fail(code, message string) error { return &Error{code, message} }
func ValidateOutput(v any) error      { return contract.Validate(v) }

// Run serializes responses and events. EOF cancels I/O and subscriptions, never
// bridges. Requests already accepted still receive their response if writable.
func (s *Server) Run(parent context.Context, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	lines := make(chan []byte)
	readDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), MaxLineBytes)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				readDone <- nil
				close(lines)
				return
			}
		}
		readDone <- scanner.Err()
		cancel()
		close(lines)
	}()
	requestsDone := make(chan struct{})
	monitorDone := make(chan struct{})
	defer close(monitorDone)
	go func() {
		select {
		case <-ctx.Done():
			if c, ok := input.(io.Closer); ok {
				c.Close()
			}
			if c, ok := output.(io.Closer); ok {
				select {
				case <-requestsDone:
				case <-time.After(time.Second):
				}
				c.Close()
			}
		case <-monitorDone:
		}
	}()
	var writeMu sync.Mutex
	emit := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var data any
		_ = json.Unmarshal(raw, &data)
		raw, err = json.Marshal(s.clean(data, ""))
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		n, err := output.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		if err != nil {
			cancel()
		}
		return err
	}
	subscriptions := map[string]*subscription{}
	defer func() {
		for _, sub := range subscriptions {
			sub.close()
		}
	}()
	var outputErr error
	for line := range lines {
		var req Request
		var packet any
		err := json.Unmarshal(line, &packet)
		if err == nil {
			_ = json.Unmarshal(line, &req)
			err = contract.Validate(packet)
		}
		if err != nil {
			response := map[string]any{"v": 1, "id": req.ID, "ok": false, "error": Error{"INVALID_REQUEST", "Petición incompatible con el contrato v1."}}
			if e := emit(response); e != nil {
				outputErr = e
				break
			}
			continue
		}
		opctx, done := context.WithTimeout(ctx, 25*time.Second)
		result, after, err := s.dispatch(opctx, ctx, req, subscriptions, emit)
		done()
		response := map[string]any{"v": 1, "id": req.ID, "ok": err == nil}
		if err != nil {
			response["error"] = publicError(err)
		} else {
			response["result"] = result
		}
		if err = emit(response); err != nil {
			outputErr = err
			break
		}
		if after != nil {
			after()
		}
	}
	cancel()
	close(requestsDone)
	if outputErr != nil && parent.Err() != nil {
		return parent.Err()
	}
	if outputErr != nil {
		return outputErr
	}
	if err := <-readDone; err != nil {
		return errors.New("entrada JSONL excede el límite o no se pudo leer")
	}
	return nil
}
func publicError(err error) Error {
	var typed *Error
	if errors.As(err, &typed) {
		return *typed
	}
	var exported *bridgeexport.Error
	if errors.As(err, &exported) {
		return Error{exported.Code, "No se pudo exportar la conversación."}
	}
	if errors.Is(err, bridges.ErrNotFound) || errors.Is(err, control.ErrInstanceNotFound) {
		return Error{"INSTANCE_NOT_FOUND", "No hay un puente disponible con esa instancia."}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Error{"CANCELLED", "Operación cancelada o fuera de tiempo."}
	}
	return Error{"CONTROL_UNREACHABLE", "No se pudo completar la operación del motor."}
}
func (s *Server) remember(d control.Descriptor) {
	s.privateMu.Lock()
	defer s.privateMu.Unlock()
	for _, value := range []string{d.Capability, d.ControlURL} {
		found := false
		for _, known := range s.private {
			if known == value {
				found = true
				break
			}
		}
		if value != "" && !found {
			s.private = append(s.private, value)
		}
	}
}

func (s *Server) clean(v any, key string) any {
	switch x := v.(type) {
	case string:
		s.privateMu.Lock()
		paths := append([]string(nil), s.private...)
		s.privateMu.Unlock()
		for _, p := range paths {
			if p != "" {
				x = strings.ReplaceAll(x, p, "[dato privado omitido]")
			}
		}
		if key == "body" || key == "message" || key == "detail" || key == "tool" || key == "project" || key == "name" {
			x = control.RedactText(x)
		}
		return x
	case map[string]any:
		for k, a := range x {
			x[k] = s.clean(a, k)
		}
		return x
	case []any:
		for i, a := range x {
			x[i] = s.clean(a, key)
		}
		return x
	default:
		return v
	}
}
func (s *Server) descriptor(id string) (control.Descriptor, error) {
	descriptors, err := control.ListDescriptors(s.options.Root)
	if err != nil {
		return control.Descriptor{}, err
	}
	d, err := bridges.SelectStopDescriptor(descriptors, id)
	if err == nil {
		for _, candidate := range descriptors {
			if candidate.InstanceID == id {
				s.remember(candidate)
			}
		}
	}
	return d, err
}

func (s *Server) call(ctx context.Context, d control.Descriptor, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	res, err := control.Do(ctx, d, method, path, reader)
	if err != nil {
		return fail("CONTROL_UNREACHABLE", "El puente no responde.")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var e struct{ Code string }
		_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&e)
		switch e.Code {
		case "INBOX_NOT_EMPTY", "MESSAGE_INVALID", "FORBIDDEN", "CONTROL_BACKPRESSURE", "CURSOR_EXPIRED", "INSTANCE_CLOSED":
			return fail(e.Code, "El plano de control rechazó la operación.")
		}
		return fail("CONTROL_UNREACHABLE", "El plano de control rechazó la operación.")
	}
	if target != nil {
		if err = json.NewDecoder(io.LimitReader(res.Body, MaxLineBytes)).Decode(target); err != nil {
			return fail("INVALID_RESPONSE", "Respuesta incompatible del puente.")
		}
	}
	return nil
}
func arg(r Request, key string) string { v, _ := r.Args[key].(string); return v }
func (s *Server) dispatch(ctx, sessionctx context.Context, r Request, subs map[string]*subscription, emit func(any) error) (any, func(), error) {
	switch r.Op {
	case "integration_status":
		result, err := integration.Status(s.options.Integration)
		if err != nil {
			return nil, nil, fail("INTEGRATION_FAILED", "No se pudo leer el estado de integración.")
		}
		return result, nil, nil
	case "integration_ensure":
		result, err := integration.Ensure(ctx, s.options.Integration)
		if err != nil {
			return nil, nil, fail("INTEGRATION_FAILED", "No se pudo preparar la integración de hooks.")
		}
		return result, nil, nil
	case "integration_set":
		enabled, _ := r.Args["enabled"].(bool)
		result, err := integration.Set(ctx, s.options.Integration, arg(r, "harness"), enabled)
		if err != nil {
			return nil, nil, fail("INTEGRATION_FAILED", "No se pudo cambiar la integración de hooks.")
		}
		return result, nil, nil
	case "hello":
		return map[string]any{"engine_version": s.options.Version, "contract_version": 1}, nil, nil
	case "list":
		infos, err := bridges.List(ctx, s.options.Root)
		if err != nil {
			return nil, nil, err
		}
		instances := make([]Instance, 0, len(infos))
		for _, i := range infos {
			instances = append(instances, instance(i))
		}
		return map[string]any{"instances": instances}, nil, nil
	case "create_local":
		idle := arg(r, "idle_timeout")
		if idle != "" {
			d, err := time.ParseDuration(idle)
			if err != nil || d < 0 {
				return nil, nil, fail("INVALID_ARGS", "idle_timeout debe ser una duración no negativa.")
			}
		}
		if s.options.CreateLocal == nil {
			return nil, nil, fail("UNAVAILABLE", "El lanzador no está configurado.")
		}
		id, err := s.options.CreateLocal(ctx, idle)
		return map[string]any{"instance_id": id, "state": "running"}, nil, err
	case "unsubscribe":
		key := arg(r, "sub")
		sub, ok := subs[key]
		if !ok {
			return nil, nil, fail("SUBSCRIPTION_NOT_FOUND", "Suscripción inexistente.")
		}
		sub.close()
		delete(subs, key)
		return map[string]any{"unsubscribed": key}, nil, nil
	}
	id := arg(r, "instance_id")
	if id == "" {
		return nil, nil, fail("INVALID_ARGS", "instance_id es obligatorio.")
	}
	d, err := s.descriptor(id)
	if err != nil {
		return nil, nil, err
	}
	switch r.Op {
	case "health":
		h, err := s.health(ctx, d)
		return h, nil, err
	case "send":
		body := arg(r, "body")
		if label := arg(r, "label"); label != "" {
			body = label + "\n" + body
		}
		messageID, err := control.NewID()
		if err != nil {
			return nil, nil, err
		}
		err = s.call(ctx, d, http.MethodPost, "/v1/send", map[string]any{"v": 1, "message_id": messageID, "body": body, "source": "human-operator"}, nil)
		return map[string]any{"instance_id": id, "message_id": messageID, "source": "human-operator", "role": control.RoleKey(d.LocalRole)}, nil, err
	case "rename":
		raw, ok := r.Args["name"].(string)
		if !ok {
			return nil, nil, fail("INVALID_ARGS", "name es obligatorio; usa \"\" para quitar el nombre.")
		}
		name, err := control.NormalizeName(raw)
		if err != nil {
			return nil, nil, fail("INVALID_ARGS", err.Error())
		}
		err = s.call(ctx, d, http.MethodPost, "/v1/name", map[string]string{"name": name}, nil)
		return map[string]any{"instance_id": id, "name": name}, nil, err
	case "stop":
		err = bridges.Stop(ctx, s.options.Root, id)
		return map[string]any{"instance_id": id, "state": "stopping"}, nil, err
	case "export":
		format, output := arg(r, "format"), arg(r, "output")
		if strings.TrimSpace(output) == "" {
			return nil, nil, fail("INVALID_ARGS", "output es obligatorio.")
		}
		absolute, err := filepath.Abs(output)
		if err != nil {
			return nil, nil, fail("INVALID_ARGS", "Ruta de exportación inválida.")
		}
		err = bridgeexport.Write(ctx, d, absolute, format, bridgeexport.Options{Redact: func(text string) string { return s.clean(text, "body").(string) }})
		return map[string]any{"instance_id": id, "format": format, "output": absolute}, nil, err
	case "subscribe":
		if len(subs) >= 8 {
			return nil, nil, fail("CONTROL_BACKPRESSURE", "Máximo de ocho suscripciones por proceso API.")
		}
		if _, err = s.health(ctx, d); err != nil {
			return nil, nil, err
		}
		key, err := control.NewID()
		if err != nil {
			return nil, nil, err
		}
		key = "sub-" + key
		sub := s.subscribe(sessionctx, key, d, emit)
		subs[key] = sub
		return map[string]any{"sub": key}, sub.start, nil
	default:
		return nil, nil, fail("UNKNOWN_OPERATION", "Operación no disponible.")
	}
}
