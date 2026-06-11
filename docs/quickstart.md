# 快速开始

## 1. 本地启动

单上游简单测试：

```bash
cp config.example.yaml config.yaml
# 按现场修改 config.yaml：上游地址、前端 key、route、endpoint
go mod tidy
go run ./cmd/sbgw
```

多模型 / 多上游 / thinking-direct 拆分测试：

```bash
cp config.multi-model.example.yaml config.yaml
# 修改 qwen36-a/qwen36-b/qwen35-a 的 base_url、api_key，以及 auth.keys
go mod tidy
go run ./cmd/sbgw
```

健康检查：

```bash
curl http://127.0.0.1:12224/healthz
```

## 2. 标准 OpenAI-compatible 入口

```bash
curl http://127.0.0.1:12224/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-demo-user-001" \
  -d '{
    "model": "qwen3.6",
    "messages": [{"role":"user","content":"你好"}],
    "stream": false
  }'
```

这个入口主要靠 `model` 字段选择网关逻辑模型。比如 `qwen3.6` 会通过 `upstream.model_map` 改写成上游真实模型 `qwen3.6-27b-w8a8`。

## 3. route 前缀入口

推荐入口：

```text
/{route}/v1/chat/completions
```

示例：

```bash
curl http://127.0.0.1:12224/qwen36-direct/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-demo-user-001" \
  -d '{
    "model": "anything",
    "messages": [{"role":"user","content":"直接回答"}],
    "stream": false
  }'
```

命中 route 后，网关以 route 配置为准：

- `route.model` 决定对外逻辑模型。
- `route.upstream_model` 决定上游真实模型。
- `route.endpoints` 决定可用上游范围。
- `route.request_patches` 决定是否开启或关闭 thinking。

## 4. OpenAI SDK 接入方式

route 前缀的好处是 SDK 不需要改 path，只需要换 `base_url`。

thinking 模式：

```text
base_url = http://gateway:30088/qwen36-think/v1
```

非思考模式：

```text
base_url = http://gateway:30088/qwen36-direct/v1
```

SDK 内部仍然请求：

```text
/chat/completions
/models
```

## 5. 查看模型

全局模型列表：

```bash
curl http://127.0.0.1:12224/v1/models \
  -H "Authorization: Bearer sk-demo-user-001"
```

当前 route 模型列表：

```bash
curl http://127.0.0.1:12224/qwen36-direct/v1/models \
  -H "Authorization: Bearer sk-demo-user-001"
```

## 6. 查看额度

```bash
curl http://127.0.0.1:12224/v1/usage \
  -H "Authorization: Bearer sk-demo-user-001"
```

当前 quota 是进程内存统计，单副本网关可直接使用；多副本全局额度建议后续接 Redis 或数据库。

## 7. 离线安装最终多模型示例

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

查看帮助：

```bash
./sbgw-v0.1.0-linux-amd64.run -h
./sbgw-v0.1.0-linux-amd64.run install -h
```

## 8. 测试脚本

```bash
bash scripts/test-nonstream.sh
bash scripts/test-stream.sh
```

指定 route 前缀：

```bash
ROUTE_PREFIX=/qwen36-direct MODEL=anything bash scripts/test-nonstream.sh
ROUTE_PREFIX=/qwen36-think MODEL=anything bash scripts/test-stream.sh
```
