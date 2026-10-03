# ZoneNaN Backend

Go 后端及生产环境的唯一编排入口。Admin 和 Merchant 使用独立 Git 仓库。

服务器日常更新：

```bash
cd ~/zonenan-backend-new
bash manage.sh update
```

状态：`bash manage.sh status`；日志：`bash manage.sh logs backend`。

完整维护说明见 [docs/production-update.md](docs/production-update.md)。生产配置和数据库归本仓库的 compose.yml 管理，.env 不提交。

### 校园账号登记与按需验证

新版 App 使用 `POST /auth/campus-login`，仅提交学号与设备信息，不提交学校 Cookie、CAS ticket、IDS token 或姓名。相同学号复用同一账号；未持有该账号有效 ZoneNaN 凭证的设备只能取得受限会话。受限会话使用独立派生签名密钥，旧服务实例也不会把它当作完整 JWT。

`GET /auth/me` 对受限会话只返回固定展示资料和 `campus_verified: false`，不返回已有账号的邮箱、角色、会员或私人资料。其余需要账号身份的接口均拒绝受限会话，返回 403。App 在给分、会员和账号管理入口通过 `POST /auth/campus-verify` 按需创建挑战，随后复用 `/auth/device-challenge/*` 完成校园邮箱验证或可信设备批准，取得正式凭证。验证只升级当前会话，同学号的其他受限会话不会自动升级，累计试用次数不会重置。

保留 `/auth/cas-login`、`/auth/cas-device-verify` 供旧客户端使用；旧版 CAS 登录每次都回验凭证，不再仅凭设备标识放行。部署时先更新全部后端实例，再发布新版 App；新 App 不会在新接口缺失时回退上传学校凭证。本次复用已有 identity `verified` 字段和设备挑战表，不增加数据库迁移。

集成测试只允许显式设置 `ZONENAN_CAMPUS_TEST_DATABASE_URL`，指向本机 `127.0.0.1` 的专用可丢弃 `zonenan_campus_test` 数据库，需 PostGIS。该测试会执行全量迁移和重复迁移，不应连接生产数据库。
