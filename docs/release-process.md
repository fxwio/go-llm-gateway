# Release 流程

## 版本规范
- 使用语义化版本：`vMAJOR.MINOR.PATCH`
- 根目录 `VERSION` 保存当前候选版本。
- Git tag 是发布的唯一触发器。

## 发布步骤
1. 更新 `VERSION`
2. 更新 `CHANGELOG.md`
3. 提交并合并到 `main`
4. 打 tag：`git tag vX.Y.Z && git push origin vX.Y.Z`
5. GitHub Actions 自动执行：
   - 编译、测试、lint、安全扫描
   - 构建并推送镜像
   - 生成 release notes
   - 发布 GitHub Release

## 镜像标签策略
- `ghcr.io/<repo>:<semver>`
- `ghcr.io/<repo>:<git-sha>`

## 回滚策略
- 镜像级：回滚到上一个稳定 tag
- Helm 级：`helm rollback`
- 配置级：通过 ConfigMap/Secret 回滚到上一版并重新 `helm upgrade`
