# sbgw

`sbgw` 是一个 Go + Gin 实现的 OpenAI-compatible Gateway，面向 Qwen3.5/Qwen3.6、DeepSeek V4、MiMo ASR 等 OpenAI 兼容模型服务。它的目标不是重新定义协议，而是在一个统一公网端口上完成 **鉴权、模型映射、thinking/direct 变体拆分、多上游负载、音频转写代理、日志与返回格式兼容**。

## 适用场景

- 前端/业务系统只接一个网关地址，但后面挂多个 vLLM、SGLang、DashScope、DeepSeek、MiMo ASR 或其他 OpenAI-compatible 服务。
- 同一个真实模型需要拆成多个对外 route，例如 Qwen thinking/direct、DeepSeek V4、MiMo ASR。
- Qwen3.5/Qwen3.6 等模型要求 `system` message 必须在最前面，需要网关统一修复历史消息顺序。
- DeepSeek V4 等模型希望保守透传 OpenAI Chat 请求，同时兼容 `reasoning` / `reasoning_content` 返回。
- MiMo ASR 等语音识别服务使用 OpenAI-compatible `/v1/audio/transcriptions` multipart 接口，需要统一鉴权、模型名改写和上游隔离。
- 需要把前端用户 API key 和上游模型 API key 分离，避免把用户 key 透传给后端模型服务。
- 需要离线 `.run` 包、Kubernetes NodePort、GitHub Actions 多架构构建和 Release。

## 核心能力

1. OpenAI-compatible `/v1/chat/completions` 代理。
2. OpenAI-compatible `/v1/audio/transcriptions` 音频转写代理，支持 multipart 音频文件透传和 model 字段改写。
3. 推荐路由形态：`/{route}/v1/chat/completions`、`/{route}/v1/audio/transcriptions`，方便 OpenAI SDK 把 `base_url` 设置成 `http://host:port/{route}/v1`。
4. 兼容旧路由：`/v1/{route}/chat/completions`、`/v1/{route}/audio/transcriptions`。
5. `/v1/models` 暴露网关逻辑模型名；`/{route}/v1/models` 只暴露当前 route 的模型。
6. `/v1/usage` 查看客户端 key 的 token 使用量和额度。
7. 双层 API key：网关客户端 key 与上游模型 key 完全隔离。
8. 多客户端 key，并可为每个 key 配置 `quota_tokens`。
9. 多 upstream endpoint，支持 `round_robin`、`weighted_round_robin`、`random`、`weighted_random`、`least_inflight`。
10. model 映射：前端传 `qwen3.6`、`deepseek-v4`、`mimo-asr`，网关转发给上游真实模型名。
11. route 级别 request patch，可配置 `chat_template_kwargs.enable_thinking`、`enable_thinking`、`response_format` 等不同框架参数。
12. route 支持 `kind`：`chat` 或 `audio_transcription`，避免把 ASR route 误打到 Chat 接口。
13. route 支持 `adapter` 标签：`qwen`、`deepseek-v4`、`mimo-asr` 等，用于日志和配置归类。
14. 自动把后置 `system` message 移到 `messages` 最前面，适配 Qwen3.5/Qwen3.6 严格校验。
15. 兼容 `reasoning`、`reasoning_content`、`delta.reasoning`、`delta.reasoning_content`。
16. 把思考过程统一归集到 `content` 里的真实 `<think>...</think>`，不会输出 `\u003cthink\u003e` 这种 HTML escape 文本。
17. 保守透传：除被消费的 reasoning 字段外，其他字段不解释、不重组、不删除。
18. 兼容上游已经把 `<think>...</think>` 混在 `content` 里的情况。
19. 支持流式 SSE 转换，并按 `choices[].index` 分开维护状态。
20. 结构化 JSON 日志，包含 `request_id`、`client`、`route`、`route_kind`、`adapter`、`model`、`upstream`、`strategy`、`latency`、`inflight`、`token usage` 等字段。
21. 支持 Docker、Kubernetes NodePort/LoadBalancer/ClusterIP、GitHub Actions 多架构构建和离线 `.run` 包。

## 路由设计

`sbgw` 同时支持这些入口：

| 入口 | 用途 | 推荐程度 |
|---|---|---|
| `/v1/chat/completions` | 标准 Chat 入口，通过 `model` 字段选择网关模型 | 常规兼容 |
| `/{route}/v1/chat/completions` | Chat route 前缀入口，例如 `/deepseek-v4/v1/chat/completions` | 推荐 |
| `/v1/{route}/chat/completions` | 旧版 Chat route 入口 | 兼容保留 |
| `/v1/audio/transcriptions` | 标准 Audio Transcriptions 入口，通过 `model` 字段选择 ASR 模型 | 常规兼容 |
| `/{route}/v1/audio/transcriptions` | ASR route 前缀入口，例如 `/mimo-asr/v1/audio/transcriptions` | 推荐 |
| `/v1/{route}/audio/transcriptions` | 旧版 ASR route 入口 | 兼容保留 |

推荐把不同模型变体配置成不同 `base_url`：

```text
http://gateway:30088/qwen36-think/v1
http://gateway:30088/qwen36-direct/v1
http://gateway:30088/deepseek-v4/v1
http://gateway:30088/mimo-asr/v1
```

SDK 侧仍然请求标准路径：

```text
/chat/completions
/models
/audio/transcriptions
```

## 快速启动

```bash
cp config.example.yaml config.yaml
# 修改 upstream.endpoints / upstream.routes / auth.keys
go run ./cmd/sbgw
```

标准 Chat 入口：

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

route 前缀 Chat 入口：

```bash
curl http://127.0.0.1:12224/deepseek-v4/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-demo-user-001" \
  -d '{
    "model": "anything",
    "messages": [{"role":"user","content":"直接回答：你是什么模型？"}],
    "stream": false
  }'
```

route 前缀 MiMo ASR 入口：

```bash
curl http://127.0.0.1:12224/mimo-asr/v1/audio/transcriptions \
  -H "Authorization: Bearer sk-demo-user-001" \
  -F "model=mimo-asr" \
  -F "file=@./sample.wav" \
  -F "language=zh" \
  -F "response_format=json"
```

命中 `mimo-asr` route 后，网关会自动：

1. 校验这是 `kind: audio_transcription` route。
2. 重建 multipart/form-data 请求并保留音频文件。
3. 把表单里的 `model=mimo-asr` 改写成 `upstream_model`，例如 `mimo-audio-asr`。
4. 注入配置里的表单 patch，例如 `response_format=json`。
5. 只在该 route 允许的 endpoints 中做负载均衡。

查看模型和额度：

```bash
curl http://127.0.0.1:12224/v1/models \
  -H "Authorization: Bearer sk-demo-user-001"

curl http://127.0.0.1:12224/deepseek-v4/v1/models \
  -H "Authorization: Bearer sk-demo-user-001"

curl http://127.0.0.1:12224/mimo-asr/v1/models \
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
ROUTE_PREFIX=/deepseek-v4 MODEL=anything bash scripts/test-nonstream.sh
AUDIO_FILE=./sample.wav ROUTE_PREFIX=/mimo-asr MODEL=mimo-asr bash scripts/test-audio-transcription.sh
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
    deepseek-v4: "deepseek-v4-flash"
    mimo-asr: "mimo-audio-asr"

  routes:
    - name: "deepseek-v4"
      path: "/deepseek-v4"
      kind: "chat"
      adapter: "deepseek-v4"
      model: "deepseek-v4"
      upstream_model: "deepseek-v4-flash"
      upstream_path: "/v1/chat/completions"
      endpoints: ["deepseek-v4-a"]
      request_patches:
        - op: delete
          path: "chat_template_kwargs.enable_thinking"
        - op: delete
          path: "enable_thinking"

    - name: "mimo-asr"
      path: "/mimo-asr"
      kind: "audio_transcription"
      adapter: "mimo-asr"
      model: "mimo-asr"
      upstream_model: "mimo-audio-asr"
      upstream_path: "/v1/audio/transcriptions"
      endpoints: ["mimo-asr-a"]
      request_patches:
        - op: set
          path: "response_format"
          value: "json"

  endpoints:
    - name: "deepseek-v4-a"
      base_url: "http://127.0.0.1:18492"
      api_key: ""
      weight: 1
      timeout: "10m"
      models: ["deepseek-v4"]

    - name: "mimo-asr-a"
      base_url: "http://127.0.0.1:18493"
      api_key: ""
      weight: 1
      timeout: "30m"
      models: ["mimo-asr"]
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

## DeepSeek V4 适配

DeepSeek V4 / V4 Flash 这类 OpenAI-compatible Chat 模型通常不需要 Qwen 的 `enable_thinking` 参数。推荐配置独立 route，并删除可能由客户端误带来的 Qwen 专属字段：

```yaml
- name: "deepseek-v4"
  path: "/deepseek-v4"
  kind: "chat"
  adapter: "deepseek-v4"
  model: "deepseek-v4"
  upstream_model: "deepseek-v4-flash"
  endpoints: ["deepseek-v4-a"]
  request_patches:
    - op: delete
      path: "chat_template_kwargs.enable_thinking"
    - op: delete
      path: "enable_thinking"
```

返回侧无需写死 DeepSeek 逻辑：网关已经兼容 `reasoning`、`reasoning_content`、`delta.reasoning`、`delta.reasoning_content`，并会把推理内容归集到 `<think>...</think>`。

## MiMo ASR 适配

MiMo ASR 按 OpenAI-compatible Audio Transcriptions 接入：

```yaml
- name: "mimo-asr"
  path: "/mimo-asr"
  kind: "audio_transcription"
  adapter: "mimo-asr"
  model: "mimo-asr"
  upstream_model: "mimo-audio-asr"
  upstream_path: "/v1/audio/transcriptions"
  endpoints: ["mimo-asr-a"]
```

调用：

```bash
curl http://127.0.0.1:12224/mimo-asr/v1/audio/transcriptions \
  -H "Authorization: Bearer sk-demo-user-001" \
  -F "model=mimo-asr" \
  -F "file=@./sample.wav" \
  -F "language=zh" \
  -F "response_format=json"
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

`-h`/`install -h` 里也能看到这套推荐用法。
