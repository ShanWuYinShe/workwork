DEV := python3 $(dir $(lastword $(MAKEFILE_LIST)))dev.py

.PHONY: help start stop stop-all teardown restart reset shell status logs wait exec \
        init backup restore list-backups snapshot-list snapshot-create snapshot-restore \
        generate list-instances xray-up xray-down \
        start-test stop-test reset-test test \
        doctor prune version completion config-show config-edit ip-list port-usage \
        up down build ps run

help:
	@echo "dev-env - Development Environment Manager v5.0.0"
	@echo ""
	@echo "Development:"
	@echo "  make up             Start environment (alias: start)"
	@echo "  make down           Stop environment (alias: stop)"
	@echo "  make build          Rebuild dev container image"
	@echo "  make ps             Show status (alias: status)"
	@echo "  make shell SH=fish  Enter dev container"
	@echo "  make logs SVC=      View logs (follow)"
	@echo "  make exec CMD=...   Run command in dev container"
	@echo "  make run CMD=...    Run one-off command (non-interactive)"
	@echo "  make init           Initialize environment"
	@echo ""
	@echo "Backup/Restore:"
	@echo "  make backup          Backup PostgreSQL + Redis"
	@echo "  make restore TARGET= Restore from backup"
	@echo "  make list-backups    List backups"
	@echo ""
	@echo "Snapshots:"
	@echo "  make snapshot-list               List PG snapshots"
	@echo "  make snapshot-create NAME=       Create PG snapshot"
	@echo "  make snapshot-restore NAME=latest Restore PG snapshot"
	@echo ""
	@echo "Xray Proxy:"
	@echo "  make generate          Generate configs"
	@echo "  make list-instances    List Xray instances"
	@echo "  make xray-up           Start Xray services"
	@echo "  make xray-down         Stop Xray services"
	@echo ""
	@echo "Test Environment:"
	@echo "  make start-test   Start test environment"
	@echo "  make stop-test    Stop test environment"
	@echo "  make reset-test   Reset test data"
	@echo "  make test         Full restart"
	@echo ""
	@echo "Maintenance:"
	@echo "  make doctor          Diagnostics"
	@echo "  make prune           Clean orphaned Docker resources"
	@echo "  make version         Show version"
	@echo "  make config-show     Show configuration"
	@echo "  make config-edit     Edit config"
	@echo "  make ip-list         Show IP allocations"
	@echo "  make port-usage      Show port usage"

up:        ; $(DEV) up
start:     ; $(DEV) start
down:      ; $(DEV) down
stop:      ; $(DEV) stop
stop-all:  ; $(DEV) stop --all
teardown:  ; $(DEV) teardown
build:     ; $(DEV) build
ps:        ; $(DEV) ps
restart:   ; $(DEV) restart $(SVC)
reset:     ; $(DEV) reset
shell:     ; $(DEV) shell $(SH)
status:    ; $(DEV) status
logs:      ; $(DEV) logs $(SVC)
wait:      ; $(DEV) wait
exec:      ; $(DEV) exec $(CMD)
run:       ; $(DEV) run $(CMD)
init:      ; $(DEV) init

backup:        ; $(DEV) backup
restore:       ; $(DEV) restore $(TARGET)
list-backups:  ; $(DEV) list-backups

snapshot-list:   ; $(DEV) snapshot list
snapshot-create: ; $(DEV) snapshot create $(NAME)
snapshot-restore:; $(DEV) snapshot restore $(NAME)

generate:       ; $(DEV) generate
list-instances: ; $(DEV) list-instances
xray-up:        ; $(DEV) xray-start
xray-down:      ; $(DEV) xray-stop

start-test: ; $(DEV) start-test
stop-test:  ; $(DEV) stop-test
reset-test: ; $(DEV) reset-test
test:       ; $(DEV) test

doctor:      ; $(DEV) doctor
prune:       ; $(DEV) prune
version:     ; $(DEV) version
config-show: ; $(DEV) config show
config-edit: ; $(DEV) config edit
ip-list:     ; $(DEV) ip list
port-usage:  ; $(DEV) port-usage
