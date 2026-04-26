DEV := python3 $(dir $(lastword $(MAKEFILE_LIST)))dev.py

.PHONY: help start stop stop-all teardown restart reset shell status logs wait exec \
        init backup restore list-backups snapshot-list snapshot-create snapshot-restore \
        generate list-instances xray-up xray-down \
        start-test stop-test reset-test test \
        doctor prune version completion config-show config-edit ip-list port-usage

help:
	@echo "dev-env - Development Environment Manager v4.2.0"
	@echo ""
	@echo "Development:"
	@echo "  make start         $(shell $(DEV) version 2>/dev/null)"
	@echo "  make stop          Stop environment"
	@echo "  make stop-all      Stop all dev projects"
	@echo "  make teardown      Destroy environment (delete data)"
	@echo "  make restart SVC=  Restart service (or all)"
	@echo "  make reset         Reset service data"
	@echo "  make shell SH=fish Enter dev container"
	@echo "  make status        Show status"
	@echo "  make logs SVC=     View logs (follow)"
	@echo "  make wait          Wait for services to be healthy"
	@echo "  make exec CMD=...  Run command in dev container"
	@echo "  make init          Initialize environment (.env, checks)"
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
	@echo "  make config-edit     Edit .env"
	@echo "  make ip-list         Show IP allocations"
	@echo "  make port-usage      Show port usage"

start:   ; $(DEV) start
stop:    ; $(DEV) stop
stop-all:; $(DEV) stop --all
teardown:; $(DEV) teardown
restart: ; $(DEV) restart $(SVC)
reset:   ; $(DEV) reset
shell:   ; $(DEV) shell $(SH)
status:  ; $(DEV) status
logs:    ; $(DEV) logs $(SVC)
wait:    ; $(DEV) wait
exec:    ; $(DEV) exec $(CMD)
init:    ; $(DEV) init

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
