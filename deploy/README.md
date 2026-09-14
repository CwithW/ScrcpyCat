# USB 接入与部署器

网页资源已嵌入 Go 控制平面，Compose 不再运行独立 frontend 服务。完整安装、源码构建与 Release/GHCR 用法见 [项目说明](../readme.md)。默认 8080 和 8443 均映射到同一个控制平面 HTTP 服务；升级旧部署时先移除占用 8080 的 frontend 容器，保留其他状态与持久卷。

adb-deployer 默认使用 `host` 模式：在 Linux 上为每台 USB 设备启动一个 Agent，WebRTC、WebSocket 和资产下载走 Linux 网络；scrcpy、摄像头、麦克风、Shell、PTY、文件和应用管理仍在所选 Android 设备上执行。

| 模式 | Agent 位置 | 网络出口 | USB 断开后 |
| --- | --- | --- | --- |
| `host`（默认） | Linux，按 ADB serial 隔离进程与身份 | Linux 的以太网或其他网络 | 关闭媒体和终端，退出对应 Agent；重新连接后自动启动 |
| `device` | 注入 Android 的独立 Agent | Android 自己的网络 | 已启动 Agent 可继续运行；若信令使用 ADB reverse 则仍依赖 USB |

## 选择模式

优先级为 `--mode` > `SCRCPYCAT_ADB_MODE` > `host`。在 Compose 部署目录 `.env` 中配置：

~~~dotenv
SCRCPYCAT_ADB_MODE=host
SCRCPYCAT_ADB_FORCE=false
SCRCPYCAT_CONTROLPLANE_URL=http://127.0.0.1:8443
SCRCPYCAT_AGENT_SIGNALING_URL=wss://your-domain.example/register_agent
~~~

HTTP/WS 示例用于可信内网；公网使用 HTTPS/WSS。部署器使用 host 网络，以便 Linux Agent 发布可达的 ICE 地址，因此 `controlplane` 等 Compose 服务名不再用于部署器的 URL。使用两端都能访问的信令地址，可直接切换到 `device` 模式。旧模式仍支持对 loopback 信令地址自动设置 ADB reverse。

在部署目录执行：

~~~sh
docker compose -p scrcpycat --profile usb up -d --no-deps adb-deployer
docker compose -p scrcpycat --profile usb logs -f adb-deployer
~~~

回退到独立 Android Agent 时，将 `.env` 改为 `SCRCPYCAT_ADB_MODE=device`，再次执行相同的 `up` 命令。`--force` 仅对 `device` 模式有效。切换到 host 前会检查工件、Shell v2 和所需接入凭据，再精确停止旧 Android Agent；原有 Android Agent、常驻 JAR 和 identity 文件均保留。

host 模式仅自动接管 ADB 枚举中的在线 USB 设备，不会自动接管网络 ADB 设备。直接运行 Linux Agent 可显式选择现有 ADB Server 和 serial：

~~~sh
SCRCPYCAT_ENROLLMENT_TOKEN='<one-time-token>' scrcpycat-agent \
  --adb-serial 90203f83 \
  --adb-server 127.0.0.1:5037 \
  --signaling wss://your-domain.example/register_agent \
  --scrcpy-jar /opt/scrcpycat/scrcpy/scrcpy-server
~~~

默认 `device-id` 为 serial。部署器状态保存在持久卷 `adb-state` 对应的 `/var/lib/scrcpycat/adb-deployer/<serial-hash>/`，包含 `identity.json`、PID/锁文件和日志；直接运行 Agent 的默认状态目录是 `/var/lib/scrcpycat/agent/<serial-hash>/`。重启重用身份，不会每次申请 enrollment。设备拔插不会把另一台设备的状态或进程关闭。

## ADB 与采集清理

Linux Agent 和部署器以 `CGO_ENABLED=0` 构建。设备枚举、Shell、Sync 文件传输和抽象 socket 使用纯 Go 的 `gadb` 库及小范围扩展；ADB 可执行文件仅用于在指定的 loopback 地址启动官方 ADB Server。已有外部 Server 通过 `--adb-server` 连接，不由部署器启动。标准部署中只允许 adb-deployer 容器持有 USB，避免宿主或 Windows ADB Server 争用设备。

首次安装默认在 `adb-keys` 持久卷生成 ADB 主机密钥，需在手机上确认 USB 调试授权。已有密钥可通过 `SCRCPYCAT_ADB_KEY_DIR` 指定宿主目录，以保持设备信任关系；升级时保留它及 `adb-state`。部署器镜像采用 Alpine 的 `android-tools` 包，支持 amd64 与 arm64，不再下载仅适用于 x86-64 的预编译 Platform Tools ZIP。

镜像默认设置 `ADB_LIBUSB=0`，使用 ADB 的 Linux 原生 USB 后端扫描已挂载的 `/dev/bus/usb`。这保留了此前实测可靠的容器配置，避免依赖可能在容器内缺失的 libusb 热插拔事件。可在 `.env` 中设 `SCRCPYCAT_ADB_LIBUSB=1` 并重建部署器容器，使用 libusb 后端；此设置只影响 ADB Server，不改变纯 Go 客户端，也不要求 Go 程序启用 cgo。

Alpine 3.22 的 ADB 同时包含两种 USB 后端，其 libusb 枚举补丁额外接受 `LIBUSB_CLASS_MISCELLANEOUS` 设备，主要针对 PinePhone modem；它没有改变 Docker 的热插拔事件投递方式。需要 libusb 的设备可显式开启，并在实际部署环境验证拔插恢复。`adb host-features` 包含 `libusb` 表示当前 Server 使用 libusb，修改环境变量后必须重启 Server 才生效。

host 模式要求设备支持 Shell v2，不支持时明确报错，可选 `device` 模式。摄像头仍由手机上的 scrcpy 捕获，保留设备与 Android 版本的原有限制，不要求 Linux 上安装摄像头驱动。

每次 host 采集使用独立 JAR，放在 `/data/local/tmp/scrcpycat-host-<serial-hash>/`，并启用 `cleanup=true`。通过前台 Shell v2 的 `exec app_process` 启动，保持该 Shell 连接直至会话结束。USB/ADB 连接中断时 adbd 向前台进程发送 SIGHUP，scrcpy 的原生 cleanup helper 在管道关闭后恢复其管理的电源和常亮设置；摄像头、麦克风与编码器随采集进程释放。正常关闭先断媒体 socket，并给服务端退出宽限；握手失败也会关闭 Shell 会话。

scrcpy cleanup 启动时就会删除它自己的 JAR，所以 host 模式不复用 Android 常驻 JAR。设备注入模式继续使用 `/data/local/tmp/scrcpy-server.jar` 和 `cleanup=false`。host 启动只清理自己私有目录中的临时工件；文件管理的路径始终表示 Android 路径，Linux 只使用自动生成的上传临时文件。

独立 device Agent 使用后台会话，并在启动前忽略 legacy ADB shell 的 HUP；部署器等待健康检查成功后才确认启动。host 的 scrcpy 保持前台，不能使用这种脱离方式。两种模式的身份与工件独立保留，可来回切换。

当前真机已验证 ADB Server 终止、容器重启时的采集释放及自动恢复，也验证了手机关闭 Wi-Fi 后仍可使用 host 视频、音频和终端。真实 USB 物理拔插尚未验收；测试 VM 的 sysfs 端口禁用会触发 QEMU xHCI 重新枚举问题，不能用它替代物理拔插测试。

## 常驻部署器凭据

先启动控制平面和网页，再以管理员身份进入「部署 → 常驻部署器凭据」创建凭据。默认选择「无限期」，也可以选择到期时间；原始凭据只显示一次。

将凭据写入部署目录的 `.env`：

~~~dotenv
SCRCPYCAT_DEPLOYMENT_TOKEN=scd.<id>.<secret>
~~~

然后更新 USB 部署器：

~~~sh
docker compose -p scrcpycat --profile usb up -d --no-deps adb-deployer
~~~

Compose 通过环境变量传入凭据，不把它展开到部署器命令行。控制平面只保存 SHA-256 哈希、名称、创建人、到期时间、撤销时间及最近使用时间。部署器配置作为凭据的使用方，需要保存原始值。

## 权限与生命周期

- 只有管理员 JWT 可以创建、列出和撤销部署凭据。
- 部署凭据仅可通过 Authorization Bearer 请求 `POST /api/deploy/enrollment-tokens`；不能访问用户、媒体、终端、文件、任务、部署包或 Agent 工件接口，也不能直接注册为 Agent。
- 接入请求必须指定 `device_id`，签发的一次性 enrollment 默认且最长有效 15 分钟；有期限的部署凭据签发的 enrollment 不会超过其到期时间。
- 撤销或过期同时使尚未使用的下属 enrollment 失效。已经注册的 Agent 继续使用原有 identity 校验，不受部署凭据撤销或过期影响。
- 创建、撤销和签发 enrollment 均进入既有审计记录，审计中不保存原始凭据。
- 管理接口 `POST /api/admin/deployment-credentials` 接受 `name` 和可选 `ttl_seconds`。省略或设为 0 表示无限期，正值表示有限秒数；负值及溢出值会被拒绝。
- 管理员登录 JWT 默认有效期为 7 天。

## 公网访问保护

- 登录失败默认按账号限制为 10 次/15 分钟、按来源 IP 限制为 60 次/15 分钟。可通过 `SCRCPYCAT_LOGIN_*` 调整。
- 若 HTTPS 由反向代理终止，只有将代理网段配置到 `SCRCPYCAT_TRUSTED_PROXY_CIDRS` 后服务才会读取 `X-Forwarded-For`。代理必须覆盖客户端传入的该请求头，不能原样透传。
- Agent 注册前的 WebSocket 握手默认限制为 10 秒、64 KiB 与 64 条并发连接；对应环境变量以 `SCRCPYCAT_AGENT_HELLO_` 和 `SCRCPYCAT_MAX_PENDING_AGENT_HANDSHAKES` 开头。

## 轮换

1. 新建凭据。
2. 更新 `SCRCPYCAT_DEPLOYMENT_TOKEN`，重建 adb-deployer 容器并确认部署成功。
3. 在管理界面撤销旧凭据。

手工 CLI 仍兼容显式的 `--admin-token`，但不能与 `--deployment-token` 同时配置。使用部署凭据时，认证失败不会回退到管理员 JWT。`device` 模式跳过健康 Agent；故障设备在取得有效 enrollment 后才会停止旧进程及推送工件。`host` 模式通常重用持久身份；身份缺失或被拒绝时才申请新 enrollment。部署凭据与管理员 JWT 不传给 Linux Agent 子进程。
