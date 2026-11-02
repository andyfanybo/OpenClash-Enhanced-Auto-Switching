# OpenClash Enhanced Auto Switching

<img src="docs/images/icon.svg" width="80" alt="节点自动切换图标">

**OpenClash 增强自动切换插件，一元机场用户的晚高峰救场工具。**

用低价机场，最烦的不是节点少，而是用着用着突然卡住。网页还在转圈，下一轮自动测速可能还要等几分钟。

这个插件每秒检查当前选中的节点，失败后按组内顺序寻找下一个可用候选。搭配 OpenClash 使用，保留现有订阅、DNS 和分流规则，也能跟随你手动选的节点。

![仅监控业务策略组的配置示例](docs/images/luci.png)

## 一键安装

在路由器 SSH 中执行：

```sh
(curl -fL --connect-timeout 10 --max-time 60 https://gh-proxy.com/https://raw.githubusercontent.com/andyfanybo/OpenClash-Enhanced-Auto-Switching/main/install.sh -o /tmp/install-openclash-switch.sh || curl -fL --connect-timeout 10 --max-time 60 https://raw.githubusercontent.com/andyfanybo/OpenClash-Enhanced-Auto-Switching/main/install.sh -o /tmp/install-openclash-switch.sh) && sh /tmp/install-openclash-switch.sh
```

自动识别架构，下载最新 Release，核对 SHA-256 并安装 LuCI 页面。默认优先大陆加速线路，失败后回退；升级保留配置和原运行状态。

依赖已安装的 OpenClash、curl、tar 和 sha256sum。首次安装尝试读取 OpenClash 控制端口与认证设置并启用服务，随后请按下面的推荐方式设置监控范围。

## 推荐配置：只选业务策略组

打开 **服务 → OpenClash → 节点自动切换**：

1. 将监控范围改为 **仅选择下列策略组**。
2. 勾选平时使用的业务组，例如 **节点选择、AI 平台、TikTok**。
3. **不要同时勾选香港节点、日本节点、美国节点等地区组，也不必勾选“所有节点”。**
4. 检测间隔先用 **1 秒**，超时先用 **800ms**，点击“保存并应用”。

例如 `AI 平台 → 节点选择 → 日本节点 → 日本01`，只监控 AI 平台、节点选择即可。插件会沿当前选择找到实际节点，不需要把中间每一层都勾上。多个业务组使用同一节点时会合并检查。

把各地区组全部纳入高频监控，会连未用于当前业务的地区节点也一起检测、切换。实际使用中容易造成大量切换记录，也增加检测开销。**少选几个真正需要保护的业务组就够了。**

> 程序的 `groups: []` 仍表示全部支持的策略组，推荐配置需要在页面手动选择。地区组内原有的自动选线可以保留。

## 怎么切换

当前节点检查失败后，下一候选加入下一轮检查，通过才切换；也失败就继续往后找。支持 Selector、URLTest、Fallback 和嵌套组。直连、拒绝等动作会跳过。

每秒检测、超时等待、候选验证和最终改选是不同环节。“毫秒级切换”指选路动作，不代表故障发现到业务恢复全程都在毫秒内完成；本项目尚无覆盖各种网络的毫秒级恢复保证。多个坏候选会延长等待，已有长连接可能仍需应用重连。

检测的是连通性，不是下载速度，也不能判断 AI 账户或地区限制。超时过短可能误判抖动，切换过于频繁时先适当调大超时。

URLTest/Fallback 被服务改选后，可能显示为固定选择，可在原面板解除固定。

## 设备支持

| 环境 / 架构 | 状态 |
| --- | --- |
| OpenWrt / ImmortalWrt ARM64 + OpenClash | 已实机验证，含 LuCI 页面 |
| amd64、386、ARMv6/v7、MIPS/MIPSLE | 已交叉编译，待对应设备实测 |
| 普通 Linux + Mihomo | 可运行守护程序，需自行配置接口和进程管理 |
| Windows、macOS、iOS | 当前未适配 |

LuCI 页面使用现有 OpenClash 环境中的 luci-compat、luci.jsonc、nixio 和 curl。自动读取控制接口设置还需 Ruby。服务通过 Mihomo API 工作，不改防火墙，iptables / nftables 均可使用。

## 管理与排查

```sh
logread -e node-guard
cat /tmp/node-guard/status.json
/etc/init.d/node-guard restart
/etc/init.d/node-guard stop
```

配置文件为 `/etc/node-guard/config.json`。服务名仍为 `node-guard`，与早期版本兼容。暂停不会停止 OpenClash，开机设置可在页面调整。

<details>
<summary>下载线路、认证和手动安装</summary>

仅直连 GitHub：`FF_MIRROR=direct sh /tmp/install-openclash-switch.sh`。

自定义加速前缀：`FF_MIRROR=https://你的加速域名 sh /tmp/install-openclash-switch.sh`。第三方加速可用性会变化，SHA-256 用于检查下载完整性。

若自动读取失败，手动设置配置文件中的 `controller`。控制接口设置了 secret 时，将其保存到权限为 600 的 `/etc/node-guard/secret`，并把 `secret_file` 指向该文件；这里不是 SSH 密码。

也可从 [Releases](https://github.com/andyfanybo/OpenClash-Enhanced-Auto-Switching/releases) 下载对应架构的 `openclash-ff-linux-*.tar.gz`，核对 SHA256SUMS，解压后执行 `sh packaging/install.sh`，再到 LuCI 保存启用。安装包保留原文件名前缀，方便已有安装升级。

从源码构建需要 Go 1.23 或以上版本：

```sh
CGO_ENABLED=0 go test ./...
sh scripts/build.sh
sh scripts/package.sh
```

MIPS 大小端由安装脚本自动识别。手动安装时不能只根据 `uname -m` 判断，请结合固件架构选择。

</details>

<details>
<summary>卸载</summary>

```sh
/etc/init.d/node-guard stop
/etc/init.d/node-guard disable
rm /usr/bin/node-guard /etc/init.d/node-guard
```

移除 LuCI 页面时，再删除安装脚本中列出的页面文件与图标并清理菜单缓存。配置目录可保留。

</details>

实现细节见 [DESIGN.md](DESIGN.md)。感谢 [OpenClash](https://github.com/vernesong/OpenClash) 和 [Mihomo](https://github.com/MetaCubeX/mihomo)。
