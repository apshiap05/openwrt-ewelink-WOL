// ewelink-wol subscribes to eWeLink cloud changes and wakes local devices.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func (a *Agent) request(ctx context.Context, base, path string, body any, signed bool) (Response, error) {
	var out Response
	if gap := 1200*time.Millisecond - time.Since(a.lastRequest); gap > 0 {
		if err := wait(ctx, gap); err != nil {
			return out, err
		}
	}
	a.lastRequest = time.Now()
	var b []byte
	var err error
	method := "GET"
	if body != nil {
		method = "POST"
		b, err = json.Marshal(body)
		if err != nil {
			return out, errors.New("request_encoding_failed")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(b))
	if err != nil {
		return out, errors.New("invalid_request")
	}
	req.Header.Set("X-CK-Appid", a.App.ID)
	req.Header.Set("X-CK-Nonce", nonce())
	req.Header.Set("Content-Type", "application/json")
	if signed {
		h := hmac.New(sha256.New, []byte(a.App.Secret))
		h.Write(b)
		req.Header.Set("Authorization", "Sign "+base64.StdEncoding.EncodeToString(h.Sum(nil)))
	} else {
		req.Header.Set("Authorization", "Bearer "+a.Auth.AT)
	}
	res, err := a.Client.Do(req)
	if err != nil {
		return out, errors.New("cloud_network_error")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 {
		return out, apiError{401}
	}
	if res.StatusCode != 200 {
		return out, fmt.Errorf("cloud_http_%d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return out, errors.New("cloud_response_invalid")
	}
	if json.Unmarshal(raw, &out) != nil {
		return out, errors.New("cloud_response_invalid")
	}
	if out.Error != 0 {
		return out, apiError{out.Error}
	}
	return out, nil
}
func nonce() string { b := make([]byte, 4); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (a *Agent) login(ctx context.Context, account, password, country, region string) error {
	if regions[region] == "" || !regexp.MustCompile(`^\+\d{1,4}$`).MatchString(country) || account == "" || password == "" {
		return errors.New("invalid_login_fields")
	}
	body := map[string]string{"countryCode": country, "password": password}
	if strings.Contains(account, "@") {
		body["email"] = account
	} else {
		if !strings.HasPrefix(account, "+") {
			account = country + account
		}
		body["phoneNumber"] = account
	}
	var out Response
	var err error
	for i := 0; i < 2; i++ {
		out, err = a.request(ctx, a.APIBase(region), "/v2/user/login", body, true)
		var e apiError
		if errors.As(err, &e) && e.Code == 10004 {
			var r struct {
				Region string `json:"region"`
			}
			_ = json.Unmarshal(out.Data, &r)
			if regions[r.Region] != "" {
				region = r.Region
				continue
			}
		}
		break
	}
	body["password"] = ""
	if err != nil {
		return err
	}
	var data struct {
		AT   string `json:"at"`
		RT   string `json:"rt"`
		User struct {
			APIKey string `json:"apikey"`
		} `json:"user"`
	}
	if json.Unmarshal(out.Data, &data) != nil || data.AT == "" || data.RT == "" || data.User.APIKey == "" {
		return errors.New("login_response_invalid")
	}
	a.Auth = Auth{AT: data.AT, RT: data.RT, APIKey: data.User.APIKey, Region: region, Updated: time.Now()}
	if err = a.validateTargets(ctx); err != nil {
		return err
	}
	return atomicJSON(a.AuthPath, a.Auth)
}

type VirtualSwitch struct {
	Name  string `json:"name"`
	ID    string `json:"device_id"`
	State string `json:"state"`
}

func maskAccount(s string) string {
	if at := strings.IndexByte(s, '@'); at >= 0 {
		if at == 0 {
			return "***" + s[at:]
		}
		return s[:1] + "***" + s[at:]
	}
	if strings.HasPrefix(s, "+86") && len(s) == 14 {
		return "+86 " + s[3:6] + "****" + s[len(s)-4:]
	}
	if len(s) >= 10 {
		return s[:3] + "****" + s[len(s)-4:]
	}
	if len(s) >= 7 {
		return s[:1] + "****" + s[len(s)-2:]
	}
	return "已授权账号"
}
func (a *Agent) accountInfo(ctx context.Context) (map[string]string, error) {
	out, err := a.request(ctx, a.APIBase(a.Auth.Region), "/v2/user/profile", nil, false)
	if err != nil {
		return nil, err
	}
	var data struct {
		User struct {
			Phone string `json:"phoneNumber"`
			Email string `json:"email"`
		} `json:"user"`
	}
	if json.Unmarshal(out.Data, &data) != nil {
		return nil, errors.New("profile_invalid")
	}
	id := data.User.Phone
	if id == "" {
		id = data.User.Email
	}
	ref := sha256.Sum256([]byte(a.Auth.APIKey))
	return map[string]string{"display": maskAccount(id), "region": a.Auth.Region, "account_ref": hex.EncodeToString(ref[:])}, nil
}

func (a *Agent) listSwitches(ctx context.Context) ([]VirtualSwitch, error) {
	out, err := a.request(ctx, a.APIBase(a.Auth.Region), "/v2/device/thing?num=0", nil, false)
	if err != nil {
		return nil, err
	}
	var data struct {
		Things []struct {
			Item struct {
				ID     string          `json:"deviceid"`
				Name   string          `json:"name"`
				Params json.RawMessage `json:"params"`
				Extra  struct {
					UIID int `json:"uiid"`
				} `json:"extra"`
			} `json:"itemData"`
		} `json:"thingList"`
	}
	if json.Unmarshal(out.Data, &data) != nil {
		return nil, errors.New("device_list_invalid")
	}
	switches := []VirtualSwitch{}
	for _, item := range data.Things {
		if item.Item.Extra.UIID == 264 && validID.MatchString(item.Item.ID) {
			switches = append(switches, VirtualSwitch{Name: item.Item.Name, ID: item.Item.ID, State: stateOf(item.Item.Params)})
		}
	}
	return switches, nil
}
func (a *Agent) validateTargets(ctx context.Context) error {
	switches, err := a.listSwitches(ctx)
	if err != nil {
		return err
	}
	for _, t := range a.C.activeTargets() {
		found := false
		for _, item := range switches {
			if item.ID == t.DeviceID {
				found = true
			}
		}
		if !found {
			return errors.New("configured_virtual_switch_not_authorized")
		}
	}
	return nil
}
func (a *Agent) refresh(ctx context.Context) error {
	if a.Auth.RT == "" {
		return errors.New("reauthorization_required")
	}
	out, err := a.request(ctx, a.APIBase(a.Auth.Region), "/v2/user/refresh", map[string]string{"rt": a.Auth.RT}, true)
	if err != nil {
		return err
	}
	var data struct {
		AT string `json:"at"`
		RT string `json:"rt"`
	}
	if json.Unmarshal(out.Data, &data) != nil || data.AT == "" || data.RT == "" {
		return errors.New("refresh_response_invalid")
	}
	a.Auth.AT = data.AT
	a.Auth.RT = data.RT
	a.Auth.Updated = time.Now()
	if err = atomicJSON(a.AuthPath, a.Auth); err != nil {
		return errors.New("auth_save_failed")
	}
	a.event("", "token_refreshed", "", "")
	return nil
}
