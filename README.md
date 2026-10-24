# OpenWrt eWeLink WOL：从易微联或华为智慧生活唤醒电脑

本项目把易微联会员虚拟开关变成 OpenWrt 的网络唤醒入口。可在易微联 App 操作开关；将易微联账号关联到华为智慧生活后，还可从华为智慧生活 App 和华为手机系统控制中心操作，远程唤醒家中的电脑或 NAS。路由器主动连接易微联云端并在局域网发送 WOL 魔术包，无需开放公网入站端口，也不受家庭公网 IPv6 地址变化影响。

**使用前提：需要支持创建虚拟设备的易微联会员，以及至少一个已创建的会员虚拟开关。** 还需要有权用于本项目的易微联应用 AppID/AppSecret、可运行此插件的路由器，以及已在目标设备上配置好的局域网 WOL。会员权益和虚拟设备数量以[易微联官方说明](https://ewelink.cc/a-comprehensive-guide-to-ewelink-virtual-devices/)为准。

项目已在一台 ARM64/QWRT 路由器上验证易微联开关推送、华为智慧生活及系统控制中心操作、目标设备 WOL 开机。安装脚本使用该固件的 LuCI 目录布局；其他 OpenWrt 固件可能需要适配。本项目由社区独立开发，不提供易微联开发者应用凭证。

## 工作方式

```text
易微联 App / 已关联的华为智慧生活 App 或控制中心
                    ↓ 操作会员虚拟开关
                 易微联云端
                    ↓ WebSocket 推送
           OpenWrt 上的 ewelink-wol
                    ↓ 局域网 etherwake
                 电脑或 NAS
```

服务在连接建立后读取开关当前状态作为基线，之后处理云端推送。只有已启用映射的虚拟开关从“关闭”变为“开启”才会尝试唤醒。启动或重连时读到“开启”不会唤醒；同一映射在设定的间隔内不会重复唤醒。关闭虚拟开关不会关闭设备。网页每 5 秒读取路由器**本地**状态，不代表每 5 秒查询易微联。

## 页面

LuCI 入口为“服务 → 易微联唤醒”。页面使用 QWRT 原生选项卡、表单、表格和按钮，没有单独的插件样式表。

| 选项卡 | 模块 | 功能 |
| --- | --- | --- |
| 基础 | 服务 | 查看连接状态，启用或停用常驻服务及开机自启动 |
| 基础 | 设备映射 | 同步虚拟开关列表，新增、编辑、暂停或启用映射 |
| 高级 | 应用凭证 | 保存或更换 AppID/AppSecret |
| 高级 | 易微联账号 | 显示脱敏账号，登录或更换账号 |
| 高级 | 触发设置 | 正常唤醒 / 仅记录事件，设置重复唤醒间隔 |
| 高级 | 连接诊断 | 查看通信与重连信息，必要时手动重连 |
| 高级 | 事件记录 | 查看最近的状态和唤醒事件 |

虚拟开关的状态不等于设备的实际电源状态。页面上的“最近唤醒请求”表示路由器发送了 WOL，不保证目标设备已经启动。

## 构建

需要 Go 1.23 或更高版本。构建时不需要 AppID/AppSecret，也不会将它们编入二进制。安装后须在路由器页面提供有权用于本项目的易微联应用凭证。请勿把账号密码、应用凭证或令牌放进源码或安装包。

仓库不提供 AppID/AppSecret；使用者必须自行取得有权使用的应用凭证。

Windows PowerShell：

```powershell
.\scripts\build-package.ps1
```

Linux / WSL：

```sh
sh scripts/build-package.sh
```

脚本运行 Go 测试和 `go vet`，编译 Linux ARM64 程序，在 `deploy/` 生成 `SHA256SUMS`，并在项目根目录生成 `ewelink-wol-openwrt-arm64.tar.gz`。二进制和安装包被 `.gitignore` 排除；安装包包含项目和依赖许可声明。安装包内的默认 [`deploy/config.json`](deploy/config.json) 为中国区、仅记录事件、30 秒重复唤醒间隔和**空设备映射**。

## 安装与首次配置

以路由器的 root 身份把安装包传到 `/tmp/`，解压后运行：

```sh
mkdir -p /tmp/ewelink-wol-install
cd /tmp/ewelink-wol-install
tar -xzf /tmp/ewelink-wol-openwrt-arm64.tar.gz
sh install.sh
```

安装脚本先核对包内哈希、配置、Lua 语法、页面模板和管理逻辑回归，再备份已有程序及配置，最后替换文件。升级时保留路由器上的配置和授权信息；已有服务仅在应用凭证已配置时重启。若从旧版升级且尚无路由器端应用凭证，安装会停用服务及开机自启动，需要先完成以下配置。备份放在 `/root/ewelink-wol-backup-时间戳/`。安装需要路由器具备 LuCI、Lua 5.1、`etherwake`、`sha256sum` 和 procd。其他 OpenWrt 固件可能需要调整安装路径与依赖。

安装后打开“服务 → 易微联唤醒”：

1. 在“高级 → 应用凭证”填写 AppID/AppSecret 并保存。更换凭证会停用服务、清除原账号授权并暂停映射。
2. 紧接着在“高级 → 易微联账号”登录。密码只用于本次请求；路由器将访问令牌和刷新令牌保存在仅 root 可读的 `/etc/ewelink-wol/auth.json`，以支持长期运行。
3. 在“基础 → 设备映射”同步虚拟开关列表，为每个目标选择一个会员虚拟开关，填写网卡 MAC 和局域网接口（通常为 `br-lan`），保存并启用映射。
4. 保持“高级 → 触发设置”为“仅记录事件”，启用服务并从 App 操作开关，先验证页面能收到关闭、开启事件。
5. 确认设备本身已启用 WOL，再选择“正常唤醒”。如需再次触发，先在 App 中关闭对应虚拟开关，再开启。

更换为不同易微联账号后，旧映射会暂停；请重新同步并绑定新账号的开关。停用服务会同时关闭路由器开机自启动。保存未修改的设置或映射不会重连。

## 从华为智慧生活和手机控制中心开机

1. 先按上面的步骤确认：在易微联 App 中关闭、开启虚拟开关，路由器能收到事件并发送 WOL。
2. 在华为智慧生活 App 的“我的 → 连接三方平台”中关联**同一个**易微联账号，按页面提示登录，让会员虚拟开关同步到智慧生活。操作可参考[华为的第三方设备说明](https://consumer.huawei.com/cn/support/content/zh-cn00752585/)和[易微联的华为智慧生活关联指南](https://ewelink.org/zh/docs/add-device-link/huawei-hilink)。
3. 在智慧生活中先关闭、再开启该虚拟开关，即可触发路由器发送 WOL。还可以按[华为的控制中心设备说明](https://consumer.huawei.com/cn/support/content/zh-cn15819010/)把开关卡片加入手机系统控制中心，从下拉界面直接操作。

华为端控制的仍是同一只易微联虚拟开关；路由器通过易微联云端接收推送并在局域网发送 WOL。再次开机时，先关闭虚拟开关，再开启。

## 源码结构

| 路径 | 内容 |
| --- | --- |
| `agent/config.go` | 配置、状态、开关状态转换与校验 |
| `agent/agent.go` | 客户端初始化、持久化、事件和实际 WOL 调用 |
| `agent/cloud.go` | 易微联 HTTP 登录、资料、设备列表及令牌刷新 |
| `agent/subscription.go` | WebSocket 订阅、基线同步、推送处理和重连 |
| `agent/main.go` | 命令行入口 |
| `deploy/*.lua`、`deploy/app.htm`、`deploy/app.js` | LuCI 页面和管理操作 |
| `deploy/install.sh`、`deploy/ewelink-wol.init` | 安装与 procd 服务 |
| `scripts/` | 可重复执行的构建与打包脚本 |

## 安全与验证范围

页面操作由 LuCI 登录和 POST/CSRF 校验保护；账号令牌存放在路由器私有目录。插件只主动连接易微联云端，不开放新的公网端口。事件记录不保存账号密码或令牌。云端断线期间的操作可能无法补收；恢复连接只同步当前状态，不追补旧的“开启”操作，避免误唤醒。

已在一台 ARM64/QWRT 路由器上验证会员虚拟开关的云端推送、华为智慧生活及手机系统控制中心操作，以及目标设备的 WOL 开机。当前 1.2.0 源码中的应用凭证配置流程尚未安装到路由器验证。Go 测试覆盖初始状态、重复消息、冷却、断线重连和目标隔离；Lua 回归使用替身验证页面操作。其他固件和设备组合未验证。

## 许可与来源

本项目由 apshiap05 以 [PolyForm Noncommercial 1.0.0](LICENSE) 授权，允许在该许可规定的非商业范围内使用、修改和再分发；再分发须保留许可与原作者声明。依赖组件各自的许可证随安装包提供。

易微联云端交互参考公开接口资料及 [homebridge-ewelink](https://github.com/homebridge-plugins/homebridge-ewelink) 的实现思路；本项目没有直接包含其 JavaScript 源文件。`homebridge-ewelink` 采用 [MIT 许可证](https://github.com/homebridge-plugins/homebridge-ewelink/blob/latest/LICENSE)，其应用凭证不随本项目源码或安装包提供。

