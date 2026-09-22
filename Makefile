# workwork 构建/部署入口：把 AGENTS.md「构建 & 部署」的散文映射变成可执行规则。
# 部署推导逻辑在 scripts/deploy-changed.sh，本文件只做薄封装。

COMPOSE := docker compose
# 基础镜像版本唯一来源(独立站拆分后本仓仅剩编排栈自身版本):
# 升级改 versions.env → make build 重建;compose 内的 :- 默认值仅为兜底,
# 与 versions.env 必须保持一致(check 门禁/CI 校验)。
-include versions.env
export

SERVICES := admin docker-proxy xray nginx

# 版本号自动导出,compose build args 读取后烙进各服务二进制/镜像并经
# 健康端点暴露;调用方显式传入时以传入值为准。
export GIT_VERSION ?= $(shell git describe --always --tags --dirty 2>/dev/null || echo dev)
# 不可变镜像 tag（与 deploy-changed.sh 一致）：回滚 = IMAGE_TAG=<旧tag> make up <svc...>。
export IMAGE_TAG ?= $(GIT_VERSION)

.DEFAULT_GOAL := help
.PHONY: help $(SERVICES) build up restart logs ps check backup deploy-changed

KNOWN_TARGETS := help build up restart logs ps check backup deploy-changed

# 变长服务名目标的兜底规则（make build admin 中的 "admin" 不是 make 目标）。
# 必须校验：无守卫时拼错目标（make buil admin）会静默成功退出 0。
# 用 $@ 而非 $*：GNU Make 在 %: 兜底规则里 $* 不保证展开（实测为空）。
%:
	@if ! echo " $(KNOWN_TARGETS) $(SERVICES) " | grep -q " $@ "; then \
		echo "make: *** 未知目标 '$@'。可用: make {build|up|restart|logs} <服务> 或 $(KNOWN_TARGETS)"; \
		exit 1; \
	fi

help: ## 显示帮助
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36mmake %-22s\033[0m %s\n",$$1,$$2}'
	@echo '  变长参数用法: make build <svc...> / make up <svc...> / make restart <svc...> / make logs <svc...>'

build: ## 构建指定服务镜像: make build admin nginx
	$(COMPOSE) build $(filter-out $@,$(MAKECMDGOALS))

up: ## 重建指定容器(不牵连依赖): make up admin
	$(COMPOSE) up -d --no-deps $(filter-out $@,$(MAKECMDGOALS))

restart: ## 重启指定容器: make restart admin
	$(COMPOSE) restart $(filter-out $@,$(MAKECMDGOALS))

logs: ## 跟踪指定服务日志: make logs nginx
	$(COMPOSE) logs -f $(filter-out $@,$(MAKECMDGOALS))

ps: ## 查看服务状态
	$(COMPOSE) ps

check: ## 本地门禁: gofmt / go vet / go test / 前端格式与构建
	@test -z "$$(gofmt -l services/admin/)" || { echo 'gofmt 未通过:'; gofmt -l services/admin/; exit 1; }
	@test -z "$$(gofmt -l services/docker_proxy/)" || { echo 'gofmt 未通过:'; gofmt -l services/docker_proxy/; exit 1; }
	go vet -C services/admin ./...
	go test -C services/admin -race ./...
	go vet -C services/docker_proxy ./...
	go test -C services/docker_proxy -race ./...
	@for sh in scripts/*.sh setup-networks.sh services/nginx/*.sh services/xray/*.sh; do \
	  bash -n "$$sh" || exit 1; \
	  if command -v shellcheck >/dev/null 2>&1; then shellcheck -S warning "$$sh" || exit 1; fi; \
	done
	@docker compose config -q 2>/dev/null || echo '(compose config 校验跳过:需 .env,由 CI 兜底)'
	@# 版本口径门禁:versions.env 与 compose 内 :- 兜底值不得漂移
	@for v in GO_VERSION ALPINE_VERSION BUN_VERSION NGINX_VERSION; do \
	  want=$$(grep -E "^$$v=" versions.env | cut -d= -f2); \
	  got=$$(grep -oE "\$${$$v:-[^}]*}" docker-compose.yml | head -1 | sed 's/.*:-//;s/}//'); \
	  if [ -n "$$got" ] && [ "$$want" != "$$got" ]; then \
	    echo "$$v 漂移:versions.env=$$want compose兜底=$$got"; exit 1; \
	  fi; \
	done
	bun install --frozen-lockfile --cwd services/admin/ui
	bun run --cwd services/admin/ui format:check
	bun run --cwd services/admin/ui lint
	bun run --cwd services/admin/ui check
	bun run --cwd services/admin/ui build

backup: ## 备份运行时数据: scripts/backup.sh（BACKUP_DIR/BACKUP_RSYNC_TARGET 见脚本头）
	./scripts/backup.sh

deploy-changed: ## 服务器部署: pull + 按变更最小 build/up
	./scripts/deploy-changed.sh
