# ZIP 多 IDL 改造开发计划

本计划替代原 Git 单入口 IDL 计划。继续使用 Wire、Hertz、Kitex、Consul，不增加运行时 DI 容器。

- [x] 元数据保存 idl.type=zip、resolvedRevision=ZIP SHA-256；idl.url 仅为一次性导入参数。
- [x] 删除 Git fetcher 与 bootstrap Git 工作目录。
- [x] HTTP 下载、超时和大小限制、ZIP 格式/CRC/条目校验。
- [x] 多 Thrift 文件解析、公共 include、整包严格校验。
- [x] 每 service 文件一个 Generic Client，应用级原子发布和整体资源释放。
- [x] 维护跨应用路由校验、目录 CAS、LKG 和 lease。
- [x] Watcher 按目标 SHA-256 加载 Consul 持久化契约，重启不下载链接。
- [x] 管理 API/UI 改为 ZIP 链接，提供旧记录替换路径。
- [x] 更新 Wire 装配、Dockerfile、部署示例、README 和 CI 说明。
- [x] 提供测试、race、vet 和构建验证（范围见 implementation-status.md）。

测试重点：多 IDL 同名 RPC 的客户端选择、公共类型引用、坏 ZIP/坏路径/软链接/缺文件拒绝，
更新失败保留旧快照，多客户端关闭一次，摘要相同不重建，副本摘要不符拒绝。

测试位于 internal 各包内，默认不依赖外部业务服务。

- [x] ZIP 分块持久化，完整存储后发布配置；失败保留旧版本。
- [x] 覆盖一次性链接失效后的重启恢复、契约分块失败和摘要损坏。
