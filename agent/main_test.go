package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStateRejectsAmbiguity(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"switches":[{"outlet":0,"switch":"on"}]}`, "on"},
		{`{"switches":[{"outlet":1,"switch":"on"}]}`, ""},
		{`{"switches":[{"switch":"on"}]}`, ""},
		{`{"switches":[{"outlet":0,"switch":"on"},{"outlet":0,"switch":"off"}]}`, ""},
		{`{"switches":[{"outlet":0,"switch":"off"}],"switch":"on"}`, ""},
		{`{"switch":"off"}`, "off"},
		{`{"switch":true}`, ""},
	} {
		if got := stateOf(json.RawMessage(tc.in)); got != tc.want {
			t.Errorf("stateOf(%s)=%q want %q", tc.in, got, tc.want)
		}
	}
}
func TestGateStartupReplayAndCooldown(t *testing.T) {
	now := time.Now()
	g := &Gate{}
	cool := 30 * time.Second
	if g.Push("on", "1", now, cool) {
		t.Fatal("unarmed push woke")
	}
	g.Baseline("on")
	if g.Push("on", "2", now, cool) {
		t.Fatal("startup ON woke")
	}
	if g.Push("off", "3", now, cool) {
		t.Fatal("OFF woke")
	}
	if !g.Push("on", "4", now, cool) {
		t.Fatal("new OFF to ON did not wake")
	}
	if g.Push("on", "4", now, cool) {
		t.Fatal("duplicate woke")
	}
	g.Push("off", "5", now.Add(time.Second), cool)
	if g.Push("on", "6", now.Add(2*time.Second), cool) {
		t.Fatal("cooldown failed")
	}
	g.Baseline("on")
	if g.Push("on", "7", now.Add(time.Minute), cool) {
		t.Fatal("reconnect snapshot woke")
	}
	g.Push("off", "8", now.Add(time.Minute), cool)
	if !g.Push("on", "9", now.Add(time.Minute), cool) {
		t.Fatal("fresh edge after cooldown did not wake")
	}
}
func testConfig() Config {
	return Config{Region: "cn", Cooldown: 30, Targets: []Target{{Name: "test", DeviceID: "testswitch0001", MAC: "02:00:00:00:00:01", Interface: "br-lan"}}}
}
func TestRealProtocolLifecycleWithoutRealWOL(t *testing.T) {
	dir := t.TempDir()
	c := testConfig()
	disabled := false
	c.Targets = append(c.Targets, Target{Name: "paused", DeviceID: "other00001", MAC: "02:11:22:33:44:55", Interface: "br-lan", Enabled: &disabled})
	a := newAgent(c, filepath.Join(dir, "auth.json"), filepath.Join(dir, "status.json"))
	a.Auth = Auth{AT: "fixture-at", RT: "fixture-rt", APIKey: "fixture-key", Region: "cn", Updated: time.Now()}
	var wakes atomic.Int32
	var sessions atomic.Int32
	a.Wake = func(context.Context, Target) error { wakes.Add(1); return nil }
	up := websocket.Upgrader{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dispatch/app" {
			fmt.Fprint(w, `{"error":0,"domain":"fixture.coolkit.cn","port":443}`)
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		n := sessions.Add(1)
		var handshake map[string]any
		if conn.ReadJSON(&handshake) != nil || handshake["action"] != "userOnline" {
			return
		}
		_ = conn.WriteJSON(map[string]any{"error": 0, "config": map[string]int{"hb": 1, "hbInterval": 10}})
		var q map[string]any
		if conn.ReadJSON(&q) != nil || q["action"] != "query" || q["deviceid"] != "testswitch0001" {
			return
		}
		push := func(id, state, seq string) {
			_ = conn.WriteJSON(map[string]any{"action": "update", "deviceid": id, "sequence": seq, "params": map[string]any{"switches": []map[string]any{{"outlet": 0, "switch": state}}}})
		}
		// An unsolicited event before baseline must not wake anything.
		push("testswitch0001", "on", "pre-baseline")
		baseline := "off"
		if n > 1 {
			baseline = "on"
		}
		_ = conn.WriteJSON(map[string]any{"error": 0, "deviceid": "testswitch0001", "sequence": q["sequence"], "params": map[string]any{"switches": []map[string]any{{"outlet": 0, "switch": baseline}}}})
		push("testswitch0001", "on", "1")
		push("testswitch0001", "on", "1")
		push("other00001", "on", "2")
		if n == 1 {
			push("testswitch0001", "off", "3")
			push("testswitch0001", "on", "4")
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	a.Client = srv.Client()
	a.DispatchBase = func(string) string { return srv.URL }
	a.WSEndpoint = func(Response) (string, error) { return "wss" + strings.TrimPrefix(srv.URL, "https") + "/api/ws", nil }
	a.WSDialer = &websocket.Dialer{TLSClientConfig: srv.Client().Transport.(*http.Transport).TLSClientConfig, HandshakeTimeout: time.Second}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- a.session(ctx) }()
		deadline := time.Now().Add(4 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			a.mu.Lock()
			ready = sessions.Load() == int32(i+1) && a.status.States["testswitch0001"] == "on" && a.status.Phase == "connected"
			a.mu.Unlock()
			if ready {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !ready {
			cancel()
			t.Fatal("session never synchronized")
		}
		time.Sleep(40 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("session did not stop")
		}
	}
	if wakes.Load() != 1 {
		t.Fatalf("wanted one real edge across replay and reconnect, got %d", wakes.Load())
	}
}
func TestRefreshRotatesAndPersistsTokensOnly(t *testing.T) {
	dir := t.TempDir()
	a := newAgent(testConfig(), filepath.Join(dir, "auth.json"), filepath.Join(dir, "status.json"))
	a.Auth = Auth{AT: "old-at", RT: "old-rt", APIKey: "fixture-key", Region: "cn", Updated: time.Now().Add(-21 * 24 * time.Hour)}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/user/refresh" || r.Method != "POST" {
			t.Error("wrong refresh endpoint")
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Sign ") {
			t.Error("refresh must support expired AT with Sign")
		}
		fmt.Fprint(w, `{"error":0,"data":{"at":"new-at","rt":"new-rt"}}`)
	}))
	defer srv.Close()
	a.Client = srv.Client()
	a.APIBase = func(string) string { return srv.URL }
	if err := a.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	var stored Auth
	if err := readJSON(a.AuthPath, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.AT != "new-at" || stored.RT != "new-rt" || stored.APIKey != "fixture-key" {
		t.Fatal("token rotation not preserved")
	}
	b, _ := os.ReadFile(a.AuthPath)
	if strings.Contains(string(b), "password") || strings.Contains(string(b), "account") {
		t.Fatal("account/password persisted")
	}
}
