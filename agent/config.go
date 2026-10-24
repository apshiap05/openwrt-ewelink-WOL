// ewelink-wol subscribes to eWeLink cloud changes and wakes local devices.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

const version = "1.2.0"

var regions = map[string]string{"cn": "cn-apia.coolkit.cn", "as": "as-apia.coolkit.cc", "us": "us-apia.coolkit.cc", "eu": "eu-apia.coolkit.cc"}
var dispatches = map[string]string{"cn": "cn-dispa.coolkit.cn", "as": "as-dispa.coolkit.cc", "us": "us-dispa.coolkit.cc", "eu": "eu-dispa.coolkit.cc"}
var validID = regexp.MustCompile(`^[a-zA-Z0-9]{10,40}$`)
var validInterface = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,32}$`)
var validAppID = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,128}$`)
var validAppSecret = regexp.MustCompile(`^[!-~]{8,256}$`)
var coolkitDomain = regexp.MustCompile(`^[a-zA-Z0-9-]+\.coolkit\.(cn|cc)$`)
var errRefresh = errors.New("refresh_required")

type Target struct {
	Name      string `json:"name"`
	DeviceID  string `json:"device_id"`
	MAC       string `json:"mac"`
	Interface string `json:"interface"`
	Enabled   *bool  `json:"enabled,omitempty"`
}
type Config struct {
	Region   string   `json:"region"`
	DryRun   bool     `json:"dry_run"`
	Cooldown int      `json:"cooldown_seconds"`
	Targets  []Target `json:"targets"`
}

func (c Config) activeTargets() []Target {
	var active []Target
	for _, t := range c.Targets {
		if t.Enabled == nil || *t.Enabled {
			active = append(active, t)
		}
	}
	return active
}

type Auth struct {
	AT      string    `json:"at"`
	RT      string    `json:"rt"`
	APIKey  string    `json:"apikey"`
	Region  string    `json:"region"`
	Updated time.Time `json:"updated"`
}
type App struct {
	ID     string `json:"appId"`
	Secret string `json:"appSecret"`
}

func loadApp(path string) (App, error) {
	var app App
	if err := readJSON(path, &app); err != nil {
		return App{}, errors.New("app_credentials_required")
	}
	if !validAppID.MatchString(app.ID) || !validAppSecret.MatchString(app.Secret) {
		return App{}, errors.New("invalid_app_credentials")
	}
	return app, nil
}

type Response struct {
	Error  int             `json:"error"`
	Data   json.RawMessage `json:"data"`
	Domain string          `json:"domain"`
	Port   int             `json:"port"`
}
type apiError struct{ Code int }

func (e apiError) Error() string { return fmt.Sprintf("cloud_error_%d", e.Code) }
func authError(err error) bool {
	var e apiError
	return errors.As(err, &e) && (e.Code == 401 || e.Code == 402)
}

type Gate struct {
	State        string
	Ready        bool
	LastWake     time.Time
	LastSequence string
}

func (g *Gate) Baseline(state string) {
	g.State = state
	g.Ready = state == "off" || state == "on"
	g.LastSequence = ""
}
func (g *Gate) Push(state, sequence string, now time.Time, cooldown time.Duration) bool {
	if !g.Ready || (state != "on" && state != "off") {
		return false
	}
	if sequence != "" && sequence == g.LastSequence {
		return false
	}
	previous := g.State
	g.State = state
	g.LastSequence = sequence
	if previous != "off" || state != "on" || (!g.LastWake.IsZero() && now.Sub(g.LastWake) < cooldown) {
		return false
	}
	g.LastWake = now
	return true
}
func stateOf(params json.RawMessage) string {
	var p struct {
		Switch   string `json:"switch"`
		Switches []struct {
			Outlet *int   `json:"outlet"`
			Switch string `json:"switch"`
		} `json:"switches"`
	}
	if json.Unmarshal(params, &p) != nil {
		return ""
	}
	if p.Switches != nil {
		count := 0
		state := ""
		for _, v := range p.Switches {
			if v.Outlet != nil && *v.Outlet == 0 {
				count++
				state = v.Switch
			}
		}
		if count != 1 || (state != "on" && state != "off") || (p.Switch != "" && p.Switch != state) {
			return ""
		}
		return state
	}
	if p.Switch == "on" || p.Switch == "off" {
		return p.Switch
	}
	return ""
}

type Event struct {
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Device string    `json:"device_id,omitempty"`
	State  string    `json:"state,omitempty"`
}
type Status struct {
	Version    string               `json:"version"`
	Phase      string               `json:"phase"`
	DryRun     bool                 `json:"dry_run"`
	Updated    time.Time            `json:"updated"`
	States     map[string]string    `json:"states"`
	LastWake   map[string]time.Time `json:"last_wake"`
	LastEvent  string               `json:"last_event"`
	Wakes      int                  `json:"wake_count"`
	Reconnects int                  `json:"reconnect_count"`
	Started    time.Time            `json:"started"`
	LastSeen   time.Time            `json:"last_seen"`
	Events     []Event              `json:"events"`
}

func loadConfig(path string) (Config, error) {
	var c Config
	if err := readJSON(path, &c); err != nil {
		return c, errors.New("config_read_failed")
	}
	if c.Cooldown < 5 || c.Cooldown > 3600 || len(c.Targets) > 16 || regions[c.Region] == "" {
		return c, errors.New("invalid_config")
	}
	seen := map[string]bool{}
	for _, t := range c.Targets {
		mac, err := net.ParseMAC(t.MAC)
		if err != nil || len(mac) != 6 || mac[0]&1 != 0 || bytes.Equal(mac, make([]byte, 6)) || strings.TrimSpace(t.Name) == "" || len(t.Name) > 96 || !validID.MatchString(t.DeviceID) || !validInterface.MatchString(t.Interface) || seen[t.DeviceID] {
			return c, errors.New("invalid_target")
		}
		seen[t.DeviceID] = true
	}
	return c, nil
}
