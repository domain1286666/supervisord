# Supervisord

Go 编写的进程管理器。一个可执行文件和一个 INI/conf 配置即可运行，Vue 3 + Vite + Element Plus 控制台、Coraza WAF 和 OWASP CRS 均内嵌，无需在运行机器安装 Python、Node 或数据库。

管理入口默认 **HTTPS 8002**。支持多标签进程管理、实时 stdout/stderr、优雅停止、立即强制终止、工作目录、自动重启、健康检查及远程节点。状态和日志通过 SSE 更新；TLS 支持 HTTP/2。

## 安装与启动

在 Linux/systemd 主机执行（公开 GitHub Release，无需 GitHub 账号或令牌）：

```sh
curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fL https://github.com/domain1286666/supervisord/releases/latest/download/install.sh -o install.sh && sudo sh install.sh
```

脚本安装到 `/usr/local/bin/supervisord`，生成 `/etc/supervisord/supervisord.conf`，启用并启动 `go-supervisord.service`。终端只在首次生成时显示随机账号和密码，请立即保存。访问 `https://服务器IP:8002`，核对输出的证书 SHA-256 指纹后信任自签名证书。重复安装保留配置、密码和证书。

安装脚本默认从[公开仓库](https://github.com/domain1286666/supervisord)的 [Release](https://github.com/domain1286666/supervisord/releases/latest) 下载对应架构的二进制，并验证 `checksums.txt`。支持 Linux amd64、arm64、armv7。用 `--version v0.8.0` 固定版本；已有本地文件时用 `sudo sh install.sh --binary ./supervisord_linux_amd64`。脚本不会修改防火墙规则。

直接运行，或在 Windows 使用 `supervisord.exe`：

```sh
./supervisord init --output ./supervisord.conf
./supervisord -n -c ./supervisord.conf
```

`init --port 8002 --bind 127.0.0.1` 可限制新配置的监听地址。`supervisor.ini` 是无固定凭据的参考文件，使用它时先执行 `supervisord init --output supervisor.ini`。始终用 `-c` 明确指定配置。

## 添加程序

编辑 conf，加入实际存在的程序：

```ini
[program:my-app]
directory = %(here)s/apps/my-app
command = ./my-app --name "hello world" --label 'single quotes'
autostart = true
autorestart = unexpected
stopsignal = TERM
stopwaitsecs = 10
stopasgroup = true
killasgroup = true
stdout_logfile = logs/stdout.log
stderr_logfile = logs/stderr.log
stdout_logfile_timestamp_suffix = false
stderr_logfile_timestamp_suffix = false
stdout_logfile_maxbytes = 50MB
stderr_logfile_maxbytes = 50MB
stdout_logfile_backups = 5
stderr_logfile_backups = 5
```

`directory` 相对 conf 所在目录解析，显式相对可执行路径和相对日志路径基于程序工作目录。`command` 直接解析为 argv；管道和重定向需显式使用 `/bin/sh -c '...'`。在网页“配置管理”加载变更，或运行 CLI：

```sh
./supervisord -c ./supervisord.conf ctl status
./supervisord -c ./supervisord.conf ctl reload
./supervisord -c ./supervisord.conf ctl start my-app
./supervisord -c ./supervisord.conf ctl stop my-app
./supervisord -c ./supervisord.conf ctl tail -f my-app stderr
```

网页的“停止”先发送停止信号，等待超时后强杀；“更多 → 强制终止”立即终止并抑制自动重启。Windows 使用原生进程树终止机制，优雅退出能力取决于应用，详见配置文档。

## 登录与证书

禁止明文 HTTP 和 Basic Auth。网页登录使用 bcrypt 密码哈希、内存会话、Secure/HttpOnly/SameSite Cookie 和 CSRF 校验；密码不会保存到前端存储。conf 内包含 Base64 编码的 TLS 证书和私钥，**Base64 不是加密**，请保护整份文件。Linux 配置为 `0600`，配置目录为 `0700`；Windows 还需用 NTFS ACL 限制服务账号访问。

```sh
# 忘记密码：在服务器上生成新密码，然后重启管理服务
./supervisord init --output ./supervisord.conf --reset-password
# 自签名证书有效期一年；到期前续签并重启，重新核对和信任新指纹
./supervisord init --output ./supervisord.conf --renew-certificate
```

本地 CLI 从受保护的同一 conf 读取证书并签名请求，无需保存明文管理密码。远程 CLI 和节点必须验证服务器证书并使用账号会话，配置方法见[迁移指南](docs/MIGRATION.md)。旧 HTTP 客户端和 Basic Auth 集成需要迁移。

## 构建与开发

构建需要 **Go 1.27.2+**、Node 22.12+（CI 为 Node 24）及 npm。Go 补丁版本包含本项目使用的 HTTP/2/TLS 安全修复。

```sh
cd frontend
npm ci
npm run format:check
npm test
npm run build
cd ..
go build -trimpath -tags release -o supervisord .
go test ./... ./xmlrpcclient/...
go vet ./... ./xmlrpcclient/...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...
```

Go 使用 gofmt；Vue/JS/CSS 使用固定版本 Prettier，`npm run format` 格式化，`npm run format:check` 检查。前端构建输出到 `webgui/`，随后由 `go:embed` 打入二进制。修改前端后必须重新构建。开发时可在 `frontend/` 执行 `npm run build -- --watch`，同时用 `STATIC_DIR` 指向构建目录启动 Go HTTPS 服务，保留同源登录与 Secure Cookie 行为。浏览器回归说明见[验证记录](docs/CODE_AUDIT.md)。Dockerfile 同样在构建阶段完成前端打包，运行时配置需挂载为可写文件。

## 文档

- [升级重构方案与实施状态](docs/UPGRADE_REFACTOR.md)
- [配置语义和运行维护](docs/CONFIGURATION.md)
- [旧版本迁移、远程 CLI 与证书](docs/MIGRATION.md)
- [架构、页面和接口约定](docs/ARCHITECTURE.md)
- [代码审计、测量数据和验证边界](docs/CODE_AUDIT.md)
- [重构后的第二轮审查与修复](docs/REVIEW_2026-10-18.md)
- [贡献规范](AGENTS.md)

MIT License，见 [LICENSE](LICENSE)。
