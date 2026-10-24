module("luci.controller.ewelink_wol", package.seeall)

function index()
    if not nixio.fs.access("/usr/bin/ewelink-wol") then return end
    entry({"admin", "services", "ewelink_wol"}, form("ewelink_wol/status"), _("易微联唤醒"), 96).dependent = false
    entry({"admin", "services", "ewelink_wol", "advanced"}, form("ewelink_wol/advanced")).leaf = true
    entry({"admin", "services", "ewelink_wol", "login"}, form("ewelink_wol/login")).leaf = true
    entry({"admin", "services", "ewelink_wol", "state"}, call("state")).leaf = true
end

function state()
    local http=require("luci.http")
    if http.getenv("REQUEST_METHOD")~="GET" then http.status(405,"Method Not Allowed");return end
    http.header("Cache-Control","no-store")
    http.prepare_content("application/json")
    http.write_json(require("luci.model.ewelink_wol").snapshot())
end
