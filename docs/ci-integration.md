# ZIP 契约发布

1. 先部署支持新 RPC 的后端并等待 Ready。
2. 只把 `idl/` 目录打包成 ZIP，里面可以有多个 Thrift 文件、公共类型和子目录。
3. 上传到网关可访问的 HTTP(S) 下载地址；链接仅需在导入期间有效。
4. 调用同一管理更新接口；不要直接修改 Consul KV。

```sh
zip -r contract-v2.zip idl/ -i '*.thrift'
curl --fail-with-body --max-time 130 \
  -X POST 'http://xuandu-console:9090/admin/apps/product/update' \
  -H 'Content-Type: application/json' \
  -d '{"idl":{"type":"zip","url":"https://artifacts.example.com/product/contract-v2.zip"}}'
```

网关验证整个 ZIP 和所有 IDL 后，将契约分块存入 Consul，再保存 SHA-256 与应用配置并发布 Runtime。
下载 URL 不持久化；其他副本和重启恢复读取已保存契约并核对摘要。

- 200：更新成功，changed=false 表示 ZIP 字节摘要和配置均未变化。
- 409：应用内路由、域名或元数据版本冲突，检查状态后重新提交。
- 422：下载、ZIP、文件路径、IDL 或 Runtime 构建失败；保留旧版本。
- 超时不能证明事务没提交，应 GET 应用检查 resolvedRevision。

已有应用若缺少域名，可在更新 JSON 中加入 `"domain":"product.example.com"`；HTTP 调用须携带匹配的 Host 与 X-App-Code，MCP 仅校验应用域名和其独立 Key。

无 IDL 变化的业务发版无需网关更新。网关不管理代码仓库或仓库权限。
