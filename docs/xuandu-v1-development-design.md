# 玄渡（xuandu）V1 开发设计

## 当前约束

本设计根据 ZIP 下载与多 IDL 要求修订，替代原 Git 单入口 IDL 设计。
网关保持薄 HTTP/JSON → Kitex Thrift 定位，依赖注入统一使用 Google Wire。

## 应用与契约

一个应用配置一个 Consul serviceName 和一个已持久化的 ZIP SHA-256 版本。下载链接仅在导入时使用，不保存。一个 ZIP 可包含多个
`idl/**/*.thrift` 文件。每个包含 service 的文件创建独立 Generic Client，所有路由
合并为同一应用的 immutable Runtime。纯类型文件作为 include 依赖。

```json
{
  "name": "product",
  "domain": "product.example.com",
  "serviceName": "product",
  "enabled": true,
  "rpcTimeout": "3s",
  "idl": {
    "type": "zip",
    "resolvedRevision": "<64 位 ZIP SHA-256>"
  }
}
```

取消 repository/path/ref 配置、Git 命令、SSH key、known_hosts 和 Git 工作目录。
Bootstrap 包括 server、consul、admin、mcp；Consul 地址从 CONSUL_HTTP_ADDR 读取。

## 下载与拒绝规则

只接受 HTTP(S) URL。GET 必须返回 200；检查 ZIP 字节格式与目录结构，不依赖后缀/MIME。
根目录仅 `idl/`；普通文件仅 `.thrift`，允许子目录。其他文件或根目录整体拒绝。
禁止绝对路径、`..`、反斜杠路径、重复路径、文件/目录冲突、软链接和特殊文件。
空包、损坏 ZIP、CRC 错误、非法 Thrift、缺 include、include 越界/循环都拒绝。

下载上限 32 MiB，解包总量 64 MiB，单文件 8 MiB，条目 1024，下载超时 60 秒。
所有文件在内存中校验和解包，不写入磁盘。

## 多 IDL 构建

1. 下载完整 ZIP，计算 SHA-256。
2. 校验每个 ZIP 条目。
3. thriftgo 解析所有文件，包括未被 include 引用的文件。
4. 校验 include 闭包，找到所有 service 文件。
5. 从 annotation 提取各文件 HTTP 路由。
6. 各文件建立独立 DynamicGo-aware HTTPThriftGeneric + Kitex Client。
7. 合并应用内路由，校验应用内路径冲突；同一域名下不同应用允许相同 HTTP 方法/路径。
8. 持久化完整 ZIP，最后写入完成标记。
9. 成功后统一 CAS 和发布；任何失败关闭已创建的所有候选客户端。

一个文件当前最多一个 service；不同文件可包含同名 RPC method。
数据面先选应用，再按 HTTP method/path 选择文件对应客户端，GenericCall method 保持空串。
支持 GET/POST/PUT/DELETE、单 struct 入参和 struct 返回，不支持 oneway/service extends。

## 发布与多副本

Consul KV 保存应用配置、ZIP 摘要和分块 ZIP 字节。
发布事务 CAS 应用 ModifyIndex 和目录 `_catalog` 版本；目录标记值每次变化，
避免 Consul 同值写入未推进 ModifyIndex。事务成功后本地原子切换。

Watcher 和启动加载只按目标 SHA-256 读取 Consul 中持久化的 ZIP，不访问下载链接。契约缺失、摘要不符或构建失败时保留已有 Last Known Good；没有运行版本则明确报告不可用。
ZIP 按 256 KiB 分块写入 `<metadataPrefix>-contracts/<sha256>/`，以 CAS 创建不可变块，最后写入完整标记；完整持久化成功后才允许 CAS 发布应用配置。重启按摘要校验完整 ZIP 后重建路由。一次性链接不持久化。旧配置需重新导入以补齐未保存的契约。

## 管理与生命周期

POST /admin/apps；GET /admin/apps；GET /admin/apps/:name；
POST /admin/apps/:name/update；DELETE /admin/apps/:name。
Update 支持可选 domain/serviceName/rpcTimeout/enabled/idl 补丁；空请求重新检查当前 ZIP。
旧 Git 记录仅供管理读取/删除，通过 Update 提供 ZIP URL 后迁移，不进行自动 Git 拉取。

Hertz 保留 healthz、readyz、NoRoute catch-all。业务请求持有快照 lease；旧应用的
所有客户端在最后一个旧 lease 释放后关闭。SIGTERM 停止管理写入、取消构建，25 秒退出期限。

错误：下载/包/IDL 构建失败 422，冲突 409；业务 400/404/413/502/503/504 保持原语义。
HTTP 业务请求通过 Host + X-App-Code 联合定位应用，再匹配应用内路由。MCP 入口同样校验关联应用域名，但不要求 X-App-Code，仍执行原有 Key 授权。旧记录缺少 domain 时保留管理读取和编辑，补充域名后才能发布 Runtime。

普通契约构建失败保留 LKG；观察到域名变更、停用或删除时先撤销旧身份的路由，再尝试恢复新版本，不能用 LKG 保留已撤销的域名。更新通过目录 CAS 保护应用目录一致性，副本通过 Watch 同步，传播期间不是所有副本同时切换。

日志默认写入 /app/logs/xuandu.log 并保留 stdout，50 MiB 轮转、5 个备份。可用 XUANDU_LOG_DIR 覆盖；K8s 使用每 Pod 的 emptyDir。日志不打印下载 URL 的 query、认证头、请求体或响应体。

## 登录要求和请求上下文

方法注解 `xuandu.Auth = "required"` 标记需要用户身份；缺失或 optional 不要求。网关读取五个约定请求头 UserId / AppCode / DeviceId / DeviceType / DeviceName，原样写入 rpcmeta.RequestInfo，空值也写入；不额外检查格式或有效性。只有 required 接口检查 UserId 是否为空，空时返回公共错误 401003「用户身份不能为空」，不调用 RPC。HTTP 和 MCP 共享此规则，MCP Key 与这些头独立。
