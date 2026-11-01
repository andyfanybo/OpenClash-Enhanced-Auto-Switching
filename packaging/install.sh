#!/bin/sh
set -eu
project_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
test -f /etc/config/openclash
test -f "$project_root/dist/node-guard"
chmod 755 "$project_root/dist/node-guard"
"$project_root/dist/node-guard" -version
mkdir -p /etc/node-guard
chmod 700 /etc/node-guard
if [ -x /etc/init.d/node-guard ]; then /etc/init.d/node-guard stop; fi
cp "$project_root/dist/node-guard" /usr/bin/node-guard
cp "$project_root/packaging/node-guard.init" /etc/init.d/node-guard
chmod 755 /usr/bin/node-guard /etc/init.d/node-guard
if [ ! -f /etc/node-guard/config.json ]; then
    cp "$project_root/config.example.json" /etc/node-guard/config.json
fi
chmod 600 /etc/node-guard/config.json
sh "$project_root/packaging/install-luci.sh"
echo '安装完成，请在 LuCI 节点自动切换页面检查配置并保存启用。'
