package api

import (
	"context"
	"encoding/json"
	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"sync"
	"time"
)

type subscription struct {
	cancel context.CancelFunc
	start  func()
	wg     sync.WaitGroup
	once   sync.Once
}

func (s *subscription) close() { s.cancel(); s.wg.Wait() }
func (s *Server) subscribe(parent context.Context, key string, d control.Descriptor, emit func(any) error) *subscription {
	ctx, cancel := context.WithCancel(parent)
	sub := &subscription{cancel: cancel}
	send := func(kind string, data any) {
		if ctx.Err() == nil {
			_ = emit(map[string]any{"v": 1, "sub": key, "event": kind, "data": data})
		}
	}
	sub.start = func() {
		sub.once.Do(func() {
			sub.wg.Add(2)
			go func() {
				defer sub.wg.Done()
				defer cancel()
				watch := control.NewSubscription(d, 0)
				defer watch.Close()
				for {
					event, err := watch.Next(ctx)
					if err != nil {
						return
					}
					data := map[string]any{"instance_id": d.InstanceID}
					if event.EventSeq > 0 {
						data["event_seq"] = event.EventSeq
					}
					if event.ServerSeq > 0 {
						data["server_seq"] = event.ServerSeq
					}
					if event.MessageID != "" {
						data["message_id"] = event.MessageID
					}
					if event.Status != "" {
						data["status"] = event.Status
					}
					if event.State != "" {
						data["state"] = event.State
					}
					if event.Detail != "" {
						data["detail"] = event.Detail
					}
					if event.Envelope != nil {
						data["message"] = event.Envelope
					}
					kind := string(event.Kind)
					if kind == "" {
						kind = "state"
					}
					send(kind, data)
					if event.Kind == bridge.EventLifecycle {
						return
					}
				}
			}()
			go func() {
				defer sub.wg.Done()
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				previous := ""
				for {
					hctx, done := context.WithTimeout(ctx, 500*time.Millisecond)
					h, err := s.health(hctx, d)
					done()
					if err == nil {
						raw, _ := json.Marshal(h)
						if string(raw) != previous {
							send("state", map[string]any{"instance_id": d.InstanceID, "health": h})
							previous = string(raw)
						}
					}
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
				}
			}()
		})
	}
	return sub
}
