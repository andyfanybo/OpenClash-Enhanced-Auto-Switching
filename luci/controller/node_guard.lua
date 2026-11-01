module("luci.controller.node_guard", package.seeall)

function index()
    if not nixio.fs.access("/etc/config/openclash") or not nixio.fs.access("/usr/bin/node-guard") then
        return
    end
    local page = entry({"admin", "services", "openclash", "node-guard"}, view("node_guard/config"), "节点自动切换", 35)
    page.leaf = true
    page.acl_depends = { "luci-app-openclash" }
    entry({"admin", "services", "openclash", "node_guard_state"}, call("action_state")).leaf = true
    entry({"admin", "services", "openclash", "node_guard_save"}, post("action_save")).leaf = true
    entry({"admin", "services", "openclash", "node_guard_control"}, post("action_control")).leaf = true
end

local function reply(code, data)
    local http = require "luci.http"
    http.status(code)
    http.header("Cache-Control", "no-store")
    http.prepare_content("application/json")
    http.write_json(data)
end

local function can_write()
    local ctx = require("luci.dispatcher").context
    if not ctx.authsession then return false end
    local ok, result = pcall(require("luci.util").ubus, "session", "access", {
        ubus_rpc_session = ctx.authsession,
        scope = "access-group", object = "luci-app-openclash", ["function"] = "write"
    })
    return ok and result and result.access == true
end

function action_state()
    local http = require "luci.http"
    local model = require "luci.model.node_guard"
    reply(200, model.state(http.formvalue("settings") == "1"))
end

function action_save()
    if not can_write() then return reply(403, { error = "没有修改 OpenClash 配置的权限" }) end
    local raw = require("luci.http").formvalue("data")
    if type(raw) ~= "string" or #raw > 65536 then
        return reply(400, { error = "配置数据无效" })
    end
    local data = require("luci.jsonc").parse(raw)
    local ok, err = require("luci.model.node_guard").save(data)
    if not ok then return reply(400, { error = err }) end
    reply(200, { ok = true })
end

function action_control()
    if not can_write() then return reply(403, { error = "没有管理服务的权限" }) end
    local action = require("luci.http").formvalue("action")
    local ok, err = require("luci.model.node_guard").control(action)
    if not ok then return reply(400, { error = err }) end
    reply(200, { ok = true })
end
