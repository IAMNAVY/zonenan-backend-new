# ZoneNaN 生产维护

## 唯一入口

服务器只使用 `~/zonenan-backend-new/compose.yml` 管理 db、backend、worker、admin、merchant、website。
三个源码仓库分别是 `~/zonenan-backend-new`、`~/zonenan-admin`、`~/zonenan-merchant`，均使用对应只读 Deploy Key。
官网静态文件保持在 `~/zonenan-website`。
旧项目目录归档在备份目录，不再作为运行依赖。

- `panel.zonenan.pro` → Cloudflare Tunnel → `http://zonenan-admin:80`
- `merchant.zonenan.pro` → Cloudflare Tunnel → `http://zonenan-merchant:80`
- `www.zonenan.pro` → Cloudflare Tunnel → `http://zonenan-nginx:80`（官网及 API）
- 内部反代统一 `app:8080`。数据库不发布公网端口。

生产密码等配置只放在后端目录的 `.env`，不要上传 Git。本地和服务器的版本化配置文件保持相同，秘密值按环境分别提供。
`current` 是 Docker 镜像版本标签，不是端口。实际本机诊断端口：后端 18088，Admin 13000，Merchant 13001。

## 日常使用

你在本地三个独立仓库分别提交、推送后，服务器只需：

```bash
cd ~/zonenan-backend-new
bash manage.sh update
```

脚本依次拉取三个仓库、先构建镜像、记录回滚标签、更新容器、检查后端健康、刷新代理解析。
首次配置已部署但尚未发布到 Git 时，先提交并推送本地三个仓库的配置和说明。脚本仅在服务器所有已发布文件与 origin/main 完全相同时同步 Git 元数据，保留工作文件；有任何不一致就停止，不会 stash 或覆盖改动。正常更新只允许 main 分支快进。
单实例更新会有短暂重启，请选低峰期。脚本不执行数据库回滚；未来有 schema 变更时必须先按该版本说明备份迁移。

```bash
bash manage.sh status
bash manage.sh logs backend
bash manage.sh logs db
bash manage.sh logs worker
# 已拉取代码或只更新了 .env：
bash manage.sh apply
# 使用更新时打印的真实标签：
bash manage.sh rollback rollback-YYYYMMDD-HHMMSS
```

不要只 `docker restart` 来发布源码：它不会重新构建镜像。
不再使用旧项目 Compose、`deploy/compose.server.yml` 或 `phase8` 发布命令。

## 数据与备份

数据库由新项目的 `db` 服务管理，容器名 `zonenan-db`。
现有数据卷原位接管：`zonenan-backend_db_data`、`zonenan-backend_map_tile_cache`、`zonenan_v2_uploads`。
名称只是已有存储标识，保留名称可避免复制时遗漏最新写入；它们不依赖旧代码目录、旧 Compose 或旧网络。
绝不能因为卷名含旧前缀就删除：这些是当前生产数据。

接管前备份、旧环境归档保存在 `/root/backups/zonenan/`。归档只是恢复材料，不是第二套需要日常维护的服务。
数据库版本固定到接管时已在运行的 PostGIS 镜像 digest，避免此次顺带升级 PostgreSQL。

## 修改管理员密码与接口下线

新 Admin 侧边栏底部“修改密码”：原密码、新密码、再次确认。新密码为 12–72 字节，不能与原密码相同。
成功后所有旧 Session 失效，重新登录。密码修改、会话撤销和审计在同一事务中执行。
初始凭据文件只记载首次密码；已有账号不能通过修改 ADMIN_BOOTSTRAP_* 重置。
本次不包含修改邮箱或重置其他管理员密码。

`/panel`、`/admin/*` 已在新代码中返回 410；旧 ADMIN_SECRET 无法绕过。
`/admin-api/v1/*` 和 App 的认证、成绩、会员、地图、分析、回调等业务接口保留。

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18088/panel
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18088/admin/users
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18088/app/version
```

应为 410、410、200。Cloudflare Access 等边缘规则可能先返回登录跳转，直接本机检查可验证后端真实行为。
