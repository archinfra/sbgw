# GitHub Actions 与 Release

## Workflow 阶段

`.github/workflows/build.yml` 包含三段：

1. `test`：`go mod tidy`、`go test ./...`、`go build`。
2. `docker-multiarch`：构建并发布 `linux/amd64,linux/arm64` 多架构镜像。
3. `run-packages`：基于发布镜像或本地 Dockerfile 生成离线 `.run` 包。
4. `github-release`：tag 触发时把 `.run` 和 `.sha256` 上传到 GitHub Release。

## `.dockerbuild` 非产物问题

`actions/download-artifact@v4` 如果不指定 `name`、`artifact-ids` 或 `pattern`，会下载当前 workflow 的所有 artifact。Docker build action 可能额外生成 `.dockerbuild` 构建记录 artifact，导致 Release 阶段把非交付产物下载到 `dist`。

当前 workflow 使用两层保护：

```yaml
env:
  DOCKER_BUILD_RECORD_UPLOAD: "false"
```

并且 Release 阶段只下载真正的 `.run` artifact：

```yaml
- uses: actions/download-artifact@v4
  with:
    pattern: sbgw-run-*
    path: dist
    merge-multiple: true
```

最终只上传：

```text
dist/*.run
dist/*.sha256
```

## 发布 tag

```bash
git tag v0.1.0
git push origin v0.1.0
```

如果需要重打 tag：

```bash
git tag -d v0.1.0
git push origin :refs/tags/v0.1.0
git tag v0.1.0
git push origin v0.1.0
```

## Registry 变量

Aliyun 发布需要：

- Repository Variables:
  - `PUBLISH_ALIYUN=true`
  - `ALIYUN_REGISTRY`
  - `ALIYUN_NAMESPACE`
- Secrets:
  - `ALIYUN_USERNAME`
  - `ALIYUN_PASSWORD`

Docker Hub 发布需要：

- Repository Variables:
  - `PUBLISH_DOCKERHUB=true`
  - `DOCKERHUB_NAMESPACE`
- Secrets:
  - `DOCKERHUB_USERNAME`
  - `DOCKERHUB_TOKEN`

至少启用一个 registry，`.run` 包才能优先从已发布多架构镜像制作；都不启用时会退回到本地 Dockerfile 构建。

## 本地构建离线包

```bash
bash build.sh --arch amd64 --version v0.1.0
bash build.sh --arch arm64 --version v0.1.0
bash build.sh --arch all --version v0.1.0
```

从已发布多架构镜像制作离线包：

```bash
bash build.sh \
  --arch amd64 \
  --version v0.1.0 \
  --source-image registry.cn-beijing.aliyuncs.com/ainfracn/sbgw:v0.1.0
```
