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

新版 App 使用 `POST /auth/campus-login`，提交学号、设备信息及可选的 `device_credential`，不提交学校 Cookie、CAS ticket、IDS token 或姓名。相同学号复用同一账号；持有有效 ZoneNaN 凭证或对应可信设备凭据时恢复完整会话。未携带设备凭据时，沿用旧版规则：学号与设备标识命中服务端可信设备记录也直接恢复完整会话，无需旧 JWT 或邮箱验证码；其他设备取得受限会话。受限会话使用独立派生签名密钥，旧服务实例也不会把它当作完整 JWT。

校园登录和 `GET /auth/me` 对受限会话返回已有昵称和 `campus_verified: false`，不返回邮箱、真实角色、会员或其他私人资料。昵称属于可公开展示资料，仅知道学号即可查询；不能用它证明账号所有权。其余需要账号身份的接口均拒绝受限会话，返回 403。App 在给分、会员和账号管理入口通过 `POST /auth/campus-verify` 按需创建挑战，随后复用 `/auth/device-challenge/*` 完成校园邮箱验证或可信设备批准，取得正式凭证。验证只升级当前会话，同学号的其他受限会话不会自动升级，累计试用次数不会重置。

验证成功及旧版 CAS 登录的响应新增可选 `device_credential`，新版 App 按账号保存到安全存储，普通退出保留。凭据使用独立派生签名密钥，绑定用户、设备编号与既有 `trusted_devices.id`，有效期一年，成功登录时续期；不能用于普通 API。服务端每次恢复都核对现存可信设备记录，撤销、封禁后不能恢复，删除再重新信任也不会复活旧凭据。旧设备未持有新增凭据也可通过服务端信任记录直接登录并补发凭据，不修改设备 ID、信任记录或试用累计用量。该兼容路径继续采用旧版设备指纹信任模型，不要求所有客户端都持有新增凭据。旧客户端忽略新增字段即可继续使用。

保留 `/auth/cas-login`、`/auth/cas-device-verify` 供旧客户端使用；旧版 CAS 登录保留可信设备快路径，只有未命中可信设备时回验学校凭证。挑战接口仍由已经登录的可信设备批准目标设备，或通过校园邮箱验证批准。部署时先更新全部后端实例，再发布新版 App；新 App 不会在新接口缺失时回退上传学校凭证。本次复用已有 identity `verified` 字段和设备挑战表，不增加数据库迁移。

集成测试只允许显式设置 `ZONENAN_CAMPUS_TEST_DATABASE_URL`，指向本机 `127.0.0.1` 的专用可丢弃 `zonenan_campus_test` 数据库，需 PostGIS。该测试会执行全量迁移和重复迁移，不应连接生产数据库。
