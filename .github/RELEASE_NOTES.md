## 安全控制台与进程管理重构

- 单个 Go 二进制内嵌 Vue 3、Vite、Element Plus 控制台和 Coraza/OWASP CRS；默认 HTTPS 8002，首次安装自动生成随机账号、密码及自签名证书。
- 网页会话取代 Basic Auth，使用 bcrypt、Secure/HttpOnly Cookie 和 CSRF 校验。状态与日志采用 HTTP/2 SSE，支持多标签、断线重连和连接清理。
- 修复日志页面 404、stdout/stderr 分流、工作目录、命令引号与日志轮转；补齐优雅停止、强杀、重启顺序和服务退出清理。
- 再次审查修复静默 SSE 超时、事件捕获 panic/超限、旧钩子/健康控制干扰新进程、近似日志文件名误匹配等问题。

Linux/systemd 一键安装，无需 GitHub 登录：

```sh
curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fL https://github.com/domain1286666/supervisord/releases/latest/download/install.sh -o install.sh && sudo sh install.sh
```

安装器按架构下载二进制并检查 SHA-256。首次显示的随机密码请保存；重复安装保留 conf、账号和证书。HTTPS 自签名证书需核对指纹后信任。

旧版用户先备份二进制和 conf，按仓库 `docs/MIGRATION.md` 初始化安全配置后重启；HTTP/Basic Auth 客户端必须迁移。审查与验证边界见 `docs/REVIEW_2026-10-18.md`，本次短回归不等同于 7×24 生产浸泡验证。
