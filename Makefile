# Grimoire 빌드/운영 태스크
#
# 처음이라면:  make setup KB=~/notes   (config 초안 생성까지)
# 이후:        make build && make register

BIN     := bin
CONFIG  ?= kb.config.json
KB      ?= ~/notes
GO      ?= go

# 실행 파일 목록 (cmd/<name> → bin/<name>)
CMDS := grimoire reindex grimoire-context lint grimoire-init grimoire-doctor

.DEFAULT_GOAL := help

.PHONY: help
help: ## 사용 가능한 타겟을 보여준다
	@echo "Grimoire — 개인 지식베이스 MCP 서버"
	@echo
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "변수: CONFIG=$(CONFIG)  KB=$(KB)"

.PHONY: build
build: $(CMDS:%=$(BIN)/%) ## 모든 실행 파일을 빌드한다

$(BIN)/%: FORCE
	@mkdir -p $(BIN)
	@$(GO) build -o $@ ./cmd/$*
	@echo "  built $@"

FORCE:

.PHONY: setup
setup: build ## 새 KB 를 위한 config 초안을 만든다 (KB=~/notes)
	@echo "==> KB 스캔: $(KB)"
	@./$(BIN)/grimoire-init $(KB) -o $(CONFIG)
	@echo
	@echo "다음 순서로 진행하세요:"
	@echo "  1) $(CONFIG) 를 열어 boundary(공개/비공개)를 확인"
	@echo "  2) make index      # 인덱싱"
	@echo "  3) make register   # Claude Code 에 등록"

.PHONY: index
index: $(BIN)/reindex ## 인덱스를 전체 재구축한다
	@./$(BIN)/reindex $(CONFIG)

.PHONY: doctor
doctor: $(BIN)/grimoire-doctor ## 설정과 인덱스 상태를 진단한다
	@./$(BIN)/grimoire-doctor $(CONFIG)

.PHONY: lint
lint: $(BIN)/lint ## KB 건강검진(frontmatter/링크/미분류)
	@./$(BIN)/lint $(CONFIG)

.PHONY: register
register: build ## Claude Code 에 MCP 서버로 등록한다
	@command -v claude >/dev/null 2>&1 || { echo "claude CLI 를 찾을 수 없습니다."; exit 1; }
	claude mcp add grimoire -- $(CURDIR)/$(BIN)/grimoire $(CURDIR)/$(CONFIG)
	@echo "등록 완료. 세션을 새로 시작하면 적용됩니다."

.PHONY: test
test: ## 테스트를 실행한다
	@$(GO) test ./...

.PHONY: check
check: ## fmt·vet·test 를 한 번에 돌린다
	@$(GO) fmt ./... >/dev/null
	@$(GO) vet ./...
	@$(GO) test ./...
	@echo "check 통과"

.PHONY: clean
clean: ## 빌드 산출물을 지운다 (인덱스는 건드리지 않는다)
	@rm -rf $(BIN)
	@echo "bin/ 삭제"
