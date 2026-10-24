// ewelink-wol subscribes to eWeLink cloud changes and wakes local devices.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	debug.SetMemoryLimit(32 * 1024 * 1024)
	config := flag.String("config", "/etc/ewelink-wol/config.json", "configuration path")
	appPath := flag.String("app", "/etc/ewelink-wol/client-app.json", "private application credentials path")
	auth := flag.String("auth", "/etc/ewelink-wol/auth.json", "private token path")
	status := flag.String("status", "/var/run/ewelink-wol/status.json", "sanitized status path")
	mode := flag.String("set-mode", "", "set dry-run or live without connecting")
	login := flag.Bool("login-stdin", false, "read one-time account/password JSON from stdin")
	refresh := flag.Bool("refresh-once", false, "verify and persist token refresh without WOL")
	list := flag.Bool("list-switches", false, "list authorized virtual switches without WOL")
	validate := flag.Bool("validate-targets", false, "check enabled mappings against cloud without WOL")
	accountInfo := flag.Bool("account-info", false, "show masked authorized account without credentials")
	check := flag.Bool("check-config", false, "validate only")
	checkApp := flag.Bool("check-app", false, "validate application credentials without connecting")
	v := flag.Bool("version", false, "print version")
	dry := flag.Bool("dry-run", false, "force no WOL")
	flag.Parse()
	if *v {
		fmt.Println(version)
		return
	}
	if *checkApp {
		if _, err := loadApp(*appPath); err != nil {
			log.Fatal(err)
		}
		fmt.Println("app_credentials_valid")
		return
	}
	c, err := loadConfig(*config)
	if err != nil {
		log.Fatal(err)
	}
	if *check {
		fmt.Println("configuration_valid")
		return
	}
	if *mode != "" {
		if *mode != "live" && *mode != "dry-run" {
			log.Fatal("invalid_mode")
		}
		c.DryRun = *mode == "dry-run"
		if err = atomicJSON(*config, c); err != nil {
			log.Fatal("config_save_failed")
		}
		fmt.Println("mode_saved")
		return
	}
	if *dry {
		c.DryRun = true
	}
	app, err := loadApp(*appPath)
	if err != nil {
		log.Fatal(err)
	}
	a := newAgent(c, *auth, *status)
	a.App = app
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *list || *validate || *accountInfo {
		if readJSON(*auth, &a.Auth) != nil || regions[a.Auth.Region] == "" || a.Auth.AT == "" {
			log.Fatal("login_required")
		}
		if *accountInfo {
			info, e := a.accountInfo(ctx)
			if e != nil {
				log.Fatal(e)
			}
			if json.NewEncoder(os.Stdout).Encode(info) != nil {
				log.Fatal("output_failed")
			}
		} else if *list {
			switches, e := a.listSwitches(ctx)
			if e != nil {
				log.Fatal(e)
			}
			if json.NewEncoder(os.Stdout).Encode(map[string]any{"switches": switches}) != nil {
				log.Fatal("output_failed")
			}
		} else {
			if e := a.validateTargets(ctx); e != nil {
				log.Fatal(e)
			}
			fmt.Println("targets_authorized")
		}
		return
	}
	if *login {
		var data struct {
			Account  string `json:"account"`
			Password string `json:"password"`
			Country  string `json:"countryCode"`
			Region   string `json:"region"`
		}
		err = json.NewDecoder(io.LimitReader(os.Stdin, 8192)).Decode(&data)
		if err == nil {
			err = a.login(ctx, strings.TrimSpace(data.Account), data.Password, data.Country, data.Region)
		} else {
			err = errors.New("invalid_login_fields")
		}
		data.Password = ""
		if err != nil {
			a.event("authorization_required", err.Error(), "", "")
			fmt.Println("login_failed")
			os.Exit(1)
		}
		a.event("authorized", "credentials_saved_without_password", "", "")
		fmt.Println("login_success")
		return
	}
	if *refresh {
		if readJSON(*auth, &a.Auth) != nil {
			log.Fatal("login_required")
		}
		if err = a.refresh(ctx); err != nil {
			log.Fatal(err)
		}
		fmt.Println("refresh_success")
		return
	}
	a.run(ctx)
}
