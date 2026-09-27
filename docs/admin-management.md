# 管理员账号与角色管理

此次使用现有 admin_users、admin_roles、admin_permissions 等表，不增加 migration，不改变 App 或 Merchant 认证。

## API

所有接口前缀为 `/admin-api/v1`。写操作需要 Admin Session、CSRF 和 `admin.manage`；读取需要 `admin.read`。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | /admins | 查看启用的管理员及角色 |
| POST | /admins | 创建管理员：email、display_name、password、roles |
| PUT | /admins/roles | 分配角色：admin_id、roles |
| GET | /roles | 查看角色与权限键 |
| POST | /roles | 创建角色：key、title、description、permissions |
| PUT | /roles | 修改同一 key 的名称、说明和权限 |
| GET | /permissions | 已实现的权限键及中文说明 |

邮箱转小写、去首尾空格且必须是纯邮箱地址。初始密码长度为 12–72 字节，仅保存 bcrypt 哈希，不回显、不写入审计。角色与权限至少选择一项，不接受未知项或重复项。角色 key 为 2–64 位小写字母、数字、下划线，以字母开头，不可重命名。

`super_admin` 受保护，其权限不可修改。角色分配不能移除最后一位启用的超级管理员。访问控制写入串行化，并在事务内重新检查操作人的权限。账号创建、角色创建、角色修改和角色分配均与审计同一事务，失败整体回滚。已有会话在后续请求重新加载权限。

前端对键名保持原文，中文名称与说明分别显示。不支持任意添加权限字符串：一个新的业务权限还必须在后端实现对应授权检查并通过 migration 注册。

## 验证与发布

新增单元测试、认证/CSRF/RBAC 路由测试和独立本机数据库集成测试，覆盖邮箱重复、未知角色/权限、超级管理员保护、权限变更对既有会话生效及审计。

提交并推送 backend 与 admin-web 对应独立仓库后，在服务器运行 `cd ~/zonenan-backend-new && bash manage.sh update`。本次本地开发不自动部署，不更改生产账号密码。
