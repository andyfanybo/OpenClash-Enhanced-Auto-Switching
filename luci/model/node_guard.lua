local fs = require "nixio.fs"
local nixio = require "nixio"
local json = require "luci.jsonc"
local sys = require "luci.sys"
local util = require "luci.util"
local datatypes = require "luci.cbi.datatypes"
local M = {}
local CONFIG = "/etc/node-guard/config.json"
local INIT = "/etc/init.d/node-guard"

local function read_json(path)
    local raw = fs.readfile(path)
    if not raw then return nil end
    local value = json.parse(raw)
    if type(value) ~= "table" then return nil end
    return value
end

local function service()
    local ok, data = pcall(util.ubus, "service", "list", { name = "node-guard" })
    local running = false
    if ok and data and data["node-guard"] then
        for _, instance in pairs(data["node-guard"].instances or {}) do
            if instance.running then running = true end
        end
    end
    return running, sys.call(INIT .. " enabled >/dev/null 2>&1") == 0
end

local function command(action)
    local allowed = { start = true, stop = true, restart = true, enable = true, disable = true }
    if not allowed[action] then return false end
    return sys.call(INIT .. " " .. action .. " >/dev/null 2>&1") == 0
end

-- Advisory locking is released by the OS even if a LuCI request terminates.
local function locked(fn)
    local lock = nixio.open("/var/lock/node-guard-luci", "w", "600")
    if not lock or not lock:lock("tlock") then
        if lock then lock:close() end
        return nil, "另一个操作正在应用，请稍后重试"
    end
    local success, ok, err = pcall(fn)
    lock:close()
    if not success then return nil, "应用失败，原有代理服务不受影响" end
    return ok, err
end

local function atomic_write(raw)
    local marker = os.tmpname()
    local temp = "/etc/node-guard/.config-" .. fs.basename(marker)
    local ok = fs.writefile(temp, raw)
    if ok then ok = fs.chmod(temp, "600") and fs.rename(temp, CONFIG) end
    fs.unlink(temp)
    fs.unlink(marker)
    return ok
end

local function http_url(value)
    if type(value) ~= "string" or #value > 2048 or value:find("[%z\1-\32\127]") or value:find("#", 1, true) then return false end
    local authority = value:match("^https?://([^/?#]+)")
    if not authority or authority:find("@", 1, true) then return false end
    local host, port
    if authority:sub(1, 1) == "[" then
        host, port = authority:match("^%[([^%]]+)%]:?(%d*)$")
        if not host or not datatypes.ip6addr(host) or authority:sub(-1) == ":" then return false end
    else
        host, port = authority:match("^([^:]+):?(%d*)$")
        if not host or not datatypes.host(host) or authority:sub(-1) == ":" then return false end
    end
    return port == "" or (tonumber(port) and tonumber(port) >= 1 and tonumber(port) <= 65535)
end

local function integer(value, low, high)
    return type(value) == "number" and value == math.floor(value) and value >= low and value <= high
end

function M.validate(data, current)
    if type(data) ~= "table" or type(data.enabled) ~= "boolean" then return nil, "请选择服务启用状态" end
    if not integer(data.interval_ms, 100, 60000) then return nil, "检测间隔应为 100～60000 毫秒的整数" end
    if not integer(data.timeout_ms, 100, 30000) then return nil, "检测超时应为 100～30000 毫秒的整数" end
    if not http_url(data.probe_url) then return nil, "请填写有效的 HTTP 或 HTTPS 检测地址" end
    if data.mode ~= "all" and data.mode ~= "selected" then return nil, "请选择监控范围" end
    if type(data.groups) ~= "table" or #data.groups > 256 then return nil, "策略组列表无效" end
    for key, _ in pairs(data.groups) do
        if not integer(key, 1, #data.groups) then return nil, "策略组列表必须是数组" end
    end
    if data.mode == "selected" and #data.groups == 0 then return nil, "请至少选择一个策略组" end
    local groups, seen = {}, {}
    for _, name in ipairs(data.groups) do
        if type(name) ~= "string" or #name == 0 or #name > 512 or name:find("[%z\1-\31\127]") then
            return nil, "策略组名称无效"
        end
        if name == "GLOBAL" then return nil, "GLOBAL 不参与监控" end
        if not seen[name] then groups[#groups + 1] = name; seen[name] = true end
    end
    local config = {}
    for key, value in pairs(current or {}) do config[key] = value end
    config.interval_ms = data.interval_ms
    config.timeout_ms = data.timeout_ms
    config.probe_url = data.probe_url
    config.groups = data.mode == "all" and {} or groups
    return config
end

function M.save(data)
    return locked(function()
        local raw = fs.readfile(CONFIG)
        local current = raw and json.parse(raw)
        if type(current) ~= "table" then return nil, "无法读取现有配置，请先检查服务安装" end
        local next_config, err = M.validate(data, current)
        if not next_config then return nil, err end
        local was_running, was_enabled = service()
        if not atomic_write(json.stringify(next_config)) then return nil, "配置写入失败" end
        local ok
        if data.enabled then ok = command("enable") and command("restart")
        else ok = command("disable") and command("stop") end
        if not ok then
            atomic_write(raw)
            command(was_enabled and "enable" or "disable")
            command(was_running and "restart" or "stop")
            return nil, "服务操作失败，已尝试恢复原配置"
        end
        return true
    end)
end

function M.control(action)
    if action ~= "start" and action ~= "stop" and action ~= "restart" then return nil, "不支持的操作" end
    return locked(function()
        if not command(action) then return nil, "服务操作失败，请查看系统日志" end
        return true
    end)
end

local function switchable(kind)
    return kind == "Selector" or kind == "URLTest" or kind == "Fallback"
end

local function choices(config, status)
    local header_path, header
    if config.secret_file and config.secret_file ~= "" then
        local secret = fs.readfile(config.secret_file)
        if not secret then return {}, "无法读取控制接口认证文件" end
        secret = secret:gsub("[\r\n]+$", "")
        if secret:find("[\r\n]") then return {}, "控制接口认证文件格式无效" end
        header_path = os.tmpname()
        header = 'header = "Authorization: Bearer ' .. secret:gsub("\\", "\\\\"):gsub('"', '\\"') .. '"\n'
        if not fs.writefile(header_path, header) or not fs.chmod(header_path, "600") then
            fs.unlink(header_path)
            return {}, "无法准备控制接口请求"
        end
    end
    local controller = config.controller or "http://127.0.0.1:9090"
    local proxies
    if http_url(controller) and not controller:find("?", 1, true) then
        local cmd = "curl -fsS --noproxy '*' --connect-timeout 1 --max-time 2 "
        if header_path then cmd = cmd .. "--config " .. util.shellquote(header_path) .. " " end
        cmd = cmd .. "--url " .. util.shellquote(controller:gsub("/+$", "") .. "/proxies") .. " 2>/dev/null"
        local pipe = io.popen(cmd)
        if pipe then
            local raw = pipe:read(2 * 1024 * 1024)
            pipe:close()
            local payload = raw and json.parse(raw)
            proxies = payload and payload.proxies
        end
    end
    if header_path then fs.unlink(header_path) end
    local groups, seen = {}, {}
    if type(proxies) == "table" then
        for name, proxy in pairs(proxies) do
            if name ~= "GLOBAL" and switchable(proxy.type) then
                groups[#groups + 1] = { name = name, kind = proxy.type, selected = proxy.now }
                seen[name] = true
            end
        end
    end
    for _, name in ipairs(config.groups or {}) do
        if not seen[name] then groups[#groups + 1] = { name = name, kind = "未发现" }; seen[name] = true end
    end
    if not proxies then
        for _, group in ipairs(status.groups or {}) do
            if not seen[group.group] then groups[#groups + 1] = { name = group.group, kind = group.type, selected = group.selected }; seen[group.group] = true end
        end
    end
    table.sort(groups, function(a, b) return a.name < b.name end)
    return groups, not proxies and "暂时无法读取内核策略组，已显示现有配置和运行记录" or nil
end

function M.state(with_settings)
    local running, enabled = service()
    local config = read_json(CONFIG)
    local status = read_json(config and config.status_file or "/tmp/node-guard/status.json") or {}
    local result = { running = running, enabled = enabled, status = status }
    if with_settings then
        if not config then result.error = "无法读取服务配置"; return result end
        result.config = {
            interval_ms = config.interval_ms or 1000,
            timeout_ms = config.timeout_ms or 800,
            probe_url = config.probe_url or "https://www.gstatic.com/generate_204",
            groups = config.groups or {},
            exclude_groups = config.exclude_groups or {}
        }
        result.choices, result.choices_error = choices(config, status)
    end
    return result
end

return M
