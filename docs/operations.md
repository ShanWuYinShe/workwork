# 运维手册

生产环境的日常运维操作：备份与恢复、证书轮换、git 历史敏感信息处理。架构设计见
[architecture.md](./architecture.md)，部署流程见 [README](../README.md)「部署流程」。

## 备份与恢复

### 为什么必须备份

所有运行时状态都在单机 `.wework/`（htpasswd + users.json = 登录能力、admin 生成的
nginx/xray/媒体配置、曲库、网易云/YouTube cookies 凭证、内部令牌）、
`services/nginx/fail2ban-data/`（fail2ban 持久库：撞库计数与封禁状态）、
named volume `workwork_media_center_data`（catalog.db SQLite）里，另加 `cert/` 与
`.env`。宿主机盘损 = 数据永久丢失，其中登录能力与 App 更新链路（APK+manifest）不可
重建。

### 每日备份（服务器 cron）

```bash
# crontab -e（服务器），异机目标必配——单机备份挡不住整机故障
30 3 * * * cd ~/workwork && BACKUP_RSYNC_TARGET=<user@nas:/backups/workwork> ./scripts/backup.sh >> logs/backup.log 2>&1
```

环境变量（见 `scripts/backup.sh` 头注释）：`BACKUP_DIR`（默认 `/var/backups/workwork`）、
`BACKUP_RSYNC_TARGET`（异机 rsync 目标，强烈建议配置）、`BACKUP_RETENTION_DAYS`
（默认 14）、`BACKUP_EXCLUDE`（额外排除，如超大曲库 `.wework/music-library`）。

归档为 0600 权限 tar.gz，附 sha256 清单。catalog.db 不做热拷贝（活跃 SQLite 顺序
tar 可能页级撕裂），改用 `sqlite3 .backup` 一致性快照（`workwork-media_center_data-db-*.tar.gz`，
内含免 WAL 的完整库）；volume 内其余文件仍走普通 tar。备份失败会向 `WEBHOOK_URL`
发 `backup.failure` 事件。备份包含全部密钥与凭据：备份目录与异机目标都要按敏感数据处理。

### 恢复步骤

```bash
# 1. 全新机器装好 Docker + Compose，clone 仓库并 checkout 与备份匹配的 commit
# 2. 还原数据（服务器上）
tar -xzf workwork-wework-<ts>.tar.gz -C ~/workwork          # .wework/
tar -xzf workwork-cert-<ts>.tar.gz -C ~/workwork            # cert/（若有）
tar -xzf workwork-env-<ts>.tar.gz -C ~/workwork             # .env（若有）
tar -xzf workwork-fail2ban-<ts>.tar.gz -C ~/workwork        # fail2ban 状态（若有）
# 3. 还原 named volume（先 up 一次让它创建 volume，再灌数据）
docker volume ls                            # 找到 <project>_media_center_data
docker run --rm -v <project>_media_center_data:/data -v "$PWD":/backup alpine:3.24.1 \
  sh -c 'rm -rf /data/..?* /data/.[!.]* /data/*; tar xzf /backup/workwork-media_center_data-<ts>.tar.gz -C /data'
# 3b. 覆盖 catalog.db 为一致性快照（权威恢复源；覆盖前可先 PRAGMA integrity_check）
tar -xzf workwork-media_center_data-db-<ts>.tar.gz -C /tmp/catalog-snap
docker run --rm -v <project>_media_center_data:/data -v /tmp/catalog-snap:/snap alpine:3.24.1 \
  sh -c 'apk add --no-cache sqlite >/dev/null && sqlite3 /snap/catalog.db "PRAGMA integrity_check;" \
         && cp /snap/catalog.db /data/catalog.db'
# 4. sudo bash setup-networks.sh（ipvlan 网络），然后
make build admin xray xray-forward nginx media_server vod_extractor netease_api music_server
docker compose up -d
# 5. 按健康端点核对版本指纹（README「版本排查」）
```

### 恢复演练

**机械校验已自动化**：每次备份完成后 `backup.sh` 自动调用 `scripts/restore-verify.sh`
（sha256 逐文件、.htpasswd/users.json/generated 配置/.env 关键文件、catalog.db 与
fail2ban 库 integrity_check、证书配对），失败即 `backup.failure` webhook——每晚
的备份自证可用，无需人工盯。也可随时手动执行：`scripts/restore-verify.sh [目录] [STAMP]`。

**完整恢复演练（每季度）**仍需手动一次：机械校验通过 ≠ 应用一定能在旧数据上启动
（schema 兼容、磁盘布局、文档步骤漂移只有真机才能暴露）。

1. 在隔离环境（或临时目录 + 独立 compose project 名）按上述步骤从**最新备份**完整恢复；
2. 用 `.wework/nginx-auth/.htpasswd` 的账号登录 admin 面板；
3. 核对音乐曲库可播放、媒体目录可列出、transfer 历史在；
4. 记录演练日期与遇到的问题（本节下方追加）。

| 日期 | 结果 | 问题 |
|------|------|------|
|（待执行）| | |

## 证书轮换与历史敏感信息

### 背景：git 历史中的旧证书私钥

`cert/server_cert.key` 曾在 f2662eb 被提交、1a01e71 删除，但 git 历史仍可取回
（`git show f2662eb:cert/server_cert.key`）。处理分两步，顺序不能反：

### 第一步：换钥（先于一切历史清理）

历史里的私钥应视为已泄露，即使远程仓库是私有的。若 `cert/server_cert.pem/key`
（源站 TLS 证书）仍在有效期且私钥与历史中一致：

1. 签发新证书替换（源站证书按 `~/cf_token_and_cert/` 内 README 的 Cloudflare
   Origin CA 流程签发；确认新私钥 != 历史泄露的那把）；
2. 新证书放到服务器 `workwork/cert/`，`make restart nginx` 生效；
3. 用 `openssl s_client -connect yinghuoren.com:443` 核对线上证书已更换、
   curl 全站可用；
4. CF Origin Pull / 边缘侧无需变动（验证的是客户端证书，不是源站证书）。

### 第二步：清理 git 历史（已于 2026-09-19 执行完毕）

历史改写会使所有既有 clone 失效，需要在所有协作者重新 clone 的窗口内完成。

```bash
# 用 git-filter-repo 从全部历史中移除证书文件（在全新 clone 上操作）
pip install git-filter-repo
git clone --mirror <repo-url> workwork-rewrite && cd workwork-rewrite
git filter-repo --invert-paths --path cert/server_cert.key --path cert/server_cert.pem
git push --force --mirror   # force 仅用于这次历史清理
```

主工作树的日常开发仍遵守「禁止改写 main 历史」——本节是唯一例外的一次性灾后清理。

**2026-09-19 执行记录**（两步均已完成）：

1. 换钥：yinghuoren.com 源站证书重签（ECC，2041-09-15 到期，公钥指纹
   `19acf77e…`），workwork 与 ops-portal 两处 nginx 均已替换并重启验证；
   旧钥 retired 备份已删除。
2. 历史清理：`git filter-repo` 移除 `cert/server_cert.{key,pem}`，按内容
   哈希确认旧 blob 已从对象库物理清除；force-push 后本机与服务器均重新
   clone/重置，`git log --all` 对该文件为 0 条。GitHub 侧残留的旧历史
   分支（worktree-audit-fixes 等）已删除；dependabot 分支核查不含旧文件。
   服务器 `~/workwork` 的 2026-05 stash（旧结构残片 api.go/cliproxy.go）
   已随重置丢弃。`.gitleaksignore` 指纹已按新历史更新（cert 条目不再需要）。

### Cloudflare 凭据轮换（常规运维）

- CF API token 与各域名源站证书统一归档在 `~/cf_token_and_cert/`（换证书注意事项
  见该目录 README）；
- 轮换 CF API token 后同步更新服务器 `.env` 的 `CF_API_TOKEN` 并 `make up admin`；
- fail2ban / internal token 随 `.wework/` 备份保留，不需要单独轮换；怀疑泄露时
  删除 `.wework/.internal-token` / `.wework/.fail2ban-token` 并重启 admin（自动再生）。
