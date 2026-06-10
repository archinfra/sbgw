# sbgw

`sbgw` 是一个 Go + Gin 实现的 OpenAI-compatible Chat Gateway，面向 Qwen3.5/Qwen3.6/DeepSeek 等 OpenAI 兼容模型服务。

## 核心能力

1. 代理 `/v1/chat/completions` 到一个或多个上游模型服务。
2. 支持 `/v1/models`，前端只暴露网关定义的逻辑模型名。
3. 支持 `/v1/usage`，可查看当前客户端 key 的 token 额度使用情况。
4. 兼容 `reasoning`、`reasoning_content`、`delta.reasoning`、`delta.reasoning_content`。
5. 把思考过程统一归集到 `content` 里的真实 `<think>...</think>`，不会再输出 `\u003cthink\u003e` 这种 HTML escape 文本。
6. 保守透传：除被消费的 reasoning 字段外，其他字段不解释、不重组、不删除。
7. 兼容上游已经把 `<think>...</think>` 混在 `content` 里的情况。
8. 支持流式 SSE 转换，并按 `choices[].index` 分开维护状态。
9. 支持网关客户端 API key 和上游 API key 两层隔离。
10. 支持多个客户端 API key，并可为每个 key 配置 token 额度。
11. 支持多个 upstream endpoint：`round_robin`、`weighted_round_robin`、`random`、`weighted_random`、`least_inflight`。
12. 支持 model 映射：前端传 `qwen3.6`，网关转发给上游真实 `qwen3.6-27b-w8a8`。
13. 自动修复 Qwen3.5/Qwen3.6 严格要求的 system message 位置：后置 system 会被稳定移动到第一段消息区。
14. 输出结构化 JSON 日志，包含 request_id、client、model、upstream、strategy、latency、inflight、token usage 等字段。
15. 支持 Docker、Kubernetes NodePort/LoadBalancer/ClusterIP、GitHub Actions 多架构构建和离线 `.run` 包。

## 两层 API key 设计

`sbgw` 把 API key 分成两层：

- **网关客户端 key**：前端、业务系统、用户调用 `sbgw` 时使用，配置在 `auth.tokens` 或 `auth.keys`。
- **上游模型 key**：只有上游模型服务本身需要鉴权时才填写，配置在 `upstream.api_key` 或 `upstream.endpoints[].api_key`。

默认 `forward_client_authorization: false`，所以客户端的 `Authorization: Bearer sk-xxx` 不会泄露给上游。上游需要 key 时，填写上游自己的 key；上游不需要 key 时，留空即可。

## 配置示例

```yaml
server:
  addr: ":12224"
  mode: "release"

log:
  level: "info"
  format: "json"
  log_body: true
  log_headers: false
  max_body_size: 8192
  redact_headers:
    - authorization
    - x-api-key
    - api-key

auth:
  enabled: true
  header: "Authorization"
  tokens:
    - "sk-local-dev-001"
  keys:
    - name: "demo-user"
      key: "sk-demo-user-001"
      quota_tokens: 1000000
      disabled: false

upstream:
  base_url: "http://127.0.0.1:18489"
  timeout: "10m"
  api_key: ""
  forward_client_authorization: false
  strategy: "weighted_round_robin"

  model_map:
    qwen3.6: "qwen3.6-27b-w8a8"
    qwen3.5: "qwen3.5-32b-w8a8"

  endpoints:
    - name: "qwen-a"
      base_url: "http://127.0.0.1:18489"
      api_key: ""
      weight: 2
      timeout: "10m"
      models: ["qwen3.6", "qwen3.5"]
    - name: "qwen-b"
      base_url: "http://127.0.0.1:18490"
      api_key: ""
      weight: 1
      timeout: "10m"
      models: ["qwen3.6"]

transform:
  enabled: true
  inject_think_tag: true
  strip_reasoning_fields: true
  parse_think_from_content: true
  reorder_system_messages: true
  reasoning_fields:
    - reasoning_content
    - reasoning
```

## 负载策略

| 策略 | 说明 |
|---|---|
| `round_robin` | 普通轮询 |
| `weighted_round_robin` | 按 `weight` 比例轮询，默认策略 |
| `random` | 普通随机 |
| `weighted_random` | 按 `weight` 比例随机 |
| `least_inflight` | 动态选择当前并发请求数最少的上游 |

`endpoints[].models` 用的是网关逻辑模型名，不是上游真实模型名。为空表示该上游兜底支持所有模型。

## system message 位置修复

Qwen3.5/Qwen3.6 对 `messages` 顺序更严格，`system` 必须在最前面。多轮二次提问时，业务侧历史消息经常会把 system message 插到中间，导致上游报错。

开启：

```yaml
transform:
  reorder_system_messages: true
```

网关会把所有 `role=system` 的消息稳定移动到 `messages` 前面，其他消息保持原始相对顺序不变。

## 启动与测试

```bash
cp config.example.yaml config.yaml
# 修改 upstream.base_url 或 endpoints
go run ./cmd/sbgw
```

```bash
curl http://127.0.0.1:12224/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-demo-user-001" \
  -d '{
    "model": "qwen3.6",
    "messages": [
      {"role":"user","content":"你好"},
      {"role":"system","content":"你是一个严谨助手"}
    ],
    "stream": false
  }'
```

查看模型：

```bash
curl http://127.0.0.1:12224/v1/models \
  -H "Authorization: Bearer sk-demo-user-001"
```

查看额度：

```bash
curl http://127.0.0.1:12224/v1/usage \
  -H "Authorization: Bearer sk-demo-user-001"
```

## Kubernetes 离线安装

默认 Service 已改为 `NodePort`，默认端口 `30088`。

```bash
./sbgw-v0.1.0-linux-amd64.run install \
  --registry sealos.hub:5000/kube4 \
  --upstream-base-url http://qwen-vllm.aict.svc:8000 \
  --upstream-api-key sk-upstream-xxx \
  --auth-key user-a:sk-user-a:1000000 \
  --auth-key user-b:sk-user-b:500000 \
  --service-type NodePort \
  --node-port 30088 \
  -n aict -y
```

上游不需要 key 时：

```bash
./sbgw-v0.1.0-linux-amd64.run install \
  --upstream-base-url http://qwen-vllm.aict.svc:8000 \
  --auth-token sk-prod-xxx \
  -n aict -y
```

## GitHub Actions 修复说明

Release 最后一步以前使用 `actions/download-artifact@v4` 时没有指定过滤条件，所以会下载所有 artifact。`docker/build-push-action@v6` 会生成 `.dockerbuild` 构建记录 artifact，导致最后发布步骤把非交付产物也拉到 `dist`，进而出现下载或解压失败。

现在做了双保险：

```yaml
env:
  DOCKER_BUILD_RECORD_UPLOAD: "false"
```

并且 Release 阶段只下载真正的 `.run` 产物：

```yaml
- uses: actions/download-artifact@v4
  with:
    pattern: sbgw-run-*
    path: dist
    merge-multiple: true
```

最终 Release 只上传：

```text
dist/*.run
dist/*.sha256
```
