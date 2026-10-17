# 升级与迁移

## 从旧版升级

1. 备份原二进制与 conf，确认受管应用有可接受的停止窗口。
2. 用新二进制执行 `supervisord init --output /原路径/supervisord.conf`。保留已有程序设置和端口，迁移明文 password 为 bcrypt，生成缺失证书；配置会原子改写，不重置现有密码。原来没有 `[inet_http_server]` 的配置，需要先加入该节与 `port=0.0.0.0:8002`。
3. 若原来无凭据或为 `{SHA}`，使用 `--reset-password` 生成新随机密码并保存终端输出。`init` 只改文件；必须重启旧进程使新设置生效。
4. 将 `[supervisorctl] serverurl` 及远端入口的 `http://` 改为 `https://`。新安装默认 8002，旧配置的 9001 等端口仍保留。
5. 重启 supervisord，打开 HTTPS 网页，核对证书指纹后登录。验证目录、引号、stdout/stderr、停止和重启行为后，再扩大使用范围。

原配置中 `[inet_http_server]` 应位于主 conf，初始化凭据不能仅存于 include 文件。配置目录需要可写，以便原子替换 conf。保留备份时同样限制权限，避免备份保留旧明文密码。Linux 安装器设置目录 `0700` / 文件 `0600`；Windows 应通过 NTFS ACL 仅授权运行账号与管理员。

## 行为变化

| 旧行为 | 新行为与处理方式 |
| --- | --- |
| TCP 明文 HTTP、Basic Auth | 只接受 HTTPS，网页登录使用安全会话；不会再弹 HTTP Auth 对话框 |
| `/log?program=...` 打开缺失 HTML | 兼容旧链接，进入 Vue 日志页；程序名通过查询参数编码 |
| 直接浏览日志目录、原始 conf | 改为鉴权后的进程日志 API 与脱敏配置预览，不允许任意文件下载 |
| 相对路径依赖管理器 cwd | `directory` 基于主 conf，日志与显式相对可执行路径基于程序目录；核对旧配置 |
| 错误引号被默默拆分 | 严格解析 argv；修正未闭合引号，Shell 功能显式用 `/bin/sh -c` |
| `backups=0` 导致不写日志 | 正常写入，达到 maxbytes 时轮转且不保留备份 |
| 重载可能积累监听和协程 | 旧 console、远端流、健康检查、文件监听和日志资源有关闭路径 |
| 新程序无认证直接启动管理服务 | 先执行 `init`，不会把无法再找回的随机密码写进后台日志 |

保留命令行 `-n`、`-d`、`-e`、INI 程序/分组/include 与现有 RPC 业务方法。认证协议属于明确的兼容性变更，旧第三方客户端需要适配，不能通过关闭证书校验恢复连接。

## 本地与远程 CLI

本地优先使用同一 conf：

```sh
supervisord -c /etc/supervisord/supervisord.conf ctl status
```

CLI 读取该文件中的证书，使用私钥派生的 HMAC 签名访问 `/RPC2`，包含时间戳与一次性 nonce；服务端限制时间窗口并拒绝重放。具有 conf 读取权限即具有完整管理权限，因此应保护私钥和文件权限。

远程 CLI 使用系统信任证书或显式指定已核验的 PEM：

```sh
export SUPERVISORD_USER='实际账号'
read -r -s SUPERVISORD_PASSWORD
export SUPERVISORD_PASSWORD
supervisord ctl -s https://node.example:8002 --ca-file ./node-certificate.pem status
unset SUPERVISORD_PASSWORD
```

示例的 `read -s` 适用于 Bash。避免在命令行 `-P` 明文传密码，防止 shell 历史或进程参数泄漏。TLS 验证、表单会话和 CSRF 都由客户端处理；没有 `InsecureSkipVerify` 回退。

## 远程节点

在管理节点的 `[inet_http_server]` 内配置：

```ini
remote_1_node = node-b
remote_1_port = node-b.example:8002
remote_1_user = 远端账号
remote_1_password = 远端密码
remote_1_ca_file = %(here)s/node-b-certificate.pem
```

最多连接 32 个已配置节点，键编号范围 1–999。`remote_N_port` 可写 host:port 或带 `https://` 的地址，不能使用明文 HTTP。节点名称应唯一，且不要与本节点 nodename 重复。使用可信 CA 证书时可省略 ca_file；自签名证书必须先独立核对指纹，再导出 PEM 并配置。

远端账号密码用于建立会话，受保护的 conf 中仍需保存远端可用凭据；不能把 bcrypt 哈希当远端密码。认证信息仅在 TLS 内发送，不放进 URL、浏览器存储或审计日志。远端会话与连接复用；断线指数退避最多 30 秒，45 秒无活动取消连接，退出或重载时关闭响应体和空闲连接。远端状态流只请求对方本地数据，防止多节点互相聚合形成递归放大。

证书轮换后需要更新各客户端信任文件。已有登录会话会在管理服务重启或密码修改后失效。TLS 私钥发生更换时，本地 CLI 必须使用新 conf。

## 安装与回退

本地安装：`sudo sh install.sh --binary ./supervisord`。重复执行保留已有配置和凭据；`--no-start` 安装并启用服务但不启动。自定义监听地址、端口只影响新配置。

下载方式依赖本改造版本已发布到指定仓库，脚本只从 HTTPS 获取文件并检查 `checksums.txt`。没有发布 Release 前请使用本地二进制方式。

回退旧版本时停止新服务，恢复备份二进制和对应旧 conf。旧程序不理解新的会话/TLS 设置，直接复用新 conf 不能保证兼容。不要在仍使用新版本时恢复明文网络管理入口。
