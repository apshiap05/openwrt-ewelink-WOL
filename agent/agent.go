// ewelink-wol subscribes to eWeLink cloud changes and wakes local devices.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type Agent struct {
	C                    Config
	Auth                 Auth
	App                  App
	AuthPath, StatusPath string
	Client               *http.Client
	APIBase              func(string) string
	DispatchBase         func(string) string
	WSEndpoint           func(Response) (string, error)
	WSDialer             *websocket.Dialer
	Wake                 func(context.Context, Target) error
	mu                   sync.Mutex
	status               Status
	gates                map[string]*Gate
	lastRequest          time.Time
}

func newAgent(c Config, authPath, statusPath string) *Agent {
	a := &Agent{C: c, AuthPath: authPath, StatusPath: statusPath, Client: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second}}, gates: map[string]*Gate{}}
	a.APIBase = func(r string) string { return "https://" + regions[r] }
	a.DispatchBase = func(r string) string { return "https://" + dispatches[r] }
	a.WSEndpoint = func(r Response) (string, error) {
		if !coolkitDomain.MatchString(r.Domain) || r.Port < 1 || r.Port > 65535 {
			return "", errors.New("invalid_dispatch_target")
		}
		return "wss://" + net.JoinHostPort(r.Domain, fmt.Sprint(r.Port)) + "/api/ws", nil
	}
	a.WSDialer = &websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: nil, NetDialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext}
	a.status = Status{Version: version, Phase: "starting", Started: time.Now(), DryRun: c.DryRun, States: map[string]string{}, LastWake: map[string]time.Time{}}
	// Keep cooldown state across service restarts; never restore the armed state.
	var old Status
	if readJSON(statusPath, &old) == nil {
		a.status.Events = old.Events
		if len(a.status.Events) > 40 {
			a.status.Events = a.status.Events[len(a.status.Events)-40:]
		}
		for id, t := range old.LastWake {
			a.status.LastWake[id] = t
		}
	}
	for _, t := range c.activeTargets() {
		a.gates[t.DeviceID] = &Gate{LastWake: a.status.LastWake[t.DeviceID]}
	}
	a.Wake = func(ctx context.Context, t Target) error {
		if _, err := net.InterfaceByName(t.Interface); err != nil {
			return errors.New("lan_interface_missing")
		}
		for i := 0; i < 3; i++ {
			cmd := exec.CommandContext(ctx, "/usr/bin/etherwake", "-b", "-i", t.Interface, t.MAC)
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if cmd.Run() != nil {
				return errors.New("etherwake_failed")
			}
			if i < 2 {
				if wait(ctx, 300*time.Millisecond) != nil {
					return ctx.Err()
				}
			}
		}
		return nil
	}
	return a
}
func readJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}
func atomicJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ewelink-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func (a *Agent) event(phase, event, id, state string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if phase != "" {
		a.status.Phase = phase
	}
	a.status.LastEvent = event
	a.status.Updated = time.Now()
	a.status.Events = append(a.status.Events, Event{Time: a.status.Updated, Kind: event, Device: id, State: state})
	if len(a.status.Events) > 40 {
		a.status.Events = a.status.Events[len(a.status.Events)-40:]
	}
	if id != "" && state != "" {
		a.status.States[id] = state
	}
	if err := atomicJSON(a.StatusPath, a.status); err != nil {
		log.Print("status_write_failed")
	}
	log.Printf("event=%s device=%s state=%s phase=%s", event, id, state, a.status.Phase)
}
