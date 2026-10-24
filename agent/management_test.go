package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountDisplayNeverExposesFullIdentifier(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"+8613812345678", "+86 138****5678"}, {"13812345678", "138****5678"}, {"owner@example.com", "o***@example.com"}, {"1234567", "1****67"}, {"", "已授权账号"}} {
		if got := maskAccount(tc.in); got != tc.want {
			t.Fatalf("unexpected masked display %q", got)
		}
	}
}

func TestApplicationCredentialsAreLoadedFromPrivateRuntimeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client-app.json")
	if _, err := loadApp(path); err == nil {
		t.Fatal("missing credentials accepted")
	}
	if err := atomicJSON(path, App{ID: "APPID0001", Secret: "secret:with+symbols/123"}); err != nil {
		t.Fatal(err)
	}
	app, err := loadApp(path)
	if err != nil || app.ID != "APPID0001" || app.Secret != "secret:with+symbols/123" {
		t.Fatal("private runtime credentials were not loaded")
	}
	if err := atomicJSON(path, App{ID: "bad\nheader", Secret: "validsecret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadApp(path); err == nil {
		t.Fatal("invalid application ID accepted")
	}
}

func TestDisabledMappingsSurviveSaveAndNeverConnectWhenAllPaused(t *testing.T) {
	c := testConfig()
	disabled := false
	c.Targets[0].Enabled = &disabled
	path := filepath.Join(t.TempDir(), "config.json")
	if err := atomicJSON(path, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded.DryRun = true
	if err = atomicJSON(path, loaded); err != nil {
		t.Fatal(err)
	}
	again, err := loadConfig(path)
	if err != nil || len(again.Targets) != 1 || len(again.activeTargets()) != 0 {
		t.Fatal("paused mapping lost or re-enabled")
	}
	a := newAgent(again, path+".auth", path+".status")
	var requests atomic.Int32
	a.DispatchBase = func(string) string { requests.Add(1); return "https://invalid.example" }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	a.run(ctx)
	if requests.Load() != 0 || len(a.gates) != 0 {
		t.Fatal("paused mappings connected or armed")
	}
}

func TestCloudDiscoveryFiltersPhysicalDevicesAndValidatesEnabledMappings(t *testing.T) {
	a := newAgent(testConfig(), filepath.Join(t.TempDir(), "auth"), filepath.Join(t.TempDir(), "state"))
	a.Auth = Auth{AT: "fixture", Region: "cn"}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v2/device/thing" {
			t.Error("wrong discovery request")
		}
		fmt.Fprint(w, `{"error":0,"data":{"thingList":[{"itemData":{"deviceid":"testswitch0001","name":"desktop","extra":{"uiid":264},"params":{"switch":"off"}}},{"itemData":{"deviceid":"physical01","name":"private","extra":{"uiid":1},"apikey":"not-for-output"}}]}}`)
	}))
	defer srv.Close()
	a.Client = srv.Client()
	a.APIBase = func(string) string { return srv.URL }
	switches, err := a.listSwitches(context.Background())
	if err != nil || len(switches) != 1 || switches[0].ID != "testswitch0001" {
		t.Fatalf("wrong virtual list: %v", err)
	}
	raw, _ := json.Marshal(switches)
	if string(raw) == "" {
		t.Fatal("empty output")
	}
	disabled := false
	a.C.Targets = append(a.C.Targets, Target{Name: "paused", DeviceID: "notowned01", Enabled: &disabled})
	if err = a.validateTargets(context.Background()); err != nil {
		t.Fatal("disabled target blocked authorization")
	}
	enabled := true
	a.C.Targets[1].Enabled = &enabled
	if err = a.validateTargets(context.Background()); err == nil {
		t.Fatal("unowned active target accepted")
	}
}
