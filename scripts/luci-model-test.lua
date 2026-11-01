-- Run on OpenWrt: lua scripts/luci-model-test.lua /path/to/luci/model/node_guard.lua
local model = assert(loadfile(arg[1]))()
local json = require "luci.jsonc"
local passed = 0
local base = {
    controller = "http://127.0.0.1:9090", secret_file = "/etc/node-guard/secret",
    groups = {}, exclude_groups = { "GLOBAL" }, max_concurrent = 16,
    status_file = "/tmp/node-guard/status.json"
}
local function input()
    return { enabled = true, interval_ms = 1000, timeout_ms = 800,
        probe_url = "https://www.gstatic.com/generate_204", mode = "all", groups = {} }
end
local function valid(data)
    local next, err = model.validate(data, base)
    assert(next, err)
    assert(next.controller == base.controller and next.secret_file == base.secret_file)
    assert(next.max_concurrent == 16 and next.exclude_groups[1] == "GLOBAL")
    passed = passed + 1
    return next
end
local function invalid(change)
    local data = input()
    change(data)
    local next, err = model.validate(data, base)
    assert(not next and type(err) == "string", "invalid input accepted")
    passed = passed + 1
end

local next = valid(input())
assert(json.stringify(next):find('"groups":%[%]'), "empty selection must remain a JSON array")
local selected = input()
selected.mode = "selected"
selected.groups = { "🤖 AI 平台", '节点 "选择"', "🤖 AI 平台" }
next = valid(selected)
assert(#next.groups == 2 and next.groups[2] == '节点 "选择"')
for _, url in ipairs({ "http://127.0.0.1:9090/test", "https://[::1]:8443/test", "https://example.com/test?a=1&b=2" }) do
    local data = input(); data.probe_url = url; valid(data)
end
invalid(function(d) d.enabled = "true" end)
invalid(function(d) d.interval_ms = 99 end)
invalid(function(d) d.interval_ms = 60001 end)
invalid(function(d) d.timeout_ms = 100.5 end)
invalid(function(d) d.timeout_ms = 30001 end)
invalid(function(d) d.mode = "selected" end)
invalid(function(d) d.groups = { key = "group" } end)
invalid(function(d) d.groups = { "GLOBAL" } end)
invalid(function(d) d.groups = { "group\nname" } end)
for _, url in ipairs({ "file:///etc/passwd", "https://user:password@example.com/", "https://example.com/#fragment", "https://example.com/\n", "https://[broken/", "https://example.com:65536/", "https://example.com:/" }) do
    invalid(function(d) d.probe_url = url end)
end
print("PASS: " .. passed .. " LuCI model validation cases; existing advanced settings preserved")
