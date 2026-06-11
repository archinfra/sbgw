# Kubernetes 与离线安装

## 默认行为

离线 `.run` 包默认安装为：

- Namespace：`sbgw`
- Deployment：`sbgw`
- Service Type：`NodePort`
- NodePort：`30088`
- 容器监听端口：`12224`

## 一键安装

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

## 查看安装帮助

```bash
./sbgw-v0.1.0-linux-amd64.run -h
./sbgw-v0.1.0-linux-amd64.run install -h
```

帮助里会明确说明：单上游可以用 `--upstream-base-url`，多模型、多 route、多上游、thinking/direct 拆分推荐用 `--config-file`。

## 生成最终多模型配置

```bash
./sbgw-v0.1.0-linux-amd64.run example-config > config.multi-model.yaml
vi config.multi-model.yaml
```

需要重点修改：

- `auth.keys[]`：网关给用户/前端使用的 API key 和额度。
- `upstream.routes[]`：对外 route，例如 `/qwen36-direct`。
- `upstream.routes[].request_patches`：thinking/direct 开关参数。
- `upstream.endpoints[]`：真实上游模型服务地址、上游 key、权重、支持模型。

安装：

```bash
./sbgw-v0.1.0-linux-amd64.run install \
  --registry sealos.hub:5000/kube4 \
  --config-file ./config.multi-model.yaml \
  --service-type NodePort \
  --node-port 30088 \
  -n aict -y
```

## 使用完整配置文件

复杂场景建议使用完整 `config.yaml`，尤其是：

- 多 route。
- 多 upstream endpoint。
- thinking/direct 拆分。
- endpoint 级 API key。
- 不同框架不同 thinking 参数。

```bash
cp config.multi-model.example.yaml config-prod.yaml
# 或者：./sbgw-v0.1.0-linux-amd64.run example-config > config-prod.yaml
vi config-prod.yaml

./sbgw-v0.1.0-linux-amd64.run install \
  --registry sealos.hub:5000/kube4 \
  --config-file ./config-prod.yaml \
  --service-type NodePort \
  --node-port 30088 \
  -n aict -y
```

使用 `--config-file` 后，安装脚本会把你的完整配置写入 ConfigMap。

## 上游不需要 key

如果上游模型服务本身不需要鉴权，不要填写 `--upstream-api-key`：

```bash
./sbgw-v0.1.0-linux-amd64.run install \
  --upstream-base-url http://qwen-vllm.aict.svc:8000 \
  --auth-token sk-prod-xxx \
  -n aict -y
```

## 只渲染 manifest

```bash
./sbgw-v0.1.0-linux-amd64.run install \
  --config-file ./config-prod.yaml \
  --dry-run
```

或者生成文件但不 apply：

```bash
./sbgw-v0.1.0-linux-amd64.run install \
  --config-file ./config-prod.yaml \
  --no-apply
```

## 卸载

```bash
./sbgw-v0.1.0-linux-amd64.run uninstall -n aict -y
```

连 namespace 一起删除：

```bash
./sbgw-v0.1.0-linux-amd64.run uninstall -n aict --delete-namespace -y
```

## 验证

```bash
kubectl -n aict get pod,svc,cm -l app.kubernetes.io/name=sbgw
kubectl -n aict logs deploy/sbgw -f
```

NodePort 调用：

```bash
curl http://<node-ip>:30088/qwen36-direct/v1/models \
  -H "Authorization: Bearer sk-demo-user-001"
```
