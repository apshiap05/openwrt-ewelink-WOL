module("luci.model.ewelink_wol", package.seeall)
local fs = require("nixio.fs")
local json = require("luci.jsonc")
local sys = require("luci.sys")
local http = require("luci.http")
local nixio = require("nixio")
local dsp = require("luci.dispatcher")
local CONFIG, STATE = "/etc/ewelink-wol/config.json", "/var/run/ewelink-wol/status.json"
local CACHE, ACTIONS = "/var/run/ewelink-wol/cloud-switches.json", "/var/run/ewelink-wol/ui-events.json"
local ACCOUNT = "/etc/ewelink-wol/account.json"
local APP = "/etc/ewelink-wol/client-app.json"
local LOCK = "/var/lock/ewelink-wol-ui"

local function read(path) return json.parse(fs.readfile(path) or "{}") or {} end
local function write(path, value)
    local tmp=path..".new"
    assert(fs.writefile(tmp, json.stringify(value).."\n"), "write_failed")
    assert(fs.chmod(tmp, "600"), "permissions_failed")
    assert(fs.rename(tmp,path), "save_failed")
end
local function running()
    local all=json.parse(sys.exec("ubus call service list '{\"name\":\"ewelink-wol\"}' 2>/dev/null")) or {}
    for _, instance in pairs(all["ewelink-wol"] and all["ewelink-wol"].instances or {}) do
        if instance.running then return true, instance.pid end
    end
    return false
end
local function log(action)
    nixio.syslog("notice", "ewelink-wol ui_action="..action)
    local events=read(ACTIONS); if type(events)~="table" then events={} end
    events[#events+1]={time=os.date("!%Y-%m-%dT%H:%M:%SZ"),kind="ui_"..action}
    while #events>15 do table.remove(events,1) end
    write(ACTIONS,events)
end
local function enabled(t) return t.enabled ~= false end
local function checkedConfig()
    local c=read(CONFIG)
    assert(type(c.targets)=="table" and c.region and type(c.dry_run)=="boolean", "config_invalid")
    return c
end
local function interfaces()
    local result={}
    for name in fs.dir("/sys/class/net") or function() end do
        if name~="lo" and name:match("^[%w_.:%-]+$") then result[#result+1]=name end
    end
    table.sort(result,function(a,b) if a=="br-lan" then return b~="br-lan" elseif b=="br-lan" then return false else return a<b end end)
    return result
end

function snapshot()
    local c, status=read(CONFIG),read(STATE)
    local live,pid=running()
    local cache=read(CACHE)
    local account=read(ACCOUNT);account={display=account.display,region=account.region}
    local app=read(APP)
    local appId=type(app.appId)=="string" and app.appId or ""
    local appDisplay=appId~="" and (#appId>8 and (appId:sub(1,4).."…"..appId:sub(-4)) or "已配置") or "未配置"
    local targets={}; local active=0
    for _, t in ipairs(c.targets or {}) do
        if enabled(t) then active=active+1 end
        targets[#targets+1]={name=t.name,device_id=t.device_id,mac=t.mac,interface=t.interface,enabled=enabled(t),state=live and enabled(t) and (status.states or {})[t.device_id] or nil,last_wake=(status.last_wake or {})[t.device_id]}
    end
    local events={}
    for _, event in ipairs(status.events or {}) do events[#events+1]=event end
    for _, event in ipairs(read(ACTIONS)) do events[#events+1]=event end
    table.sort(events,function(a,b) return (a.time or "")>(b.time or "") end)
    while #events>25 do table.remove(events) end
    return {version=status.version or "1.2.0",running=live,pid=pid,boot_enabled=sys.call("/etc/init.d/ewelink-wol enabled >/dev/null 2>&1")==0,phase=live and status.phase or "stopped",dry_run=c.dry_run~=false,cooldown_seconds=c.cooldown_seconds or 30,targets=targets,active_count=active,has_app=appId~="" and type(app.appSecret)=="string" and app.appSecret~="",app_display=appDisplay,has_auth=fs.access("/etc/ewelink-wol/auth.json"),account=account,wake_count=live and (status.wake_count or 0) or 0,reconnect_count=status.reconnect_count or 0,started=live and status.started or nil,last_seen=live and status.last_seen or nil,last_event=status.last_event,events=events,interfaces=interfaces(),cloud_switches=cache.switches or {},cloud_updated=cache.updated,server_time=os.date("!%Y-%m-%dT%H:%M:%SZ")}
end

local function applyConfig(c, verify)
    write(CONFIG..".pending",c)
    assert(sys.call("/usr/bin/ewelink-wol --config /etc/ewelink-wol/config.json.pending --check-config >/dev/null 2>&1")==0,"invalid_mapping")
    if verify then
        assert(fs.access("/etc/ewelink-wol/auth.json"),"login_required")
        assert(sys.call("/usr/bin/ewelink-wol --config /etc/ewelink-wol/config.json.pending --validate-targets >/dev/null 2>&1")==0,"cloud_validation_failed")
    end
    local old=checkedConfig()
    write(CONFIG..".previous",old)
    assert(fs.rename(CONFIG..".pending",CONFIG),"save_failed")
    if running() then assert(sys.call("/etc/init.d/ewelink-wol restart >/dev/null 2>&1")==0,"restart_failed") end
end
local function field(name) return http.formvalue(name) or "" end
local function targetIndex(c,id)
    for i,t in ipairs(c.targets) do if t.device_id==id then return i,t end end
end
local function scan()
    assert(fs.access("/etc/ewelink-wol/auth.json"),"login_required")
    local cached=read(CACHE)
    if cached.at and os.time()-cached.at<30 then return "scan_cached" end
    local result=json.parse(sys.exec("/usr/bin/ewelink-wol --list-switches 2>/dev/null"))
    assert(result and type(result.switches)=="table","scan_failed")
    result.at=os.time(); result.updated=os.date("!%Y-%m-%dT%H:%M:%SZ");write(CACHE,result)
    log("scan");return "scanned"
end

local function login()
    assert(fs.access(APP),"app_required")
    local account,password=field("account"),field("password")
    local masked=account:match("@") and (account:sub(1,1).."***@"..(account:match("@(.+)$") or "")) or (#account>=7 and (account:sub(1,3).."****"..account:sub(-4)) or "已授权账号")
    local region,country=field("region"),field("country")
    assert(#account>0 and #account<=200 and #password>0 and #password<=500,"login_fields")
    assert(({cn=true,as=true,eu=true,us=true})[region] and country:match("^%+%d%d?%d?%d?$"),"login_fields")
    local previous=read(ACCOUNT)
    assert(fs.writefile("/etc/ewelink-wol/login-config.json",'{"region":"'..region..'","dry_run":true,"cooldown_seconds":30,"targets":[]}'),"write_failed")
    assert(fs.chmod("/etc/ewelink-wol/login-config.json","600"),"permissions_failed")
    local data=json.stringify({account=account,password=password,countryCode=country,region=region})
    local pipe=io.popen("umask 077; rm -f /tmp/ewelink-wol-login-status.json; /usr/bin/ewelink-wol --config /etc/ewelink-wol/login-config.json --status /tmp/ewelink-wol-login-status.json --login-stdin >/tmp/ewelink-wol-login.log 2>&1","w")
    password=nil;account=nil
    assert(pipe,"login_start_failed");pipe:write(data);pipe:close();data=nil
    local result=read("/tmp/ewelink-wol-login-status.json")
    assert(result.phase=="authorized","login_failed")
    local info=json.parse(sys.exec("/usr/bin/ewelink-wol --account-info 2>/dev/null")) or {display=masked,region=region}
    if not info.display or info.display=="" then info.display=masked end
    if not info.region then info.region=region end
    write(ACCOUNT,info)
    local changed=not previous.account_ref or not info.account_ref or previous.account_ref~=info.account_ref
    local c=checkedConfig(); c.region=info.region or region
    if changed then for _,target in ipairs(c.targets) do target.enabled=false end;write(CACHE,{switches={}}) end
    applyConfig(c,false)
    log("login_success");return changed and "account_changed" or "authorized"
end

local function saveApp()
    local id,secret=field("app_id"),field("app_secret")
    assert(#id>=8 and #id<=128 and id:match("^[%w_%-]+$") and #secret>=8 and #secret<=256 and secret:match("^[!-~]+$"),"invalid_app")
    local previous=read(APP)
    if previous.appId==id and previous.appSecret==secret then return "unchanged" end
    write(APP..".pending",{appId=id,appSecret=secret})
    assert(sys.call("/usr/bin/ewelink-wol --app /etc/ewelink-wol/client-app.json.pending --check-app >/dev/null 2>&1")==0,"invalid_app")
    assert(sys.call("/etc/init.d/ewelink-wol disable && /etc/init.d/ewelink-wol stop >/dev/null 2>&1")==0,"stop_failed")
    local c=checkedConfig();c.dry_run=true
    for _,target in ipairs(c.targets) do target.enabled=false end
    write(CONFIG,c)
    if fs.access("/etc/ewelink-wol/auth.json") then assert(fs.remove("/etc/ewelink-wol/auth.json"),"save_failed") end
    if fs.access(ACCOUNT) then assert(fs.remove(ACCOUNT),"save_failed") end
    write(CACHE,{switches={}})
    assert(fs.rename(APP..".pending",APP),"save_failed")
    log("app_saved");return "app_saved"
end

local function perform(action)
    if action=="enable" then
        assert(fs.access(APP),"app_required")
        assert(fs.access("/etc/ewelink-wol/auth.json"),"login_required")
        assert(sys.call("/etc/init.d/ewelink-wol enable && /etc/init.d/ewelink-wol start >/dev/null 2>&1")==0,"start_failed")
        log("enable");return "enabled"
    elseif action=="disable" then
        assert(sys.call("/etc/init.d/ewelink-wol disable && /etc/init.d/ewelink-wol stop >/dev/null 2>&1")==0,"stop_failed")
        log("disable");return "disabled"
    elseif action=="reconnect" then
        assert(running(),"service_stopped")
        local previous=tonumber(fs.readfile("/var/run/ewelink-wol/last-reconnect") or "0") or 0
        assert(os.time()-previous>=15,"reconnect_fast")
        fs.writefile("/var/run/ewelink-wol/last-reconnect",tostring(os.time()))
        assert(sys.call("/etc/init.d/ewelink-wol restart >/dev/null 2>&1")==0,"restart_failed")
        log("reconnect");return "reconnected"
    elseif action=="settings" then
        local c=checkedConfig(); local mode=field("mode");local cool=tonumber(field("cooldown"))
        assert((mode=="live" or mode=="dry-run") and cool and cool%1==0 and cool>=5 and cool<=3600,"invalid_settings")
        local dry=mode=="dry-run"
        if c.dry_run==dry and c.cooldown_seconds==cool then return "unchanged" end
        c.dry_run=dry;c.cooldown_seconds=cool;applyConfig(c,false);log("settings");return "settings_saved"
    elseif action=="toggle" then
        local c=checkedConfig();local _,target=targetIndex(c,field("id"));assert(target,"target_missing")
        local desired=field("desired");assert(desired=="on" or desired=="off","invalid_mapping")
        local wanted=desired=="on"
        if enabled(target)==wanted then return "unchanged" end
        target.enabled=wanted;applyConfig(c,wanted);log(wanted and "mapping_enabled" or "mapping_paused");return wanted and "mapping_enabled" or "mapping_paused"
    elseif action=="save_mapping" then
        local c=checkedConfig();local original,id=field("original"),field("device_id")
        local name,mac,iface=field("name"),field("mac"):lower(),field("interface")
        assert(#name>0 and #name<=96 and name:match("%S") and #id>=10 and #id<=40 and id:match("^%w+$"),"invalid_mapping")
        assert(#mac==17 and mac:match("^%x%x:%x%x:%x%x:%x%x:%x%x:%x%x$") and #iface<=32 and iface:match("^[%w_.:%-]+$") and iface~="lo" and fs.access("/sys/class/net/"..iface),"invalid_mapping")
        local index,old=targetIndex(c,original)
        assert(original=="" or index,"target_missing")
        for _,t in ipairs(c.targets) do assert(t.device_id~=id or t==old,"duplicate_switch") end
        assert(index or #c.targets<16,"too_many_targets")
        local t={name=name,device_id=id,mac=mac,interface=iface,enabled=field("mapping_enabled")=="1"}
        if old and old.name==t.name and old.device_id==t.device_id and old.mac:lower()==t.mac and old.interface==t.interface and enabled(old)==t.enabled then return "unchanged" end
        local verify=t.enabled and (not old or old.device_id~=id or not enabled(old))
        if index then c.targets[index]=t else c.targets[#c.targets+1]=t end
        applyConfig(c,verify);log("mapping_saved");return "mapping_saved"
    elseif action=="scan" then return scan()
    elseif action=="save_app" then return saveApp()
    elseif action=="login" then return login()
    else error("unknown_action") end
end

local messages={enabled="服务已启用，并随路由器启动。",disabled="服务已停用，开机自启动也已关闭。",reconnected="已重新连接；初始同步状态不会触发唤醒。",settings_saved="运行设置已保存；原有服务启停状态保持不变。",mapping_saved="设备映射已保存；已运行的服务会重新同步状态。",mapping_enabled="设备映射已启用；当前开关状态不会直接触发唤醒。",mapping_paused="设备映射已暂停，不再响应该开关。",scanned="已同步当前账号可用的会员虚拟开关。",scan_cached="刚刚已同步，已使用最近结果。",authorized="账号授权已更新，服务启停状态保持不变。",app_saved="应用凭证已保存。服务和自动启动已停用，原账号授权已清除，设备映射已暂停；请重新登录并检查映射。",unchanged="设置未变化，无需重新连接。"}
local errors={busy="另一项操作正在处理，请稍后再试。",config_invalid="配置文件异常，已保留原文件，请检查。",invalid_mapping="请检查名称、开关、单播 MAC 地址和局域网接口。",invalid_settings="请选择运行模式，冷却时间须为 5～3600 的整数秒。",invalid_app="请检查 AppID 和 AppSecret 格式；原凭证未更改。",app_required="请先在高级选项卡保存已获授权的 AppID 和 AppSecret。",login_required="请先在高级选项卡登录易微联账号。",duplicate_switch="此虚拟开关已绑定另一条映射，请编辑已有条目。",target_missing="设备映射已变化，请刷新后再操作。",too_many_targets="最多支持 16 条设备映射。",cloud_validation_failed="未能确认该开关属于当前账号。请同步云端开关，或检查网络和授权；原配置未更改。",scan_failed="同步失败，请检查云端连接或重新授权；既有服务继续运行。",login_fields="请检查账号、密码、地区与电话区号。",login_failed="授权失败，请检查账号、密码、地区和网络；密码未保存，原服务未被主动停止。",service_stopped="服务未运行，请先在基础选项卡启用服务。",reconnect_fast="刚刚已请求重新连接，请至少等待 15 秒。"}

function handle()
    if http.getenv("REQUEST_METHOD")~="POST" or field("cbi.submit")~="1" then return nil end
    if not dsp.test_post_security() then return nil,"请求校验失败，请刷新后重试。" end
    fs.mkdir("/var/run/ewelink-wol","700")
    local stat=fs.stat(LOCK); if stat and os.time()-stat.mtime>120 then fs.rmdir(LOCK) end
    if not fs.mkdir(LOCK,"700") then return nil,errors.busy end
    local action=field("action")
    local ok,result=pcall(perform,action)
    if not ok and action=="save_app" then fs.remove(APP..".pending") end
    fs.rmdir(LOCK)
    if ok then return result end
    local code=tostring(result):match("([a-z_]+)$")
    return nil,errors[code] or "操作未完成，请检查路由器服务状态；可在连接诊断中重试。"
end
function notice(code)
    if code=="account_changed" then return "账号已更换。原设备映射已暂停，请同步新账号的虚拟开关并重新绑定。" end
    return messages[code]
end
