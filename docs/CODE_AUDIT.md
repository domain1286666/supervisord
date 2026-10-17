# 代码审计与验证记录

日期：2026-10-18。审查对象为本工作区的一方手写 Go、Vue/JS、安装与构建代码；检查配置解析、资源创建/使用/关闭、锁顺序和错误分支，并阅读相关回归测试。Go 文件范围为 98 个（含测试及平台适配）；第三方依赖和压缩构建产物采用版本/漏洞检查，不把逐行阅读第三方库作为已完成事项。

## 文件范围与检查入口

| 范围 | 已检查的源文件/重点 |
| --- | --- |
| 根包启动与命令 | `main.go`、`config_template.go`、`ctl.go`、`service.go`、`daemonize*.go`、`version.go`：参数、环境文件、长任务退出、服务启动失败 |
| 根包协调 | `supervisor.go`、`xmlrpc.go`、`rest-rpc.go`、`rlimit*.go`、`zombie_reaper*.go`：重载、配置所有权、旧路由、等待与 OS 限制 |
| 管理接口 | `management_server.go`、`console_api.go`、`web_security.go`、`web_rpc_auth.go`、`web_waf.go`、`remote_api.go`、`remote_client.go`、`webgui.go`、`assets.go`、`logtail.go`、`content_checker.go` |
| `process/` | `process.go`、`lifecycle.go`、`process_manager.go`、`process_change_monitor.go`、`command_parser.go`、`script_executor.go`、`auxiliary.go`、`child*.go`、`proc_metrics.go`、`path.go`、`set_user_id*.go`、`pdeathsig*.go` |
| `config/` | `config.go`、`ini.go`、`write.go`、`process_group.go`、`process_sort.go`、`string_expression.go` |
| `logger/` | `log.go`、`file_io.go`、`memory_io.go`、`log_unix.go`、`log_windows.go`：轮转、读取、捕获管道、队列及 syslog 网络故障 |
| 事件与辅助 | `events/events.go`、`signals/*.go`、`pidproxy/*.go`、`types/*.go`、`util/util.go`、`internal/rpcauth/sign.go` |
| 客户端 | `xmlrpcclient/xmlrpc-client.go`、`xml_processor.go`、`vars.go`、`session.go`：TLS、认证、HTTP body 与错误返回 |
| 前端和交付 | `frontend/src/` 全部手写源码、`tests/state.test.mjs`、`scripts/smoke.mjs`、Vite 配置、`install.sh`、Dockerfile、两个工作流；Go `*_test.go` 与基准文件 |

本项目没有内置 RTMP/WebRTC 推流器、HTTP 媒体源拉流、编码器或解码器实现。针对网络的检查落实到 HTTP/TCP 健康检查、远端管理流及 syslog。受管 FFmpeg 等应用内部的编码异常和网络卡顿，需要应用指标或健康脚本判断，不能声称已在本库修复。

## 确认的问题、修正和证据

| 优先级/位置 | 原问题及影响 | 已实施修正/验证 |
| --- | --- | --- |
| 高 · 旧日志页面 | 路由尝试读取不存在的 log.html，打开日志 404 | SPA fallback、旧查询链接跳转；真实浏览器及特殊字符程序名回归 |
| 高 · HTTP 管理入口 | Basic Auth/明文入口与不统一的文件访问保护 | 强制 TLS、bcrypt、会话/CSRF、路径白名单、WAF；HTTP/Basic/跨域/SQLi/XSS 测试 |
| 高 · manager 遍历 | defer 解锁导致业务回调一直持有锁，回调重入会死锁 | 锁内复制、锁外排序/执行；重入回归与 snapshot 基准 |
| 高 · 子进程启动 | Cond 唤醒可能丢失；零延时重启可能忙等；重复 start/Wait 所有权不清 | 每实例一个重启循环、关闭 ready channel、可取消退避、单次 Wait；真实子进程测试 |
| 高 · 停止和重启 | 信号等待耦合 SIGCHLD、强杀后可能被 autorestart 拉起 | 普通信号等待后升级 KILL；独立 Kill、停止标识、进程组及 PID 检查；Unix/Windows 分别适配 |
| 高 · PID 1 回收 | 仅靠 zombie 存在时长区分孤儿，Wait 延迟时仍可能误取受管状态 | Start/Wait 显式登记所有权，回收器在同一锁内跳过受管子进程；增加延迟 Wait 的 Linux 回归（本机只编译） |
| 高 · command / INI | 解析前丢失引号/转义，混合引用及空参数错误 | INI 保留字面值，argv 状态机；引号、Windows 路径、相对文件和环境真实进程测试 |
| 高 · stdout/stderr | 错用 stderr 路径/捕获事件；backups=0 不正常写入 | 独立日志与事件发射器、零备份语义；分流、回读和轮转测试 |
| 高 · 文件轮转 | 忽略备份移动错误后仍截断原日志，可能丢失输出 | 传播轮转错误，原日志保持；阻塞备份目录的失败回归 |
| 高 · 健康检查 | goroutine/transport 缺少关闭；慢请求、TCP 和脚本可能残留 | context 取消、Close/等待、响应体上限、连接复用、全局并发上限、进程组取消；阻塞 HTTP/TCP 测试 |
| 高 · 多次重载 | 采样器、健康 worker、日志描述符和 collector 重复注册 | 显式生命周期、每 server registry、旧资源关闭；连续 20 次完整重载及无效配置保留服务测试 |
| 中 · 局部重载 | 自动启动所有 autostart 程序，误拉起手动停止且未变更的程序 | 网页重载只自动启动新增/变化的实例，保留手动停止状态；CLI 完整重载保持原语义 |
| 高 · SSE / 旧 tail | 无日志时客户端断开不能退出；慢读者可能阻塞 | 监听取消、有限队列、写期限与心跳；8 次 HTTP/2 连接取消、浏览器切页/退出/断网测试 |
| 高 · 远端连接 | 缺少完整的 TLS 会话/重连所有权，聚合有递归放大风险 | 证书验证、会话与 transport 复用、body 关闭、45 秒 watchdog、有界退避、本地快照；真实 TLS 模拟节点测试 |
| 中 · 长期 RPC 客户端 | 会话过期后继续使用旧令牌，远端响应缺少统一上限 | 会话到期前续期、401 清理缓存、响应最多 8 MiB；HTTPS 会话续期回归 |
| 中 · 旧节点代理 | 把本地节点别名继续发给远端，可能再次代理到不存在节点 | 远端请求使用对方本地路由，不继续传播代理别名；程序路径编码 |
| 中 · 事件系统 | 注册表/序列 map 并发访问、重复监听、停止不能唤醒读管道 | 同步注册表、回调快照、移除序列、有限消息/队列、关闭输入输出与捕获管道 |
| 中 · 配置 | 循环依赖和递归表达式可能无限循环；共享 map 有竞态；失败展开静默跳过程序 | 候选快照后发布、环检测、展开次数/宽度上限、重复名称/数量校验；失败保留旧快照测试 |
| 中 · 文件监控/cron | 全局注册不能解除、周期性计算整文件 hash 持续耗 CPU | 每进程一个有界元数据监控，关闭等待，cron Remove；禁止持进程锁做初始遍历 |
| 中 · 内存日志/通道 | 追加字符串及无界缓存、复用输入切片、通道满阻塞 | 1 MiB 上限、绝对游标、复制字节和非阻塞投递；回归检查 |
| 中 · syslog 故障 | 网络写可能长期阻塞进程 stdout，重连缺少资源边界 | 128 项队列、2 秒拨号/写期限、最多 30 秒退避、取消等待；拥塞时允许丢弃远端副本 |
| 中 · stdin/辅助命令 | 大输入、命令输出及继承管道可能长期持有资源 | stdin 请求 64 KiB/5 秒；file stdin 16 MiB；辅助输出 1 MiB/15 秒、WaitDelay 与进程组取消 |
| 中 · Unix 适配 | 只检查第一个资源限制，NPROC 常量硬编码；降权保留管理器补充组 | 共用 x/sys/unix 常量检查两项；替换用户组；FreeBSD 交叉编译 |
| 中 · 环境文件 | EOF 前无换行的最后一行被漏掉 | 共用 envparse，1 MiB 上限，解析错误返回；最后一行/引号回归 |
| 中 · PID helper | 缺参数访问越界、无效 PID/Process handle 生命周期 | 参数/PID 检查、释放进程 handle、复用 ticker |

没有把“代码存在 goroutine”直接视为泄漏：全局 cron scheduler 和事件 tick 各为进程生命周期单例；Coraza 编译规则按进程复用。重要的是重载不再重复创建无法停止的所有者。

## 去重与长期协作

删除旧原始配置 API、缺失日志 HTML 读取、ZIP 静态资源加载、重复信号等待、无调用的状态转换/包装函数、旧构建配置和一次性重构脚本。合并 FreeBSD/其他 Unix 资源限制逻辑、进程日志清空逻辑及 manager 快照入口。前端统一 ProcessTable、状态 store、API/CSRF 与 SSE 生命周期；新模块职责、页面和接口集中在 [ARCHITECTURE.md](ARCHITECTURE.md)，配置行为集中在 [CONFIGURATION.md](CONFIGURATION.md)。

不为统一风格重写所有兼容 RPC 方法；XML-RPC 与 JSON DTO 有不同外部契约，应共用底层进程/日志能力，而不是复制业务或强行合并线协议。

## 已测 CPU/分配优化

环境：Windows amd64，Intel Core Ultra 9 275HX，初测 Go 1.27.0，最终复测 Go 1.27.2，均重复 3 次。`BenchmarkProcessInfo` 使用固定配置、显式日志路径、已停止进程。旧版本每次状态读取重新创建环境表达式并解析固定路径；改为创建进程时缓存路径。

| 指标 | 修改前 | 修改后 |
| --- | --- | --- |
| 耗时 | 543–570 µs/op | 初测 183–200 ns/op；最终复测 160–170 ns/op |
| 分配字节 | 243,422–243,434 B/op | 192 B/op |
| 分配次数 | 2,812 allocs/op | 1 alloc/op |

这是该特定采样路径的微基准，不代表整个守护进程或运行中的应用快了同样倍数。256 进程 manager snapshot 初步测得耗时下降、分配次数下降，但索引 map 增加了字节量，未据此宣称整体内存降低。保留基准供后续复测：

```sh
go test . -run '^$' -bench BenchmarkProcessInfo -benchmem -count=3
go test ./process -run '^$' -bench BenchmarkManager -benchmem -count=3
```

还可继续测量：大量进程排序/JSON 编码，多个远端快照的总体内存，文件轮转目录扫描，SSE 高扇出写入，以及共享主机采样的系统调用耗时。应在代表性负载下记录 CPU/heap/mutex profile 再决定缓存或批处理；不在公网默认暴露 pprof。

## 验证与平台边界

本机已执行：Go 两模块完整测试与 vet、Windows/Linux 条件下 golangci-lint 2.15.0（govet/ineffassign/staticcheck/unused，0 issues）、前端 3 项状态测试、Prettier 检查与生产构建、npm audit（全部依赖 0 个漏洞）、实际 Chromium 端到端检查、安装脚本 `sh -n`/`--help`、Linux amd64 测试交叉编译、Windows 与 Linux amd64/arm64/armv7、FreeBSD amd64 构建。

浏览器覆盖 13 项：登录、概览、进程管理、日志、5 轮标签切换的连接释放、旧日志链接、390px 移动布局、本地签名 CLI、错误当前密码/密码修改、断网恢复、批量停止、重载会话清理并保留手动停止、退出。活动日志页精确持有 2 条 SSE，退出日志页回落到 1，退出登录回落到 0。Windows 沙箱会阻止 taskkill，已通过最小复现确认；批量停止流程在正常系统进程权限下重跑通过。截图来自测试 fixture，不含实际管理密码。

安全扫描使用 Go 官方 govulncheck，Windows 与 Linux 目标均为 **0 个可达漏洞**。初次 Go 1.27.0 扫描发现标准库 HTTP/2/TLS 等已修复漏洞，因此最低构建补丁升级到 1.27.2；见 [Go 漏洞记录](https://pkg.go.dev/vuln/GO-2026-6617)。模块级仍报告 [x/crypto OpenPGP 未维护](https://pkg.go.dev/vuln/GO-2026-5932)，本项目只用 bcrypt，不导入或调用该包。

**未在本机完成**：Linux 实机进程组信号/race/systemd 安装运行、Docker 镜像运行、长期生产负载浸泡。Windows 本机 `go test -race` 返回 requires cgo，未据此声明 race 通过；Linux CI 已配置 race、浏览器和实际本地二进制安装/重复安装检查，但本次未执行远端工作流。交叉编译不能替代这些运行验证。

## 运行中仍需关注

- 7×24 稳定性需要代表性负载的长时间观测，本次短回归不构成“零泄漏、零死锁”保证。至少记录启动基线、断网恢复、重载和日志轮转后的 heap/goroutine/FD 趋势，并区分规则缓存与持续增长。
- 本地文件输出保持背压；磁盘很慢或故障会影响受管程序 stdout。syslog 为保护程序运行选择有界丢弃，不保证远端日志副本完整；丢弃计数当前尚未作为 Prometheus 指标公开。
- 文件变更检测采用 mtime/size，不保证发现保留相同元数据的内容替换。SSE 不是持久消息队列，断线后的旧日志须从磁盘审计。
- 自签名证书 SAN、有效期、指纹信任与私钥 ACL 需要维护；反向代理不能把管理入口降为明文，也应关闭 SSE 缓冲。
- 并行操作上限 8、每服务 SSE 上限 64、连接上限 256、请求并发上限 128。批量提交仅在明确的 429 拒绝时有界重试，最多等待 60 秒，可取消未提交项；已接受操作仍继续执行。超时或其他错误逐项提示，不自动重放结果不明的写操作。
