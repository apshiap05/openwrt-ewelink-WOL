-- Router-side regression test: all service, file and login side effects are mocked.
local json=require("luci.jsonc")
local files,dirs,calls={}, {}, {}
local method,fields,csrf,live="GET",{},true,true
local profileRef="PRIVATE-REF"
local configPath="/etc/ewelink-wol/config.json"
local fixture={region="cn",dry_run=false,cooldown_seconds=30,targets={{name="desktop",device_id="testswitch0001",mac="02:00:00:00:00:01",interface="br-lan"},{name="nas",device_id="testswitch0002",mac="02:00:00:00:00:02",interface="br-lan"}}}
local function reset()
 files={[configPath]=json.stringify(fixture),["/etc/ewelink-wol/client-app.json"]='{"appId":"TESTAPPID0001","appSecret":"PRIVATE-APP-SECRET"}',["/etc/ewelink-wol/auth.json"]='{"at":"PRIVATE-AT"}',["/etc/ewelink-wol/account.json"]='{"display":"138****0000","account_ref":"PRIVATE-REF","region":"cn"}'}
 dirs={};calls={};fields={};method="POST";csrf=true;live=true
end
package.loaded["nixio.fs"]={readfile=function(p)return files[p]end,writefile=function(p,s)files[p]=s;return #s end,chmod=function(_,mode)assert(mode=="600","chmod requires OpenWrt octal string");return true end,rename=function(a,b)files[b]=files[a];files[a]=nil;return true end,remove=function(p)files[p]=nil;return true end,access=function(p)return files[p]~=nil or p=="/sys/class/net/br-lan" end,dir=function()return function()end end,mkdir=function(p,mode)assert(mode=="700","mkdir requires OpenWrt octal string");if dirs[p] then return false end;dirs[p]=true;return true end,rmdir=function(p)dirs[p]=nil;return true end,stat=function(p)if dirs[p]then return {mtime=os.time()}end end}
package.loaded["luci.sys"]={call=function(command)calls[#calls+1]=command;return 0 end,exec=function(command)if command:match("ubus")then return live and '{"ewelink-wol":{"instances":{"one":{"running":true,"pid":123}}}}' or '{}' elseif command:match("account%-info")then return json.stringify({display="138****0000",region="cn",account_ref=profileRef}) end;return '{}' end}
package.loaded["luci.http"]={getenv=function()return method end,formvalue=function(k)return fields[k]end}
package.loaded["luci.dispatcher"]={test_post_security=function()return csrf end}
package.loaded["nixio"]={syslog=function()end}
dofile(arg[1]);local ui=package.loaded["luci.model.ewelink_wol"]
local function submit(action,extra)
 fields=extra or {};fields["cbi.submit"]="1";fields.action=action
 return ui.handle()
end
reset();method="GET";submit("disable");assert(#calls==0,"GET disabled service")
reset();csrf=false;submit("disable");assert(#calls==0,"CSRF rejection changed service")
reset();local before=files[configPath];submit("settings",{mode="live",cooldown="30"});assert(files[configPath]==before and #calls==0,"unchanged settings restarted service")
reset();before=files[configPath];assert(submit("save_mapping",{original="testswitch0002",device_id="testswitch0002",name="nas",mac="02:00:00:00:00:02",interface="br-lan",mapping_enabled="1"})=="unchanged");assert(files[configPath]==before and #calls==0,"unchanged mapping restarted service")
reset();live=false;assert(submit("settings",{mode="dry-run",cooldown="60"})=="settings_saved");for _,c in ipairs(calls)do assert(not c:match(" restart ") and not c:match(" enable"),"settings unexpectedly started service")end
reset();assert(submit("toggle",{id="testswitch0002",desired="off"})=="mapping_paused");local c=json.parse(files[configPath]);assert(c.targets[2].enabled==false and c.targets[1].enabled~=false,"wrong mapping paused")
reset();before=files[configPath];local result=submit("save_mapping",{original="testswitch0001",device_id="testswitch0002",name="duplicate",mac="02:00:00:00:00:01",interface="br-lan",mapping_enabled="1"});assert(not result and files[configPath]==before,"duplicate replaced configuration")
reset();before=files[configPath];result=submit("save_mapping",{original="testswitch0001",device_id="testswitch0001",name="bad",mac="$(danger)",interface="../etc",mapping_enabled="1"});assert(not result and files[configPath]==before and #calls==0,"invalid input reached a command")
reset();io.popen=function()return nil end;before=files[configPath];result=submit("login",{account="fixture@example.com",password="fixture",region="cn",country="+86"});assert(not result and #calls==0 and files[configPath]==before,"failed login stopped existing service")
reset();profileRef="PRIVATE-REF";io.popen=function()return {write=function()end,close=function()files["/tmp/ewelink-wol-login-status.json"]='{"phase":"authorized"}'end}end
assert(submit("login",{account="fixture@example.com",password="fixture",region="cn",country="+86"})=="authorized");assert(json.parse(files[configPath]).targets[1].enabled~=false,"same account paused mapping")
reset();profileRef="DIFFERENT-REF";assert(submit("login",{account="another@example.com",password="fixture",region="cn",country="+86"})=="account_changed");for _,t in ipairs(json.parse(files[configPath]).targets)do assert(t.enabled==false,"new account retained active old mapping")end
reset();local snapshot=ui.snapshot();local raw=json.stringify(snapshot);assert(not raw:find("PRIVATE%-AT") and not raw:find("PRIVATE%-REF"),"private fields exposed")
assert(not raw:find("PRIVATE%-APP%-SECRET") and snapshot.has_app,"app secret exposed")
reset();before=files[configPath];result=submit("save_app",{app_id="bad",app_secret="bad"});assert(not result and files[configPath]==before and #calls==0,"invalid app changed service")
reset();assert(submit("save_app",{app_id="NEWAPPID0002",app_secret="NEW-SECRET-0002"})=="app_saved");assert(not files["/etc/ewelink-wol/auth.json"] and not files["/etc/ewelink-wol/account.json"],"old authorization retained");c=json.parse(files[configPath]);assert(c.dry_run and c.targets[1].enabled==false and c.targets[2].enabled==false,"app change armed mappings");assert(ui.snapshot().app_display~="NEWAPPID0002" and not json.stringify(ui.snapshot()):find("NEW%-SECRET%-0002"),"app credentials exposed")
print("PASS: action isolation, CSRF, paused mappings, preserved service state, duplicate/injection rejection, login failure and redaction")
