# sbgw

`sbgw` 是一个 Go + Gin 实现的 OpenAI-compatible Chat Gateway，面向 Qwen3.5/Qwen3.6/DeepSeek 等 OpenAI 兼容模型服务。它的目标不是重新定义协议，而是在一个统一公网端口上完成 **鉴权、模型映射、thinking/direct 变体拆分、多上游负载、日志与返回格式兼容**。

## 适用场景

- 前端/业务系统只接一个网关地址，但后面挂多个 vLLM、SGLang、DashScope、OpenAI-compatible 服务。
- 同一个真实模型需要拆成两个对外模型：`thinking` 模式和 `direct` 非思考模式。
- Qwen3.5/Qwen3.6 等模型要求 `system` message 必须在最前面，需要网关统一修复历史消息顺序。
- 需要把前端用户 API key 和上游模型 API key 分离，避免把用户 key 透传给后端模型服务。
- 需要离线 `.run` 包、Kubernetes NodePort、GitHub Actions 多架构构建和 Release。

## 核心能力

1. OpenAI-compatible `/v1/chat/completions` 代理。
2. 推荐路由形态：`/{route}/v1/chat/completions`，方便 OpenAI SDK 把 `base_url` 设置成 `http://host:port/{route}/v1`。
3. 兼容旧路由：`/v1/{route}/chat/completions`。
4. `/v1/models` 暴露网关逻辑模型名；`/{route}/v1/models` 只暴露当前 route 的模型。
5. `/v1/usage` 查看客户端 key 的 token 使用量和额度。
6. 双层 API key：网关客户端 key 与上游模型 key 完全隔离。
7. 多客户端 key，并可为每个 key 配置 `quota_tokens`。
8. 多 upstream endpoint，支持 `round_robin`、`weighted_round_robin`、`random`、`weighted_random`、`least_inflight`。
9. model 映射：前端传 `qwen3.6`，网关转发给上游真实 `qwen3.6-27b-w8a8`。
10. route 级别 request patch，可配置 `chat_template_kwargs.enable_thinking`、`enable_thinking`、`extra_body.enable_thinking` 等不同框架参数。
11. 自动把后置 `system` message 移到 `messages` 最前面，适配 Qwen3.5/Qwen3.6 严格校验。
12. 兼容 `reasoning`、`reasoning_content`、`delta.reasoning`、`delta.reasoning_content`。
13. 把思考过程统一归集到 `content` 里的真实 `<think>...</think>`，不会输出 `\u003cthink\u003e` 这种 HTML escape 文本。
14. 保守透传：除被消费的 reasoning 字段外，其他字段不解释、不重组、不删除。
15. 兼容上游已经把 `<think>...</think>` 混在 `content` 里的情况。
16. 支持流式 SSE 转换，并按 `choices[].index` 分开维护状态。
17. 结构化 JSON 日志，包含 `request_id`、`client`、`route`、`model`、`upstream`、`strategy`、`latency`、`inflight`、`token usage` 等字段。
18. 支持 Docker、Kubernetes NodePort/LoadBalancer/ClusterIP、GitHub Actions 多架构构建和离线 `.run` 包。

## 路由设计

`sbgw` 同时支持三类入口：

| 入口 | 用途 | 推荐程度 |
|---|---|---|
| `/v1/chat/completions` | 标准 OpenAI-compatible 入口，通过 `model` 字段选择网关模型 | 常规兼容 |
| `/{route}/v1/chat/completions` | route 前缀入口，例如 `/qwen36-direct/v1/chat/completions` | 推荐 |
| `/v1/{route}/chat/completions` | 旧版 route 入口 | 兼容保留 |

推荐把不同模型变体配置成不同 `base_url`：

```text
http://gateway:30088/qwen36-think/v1
http://gateway:30088/qwen36-direct/v1
http://gateway:30088/qwen35-think/v1
http://gateway:30088/qwen35-direct/v1
```

SDK 侧仍然请求标准路径：

```text
/chat/completions
/models
```

## 快速启动

```bash
cp config.example.yaml config.yaml
# 修改 upstream.endpoints / upstream.routes / auth.keys
go run ./cmd/sbgw
```

标准入口：

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

route 前缀入口：

```bash
curl http://127.0.0.1:12224/qwen36-direct/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-demo-user-001" \
  -d '{
    "model": "anything",
    "messages": [{"role":"user","content":"直接回答，不要展开推理"}],
    "stream": false
  }'
```

命中 `qwen36-direct` route 后，网关会自动：

1. 对外逻辑模型固定为 `qwen3.6-direct`。
2. 上游真实模型改写为 `qwen3.6-27b-w8a8`。
3. 注入配置中的请求 patch，例如 `chat_template_kwargs.enable_thinking=false`。
4. 只在该 route 允许的 endpoints 中做负载均衡。

查看模型和额度：

```bash
curl http://127.0.0.1:12224/v1/models \
  -H "Authorization: Bearer sk-demo-user-001"

curl http://127.0.0.1:12224/qwen36-direct/v1/models \
  -H "Authorization: Bearer sk-demo-user-001"

curl http://127.0.0.1:12224/v1/usage \
  -H "Authorization: Bearer sk-demo-user-001"
```

测试脚本：

```bash
bash scripts/test-nonstream.sh
bash scripts/test-stream.sh

ROUTE_PREFIX=/qwen36-direct MODEL=anything bash scripts/test-nonstream.sh
ROUTE_PREFIX=/qwen36-think MODEL=anything bash scripts/test-stream.sh
```

## 配置示例

完整示例见 [`config.example.yaml`](config.example.yaml)，最终多模型示例见 [`config.multi-model.example.yaml`](config.multi-model.example.yaml)。核心结构如下：

```yaml
server:
  addr: ":12224"

# 第一层 key：调用网关的用户/前端/业务系统使用。
auth:
  enabled: true
  header: "Authorization"
  keys:
    - name: "demo-user"
      key: "sk-demo-user-001"
      quota_tokens: 1000000

# 第二层 key：上游模型服务需要鉴权时才填写。
upstream:
  forward_client_authorization: false
  strategy: "weighted_round_robin"

  model_map:
    qwen3.6: "qwen3.6-27b-w8a8"
    qwen3.5: "qwen3.5-32b-w8a8"

  routes:
    - name: "qwen36-direct"
      path: "/qwen36-direct"
      model: "qwen3.6-direct"
      upstream_model: "qwen3.6-27b-w8a8"
      upstream_path: "/v1/chat/completions"
      endpoints: ["qwen36-a", "qwen36-b"]
      request_patches:
        - op: set
          path: "chat_template_kwargs.enable_thinking"
          value: false

  endpoints:
    - name: "qwen36-a"
      base_url: "http://127.0.0.1:18489"
      api_key: ""
      weight: 2
      timeout: "10m"
      models: ["qwen3.6", "qwen3.6-thinking", "qwen3.6-direct"]
```

## thinking/direct 模式

`enable_thinking` 不是 OpenAI Chat Completions 标准参数，不同推理框架传法不一致，所以 `sbgw` 不写死模型逻辑，而是通过 route 的 `request_patches` 实现。

常见写法：

```yaml
# vLLM / SGLang 常见写法
request_patches:
  - op: set
    path: "chat_template_kwargs.enable_thinking"
    value: false

# DashScope / Model Studio 兼容接口常见写法
request_patches:
  - op: set
    path: "enable_thinking"
    value: false

# 某些 SDK 或代理层常见写法
request_patches:
  - op: set
    path: "extra_body.enable_thinking"
    value: false
```

需要删除客户端传来的字段时：

```yaml
request_patches:
  - op: delete
    path: "enable_thinking"
```

## system message 位置修复

Qwen3.5/Qwen3.6 对 `messages` 顺序更严格，`system` 必须在最前面。多轮二次提问时，业务侧历史消息经常会把 system message 插到中间，导致上游报错。

开启：

```yaml
transform:
  reorder_system_messages: true
```

网关会把所有 `role=system` 的消息稳定移动到 `messages` 前面，其他消息保持原始相对顺序不变。

## 最终多模型用法

多模型、多上游、thinking/direct 拆分不要堆很多命令行参数，推荐用完整配置文件。离线 `.run` 包现在内置了 `example-config` 动作，直接生成最终示例：

```bash
./sbgw-v0.1.0-linux-amd64.run example-config > config.multi-model.yaml
vi config.multi-model.yaml

./sbgw-v0.1.0-linux-amd64.run install \
  --registry sealos.hub:5000/kube4 \
  --config-file ./config.multi-model.yaml \
  --service-type NodePort \
  --node-port 30088 \
  -n aict -y
```

`-h`/`install -h` 里也能看到这套推荐用法：

```bash
./sbgw-v0.1.0-linux-amd64.run -h
./sbgw-v0.1.0-linux-amd64.run install -h
```

源码仓库里也提供同样的示例文件：

```text
config.multi-model.example.yaml
```

## Kubernetes 离线安装

默认 Service 为 `NodePort`，默认端口 `30088`。

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

复杂多 route、多 upstream、thinking/direct 规则建议直接使用完整配置文件：

```bash
cp config.example.yaml config-prod.yaml
# 修改 config-prod.yaml 中的 upstream.routes / endpoints / auth
./sbgw-v0.1.0-linux-amd64.run install \
  --registry sealos.hub:5000/kube4 \
  --config-file ./config-prod.yaml \
  --service-type NodePort \
  --node-port 30088 \
  -n aict -y
```

## GitHub Actions Release 修复

Release 最后一步以前使用 `actions/download-artifact@v4` 时没有指定过滤条件，所以会下载所有 artifact。`docker/build-push-action@v6` 可能生成 `.dockerbuild` 构建记录 artifact，导致发布阶段把非交付产物也拉到 `dist`。

当前 workflow 做了双保险：

```yaml
env:
  DOCKER_BUILD_RECORD_UPLOAD: "false"

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

## 文档索引

- [快速开始](docs/quickstart.md)
- [配置说明](docs/configuration.md)
- [route 前缀与 thinking/direct 设计](docs/routes-thinking.md)
- [Kubernetes 与离线安装](docs/kubernetes-install.md)
- [GitHub Actions 与 Release](docs/github-actions-release.md)
- [日志、额度与排障](docs/operations.md)

## 构建与测试

```bash
go mod tidy
go test ./...
go build -o sbgw ./cmd/sbgw

bash build.sh --arch amd64 --version v0.1.0
bash build.sh --arch arm64 --version v0.1.0
```
