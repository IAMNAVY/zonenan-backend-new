# ZoneNaN Backend

Go 后端及生产环境的唯一编排入口。Admin 和 Merchant 使用独立 Git 仓库。

服务器日常更新：

```bash
cd ~/zonenan-backend-new
bash manage.sh update
```

状态：`bash manage.sh status`；日志：`bash manage.sh logs backend`。

完整维护说明见 [docs/production-update.md](docs/production-update.md)。生产配置和数据库归本仓库的 compose.yml 管理，.env 不提交。
