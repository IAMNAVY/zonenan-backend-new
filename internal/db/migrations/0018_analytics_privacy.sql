-- Publish a new privacy-policy version once, preserving any operator edits to the latest text.
WITH latest AS (
  SELECT version, title, content
    FROM legal_documents
   WHERE doc_type = 'privacy_policy'
   ORDER BY version DESC
   LIMIT 1
)
INSERT INTO legal_documents(doc_type, version, title, content)
SELECT 'privacy_policy', latest.version + 1, latest.title,
       latest.content || $analytics$

### 使用情况统计

为改进产品体验和判断功能是否正常，我们会收集最小化的使用情况事件，包括会话开始、页面浏览与停留时长、功能打开与结果、广告位展示与点击，以及事件时间、操作系统平台和应用版本。事件仅使用固定枚举和受限标识符，不收集或保存 IP 地址、输入内容、详情字段或其他自由文本。

安装标识符和已登录账号 ID 仅在服务端使用独立密钥进行 HMAC 不可逆处理后保存，分析结果以汇总形式展示。原始分析事件最长保留 12 个月；被管理员标记为内部测试等排除对象的账号不会计入任何分析指标。
$analytics$
  FROM latest
 WHERE latest.content NOT LIKE '%### 使用情况统计%';

