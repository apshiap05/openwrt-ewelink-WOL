// ewelink-wol subscribes to eWeLink cloud changes and wakes local devices.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"math/big"
	"regexp"
	"sync"
	"time"
)

func (a *Agent) run(ctx context.Context) {
	delay := 5 * time.Second
	for ctx.Err() == nil {
		if len(a.C.activeTargets()) == 0 {
			if a.status.Phase != "waiting_devices" {
				a.event("waiting_devices", "no_enabled_targets", "", "")
			}
			_ = wait(ctx, 30*time.Second)
			continue
		}
		if time.Now().Year() < 2025 {
			a.event("waiting_clock", "time_not_synchronized", "", "")
			_ = wait(ctx, 30*time.Second)
			continue
		}
		if readJSON(a.AuthPath, &a.Auth) != nil || a.Auth.AT == "" || a.Auth.RT == "" || a.Auth.APIKey == "" || regions[a.Auth.Region] == "" {
			a.event("authorization_required", "login_required", "", "")
			_ = wait(ctx, 30*time.Second)
			continue
		}
		if time.Since(a.Auth.Updated) >= 20*24*time.Hour {
			if err := a.refresh(ctx); err != nil {
				a.event("authorization_required", err.Error(), "", "")
				_ = wait(ctx, 5*time.Minute)
				continue
			}
		}
		for _, g := range a.gates {
			g.Ready = false
			g.State = ""
			g.LastSequence = ""
		}
		start := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, errRefresh) || authError(err) {
			if e := a.refresh(ctx); e != nil {
				a.event("authorization_required", e.Error(), "", "")
				_ = wait(ctx, 5*time.Minute)
				continue
			}
		}
		a.mu.Lock()
		a.status.Reconnects++
		a.mu.Unlock()
		if time.Since(start) > 2*time.Minute {
			delay = 5 * time.Second
		}
		reason := "cloud_connection_closed"
		if err != nil {
			reason = err.Error()
		}
		a.event("reconnecting", reason, "", "")
		jitter, _ := rand.Int(rand.Reader, big.NewInt(int64(delay/5)+1))
		_ = wait(ctx, delay+time.Duration(jitter.Int64()))
		if delay < 5*time.Minute {
			delay *= 2
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}
		}
	}
	a.event("stopped", "service_stopped", "", "")
}

type wsMessage struct {
	Action   string          `json:"action"`
	Device   string          `json:"deviceid"`
	Error    *int            `json:"error"`
	Params   json.RawMessage `json:"params"`
	Sequence json.RawMessage `json:"sequence"`
	Config   struct {
		HB       int `json:"hb"`
		Interval int `json:"hbInterval"`
	} `json:"config"`
}

func sequenceOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	if len(raw) > 0 && regexp.MustCompile(`^\d+$`).Match(raw) {
		return string(raw)
	}
	return ""
}
func (a *Agent) session(ctx context.Context) error {
	a.mu.Lock()
	for _, g := range a.gates {
		g.Ready = false
		g.State = ""
		g.LastSequence = ""
	}
	a.mu.Unlock()
	a.event("connecting", "connecting_cloud", "", "")
	out, err := a.request(ctx, a.DispatchBase(a.Auth.Region), "/dispatch/app", nil, false)
	if err != nil {
		return err
	}
	endpoint, err := a.WSEndpoint(out)
	if err != nil {
		return err
	}
	conn, _, err := a.WSDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return errors.New("websocket_connect_failed")
	}
	defer conn.Close()
	conn.SetReadLimit(1024 * 1024)
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	seq := fmt.Sprint(time.Now().UnixMilli())
	if conn.WriteJSON(map[string]any{"action": "userOnline", "version": 8, "ts": time.Now().Unix(), "at": a.Auth.AT, "userAgent": "app", "apikey": a.Auth.APIKey, "appid": a.App.ID, "nonce": nonce(), "sequence": seq}) != nil {
		return errors.New("websocket_handshake_failed")
	}
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	var hello wsMessage
	if conn.ReadJSON(&hello) != nil {
		return errors.New("websocket_handshake_failed")
	}
	if hello.Error == nil || *hello.Error != 0 {
		if hello.Error != nil {
			return apiError{*hello.Error}
		}
		return errors.New("websocket_handshake_invalid")
	}
	interval := 90 * time.Second
	if hello.Config.Interval >= 10 && hello.Config.Interval <= 300 {
		interval = time.Duration(hello.Config.Interval+7) * time.Second
	}
	deadline := func() { _ = conn.SetReadDeadline(time.Now().Add(interval*2 + 30*time.Second)) }
	deadline()
	conn.SetPongHandler(func(string) error { deadline(); return nil })
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}
	pending := map[string]string{}
	active := a.C.activeTargets()
	for i, t := range active {
		q := fmt.Sprint(time.Now().UnixMilli()) + fmt.Sprint(i)
		pending[q] = t.DeviceID
		if write(map[string]any{"action": "query", "deviceid": t.DeviceID, "apikey": a.Auth.APIKey, "params": []string{}, "userAgent": "app", "sequence": q}) != nil {
			return errors.New("baseline_query_failed")
		}
		if len(active) > 1 {
			if wait(ctx, 1200*time.Millisecond) != nil {
				return ctx.Err()
			}
		}
	}
	refreshed := make(chan struct{})
	refreshIn := time.Until(a.Auth.Updated.Add(20 * 24 * time.Hour))
	if refreshIn < time.Second {
		refreshIn = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		fresh := time.NewTimer(refreshIn)
		defer fresh.Stop()
		baseline := time.NewTimer(20 * time.Second)
		defer baseline.Stop()
		for {
			select {
			case <-sctx.Done():
				conn.Close()
				return
			case <-fresh.C:
				close(refreshed)
				conn.Close()
				return
			case <-baseline.C:
				a.mu.Lock()
				ready := true
				for _, g := range a.gates {
					if !g.Ready {
						ready = false
					}
				}
				a.mu.Unlock()
				if !ready {
					conn.Close()
					return
				}
			case <-ticker.C:
				writeMu.Lock()
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				e := conn.WriteMessage(websocket.TextMessage, []byte("ping"))
				writeMu.Unlock()
				if e != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	a.event("synchronizing", "cloud_connected", "", "")
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-refreshed:
				return errRefresh
			default:
			}
			return errors.New("websocket_disconnected")
		}
		deadline()
		a.mu.Lock()
		a.status.LastSeen = time.Now()
		_ = atomicJSON(a.StatusPath, a.status)
		a.mu.Unlock()
		if string(raw) == "pong" {
			continue
		}
		var m wsMessage
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if m.Error != nil && (*m.Error == 401 || *m.Error == 402) {
			return apiError{*m.Error}
		}
		g, ok := a.gates[m.Device]
		if !ok {
			continue
		}
		state := stateOf(m.Params)
		msgSequence := sequenceOf(m.Sequence)
		if wanted, exists := pending[msgSequence]; exists && wanted == m.Device {
			if m.Error == nil || *m.Error != 0 || state == "" {
				return errors.New("baseline_response_invalid")
			}
			a.mu.Lock()
			g.Baseline(state)
			a.mu.Unlock()
			delete(pending, msgSequence)
			phase := "synchronizing"
			if len(pending) == 0 {
				phase = "connected"
			}
			a.event(phase, "baseline_no_wake", m.Device, state)
			continue
		}
		if m.Action != "update" || state == "" {
			continue
		}
		// Query snapshots never wake. On each connection, only pushes after the baseline are eligible.
		if !g.Ready {
			continue
		}
		a.mu.Lock()
		wake := g.Push(state, msgSequence, time.Now(), time.Duration(a.C.Cooldown)*time.Second)
		if wake && !a.C.DryRun {
			a.status.LastWake[m.Device] = g.LastWake
		}
		a.mu.Unlock()
		a.event("", "state_push", m.Device, state)
		if !wake {
			continue
		}
		if a.C.DryRun {
			a.event("", "would_wake_dry_run", m.Device, state)
			continue
		}
		var target Target
		for _, t := range a.C.Targets {
			if t.DeviceID == m.Device {
				target = t
			}
		}
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		err = a.Wake(wctx, target)
		wcancel()
		if err != nil {
			a.event("", "wake_failed", m.Device, state)
		} else {
			a.mu.Lock()
			a.status.Wakes++
			a.mu.Unlock()
			a.event("", "wake_sent", m.Device, state)
		}
	}
}
