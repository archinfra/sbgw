# 日志、额度与排障

## 健康检查

```bash
curl http://127.0.0.1:12224/healthz
```

Kubernetes：

```bash
kubectl -n aict get pod,svc -l app.kubernetes.io/name=sbgw
kubectl -n aict logs deploy/sbgw -f
```

## 日志字段

`sbgw` 默认输出 JSON 日志。关键字段：

- `request_id`：请求 ID，会透传到上游 `X-Request-ID`。
- `client`：命中的网关客户端 key 名称。
- `route` / `route_path`：命中的 route。
- `model`：网关逻辑模型。
- `upstream_model`：上游真实模型。
- `upstream` / `upstream_base_url`：实际选择的 endpoint。
- `strategy`：负载策略。
- `stream`：是否流式请求。
- `latency`：耗时。
- `inflight`：当前 endpoint 并发数。
- `total_tokens`：从上游 usage 中提取的 token 数。

## Header 脱敏

默认不记录 headers。如果开启：

```yaml
log:
  log_headers: true
  redact_headers:
    - authorization
    - x-api-key
    - api-key
```

上述 header 会被脱敏，避免日志泄露 key。

## token 额度

配置：

```yaml
auth:
  keys:
    - name: "demo-user"
      key: "sk-demo-user-001"
      quota_tokens: 1000000
```

查询：

```bash
curl http://127.0.0.1:12224/v1/usage \
  -H "Authorization: Bearer sk-demo-user-001"
```

当前额度统计依赖上游返回的 `usage.total_tokens`。如果上游不返回 usage，则不会扣减。

当前实现是进程内存额度，适合单副本；多副本全局额度建议后续接 Redis 或数据库。

## system message 报错

现象：上游模型报错，提示 system message 位置不合法，或者要求 system message 是第一条。

处理：

```yaml
transform:
  reorder_system_messages: true
```

开启后，网关会把所有 `role=system` 的消息移动到最前面，其他消息相对顺序保持不变。

## thinking 没关闭

先确认当前 route 的 patch 是否匹配你的推理框架。

vLLM/SGLang 常见：

```yaml
request_patches:
  - op: set
    path: "chat_template_kwargs.enable_thinking"
    value: false
```

DashScope/Model Studio 常见：

```yaml
request_patches:
  - op: set
    path: "enable_thinking"
    value: false
```

如果客户端自己传了相反参数，可以先删除再设置：

```yaml
request_patches:
  - op: delete
    path: "enable_thinking"
  - op: set
    path: "chat_template_kwargs.enable_thinking"
    value: false
```

## 没有可用 upstream

报错：

```text
no upstream endpoint supports requested model
```

检查：

1. route 的 `model` 是否在 endpoint 的 `models` 列表里。
2. endpoint 的 `models` 是否为空。为空表示兜底支持所有模型。
3. route 的 `endpoints` 是否引用了不存在的 endpoint 名称。
4. 标准入口 `/v1/chat/completions` 的 `model` 是否存在于 `model_map` 或 endpoint 支持列表。

## 上游 key 与前端 key 混淆

推荐保持：

```yaml
upstream:
  forward_client_authorization: false
```

这样网关会删除客户端 `Authorization`，只把 `upstream.api_key` 或 `endpoints[].api_key` 作为上游 Authorization。上游不需要 key 就留空。
