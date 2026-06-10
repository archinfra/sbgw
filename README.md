# sbgw

`sbgw` 是一个 Go + Gin 实现的 OpenAI-compatible Chat Gateway。

核心能力：

1. 代理 `/v1/chat/completions` 到上游模型服务。
2. 兼容 `reasoning`、`reasoning_content`、`delta.reasoning`、`delta.reasoning_content`。
3. 把思考过程统一归集到 `content` 里的 `<think>...</think>`。
4. **保守透传**：除被消费的 reasoning 字段外，其他字段不解释、不重组、不删除。
5. 兼容上游已经把 `<think>...</think>` 混在 `content` 里的情况。
6. 支持流式 SSE 转换，并按 `choices[].index` 分开维护状态。
7. 支持 `sk-xxx` token 管理，默认不把网关 SK 泄露给上游。
8. 输出结构化 JSON 日志。
9. 支持 Docker 和 GitHub Actions 构建。

## 典型场景

有些上游，例如 vLLM 新版 Qwen3 reasoning parser，非流式可能返回：

```json
{
  "message": {
    "role": "assistant",
    "content": "最终回答",
    "reasoning": "思考过程"
  }
}
```

`sbgw` 只会消费 configured reasoning 字段，并转换成：

```json
{
  "message": {
    "role": "assistant",
    "content": "<think>\n思考过程\n</think>\n\n最终回答"
  }
}
```

有些上游已经直接在 `content` 中返回 `<think>...</think>`，`sbgw` 会保持兼容，并可在流式场景中过滤半截标签造成的前端显示问题。

## 透传原则

`sbgw` 的边界是：**只处理思考过程字段归集，不做 OpenAI 响应结构重写**。

会被处理的字段只来自配置：

```yaml
transform:
  reasoning_fields:
    - reasoning_content
    - reasoning
```

转换时：

- `choices[].message.reasoning_content` / `choices[].message.reasoning` 会被拼到 `message.content` 的 `<think>...</think>` 中。
- `choices[].delta.reasoning_content` / `choices[].delta.reasoning` 会被拼到 `delta.content` 的 `<think>...</think>` 流中。
- `id`、`object`、`created`、`model`、`usage`、`system_fingerprint`、`service_tier`、`logprobs`、`tool_calls`、`annotations`、`metadata`、`extra`、厂商自定义字段等都原样作为 JSON 值保留。
- 请求 body 原样转发，不修改 `model`、`messages`、`tools`、`tool_choice`、`extra_body`、`chat_template_kwargs` 等字段。
- 默认 `strip_reasoning_fields: true`，表示被消费的 reasoning 字符串字段会删除，避免下游同时看到两份思考过程。设为 `false` 可保留原字段。
- 非字符串类型的同名扩展字段不会被消费，也不会删除。

本项目不会尝试把不同厂商的响应规整成同一种 OpenAI Schema；它只做 `<think>` 归集这一件事。

## 快速启动

```bash
cp config.example.yaml config.yaml
# 修改 upstream.base_url，例如 http://127.0.0.1:18489
go run ./cmd/sbgw
```

默认监听：

```bash
http://127.0.0.1:12224
```

## 测试

```bash
curl http://127.0.0.1:12224/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-local-dev-001" \
  -d '{
    "model": "qwen3.6-w8a8",
    "messages": [{"role":"user","content":"你好，计算下中国的面积，对比太平洋的"}],
    "stream": false
  }'
```

流式：

```bash
curl -N http://127.0.0.1:12224/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-local-dev-001" \
  -d '{
    "model": "qwen3.6-w8a8",
    "messages": [{"role":"user","content":"你好，计算下中国的面积，对比太平洋的"}],
    "stream": true
  }'
```

## 配置

```yaml
server:
  addr: ":12224"
  mode: "release"

log:
  level: "info"
  format: "json"
  log_body: true
  max_body_size: 8192

auth:
  enabled: true
  header: "Authorization"
  tokens:
    - "sk-local-dev-001"

upstream:
  base_url: "http://127.0.0.1:18489"
  timeout: "10m"
  api_key: ""
  forward_client_authorization: false

transform:
  enabled: true
  inject_think_tag: true
  strip_reasoning_fields: true
  parse_think_from_content: true
  reasoning_fields:
    - reasoning_content
    - reasoning
```

环境变量同样支持，例如：

```bash
export SBGW_UPSTREAM_BASE_URL=http://127.0.0.1:18489
export SBGW_AUTH_ENABLED=true
```

## Docker

```bash
docker build -t sbgw:dev .
docker run --rm -p 12224:12224 \
  -v $PWD/config.yaml:/app/config.yaml \
  sbgw:dev
```

## GitHub Actions

- push / PR：执行 `go test ./...` 和构建。
- tag `v*`：构建 linux/amd64、linux/arm64 二进制产物。
- tag `v*`：构建并推送多架构镜像到 GHCR。

```bash
git tag v0.1.0
git push origin v0.1.0
```

## 多架构镜像发布

GitHub Actions 默认构建 `linux/amd64` 和 `linux/arm64` 镜像。是否推送由组织变量控制：

| 类型 | 名称 | 说明 |
|---|---|---|
| Secret | `ALIYUN_USERNAME` | 阿里云镜像仓库用户名 |
| Secret | `ALIYUN_PASSWORD` | 阿里云镜像仓库密码 |
| Secret | `DOCKERHUB_USERNAME` | Docker Hub 用户名 |
| Secret | `DOCKERHUB_TOKEN` | Docker Hub token |
| Variable | `ALIYUN_REGISTRY` | 例如 `registry.cn-beijing.aliyuncs.com` |
| Variable | `ALIYUN_NAMESPACE` | 例如 `ainfracn` |
| Variable | `DOCKERHUB_NAMESPACE` | 例如 `yuanyp8` |
| Variable | `PUBLISH_ALIYUN` | `true` / `false` |
| Variable | `PUBLISH_DOCKERHUB` | `true` / `false` |

推送规则：

- `PUBLISH_ALIYUN=true` 时推送：
  - `${ALIYUN_REGISTRY}/${ALIYUN_NAMESPACE}/sbgw:<tag>`
- `PUBLISH_DOCKERHUB=true` 时推送：
  - `${DOCKERHUB_NAMESPACE}/sbgw:<tag>`
- tag 构建时额外推送 `latest`。
- main 分支构建时推送 `main` 和 `sha-xxxxxxx`。

示例：

```bash
git tag v0.1.0
git push origin v0.1.0
```

## 离线 `.run` 交付包

本仓库支持离线 `.run` 包交付，目录约定：

```text
build.sh
install.sh
images/image.json
manifests/sbgw.yaml.tmpl
dist/
```

### 本地构建

```bash
bash build.sh --arch amd64
bash build.sh --arch arm64
bash build.sh --arch all
```

生成产物：

```text
dist/sbgw-<version>-linux-amd64.run
dist/sbgw-<version>-linux-amd64.run.sha256
dist/sbgw-<version>-linux-arm64.run
dist/sbgw-<version>-linux-arm64.run.sha256
```

`build.sh` 会：

1. 读取 `images/image.json`。
2. 按架构执行 `docker buildx build --load --platform linux/amd64|linux/arm64`。
3. 保存镜像 tar 到 payload。
4. 生成 `payload/images/image-index.tsv`。
5. 拷贝 `manifests/` 和配置文件。
6. 拼接 `install.sh + payload.tar.gz` 为 `.run`。
7. 生成 sha256 校验文件。

### GitHub Actions 构建 `.run`

Actions 会对两个架构构建 `.run`：

- `linux/amd64`
- `linux/arm64`

普通 push / PR 会上传 artifact；tag `v*` 时会把 `.run` 和 `.sha256` 上传到 GitHub Release。

### 离线安装

```bash
./sbgw-v0.1.0-linux-amd64.run install \
  --registry sealos.hub:5000/kube4 \
  --registry-user admin \
  --registry-pass passw0rd \
  --upstream-base-url http://qwen-vllm.aict.svc:8000 \
  --auth-token sk-prod-xxx \
  -n aict \
  -y
```

安装过程：

1. 从 `.run` 自身解出 payload。
2. 读取 `images/image-index.tsv`。
3. `docker load` 镜像 tar。
4. retag 到 `--registry` 指定的内网仓库前缀。
5. `docker push` 到内网仓库。
6. 渲染 `manifests/sbgw.yaml.tmpl`。
7. 执行 `kubectl apply`。
8. 等待 Deployment rollout。

### 常用安装参数

```bash
./sbgw-v0.1.0-linux-amd64.run install \
  --registry sealos.hub:5000/kube4 \
  --skip-image-prepare \
  --upstream-base-url http://127.0.0.1:18489 \
  --auth-enabled true \
  --auth-token sk-local-dev-001 \
  --service-type NodePort \
  --node-port 32224 \
  -n aict \
  -y
```

只渲染清单，不安装：

```bash
./sbgw-v0.1.0-linux-amd64.run install --dry-run -y
```

查看状态：

```bash
./sbgw-v0.1.0-linux-amd64.run status -n aict
```

卸载：

```bash
./sbgw-v0.1.0-linux-amd64.run uninstall -n aict -y
```

默认卸载不会删除 namespace。需要删除 namespace 时加：

```bash
--delete-namespace
```

## 交付边界

`sbgw` 的运行时边界仍然不变：

- 请求体全部透传，不改 `extra_body`、`chat_template_kwargs`、`tools`、`tool_choice` 等字段。
- 响应只消费配置中的 reasoning 字符串字段。
- 除被消费的 reasoning 字段外，其余字段全部保留。
- 网关 SK 默认只用于访问网关，不透传给上游。
- 如需上游独立 key，配置 `upstream.api_key`。
