# 生产更新、改密与旧后台下线

本次代码只在本地修改和测试，尚未提交、推送或更新到生产。GitHub Deploy Key 为只读。

## 三个独立仓库

| 本地提交目录 | GitHub 仓库 | 服务器拉取目录 |
| --- | --- | --- |
| `backend/` | `IAMNAVY/zonenan-backend-new` | `/root/zonenan-backend-new` |
| `admin-web/` | `IAMNAVY/zonenan-admin` | `/root/zonenan-admin` |
| `merchant-web/` | `IAMNAVY/zonenan-merchant` | `/root/zonenan-merchant` |

不要只提交外层项目：Go 后端、Admin、Merchant 都有各自的 `.git`。三处分别检查 diff、提交、推送。
服务器三处 origin/main 已关联，并配置各自 `core.sshCommand`，无需复制令牌。
外层 `/root/zonenan-backend-new/.env` 是生产配置，不能上传 GitHub，不能用 `.env.example` 覆盖。

## 第一次拉取

Admin、Merchant 服务器当前各有一处部署时修改的 `nginx.conf`。先保存这些改动；stash 不要 pop，因为此次已将相应媒体代理和 `app:8080` 配置纳入新源码。

```bash
git -C /root/zonenan-admin diff -- nginx.conf
git -C /root/zonenan-merchant diff -- nginx.conf
git -C /root/zonenan-admin stash push -m before-first-github-update -- nginx.conf
git -C /root/zonenan-merchant stash push -m before-first-github-update -- nginx.conf
```

以后工作区干净时不需要再次 stash。若有其他改动先检查，不使用 `reset --hard` 或 `git clean`。

你推送完成后，服务器执行：

```bash
git -C /root/zonenan-backend-new pull --ff-only
git -C /root/zonenan-admin pull --ff-only
git -C /root/zonenan-merchant pull --ff-only
```

## 构建与重启

以下命令请在 Bash 中按顺序执行。任一步失败先处理，不继续切换。
只 `docker restart` 不会载入新源码，必须先构建镜像再重建容器。
单实例后端重建存在短暂中断，建议选低峰期；这不是零停机发布。

```bash
set -e
cd /root/zonenan-backend-new
release_stamp=$(date +%Y%m%d-%H%M%S)
docker tag "$(docker inspect -f '{{.Image}}' zonenan-backend-new)" "zonenan-backend-new:rollback-$release_stamp"
docker tag "$(docker inspect -f '{{.Image}}' zonenan-admin)" "zonenan-admin:rollback-$release_stamp"
docker tag "$(docker inspect -f '{{.Image}}' zonenan-merchant)" "zonenan-merchant:rollback-$release_stamp"
echo "回滚镜像标记：rollback-$release_stamp"

docker build -t zonenan-backend-new:phase8 /root/zonenan-backend-new
docker build -t zonenan-admin:phase8 /root/zonenan-admin
docker build -t zonenan-merchant:phase8 /root/zonenan-merchant

docker compose -f deploy/compose.server.yml config --quiet
# 本次改密及旧接口下线不新增 migration。
# 以后版本如有数据库变更，先备份，并按对应版本迁移文档执行 migrate。
docker compose -f deploy/compose.server.yml up -d

curl --fail --retry 15 --retry-connrefused --retry-delay 1 http://127.0.0.1:18088/healthz
docker exec zonenan-admin nginx -t
docker exec zonenan-admin nginx -s reload
docker exec zonenan-merchant nginx -t
docker exec zonenan-merchant nginx -s reload
docker exec zonenan-nginx nginx -t
docker exec zonenan-nginx nginx -s reload
docker exec zonenan-backend-caddy-1 caddy reload --config /etc/caddy/Caddyfile
docker compose -f deploy/compose.server.yml ps
```

重载代理用于刷新 Docker 容器 IP。不要启动旧 `zonenan-backend-app-1`，否则会与新后端的 `app` 别名冲突。
不要执行旧项目的整体 `docker compose up` / `down`，旧项目仍承载数据库和 Caddy。
`www.zonenan.pro -> http://zonenan-nginx:80` 不变，官网首页仍使用现有静态文件。

## 验收与修改密码

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18088/panel
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18088/admin/users
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18088/app/version
```

预期依次为 410、410、200。旧入口在后端直接禁用，因此旧 ADMIN_SECRET 不能绕过，也不依赖某个域名的 Nginx 规则。
新后台 `/admin-api/v1/*`、商户后台、App 的认证/成绩/会员/地图/统计/回调等业务接口保留。

访问 Admin，点击侧边栏底部“修改密码”：输入原密码、新密码、再次确认。新密码为 12–72 字节且不能与原密码相同。
成功后所有旧 Session 失效，重新登录；更新密码和撤销会话、审计日志在同一事务中执行。密码不写入日志。
初始凭据文件只记载首次密码，改密后不会自动更新。已有管理员不能通过修改 ADMIN_BOOTSTRAP_* 重置。
本次仅提供修改自己的密码，不包含修改邮箱或重置其他管理员密码。

## 回退本次镜像更新

如新版本异常，用先前记录的 release_stamp（不要重新生成时间戳）：

```bash
docker tag "zonenan-backend-new:rollback-$release_stamp" zonenan-backend-new:phase8
docker tag "zonenan-admin:rollback-$release_stamp" zonenan-admin:phase8
docker tag "zonenan-merchant:rollback-$release_stamp" zonenan-merchant:phase8
docker compose -f /root/zonenan-backend-new/deploy/compose.server.yml up -d
```

随后执行上面的健康检查和代理重载。本次无新 migration，数据库和已修改密码保留。
回退到旧镜像会恢复其旧接口行为，回退期间应注意旧后台重新可达。
