#!/bin/sh
set -eu

project_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
test -f /etc/config/openclash
test -x /usr/bin/node-guard

mkdir -p /usr/lib/lua/luci/controller /usr/lib/lua/luci/model /www/luci-static/resources/view/node_guard
cp "$project_root/luci/model/node_guard.lua" /usr/lib/lua/luci/model/node_guard.lua
cp "$project_root/luci/resources/view/node_guard/config.js" /www/luci-static/resources/view/node_guard/config.js
cp "$project_root/luci/controller/node_guard.lua" /usr/lib/lua/luci/controller/node_guard.lua
chmod 644 /usr/lib/lua/luci/model/node_guard.lua /usr/lib/lua/luci/controller/node_guard.lua /www/luci-static/resources/view/node_guard/config.js
for cache in /tmp/luci-indexcache*; do
    if [ -f "$cache" ]; then rm -f "$cache"; fi
done
echo 'Installed: OpenClash -> 节点自动切换'
