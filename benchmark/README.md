# 性能基线

在原生 linux/amd64 机器执行。当前开发宿主和 Docker 引擎都是 ARM64，
模拟运行 amd64 不能作为生产性能基线。

```sh
go version
go list -m github.com/cloudwego/kitex github.com/cloudwego/dynamicgo
lscpu
go test ./internal/router -bench . -benchmem -run '^$' -count 5
```

真实负载测试工具：

```sh
go run ./benchmark/load -url http://127.0.0.1:8080/api/product/detail \
  -host product.example.com -app-code product \
  -body '{"name":"benchmark"}' -duration 60s -concurrency 32
```

分别采集三组：稳定流量；负载期间发布新契约；负载期间替换 Consul 后端实例。
同时对不参与更新的另一应用压测。使用 `pidstat -p <gateway-pid> 1` 记录 CPU/RSS，
记录 PID、版本、配置与请求体。更新期间现有接口和另一应用不能出现错误尖峰。

按 result-template.md 留存原始数据。本地 Match microbenchmark 只反映路由匹配，
不等同于网关端到端 QPS；Go 1.27.1 下 DynamicGo 的 fast path 未启用。
