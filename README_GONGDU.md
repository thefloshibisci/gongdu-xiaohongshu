# 共度 · 小红书 MCP

橘瓣“共度 / 一起逛”使用的小红书后端维护版本。基于 [xpzouying/xiaohongshu-mcp](https://github.com/xpzouying/xiaohongshu-mcp) v2.7.0，保留上游提交历史、版权声明和 Apache-2.0 许可。

## 版本来源

- 上游基线：`d6352e5435b45ebb48fe240dc03a2bec0476bd56`。
- 本次归档的是 2026-11-03 已部署到腾讯云的共度修复源码。
- 已部署 Linux AMD64 二进制 SHA256：`ed35774a1775f102900ad618ed697127a58953a0eddbed86af80c49cdd9cf963`。
- 已部署镜像 ID：`sha256:c7ef580dedb18920ced38871d1d2fdd8c0599e50bd649b972b26cc104ea525be`。
- 2026-11-08 入库前，85 个构建源码文件与原构建冻结记录逐字节匹配。

完整来源与按 LF 规范化的源码校验值见 [docs/gongdu-source-provenance.json](docs/gongdu-source-provenance.json)。本仓库使用独立名称；Go module 路径沿用上游，保持已验证代码不变。

## 自用修改

- 评论前先展开真实编辑区，发送前检查按钮唯一、可见、启用且可点击。
- 精确定位回复目标，结合平台回执和新评论读取核验结果；提交结果不确定时不自动重发，也不降级为顶层评论。
- 增加 `follow_user`、`follow_back`，检查目标、当前账号、状态及操作证据。
- 保留当前账号可见与公开可见的区别，返回可核对的操作结果。
- 包含对应单元测试及本地浏览器测试夹具。

## 已有验证

2026-11-03 的构建与部署记录：28 项评论相关单元/本地 Chrome 测试通过；隔离 Linux 环境 8 个包共 228 项通过；候选与部署后的 MCP 握手、鉴权和 20 个工具发现通过。2026-11-08 仅核验这些记录及源码一致性，未重跑账号操作或重新部署。

Windows 默认全套曾存在 cookies 临时路径兜底测试失败；该限制保留，不把 Linux 通过写成 Windows 全套通过。

## 开发与部署

Go 版本以 `go.mod` 为准。默认单元测试不访问真实小红书账号；浏览器集成测试由 `integration` build tag 隔离。

```sh
go build ./...
go test ./...
go test -tags integration -run '^$' ./...
```

构建与配置方式沿用 [上游 README](README.md) 和 [Docker 文档](docker/README.md)。从本仓库源码构建镜像才包含共度修复；直接拉取上游镜像不会包含这些修改。

公开 MCP 服务必须设置非空 `AUTH_TOKEN`，登录数据使用持久卷并保持单实例。连接令牌、cookies、运行数据及生产部署配置不属于源码仓库。此仓库未配置生产自动部署；推送代码不改变现有腾讯云服务。
