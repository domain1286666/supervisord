# 配置与运行维护

配置继续使用 INI/conf。使用 `supervisord init --output /path/supervisord.conf` 初始化安全设置，再以 `-c` 指定同一文件启动；不需要额外数据库或前端目录。

## 管理服务与凭据

```ini
[supervisord]
identifier = supervisor
logfile = %(here)s/supervisord.log
logfile_maxbytes = 50MB
logfile_backups = 5
logfile_timestamp_suffix = false
loglevel = info
pidfile = %(here)s/supervisord.pid

[inet_http_server]
port = 0.0.0.0:8002
nodename = node-a
; username、password_hash、tls_certificate、tls_private_key 由 init 写入。

[supervisorctl]
serverurl = https://localhost:8002
```

`port` 保留历史字段名，表示 **HTTPS** 监听地址。默认 `0.0.0.0:8002`，可改为 `127.0.0.1:8002` 或 `[::]:8002`。已有配置中的端口不会自动改为 8002。未配置网络管理服务时仍可仅运行受管进程；Unix socket 仅提供受文件权限保护的 RPC。

首次 `init` 创建随机账号及 32 字符随机密码；密码仅打印一次，conf 保存 bcrypt 哈希。重复执行保留现有凭据。`password=明文` 只作为旧配置迁移输入，迁移后删除；旧 `{SHA}` 需 `--reset-password`。保留证书与私钥的 Base64 PEM 在 conf 中，文件本身仍包含敏感私钥。

自签名证书包含 localhost、本机主机名和初始化时已有网卡 IP，一年有效。公网 NAT IP 或自定义域名不在 SAN 中时，应配置包含正确 SAN 的可信证书。将 PEM 证书链和未加密 PKCS#8 私钥分别 Base64 编码到 `tls_certificate`、`tls_private_key`；重启后生效。`init --renew-certificate` 生成新的自签名证书，保留账号密码，需要重新信任指纹及更新远端 CA 文件。

## 程序、目录和 command

```ini
[program:worker]
directory = %(here)s/apps/worker
command = ./worker --title "hello world" --label 'single quotes' --empty ""
autostart = true
autorestart = unexpected
exitcodes = 0,2
startsecs = 1
startretries = 3
environment = MODE="production",LANG="en_US.UTF-8"
```

- `directory` 默认 conf 所在目录；相对目录基于 conf 解析。设置 `cmd.Dir`，不会改变 supervisord 自身工作目录。
- `./worker`、`../bin/worker` 等显式相对可执行路径基于程序工作目录；裸名称通过 PATH 查找。Windows 路径含空格时用双引号包围。
- 空白分隔 argv；单引号内完全保留字面值；双引号内 `\"`、`\\` 转义；无引号时可转义空白、单双引号和反斜杠。相邻引用片段拼接，支持空参数。普通 Windows 路径的反斜杠保留。未闭合引号、尾随孤立反斜杠、空执行文件均报错。
- INI 在引号外、空白后的 `#`/`;` 才视为行内注释。需要 Shell 展开时显式写 `command = /bin/sh -c 'exec ./worker > output.txt'`；`$VAR`、管道、重定向不会隐式执行。
- `%(here)s`、`%(program_name)s`、`%(process_num)02d`、`%(ENV_NAME)s` 可展开。未知变量、重复进程名及循环依赖拒绝加载，不悄悄丢弃程序。
- `numprocs > 1` 时必须设置带 `%(process_num)` 的 `process_name`。当前编号保持历史行为，从 1 开始。可用 `[program-default]` 复用默认配置，程序内同名设置优先。
- `user=账号[:组]` 适用于 Unix；切换时替换补充组，避免继承管理器特权组。Windows 不支持此项，需配置服务运行账号。
- `envFiles` 兼容旧设置；使用绝对路径或 `%(here)s/file.env`，避免依赖管理器启动目录。`environment` 覆盖 envFiles，二者覆盖父进程环境。

## 日志

```ini
stdout_logfile = logs/stdout.log
stderr_logfile = logs/stderr.log
redirect_stderr = false
stdout_logfile_maxbytes = 50MB
stderr_logfile_maxbytes = 50MB
stdout_logfile_backups = 5
stderr_logfile_backups = 5
stdout_logfile_timestamp_suffix = false
stderr_logfile_timestamp_suffix = false
```

相对日志路径基于程序工作目录，父目录自动创建。stdout/stderr 独立；`redirect_stderr=true` 明确合并。多个输出可用逗号分隔。`AUTO`/`memory` 为最多 1 MiB 的内存日志，重启 supervisord 后消失；`NONE`、`/dev/null` 丢弃；`/dev/stdout`、`/dev/stderr` 转发标准流。标准流、syslog 等不支持网页回读时会提示不可读取。

`maxbytes=0` 不按大小轮转；`backups=0` 正常写入但不保留旧备份。时间戳模式默认开启，活动文件带时间戳；示例关闭该模式，便于直接找到固定文件名。写入、移动备份和清空失败会返回错误，轮转失败不会继续截断原日志。磁盘容量和权限仍需运维监控；不要让两个程序共用同一可轮转文件。

网页最多保留 2,000 行 / 256 KiB；连接时读取最新 64 KiB，随后增量读取。重连或轮转发送 reset 并替换页面片段，允许网络中断期间出现缺口；完整审计日志应保留在磁盘。“清屏”仅清除页面内容，“清空服务端日志”删除当前及轮转内容，操作有确认。

## 优雅停止与强制终止

```ini
stopsignal = TERM
stopwaitsecs = 10
stopasgroup = true
killasgroup = true
```

普通停止先运行有超时的 pre_stop_hook，再发 stopsignal；等待 stopwaitsecs，超时发送 KILL。兼容多个停止信号，以空白分隔，每个信号分别等待。强制终止跳过钩子和等待。两种停止都抑制 autorestart；需要继续运行时显式启动。

Unix 的 `stopasgroup`/`killasgroup` 控制进程组，推荐给会创建子进程的应用设置为 true。Windows 普通停止使用不带 `/F` 的 `taskkill`，强制终止使用 `/F`，`/T` 取决于组配置；Windows 控制台程序不保证响应这种优雅退出请求，超时会升级强杀。需要严格的应用级优雅退出时使用 pre_stop_hook 通知应用自身接口。

## 健康检查、钩子和变更监控

```ini
liveness_check_script = https://localhost:9443/health --ca-file /path/ca.pem
liveness_check_period = 60
liveness_check_timeout = 30
liveness_check_initial_delay = 60
liveness_check_failure_threshold = 3
liveness_check_failure_action = restart
```

检查支持 HTTP(S)、`tcp://host:port` 和本地命令。HTTP 连接复用、响应体有界并关闭；TCP 与脚本在取消时释放 socket/子进程。单个检查器不重叠，全局最多 16 个检查。周期 1–86400 秒、超时 1–3600 秒、阈值 1–1000；旧实例关闭时取消并等待检查完成。重启后的旧检查结果不能再操作新实例。

`pre_start_hook`/`pre_stop_hook` 默认超时 10 秒，`hook_timeout` 最大 60 秒。`restart_when_binary_changed`、`restart_directory_monitor`、`restart_file_pattern` 每 10 秒按元数据检查，最多遍历 100,000 个目录项、保留 10,000 个匹配文件；超限记录错误并禁用该监听，应缩小监控目录。对应 `restart_cmd_when_*_changed` 有 10 秒超时。`cron` 保留现有 cron 表达式行为，程序移除时解除注册。

## 重载与持续运行

网页“加载配置变更”只替换新增、删除及配置变化的程序；CLI `ctl reload` 保留原有完整重载语义。配置先解析和校验，再替换；校验失败保留旧服务。成功重建管理服务后内存登录会话失效，需要重新登录。

建议使用 systemd 管理 supervisord，监控磁盘、进程状态及管理器 CPU/内存趋势。`/metrics` 需要同样的 HTTPS 会话认证。WAF 不替代操作权限或应用输入校验，本项目也不检查受管编码器内部的码流质量；可用针对应用的健康检查触发重启。
