# Grimoire

개인 지식베이스(Personal Knowledge Base) MCP 서버.

Andrej Karpathy의 LLM Wiki 방식을 차용해, 마크다운 KB를 single source of truth로 둔 채 Claude Code가 반복 설명 없이 맥락을 끌어다 쓰고(RAG-lite), 분류 규약에 맞춰 산출물을 써넣게 한다. 임베딩을 쓰지 않으며, config 주도로 환경이 다른 사용자도 확장 가능하다.

## 왜 만드는가

기존 접근들이 작업 패턴과 맞지 않았다.

- **벡터DB 기반 RAG**: 지식을 벡터DB에 복제해 원본 마크다운과 단절되고, 임베딩 인프라(벡터DB + 임베딩/요약 모델) 상시 구동 부담이 크다.
- **도구 내장 위키(git-ignored)**: Karpathy 모델은 잘 맞지만 저장 위치가 도구 내부라 모바일/Obsidian/git 동기화에서 단절된다.

Grimoire는 사용자의 마크다운 KB 자체를 single source of truth로 둔다. Obsidian + git 동기화 워크플로우를 그대로 살리고, 별도 임베딩 인프라 없이 동작한다.

## 핵심 원칙

1. 원본은 마크다운 KB. 별도 저장소에 복제하지 않는다.
2. 임베딩 미사용. 검색 라우팅 주체는 Claude (Karpathy: "검색을 LLM에 위임하라"). 인덱스는 SQLite FTS5.
3. AI 접근 경계 = pull 차단 / push 허용. 차단 경로(config `boundary.locked_dirs` 기본 + `private_dirs` 추가)는 검색/인덱싱/자동주입에서 제외하되 쓰기와 명시 단건 읽기는 허용한다.
4. 확장성 = 엔진 고정 + config 외재화(`kb.config.json`). 다른 유저는 config만 교체한다.
5. frontmatter는 있으면 활용, 없으면 경로/제목/본문에서 추론(fallback), 점진 백필.

## 스택

| 구성 | 선택 |
|---|---|
| 언어/런타임 | Go (단일 바이너리, cgo 불필요) |
| 배포 | stdio per-session |
| MCP | 공식 `modelcontextprotocol/go-sdk` v1.6.1 |
| 인덱스 | `modernc.org/sqlite` FTS5 (순수 Go) |
| frontmatter | `gopkg.in/yaml.v3` |
| glob | `bmatcuk/doublestar/v4` |

> Bun + TypeScript로 시작했다가 메모리/배포 이유로 Go로 전환했다. 근거와 실측은 [docs/design.md](./docs/design.md) §8 참고.

## 구조

```
grimoire/
  kb.config.example.json  # KB 설정 템플릿 (kb.config.json 으로 복사; 실제 설정은 gitignore)
  README.md / docs/design.md
  cmd/
    grimoire/           # stdio MCP 서버 (9 툴, 시작 시 mtime 증분 Sync)
    reindex/            # full 재인덱싱 CLI (검증/복구)
    grimoire-context/   # SessionStart 훅용 컨텍스트 주입 헬퍼 (선택)
    lint/               # 배치 건강검진 CLI (Ollama 불필요)
  internal/
    config/             # kb.config.json 로딩·검증
    boundary/           # AI 접근 경계 가드 (pull 차단/push 허용)
    frontmatter/        # frontmatter 파싱 + fallback 추론
    index/              # SQLite FTS5 인덱스 + 인덱서 (Reindex / Sync)
    writer/             # write_note: 분류 역매핑·파일명·frontmatter·atomic write·redact
    contextsig/         # cwd → jump 레지스트리 역매핑 (project 추론)
    compiler/           # lint(구조검진) + suggest/apply(frontmatter 백필)
    ollama/             # 선택적 Ollama 클라이언트 (생성 전용, 기본 비활성)
  bin/                  # 빌드 산출물 (gitignore)
```

## 설정

```bash
cp kb.config.example.json kb.config.json
# kb.config.json 의 kb.root 를 본인 KB 경로로, taxonomy/boundary 를 본인 디렉토리 구조로 수정
```

실제 `kb.config.json` 은 개인 경로/구조를 담으므로 gitignore 된다. 저장소에는 제네릭 템플릿 `kb.config.example.json` 만 커밋된다. `~/memo` 등 작성자 환경에 특화된 디렉토리 규약은 어디까지나 예시이며, 사용자는 자신의 KB 구조를 config 로 정의한다.

## 빌드 / 실행

```bash
# 빌드
go build -o bin/grimoire         ./cmd/grimoire          # MCP 서버 (시작 시 증분 Sync)
go build -o bin/reindex          ./cmd/reindex           # full 재인덱싱 CLI (검증/복구)
go build -o bin/grimoire-context ./cmd/grimoire-context  # 세션시작 훅 헬퍼 (선택)
go build -o bin/lint             ./cmd/lint              # 건강검진 CLI (Ollama 불필요)

# full 재인덱싱 + 검증 통계 출력 (인덱스 손상 시 복구 경로)
./bin/reindex kb.config.json

# MCP 서버 (stdio) 직접 실행 — 시작 시 mtime 기준 변경분만 동기화
./bin/grimoire kb.config.json
```

> 시작 인덱싱은 **mtime 기반 증분 동기화**다. 인덱스를 디스크에 보존하고 변경된 노트만 갱신(삭제 노트는 스윕 제거)하므로 시작 시간·idle 메모리를 절감한다. 전체 재색인이 필요하면 `reindex` CLI 를 쓴다.

## Claude Code 등록

```bash
claude mcp add grimoire -- ~/tools/grimoire/bin/grimoire ~/tools/grimoire/kb.config.json
```

config 경로는 인자 또는 `GRIMOIRE_CONFIG` 환경변수로 지정한다.

## MCP 툴

| 툴 | 기능 | 차단경로 |
|---|---|---|
| `get_index` | 목차(제목·태그·요약·경로) 라우팅용 | 제외 |
| `search` | FTS5 키워드 + 태그/디렉토리 필터 | 제외 |
| `read_note` | 본문 읽기 | 명시 단건만 허용 |
| `links` | `[[링크]]` 그래프(outgoing/incoming) | 제외 |
| `write_note` | 분류규약 적용 저장(역매핑·frontmatter·atomic·redact) | 쓰기 허용 |
| `get_context` | cwd→project 역추론 + 관련 런북·노트 후보 | 제외 |
| `get_runbook` | 반복 절차(`type:runbook`) 반환 | 제외 |
| `lint` | 건강검진(frontmatter 결손/dangling link) — Ollama 불필요 | 제외 |
| `suggest_frontmatter` | Ollama frontmatter 백필 제안(apply 시 누락 키만 기록) | 제외/거부 |

## 사용법 (워크플로우)

툴은 Claude Code가 자동 호출한다. 아래는 각 흐름의 의도와 트리거 예시다.

### 1) 맥락 조회 (RAG-lite)

검색 주체는 Claude다. `get_index`/`search`로 KB 지도를 받아 읽을 페이지를 고르고 `read_note`로 본문을, `links`로 연결 노트를 추적한다.

```
"sre 가이드 중에 dev 클러스터 설치 절차 찾아서 요약해줘"
  → search({query:"dev 클러스터 설치", dir:"sre-guides"}) → read_note(...)
```

### 2) 산출물 저장 (write_note)

분석/문서를 분류규약에 맞는 위치에 저장한다. `type`/`domain`으로 디렉토리를 역매핑(또는 `dir` 직접 지정)하고 파일명·frontmatter를 자동 생성한다.

```
"이 분석을 backend 분석으로 저장해줘"
  → write_note({title, content, type:"analysis", domain:"backend"})
     → backend/<topic>-<detail>.md 에 frontmatter 포함 저장
```

분류가 모호하면 `ok:false` + 후보 디렉토리를 돌려준다. `redact:true` 디렉토리(예: `personal-blog`)에 사내 식별자가 섞이면 저장을 거부한다.

### 3) 런북: 반복 절차 1회 작성 → 매번 끌어쓰기

배포·클러스터 접속처럼 매번 설명하던 절차를 `type:runbook`으로 **한 번** 써두면, 이후 작업 시 Claude가 `get_runbook`으로 그 맥락을 끌어다 쓴다(반복 설명 제거).

```
# (1회) 런북 작성
"proj-a 배포 절차를 런북으로 정리해줘. alpha/beta kubeconfig 경로랑 배포 명령 포함해서"
  → write_note({title:"proj-a 배포 런북", type:"runbook", tags:["proj-a","deploy"], content:...})
     → runbooks/proj-a-deploy.md

# (이후) 끌어쓰기
"proj-a alpha 배포 진행해줘"
  → get_runbook({name:"proj-a"})  → 런북 본문(절차·주의사항)을 인지한 채 작업
```

> `get_runbook`은 `type:runbook` 노트만 본다. 현재 KB에 런북이 0건이면 빈 목록을 반환하므로, 위 (1회) 작성이 선행돼야 한다. `runbooks` 디렉토리는 config `taxonomy.directories`에 정의돼 있어 `type:runbook` write가 그리로 역매핑된다.

### 4) 작업 경로 인지 컨텍스트 (get_context)

cwd를 jump 레지스트리(`context_signals.jump_registry`)로 역매핑해 project를 추론하고, 관련 런북·노트를 좁혀 준다. 사용자가 프로젝트를 말하지 않아도 맥락을 확보한다.

```
get_context({cwd:"/Users/you/projects/proj-a"})
  → project:"proj-a" 추론 → 관련 runbooks/notes 후보 반환
```

신호원이 없거나(`cwd_project_mapping:false`) 매칭이 없으면 `enabled:false`/빈 후보를 반환하고, 라우팅은 명시 `scope` 인자에만 의존한다.

#### (선택) 세션시작 자동 주입

`bin/grimoire-context`를 Claude Code `SessionStart` 훅에 연결하면 세션 시작 시 현재 작업 경로의 런북·노트가 자동 주입된다. 매칭이 없으면 아무것도 출력하지 않는다.

```jsonc
// ~/.claude/settings.json 의 hooks.SessionStart 에 그룹 추가
{ "hooks": [ { "type": "command",
  "command": "~/tools/grimoire/bin/grimoire-context ~/tools/grimoire/kb.config.json" } ] }
```

### 5) 건강검진 & frontmatter 백필 (컴파일러)

KB가 커지면 frontmatter 결손·깨진 링크가 쌓인다. `lint`로 점검하고, 선택적으로 Ollama가 frontmatter를 제안한다.

```bash
# 구조 건강검진 (Ollama 불필요) — frontmatter 결손/dangling link 보고
./bin/lint kb.config.json          # 상위 50건
./bin/lint kb.config.json --all    # 전체
```

`suggest_frontmatter`(MCP 툴)는 Ollama로 frontmatter 후보를 제안한다. **기본 비활성**이며 켜려면 config:

```jsonc
"ollama": { "enabled": true, "host": "http://localhost:11434", "model": "qwen3:8b" }
```

```
"이 노트 frontmatter 좀 채워줘: backend/redis-penetration.md"
  → suggest_frontmatter({path:"backend/redis-penetration.md"})           # 제안만(검수용)
  → suggest_frontmatter({path:"...", apply:true})                        # 검수 후 기록
```

안전장치(design §11):
- **제안 → 검수 → 기록** 순서. `apply:true`는 **누락된 코어 키만** 삽입하고 **기존 키는 절대 덮어쓰지 않는다**(멱등, atomic write).
- 파일명에 날짜가 없으면 `date`를 임의 생성하지 않는다.
- 차단 경로는 Ollama에 **전달하지도 수정하지도 않는다**.
- Ollama 미가동/비활성이면 `ok:false`로 graceful 처리(엔진의 나머지 기능은 정상).

> 모델은 Ollama에 받아둔 아무 텍스트 생성 모델이면 된다. 백필은 짧은 JSON 분류라 경량 모델로 충분하다(예: `qwen3:8b`, `gemma3:4b`, `llama3.2:3b`). 임베딩이 아니다.

## 현재 상태

동작하는 기능: RAG-lite 4툴(`get_index`/`search`/`read_note`/`links`) + `write_note`(분류규약 저장) + 증분 인덱싱 + `get_context`/`get_runbook`(작업 경로·런북 라우팅) + `lint`/`suggest_frontmatter`(건강검진·Ollama 백필).

향후(옵션): 문서가 수만 규모로 커지고 탐색형 질문이 잦아질 때 임베딩 레이어 추가.

## 라이선스

미정 (개인 프로젝트).
