# 配置说明

`sbgw` 默认读取以下位置的 `config.yaml`：

1. 当前目录
2. `./configs`
3. `/etc/sbgw`

也可以通过环境变量覆盖，前缀为 `SBGW_`，例如 `SBGW_SERVER_ADDR=:12224`。

最终多模型示例文件：

```text
config.multi-model.example.yaml
```

离线 `.run` 包也可以直接打印同款示例：

```bash
./sbgw-v0.1.0-linux-amd64.run example-config > config.multi-model.yaml
```

## server

```yaml
server:
  addr: ":12224"
  mode: "release"
```

- `addr`：监听地址。
- `mode`：Gin 运行模式，生产建议 `release`。

## log

```yaml
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
```

- `log_body`：是否记录请求/响应 body。生产环境可以打开，但建议配合 `max_body_size`。
- `log_headers`：是否记录 headers。默认关闭。
- `redact_headers`：需要脱敏的 header 名称。

## auth：网关客户端 key

这是第一层 API key，给前端、业务系统、用户使用。

```yaml
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
```

- `tokens`：兼容旧配置，只校验，不限额。
- `keys`：新配置，支持命名、禁用、额度。
- `quota_tokens <= 0` 表示不限额。
- `header` 默认为 `Authorization`，支持 `Bearer sk-xxx`。

## upstream：上游模型服务

这是第二层 API key，只有上游模型服务需要鉴权时才填写。

```yaml
upstream:
  base_url: "http://127.0.0.1:18489"
  timeout: "30m"
  api_key: ""
  forward_client_authorization: false
  strategy: "weighted_round_robin"
```

- `api_key`：默认上游 key。
- `forward_client_authorization`：是否把客户端 Authorization 透传给上游。默认 `false`，避免泄露前端 key。
- `strategy`：负载策略。

## model_map

```yaml
model_map:
  qwen3.6: "qwen3.6-27b-w8a8"
  qwen3.5: "qwen3.5-32b-w8a8"
```

标准 `/v1/chat/completions` 入口会按这个表把网关逻辑模型改写成上游真实模型。

## endpoints

```yaml
endpoints:
  - name: "qwen36-a"
    base_url: "http://127.0.0.1:18489"
    api_key: ""
    weight: 2
    timeout: "30m"
    models: ["qwen3.6", "qwen3.6-thinking", "qwen3.6-direct"]
```

- `name`：endpoint 名称，被 route 引用。
- `base_url`：上游 OpenAI-compatible base URL，不要带 `/v1/chat/completions`。
- `api_key`：当前 endpoint 专属上游 key，优先级高于 `upstream.api_key`。
- `weight`：权重，供 weighted 策略使用。
- `timeout`：当前 endpoint 超时。
- `models`：支持的网关逻辑模型名。为空表示兜底支持所有模型。

## routes

```yaml
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
```

字段说明：

- `name`：route 名称，用于日志和识别。
- `path`：公网 subpath，只允许单段路径，例如 `/qwen36-direct`。
- `model`：网关对外模型名。
- `upstream_model`：转发给上游的真实模型名。
- `upstream_path`：上游接口路径，默认 `/v1/chat/completions`。
- `endpoints`：当前 route 可以使用的 endpoint 列表。为空时按全局 endpoint 选择。
- `request_patches`：请求体 patch 列表。

## request_patches

支持两种操作：

```yaml
request_patches:
  - op: set
    path: "chat_template_kwargs.enable_thinking"
    value: false

  - op: delete
    path: "enable_thinking"
```

- `set`：设置字段，不存在的中间对象会自动创建。
- `delete`：删除字段。
- `path`：点分路径，例如 `a.b.c`。

## transform

```yaml
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

- `inject_think_tag`：把 reasoning 内容放进 `<think>...</think>`。
- `strip_reasoning_fields`：删除上游返回中的 reasoning 字段，避免前端重复解析。
- `parse_think_from_content`：识别上游 content 里已有的 `<think>`。
- `reorder_system_messages`：把后置 system message 移到 messages 最前面。
- `reasoning_fields`：需要消费为思考内容的字段名。
