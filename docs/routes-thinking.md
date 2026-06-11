# route 前缀与 thinking/direct 设计

## 目标

一个网关只开放一个端口，例如 `30088`，通过不同 subpath 接入不同模型或同一模型的不同变体：

```text
/qwen36-think/v1/chat/completions
/qwen36-direct/v1/chat/completions
/qwen35-think/v1/chat/completions
/qwen35-direct/v1/chat/completions
```

这样前端、SDK、Agent 平台可以把不同能力当成不同 base_url 使用，而不需要暴露后端真实端口和真实模型名。

## 推荐路径

推荐路径：

```text
/{route}/v1/chat/completions
```

示例：

```text
/qwen36-direct/v1/chat/completions
```

这个形态对 OpenAI SDK 最友好：

```text
base_url = http://gateway:30088/qwen36-direct/v1
```

SDK 仍然请求标准的：

```text
/chat/completions
```

## 兼容路径

旧路径仍然保留：

```text
/v1/{route}/chat/completions
```

示例：

```text
/v1/qwen36-direct/chat/completions
```

它主要用于兼容已经接入上一版路径的客户端。新接入建议统一使用 `/{route}/v1/...`。

## route 命中规则

请求进入网关后，按以下顺序判断 route：

1. URL 中有 `route` 参数时，优先按 route path 命中，例如 `/qwen36-direct/v1/...`。
2. URL 中没有 route 参数时，按请求体 `model` 字段匹配 `routes[].model`。
3. 如果没有命中 route，则走普通 `model_map` 和 endpoint 负载逻辑。

## thinking/direct 拆分

同一个真实模型可以拆成两个对外模型：

```yaml
routes:
  - name: "qwen36-think"
    path: "/qwen36-think"
    model: "qwen3.6-thinking"
    upstream_model: "qwen3.6-27b-w8a8"
    endpoints: ["qwen36-a", "qwen36-b"]
    request_patches:
      - op: set
        path: "chat_template_kwargs.enable_thinking"
        value: true

  - name: "qwen36-direct"
    path: "/qwen36-direct"
    model: "qwen3.6-direct"
    upstream_model: "qwen3.6-27b-w8a8"
    endpoints: ["qwen36-a", "qwen36-b"]
    request_patches:
      - op: set
        path: "chat_template_kwargs.enable_thinking"
        value: false
```

前端只需要知道网关 route：

```bash
curl http://gateway:30088/qwen36-direct/v1/chat/completions \
  -H "Authorization: Bearer sk-user" \
  -H "Content-Type: application/json" \
  -d '{"model":"anything","messages":[{"role":"user","content":"直接回答"}]}'
```

网关会负责把请求改成上游需要的真实模型和真实参数。

## 为什么使用 request patch

`enable_thinking` 不是 OpenAI 标准字段，不同推理框架参数位置可能不同。网关只提供通用 patch 能力，不把 Qwen、vLLM、DashScope 逻辑写死到代码里。

常见配置：

```yaml
# vLLM / SGLang 常见写法
request_patches:
  - op: set
    path: "chat_template_kwargs.enable_thinking"
    value: false
```

```yaml
# DashScope / Model Studio 兼容接口常见写法
request_patches:
  - op: set
    path: "enable_thinking"
    value: false
```

```yaml
# 某些 SDK 或代理层常见写法
request_patches:
  - op: set
    path: "extra_body.enable_thinking"
    value: false
```

如果客户端可能自己传了不希望上游看到的字段，可以删除：

```yaml
request_patches:
  - op: delete
    path: "enable_thinking"
```

## system message 顺序修复

多轮对话里常见错误：

```json
{
  "messages": [
    {"role":"user","content":"上一轮问题"},
    {"role":"assistant","content":"上一轮回答"},
    {"role":"system","content":"你是一个严谨助手"},
    {"role":"user","content":"继续"}
  ]
}
```

开启：

```yaml
transform:
  reorder_system_messages: true
```

网关会变成：

```json
{
  "messages": [
    {"role":"system","content":"你是一个严谨助手"},
    {"role":"user","content":"上一轮问题"},
    {"role":"assistant","content":"上一轮回答"},
    {"role":"user","content":"继续"}
  ]
}
```

这解决 Qwen3.5/Qwen3.6 对 system 位置严格校验的问题。
