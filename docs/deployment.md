# 部署与扩容标准

## 测试环境一键发布
```bash
make deploy-test \
  IMAGE_REPOSITORY=ghcr.io/fxwio/go-llm-gateway \
  IMAGE_TAG=$(git rev-parse --short HEAD) \
  NAMESPACE=go-llm-gateway-test \
  RELEASE_NAME=go-llm-gateway
```

该命令会执行 `helm upgrade --install --wait --atomic`，发布失败会自动回滚到上一版本。

## 健康检查
- Liveness: `/health/live`
- Readiness: `/health/ready`
- Version: `/version`

## 扩容
- 默认通过 HPA 依据 CPU/Memory 扩容。
- 临时手动扩容：
```bash
kubectl scale deployment/go-llm-gateway --replicas=4 -n go-llm-gateway-test
```

## 回滚
```bash
helm history go-llm-gateway -n go-llm-gateway-test
make rollback-test NAMESPACE=go-llm-gateway-test RELEASE_NAME=go-llm-gateway REVISION=<revision>
```

## 安全基线
- 容器非 root。
- `allowPrivilegeEscalation=false`
- `readOnlyRootFilesystem=true`
- `capabilities.drop=["ALL"]`
- `seccompProfile=RuntimeDefault`
- `/metrics`、`/admin/*`、`/debug/pprof/*` 均有应用层鉴权；集群内再叠加 NetworkPolicy。
