<p align="center"><img src="web/public/xuandu.jpg" width="144" alt="玄渡 xuandu" /></p>

# 玄渡 · xuandu

一个轻量的 HTTP/JSON → Kitex Thrift 网关，也可以将已发布的接口开放为 MCP 工具。
上传一个包含 Thrift IDL 的 ZIP，即可生成 HTTP 路由和接口文档，无需为每个后端编写网关适配代码。

[MIT License](LICENSE) · [开发设计](docs/xuandu-v1-development-design.md) · [Kubernetes 部署](deploy/kubernetes.yaml)

## 能力

- **动态路由**：从 Thrift annotations 加载 GET / POST / PUT / DELETE 路由，一个 ZIP 支持多个 IDL 文件。
- **持久化契约**：应用配置和 ZIP 契约保存在 Consul KV；重启直接恢复，无需再次下载。
- **接口文档**：从 IDL 结构和注释生成 OpenAPI，提供独立的文档浏览页面。
- **MCP 服务**：Streamable HTTP、按路径挂载、全局调用方 Key，以及服务和工具级授权。
- **平滑更新**：完整校验后发布；更新失败保留可用版本，在途请求继续完成。
- **统一响应**：业务接口使用六位状态码及 `code/message/data` 格式。
- **单进程运行**：控制台嵌入 Go 二进制，生产环境无需 Node.js。依赖注入使用 Google Wire。

## 运行依赖与端口

网关依赖 **Consul** 进行服务发现和元数据存储，后端需为可通过 Consul 发现的 Kitex Thrift 服务。无需额外数据库、本地持久化目录或 PVC。

| 入口 | 默认端口 | 用途 |
| --- | --- | --- |
| API | `8080` | HTTP 业务接口、`/healthz`、`/readyz` |
| MCP | `8081` | 已配置的 MCP 路径，例如 `/product/mcp` |
| 控制台 | `9090` | 管理页面和 `/admin/*` 管理 API |

本地控制台默认监听 `127.0.0.1`；Kubernetes 示例监听容器内的所有接口，由 ClusterIP Service 提供访问。

## 本地启动

需要 Git、Go（版本见 `go.mod`，当前为 `1.27.1`）、Node.js 24 和可访问的 Consul。

```sh
git clone https://github.com/iamzhangxin/xuandu-gateway.git
cd xuandu-gateway

# 编译控制台，资源通过 go:embed 嵌入 Go 程序。
cd web
npm ci
npm run build
cd ..

# 替换成实际 Consul 地址；示例配置中不提供默认地址或凭据。
export CONSUL_HTTP_ADDR='127.0.0.1:8500'
export CONSUL_HTTP_TOKEN='' # Consul 启用 ACL 时设置有效 Token。
export XUANDU_LOG_DIR="$PWD/.local/logs" # 本地开发目录；容器默认 /app/logs。

go run ./cmd -config config/example.yaml
```

打开 `http://127.0.0.1:9090`。`CONSUL_HTTP_ADDR` 支持 `host:port` 或 `http(s)://host:port`，环境变量优先于 YAML；为空时启动报错，不会猜测 Consul 地址。

Consul 凭据只用于网关访问 Consul，MCP 调用方使用独立的 Key。

## 导入第一个应用

准备后端对应的 IDL，例如 `idl/product.thrift`：

```thrift
struct GetReq {
    1: required string id (api.query = "id")
}

struct GetRes {
    1: string id
    2: string name
}

service Product {
    GetRes Get(1: GetReq req) (api.get = "/api/product/get")
}
```

一个 ZIP 的目录可以是：

```text
idl/
  product.thrift
  order.thrift
  common/
    types.thrift
```

打包并上传到网关可访问的 HTTP(S) 地址：

```sh
zip -r contract.zip idl/ -i '*.thrift'
```

在控制台新增应用，填写应用编码、应用域名、Consul 服务名称、RPC 超时和 ZIP 下载链接。也可以调用管理 API：

```sh
curl --fail-with-body http://127.0.0.1:9090/admin/apps \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "product",
    "domain": "product.example.com",
    "serviceName": "product-service",
    "rpcTimeout": "3s",
    "idl": {"type": "zip", "url": "https://example.com/contract.zip"}
  }'
```

`serviceName` 必须与 Consul 中已注册、可访问的后端服务匹配。后端须支持 Kitex Thrift 与 TTHeader 元信息；网关不会启动后端服务。

应用通过 HTTP `Host` + `X-App-Code` 联合定位，再在该应用内匹配方法和路径。多个应用可以共用同一域名，也可以声明相同路径；请求只在选定应用中查找，不回退到其他应用。`X-App-Code` 必须唯一且与应用 `name` 完全一致；缺失、不匹配或重复时返回 HTTP 403，未知域名返回 HTTP 404。

本地无需修改 DNS，可这样调用：

```sh
curl --fail-with-body 'http://127.0.0.1:8080/api/product/get?id=123' \
  -H 'Host: product.example.com' \
  -H 'X-App-Code: product'
```

域名配置不包含协议、端口或路径；支持 DNS 名称、localhost 或 IP，按小写统一并忽略末尾的点。请求 Host 中的端口不参与应用匹配。代理必须保留 Host；不会信任 `X-Forwarded-Host`。域名与应用编码用于路由一致性校验，应用编码不是调用密钥。

升级前创建的应用缺少域名时显示「待配置」，不阻止 Pod 就绪和控制台访问，但必须通过「更新应用」补充域名后才会开放 HTTP/MCP 调用，无需重新上传 ZIP。`/healthz`、`/readyz` 探针和独立端口上的控制台不要求业务域名或 `X-App-Code`。

**ZIP 约束：** 根目录必须为 `idl/`，普通文件只能是 `.thrift`；禁止额外说明文件、`__MACOSX`、符号链接、路径穿越及损坏文件。压缩包上限 32 MiB，解压上限 64 MiB，最多 1024 个条目。每个含 service 的文件目前支持一个 service，方法使用单 struct 入参及 struct 返回，不支持 oneway 或 service extends。

## 更新与存储

```sh
curl --fail-with-body -X POST http://127.0.0.1:9090/admin/apps/product/update \
  -H 'Content-Type: application/json' \
  -d '{"idl":{"type":"zip","url":"https://example.com/contract-v2.zip"}}'
```

下载链接仅为一次性导入参数，不保存、不返回给控制台。网关校验 ZIP 和全部 IDL，将 ZIP 按 256 KiB 分块存储并核对 SHA-256，再通过 Consul CAS 发布配置。空更新请求复用已保存契约；失败保留当前可用版本。

默认 Consul KV 前缀：

| 内容 | 位置 |
| --- | --- |
| 应用配置 | `xuandu/apps/` |
| ZIP 契约 | `xuandu/apps-contracts/` |
| MCP 配置和 Key 摘要 | `xuandu/mcp/catalog` |

启用 ACL 时，Token 需要服务发现读取权限及上述 KV 前缀读写权限，参见 [KV 策略示例](deploy/consul-kv-policy.hcl)。请一起备份配置和契约；仅备份应用摘要无法恢复 IDL。

## 接口文档与响应

控制台的接口文档按应用和接口分组展示参数、响应和 IDL 注释，可导出：

```text
GET /admin/apps/{name}/openapi.json
```

业务成功响应：

```json
{"code":"000000","message":"success","data":{"id":"123","name":"Example"}}
```

失败时 `code` 为非 `000000` 的六位编码，`data` 为 `null`。下游业务错误保留合法业务码和描述；HTTP 状态仍反映参数错误、服务不可用、超时等情况。管理 API 和健康探针使用各自的响应协议。

HTTP 和 MCP 入口将 `X-User-ID`、`X-App-Code`、`X-Device-ID`、`X-Device-Type`、`X-Device-Name` 原样写入 `rpcmeta.RequestInfo` 并通过 TTHeader 透传。不校验字段内容，不去除空白，缺失或空值以空字符串写入。下游用 `rpcmeta.FromContext(ctx)` 读取完整信息，也可继续使用 `rpcmeta.UserId(ctx)` 等单字段方法。HTTP 的应用定位仍使用 Host + X-App-Code，MCP 保留域名及独立 Key 授权，不用 X-App-Code 校验身份。

需要登录的接口在 Thrift 方法上声明：

```thrift
GetRes Get(1: GetReq req) (
    api.get = "/api/product/get",
    xuandu.auth = "required"
)
```

`xuandu.auth = "required"` 时，UserId 为空就直接返回公共错误 `ErrIdentityRequired`：HTTP 401，`{"code":"401003","message":"用户身份不能为空","data":null}`，不调用下游。未声明或声明 `"optional"` 时不拦截空身份。登录规则随 ZIP 契约保存、更新和恢复，并展示在路由及接口文档中。注解只支持 required / optional；同一方法不能重复声明。

公共错误提示统一为中文。Token 校验由可信前置网关完成，并覆盖客户端传入的身份头；玄渡只负责声明了登录要求的接口的空用户身份检查。MCP 工具调用也遵循同一登录规则，失败时设置 `isError=true`。

## 日志

默认同时输出到标准输出与 `/app/logs/xuandu.log`，包含网关请求、MCP 工具调用、启动日志及 Hertz/Kitex 框架日志。网关日志使用 JSON，框架日志保留各自格式。文件同步追加写入，重启不清空已有文件；单文件上限 50 MiB，保留 5 个历史文件（`.1` 最新，至 `.5`），总量约 300 MiB。文件写入失败会在 stderr 报告，标准输出仍可查看。

设置 `XUANDU_LOG_DIR` 可覆盖目录；例如本地开发使用 `.local/logs`。启动时如果目录无法创建或写入，会明确报错退出。容器已创建可写目录；K8s 清单为每个 Pod 单独挂载 `emptyDir`，容器重启保留日志，Pod 删除或重建后不保留。需要长期保存时接入日志采集或自行配置持久卷。

```sh
kubectl -n xuandu-gateway logs deployment/xuandu --tail=100
kubectl -n xuandu-gateway exec deployment/xuandu -- tail -n 100 /app/logs/xuandu.log
```

## MCP

1. 在控制台新增 MCP 服务，选择应用，填写路径，例如 `/product/mcp`。
2. 新增工具，选择该应用已发布的 OpenAPI 接口并挂载到 MCP 服务。
3. 创建全局 Key，授权可连接的服务和可调用的工具。新工具不会自动获得授权。
4. 在支持自定义 Header 的 MCP 客户端中使用 Streamable HTTP：

```json
{
  "url": "https://product.example.com/product/mcp",
  "headers": {"X-MCP-Key": "<your-key>"}
}
```

MCP 服务按路径匹配，并强制校验请求 Host 属于其关联应用；不校验 `X-App-Code`，但原有 Key 和工具授权仍然生效。域名不同的请求返回 HTTP 403，即使持有有效 Key 也无法调用。配置只接受路径，不接受完整 URL，域名在应用中维护。一个服务可配置多个路径，不同服务不能占用相同路径。认证 Header 可自定义，也支持 `Authorization: Bearer <key>`。

Key 原文只在创建或轮换时显示，Consul 保存其摘要。授权同步间隔为 5 秒；无法刷新有效权限超过 60 秒后拒绝新 MCP 请求。MCP Key 只用于服务和工具授权，不透传给后端，也不充当用户身份。五个约定请求头来自调用请求，原样透传；不从工具 JSON 参数中构造身份。

本地 MCP 联调可将应用域名暂设为 `127.0.0.1`，然后访问 `http://127.0.0.1:8081/product/mcp`；也可以通过本地 DNS/hosts 或客户端 Host 设置使用已配置域名。

工具复用网关执行链路，成功时在 MCP text 和 structuredContent 中返回统一响应对象，业务失败设置 `isError=true`。当前使用无状态 JSON 响应，不提供 OAuth 登录、长期 SSE 会话或跨域浏览器配置。

## Docker

```sh
docker build -t xuandu:v0.0.2 .

# 容器内控制台监听所有接口，宿主机仅向本机开放控制台。
sed 's/127.0.0.1:9090/0.0.0.0:9090/' config/example.yaml > /tmp/xuandu.yaml
chmod 644 /tmp/xuandu.yaml

docker run --rm --name xuandu \
  -p 8080:8080 -p 8081:8081 -p 127.0.0.1:9090:9090 \
  -e CONSUL_HTTP_ADDR -e CONSUL_HTTP_TOKEN \
  -v /tmp/xuandu.yaml:/etc/xuandu/gateway.yaml:ro \
  xuandu:v0.0.2
```

先设置容器可访问的 `CONSUL_HTTP_ADDR`；容器中的 `127.0.0.1` 指向容器自身。默认 Docker 构建包含前端和 Go 编译，镜像使用非 root 用户运行。

## Kubernetes

[deploy/kubernetes.yaml](deploy/kubernetes.yaml) 包含：

- Namespace：`xuandu-gateway`
- Deployment / 容器：`xuandu`；Pod 名称由 Deployment 生成，形如 `xuandu-<hash>-<suffix>`
- `xuandu-api`：Service `8080` → API `8080`
- `xuandu-mcp`：Service `8081` → MCP `8081`
- `xuandu-console`：Service `9090` → 控制台 `9090`
- ConfigMap：只保存无凭据的启动配置

应用前，修改 Deployment 的 `image` 和 `CONSUL_HTTP_ADDR`。Consul 环境变量默认留空；启用 ACL 时，将 `CONSUL_HTTP_TOKEN` 改为从 Kubernetes Secret 引用。私有镜像仓按集群需要配置 `imagePullSecrets`。

```sh
kubectl apply -f deploy/kubernetes.yaml
kubectl -n xuandu-gateway rollout status deployment/xuandu
kubectl -n xuandu-gateway port-forward service/xuandu-console 9090:9090
```

此清单不会部署 Consul，也不创建数据库或 PVC。三个 Service 均为 ClusterIP；外部入口可通过现有代理转发，MCP 示例见 [Traefik 清单](deploy/traefik-mcp.example.yaml)。控制台没有内置登录，必须通过内网、访问控制或可信代理保护，不能直接暴露到公网。

## 开发与验证

```sh
cd web
npm ci
npm run build
npm run tsc
cd ..
go generate ./internal/app
go test ./...
go test -race ./internal/...
go vet ./...
go build -o bin/xuandu ./cmd
```

前端开发可在 `web/` 运行 `npm run dev`，管理请求会代理到 `127.0.0.1:9090`。修改前端后重新构建并重启 Go 进程，使嵌入资源更新。

测试默认使用本地模拟服务，不需要真实 Consul 或业务后端。手动联调可设置 `XUANDU_MCP_VERIFY_CONFIG`、`XUANDU_MCP_VERIFY_APP` 和 `XUANDU_MCP_VERIFY_OPERATION` 后执行 `go test ./internal/mcp -run '^TestLiveOperation$' -v`；所选操作会被真实调用，应使用无副作用、可接受空参数的接口。

## GitHub Actions 发布

推送 `v*` 标签或手动运行 [xuandu-pipeline](.github/workflows/xuandu-pipeline.yml)。工作流构建一次前端，校验 Wire、测试 Go 代码并编译 Linux amd64 / arm64 二进制，再由原生 runner 打包、推送镜像及合并多架构 manifest，不自动部署集群。

在仓库 Actions **Secrets** 中配置：

| Secret | 值 |
| --- | --- |
| `REGISTRY` | 镜像仓主机，不含协议和路径 |
| `IMAGE` | 包含仓库主机和路径的完整镜像名，不含标签 |
| `REGISTRY_USERNAME` | 推送用户名 |
| `REGISTRY_PASSWORD` | 推送密码或访问令牌 |

发布 `v0.0.2` 会生成架构标签和多架构 `v0.0.2`，正式 `v数字.数字.数字` 标签更新 `latest`。从分支手动运行使用 `sha-短SHA-runId-attempt`，预发布标签不更新 `latest`。需要仓库可使用 `ubuntu-24.04-arm` runner，推送账号具有目标路径的推送和标签覆盖权限。

镜像地址不写入源码或发布摘要，Docker 构建记录制品和构建摘要已关闭。凭据只能放入 Secrets，不要提交本地配置、Token 文件或 `.env`。

## 许可证

本项目采用 [MIT License](LICENSE)。前端中保留的上游代码遵循其原有 [MIT 声明](web/LICENSE)；第三方依赖的版权和许可证仍归各自作者所有。
