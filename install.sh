#!/bin/sh
set -eu
REPO=andyfanybo/OpenClash-Enhanced-Auto-Switching
arch="${FF_ARCH:-}"
if [ -z "$arch" ]; then
    case "$(uname -m)" in
        aarch64|arm64) arch=arm64;;
        x86_64) arch=amd64;;
        i?86) arch=386;;
        armv7*) arch=armv7;;
        armv6*) arch=armv6;;
        mips|mipsel)
            endian="$(dd if=/bin/busybox bs=1 skip=5 count=1 2>/dev/null)"
            case "$endian" in "$(printf '\001')") arch=mipsle;; "$(printf '\002')") arch=mips;; *) echo '无法识别 MIPS 大小端'; exit 1;; esac;;
        *) echo '不支持的 CPU，请参考 README 手动安装'; exit 1;;
    esac
fi
case "$arch" in arm64|amd64|386|armv6|armv7|mips|mipsle) ;; *) echo 'FF_ARCH 无效'; exit 1;; esac
if [ "${1:-}" = --arch ]; then echo "$arch"; exit 0; fi
[ "$(id -u)" = 0 ] || { echo '请使用 root 安装'; exit 1; }
[ -f /etc/config/openclash ] || { echo '请先安装 OpenClash'; exit 1; }
for tool in curl tar sha256sum; do
    command -v "$tool" >/dev/null 2>&1 || { echo "缺少依赖：$tool"; exit 1; }
done
work="$(mktemp -d /tmp/openclash-ff.XXXXXX)"
trap 'rm -r "$work"' EXIT INT TERM
asset="openclash-ff-linux-$arch.tar.gz"
origin="https://github.com/$REPO/releases/latest/download"
case "${FF_MIRROR:-auto}" in
    direct) routes=direct;;
    auto) routes='https://gh-proxy.com https://ghfast.top direct';;
    https://*) routes="$FF_MIRROR direct";;
    *) echo 'FF_MIRROR 需要为 auto、direct 或 HTTPS 加速前缀'; exit 1;;
esac
ok=0
for route in $routes; do
    prefix="${route%/}/"; [ "$route" != direct ] || prefix=''
    echo "下载 $arch：$route"
    if curl -fLsS --connect-timeout 10 --max-time 120 "${prefix}${origin}/${asset}" -o "$work/$asset" &&
       curl -fLsS --connect-timeout 10 --max-time 30 "${prefix}${origin}/SHA256SUMS" -o "$work/SHA256SUMS"; then
        hash="$(awk -v file="$asset" '$2 == file && length($1) == 64 && $1 !~ /[^0-9a-f]/ {print $1; exit}' "$work/SHA256SUMS")"
        if [ -n "$hash" ] && (cd "$work" && printf '%s  %s\n' "$hash" "$asset" | sha256sum -c -); then ok=1; break; fi
    fi
    echo '下载或校验失败，尝试下一线路'
done
[ "$ok" = 1 ] || { echo '所有下载线路失败，原服务未修改'; exit 1; }
mkdir "$work/package"
tar -tzf "$work/$asset" | awk '/^\// || /(^|\/)\.\.(\/|$)/ {bad=1} END {exit bad}' || { echo '安装包路径无效'; exit 1; }
tar -xzf "$work/$asset" -C "$work/package"
fresh=0; [ -f /etc/node-guard/config.json ] || fresh=1
running=0; pidof node-guard >/dev/null 2>&1 && running=1
sh "$work/package/packaging/install.sh"
if [ "$fresh" = 1 ]; then
    if command -v ruby >/dev/null 2>&1 && [ -f /etc/openclash/config.yaml ]; then
        ruby -ryaml -rjson -e '
          live=YAML.load_file("/etc/openclash/config.yaml")
          file="/etc/node-guard/config.json"
          c=JSON.parse(File.read(file))
          endpoint=live["external-controller"].to_s
          port=endpoint[/:(\d+)$/,1]
          c["controller"]="http://127.0.0.1:#{port}" if port
          secret=live["secret"].to_s
          unless secret.empty?
            File.open("/etc/node-guard/secret",File::WRONLY|File::CREAT|File::TRUNC,0600) { |f| f.write(secret) }
            c["secret_file"]="/etc/node-guard/secret"
          end
          File.write(file,JSON.pretty_generate(c))
        '
    fi
    /etc/init.d/node-guard enable
    /etc/init.d/node-guard start
elif [ "$running" = 1 ]; then
    /etc/init.d/node-guard start
fi
echo '安装完成：服务 → OpenClash → 节点自动切换'
