#!/bin/sh
set -eu
cd "$(dirname "$0")"
sha256sum -c SHA256SUMS
./ewelink-wol --version
./ewelink-wol --config ./config.json --check-config
for source in controller.lua manage.lua view_model.lua status.lua advanced.lua login.lua; do lua -e "assert(loadfile('$source'))"; done
lua -e "local f,_,e=require('luci.template.parser').parse('app.htm'); assert(f,e)"
lua test-manage.lua manage.lua
backup="/root/ewelink-wol-backup-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$backup"
chmod 700 "$backup"
for path in /usr/bin/ewelink-wol /etc/ewelink-wol/config.json /etc/ewelink-wol/client-app.json /etc/ewelink-wol/auth.json /etc/ewelink-wol/account.json /etc/init.d/ewelink-wol /etc/hotplug.d/iface/95-ewelink-wol /usr/lib/lua/luci/controller/ewelink_wol.lua /usr/lib/lua/luci/model/ewelink_wol.lua /usr/lib/lua/luci/model/ewelink_wol_view.lua /usr/lib/lua/luci/model/cbi/ewelink_wol/status.lua /usr/lib/lua/luci/model/cbi/ewelink_wol/advanced.lua /usr/lib/lua/luci/model/cbi/ewelink_wol/login.lua /usr/lib/lua/luci/view/ewelink_wol/app.htm /www/luci-static/resources/ewelink-wol/app.css /www/luci-static/resources/ewelink-wol/app.js; do
    if [ -f "$path" ]; then mkdir -p "$backup$(dirname "$path")"; cp -p "$path" "$backup$path"; fi
done
was_running=0
pidof ewelink-wol >/dev/null 2>&1 && was_running=1
mkdir -p /etc/ewelink-wol /usr/lib/lua/luci/model/cbi/ewelink_wol /usr/lib/lua/luci/view/ewelink_wol /www/luci-static/resources/ewelink-wol /var/run/ewelink-wol
chmod 700 /etc/ewelink-wol /var/run/ewelink-wol
cp ewelink-wol /usr/bin/ewelink-wol.new
chmod 755 /usr/bin/ewelink-wol.new
mv /usr/bin/ewelink-wol.new /usr/bin/ewelink-wol
if [ ! -f /etc/ewelink-wol/config.json ]; then cp config.json /etc/ewelink-wol/config.json; fi
chmod 600 /etc/ewelink-wol/config.json
cp ewelink-wol.init /etc/init.d/ewelink-wol
cp 95-ewelink-wol /etc/hotplug.d/iface/95-ewelink-wol
chmod 755 /etc/init.d/ewelink-wol /etc/hotplug.d/iface/95-ewelink-wol
cp controller.lua /usr/lib/lua/luci/controller/ewelink_wol.lua
cp manage.lua /usr/lib/lua/luci/model/ewelink_wol.lua
cp view_model.lua /usr/lib/lua/luci/model/ewelink_wol_view.lua
cp status.lua advanced.lua login.lua /usr/lib/lua/luci/model/cbi/ewelink_wol/
cp app.htm /usr/lib/lua/luci/view/ewelink_wol/app.htm
cp app.js /www/luci-static/resources/ewelink-wol/
rm -f /www/luci-static/resources/ewelink-wol/app.css
chmod 644 /usr/lib/lua/luci/controller/ewelink_wol.lua /usr/lib/lua/luci/model/ewelink_wol.lua /usr/lib/lua/luci/model/ewelink_wol_view.lua /usr/lib/lua/luci/model/cbi/ewelink_wol/*.lua /usr/lib/lua/luci/view/ewelink_wol/app.htm /www/luci-static/resources/ewelink-wol/*
if [ -s /etc/ewelink-wol/auth.json ]; then
    umask 077
    if /usr/bin/ewelink-wol --account-info > /etc/ewelink-wol/account.json.new 2>/dev/null; then
        mv /etc/ewelink-wol/account.json.new /etc/ewelink-wol/account.json
    else rm -f /etc/ewelink-wol/account.json.new; fi
    if /usr/bin/ewelink-wol --list-switches > /var/run/ewelink-wol/cloud-switches.json.new 2>/dev/null; then
        mv /var/run/ewelink-wol/cloud-switches.json.new /var/run/ewelink-wol/cloud-switches.json
    else rm -f /var/run/ewelink-wol/cloud-switches.json.new; fi
fi
lua -e "local f=require('nixio.fs'); for _,m in ipairs({'luci.controller.ewelink_wol','luci.model.ewelink_wol','luci.model.ewelink_wol_view'}) do local p='/tmp/luci-modulecache/'..m:gsub('.',function(c)return string.format('%02X',c:byte())end); if f.access(p) then f.remove(p) end end; f.remove('/tmp/luci-indexcache')"
if [ "$was_running" = 1 ]; then
    if [ -s /etc/ewelink-wol/client-app.json ]; then
        /etc/init.d/ewelink-wol restart
    else
        /etc/init.d/ewelink-wol disable
        /etc/init.d/ewelink-wol stop
        printf '%s\n' 'Application credentials required. Service stopped; configure them in LuCI Advanced before logging in again.'
    fi
fi
printf 'Installed %s. Backup: %s\nOpen LuCI /admin/services/ewelink_wol\n' "$(./ewelink-wol --version)" "$backup"
