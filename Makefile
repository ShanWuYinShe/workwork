# workwork 构建/部署入口：把 AGENTS.md「构建 & 部署」的散文映射变成可执行规则。
# 部署推导逻辑在 scripts/deploy-changed.sh，本文件只做薄封装。

COMPOSE := docker compose
SERVICES := admin xray xray-forward nginx media_server vod_extractor netease_api music_server

# 版本号自动导出,compose build args 读取后烙进各服务二进制/镜像并经
# 健康端点暴露;调用方显式传入时以传入值为准。
export GIT_VERSION ?= $(shell git describe --always --tags --dirty 2>/dev/null || echo dev)
export MEDIA_CENTER_VERSION ?= $(shell git -C services/media_center describe --tags --always --dirty 2>/dev/null || echo dev)
export MUSIC_PLAYER_VERSION ?= $(shell git -C services/music_player describe --tags --always --dirty 2>/dev/null || echo dev)

.DEFAULT_GOAL := help
.PHONY: help $(SERVICES) build up restart logs ps submodule check deploy-changed

# 变长服务名目标的兜底规则（make build admin 中的 "admin" 不是 make 目标）
%:
	@:

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

submodule: ## 同步子模块到主仓库记录的指针
	git submodule update --init --recursive

check: ## 本地门禁: gofmt / go vet / go test / 前端构建
	@test -z "$$(gofmt -l services/admin/)" || { echo 'gofmt 未通过:'; gofmt -l services/admin/; exit 1; }
	go vet -C services/admin ./...
	go test -C services/admin ./...
	bun install --cwd services/admin/ui
	bun run --cwd services/admin/ui build

deploy-changed: ## 服务器部署: pull + 子模块同步 + 按变更最小 build/up
	./scripts/deploy-changed.sh
