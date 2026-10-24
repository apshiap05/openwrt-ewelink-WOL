module("luci.model.ewelink_wol_view", package.seeall)
function create(tab, Form)
    local ui=require("luci.model.ewelink_wol")
    local http=require("luci.http")
    local dsp=require("luci.dispatcher")
    local m=Form("ewelink_wol_ui")
    m.submit=false;m.reset=false;m.template="ewelink_wol/app"
    local base=dsp.build_url("admin","services","ewelink_wol")
    local function refresh(self)
        local snap=ui.snapshot()
        self.view={tab=tab,data=snap,basic=base,advanced=base.."/advanced",state_url=base.."/state",post_url=tab=="basic" and base or base.."/advanced"}
        self.view.app_open=tab=="advanced" and not snap.has_app
        self.view.login_open=tab=="advanced" and snap.has_app and not snap.has_auth
        local edit=http.formvalue("edit")
        for _,target in ipairs(snap.targets) do
            if tab=="basic" and target.device_id==edit then self.view.editor=target;self.view.original=target.device_id end
        end
        if tab=="basic" and edit=="new" then self.view.editor={enabled=true,interface="br-lan"};self.view.original="" end
        self.view.notice=ui.notice(http.formvalue("result"))
    end
    refresh(m)
    function m.handle(self)
        local result,err=ui.handle()
        if result then http.redirect((tab=="basic" and base or base.."/advanced").."?result="..result..(result=="app_saved" and "#account" or ""));return false end
        refresh(self);self.view.error=err
        if err and http.formvalue("action")=="save_mapping" then
            self.view.editor={name=http.formvalue("name"),device_id=http.formvalue("device_id"),mac=http.formvalue("mac"),interface=http.formvalue("interface"),enabled=http.formvalue("mapping_enabled")=="1"}
            self.view.original=http.formvalue("original") or ""
        end
        self.view.login_open=(self.view.data.has_app and not self.view.data.has_auth) or (err and http.formvalue("action")=="login")
        self.view.app_open=not self.view.data.has_app or (err and http.formvalue("action")=="save_app")
        return true
    end
    return m
end
