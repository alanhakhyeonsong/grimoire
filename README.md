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
3. AI 접근 경계 = pull 차단 / push 허용. 차단 경로(`boundary.hard_locked_dirs` + `locked_dirs`/`private_dirs` + taxonomy 미등록 디렉토리 fail-safe)는 검색/인덱싱/자동주입에서 제외하되 쓰기와 명시 단건 읽기는 허용한다. **분류 미상(taxonomy 미등록 디렉토리)은 기본 차단(private)**이고, 노출은 노트 frontmatter `ai_access: shared` 명시(opt-in)로만 한다. `hard_locked_dirs`는 `locked_dirs`가 비거나 잘못 편집돼도 새지 않는 최후 안전망이며, 설정을 생략하면 기본값이 적용된다(다른 KB 로 이식할 때 소스를 고치지 않아도 된다). 설정은 서버 시작 시 1회만 로드되므로 세션 도중 차단을 무력화할 수 없다.
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
  Makefile                # setup / build / index / doctor / register
  kb.config.example.json  # KB 설정 템플릿 (kb.config.json 으로 복사; 실제 설정은 gitignore)
  README.md / docs/design.md / docs/configuration.md  # 설명 / 설계배경 / 경로·스캐폴딩 가이드
  cmd/
    grimoire/           # stdio MCP 서버 (10 툴, 시작 시 + 주기 mtime 증분 Sync)
    reindex/            # full 재인덱싱 CLI (검증/복구)
    grimoire-context/   # SessionStart 훅용 컨텍스트 주입 헬퍼 (선택)
    lint/               # 배치 건강검진 CLI (Ollama 불필요)
    grimoire-init/      # 기존 KB 를 훑어 kb.config.json 초안 생성
    grimoire-doctor/    # 설정·커버리지 진단 ("왜 검색이 안 되는가")
  internal/
    config/             # kb.config.json 로딩·검증
    boundary/           # AI 접근 경계 가드 (pull 차단/push 허용)
    frontmatter/        # frontmatter 파싱 + fallback 추론
    index/              # SQLite FTS5 인덱스 + 인덱서 (Reindex / Sync / 미분류 스캔)
    writer/             # write_note: 분류 역매핑·파일명·frontmatter·atomic write·redact
    contextsig/         # cwd → jump 레지스트리 역매핑 (project 추론)
    compiler/           # lint(구조검진) + suggest/apply(frontmatter 백필)
    initcfg/            # KB 구조 → taxonomy 추론 (grimoire-init 엔진)
    doctor/             # 설정 정합성 + 커버리지 진단
    study/              # 학습노트 스캐폴딩 (new_study)
    ollama/             # 선택적 Ollama 클라이언트 (생성 전용, 기본 비활성)
  bin/                  # 빌드 산출물 (gitignore)
```

## 설정

### 빠른 시작 (기존 KB 가 있다면)

`grimoire-init` 이 KB 를 훑어 디렉토리 분류를 추론하고 config 초안을 만든다. taxonomy 를 손으로 다 적을 필요가 없다.

```bash
make setup KB=~/notes      # 스캔 → kb.config.json 초안 생성
# 생성된 파일에서 boundary(공개/비공개)를 확인한 뒤
make index                 # 인덱싱
make register              # Claude Code 에 등록
```

분류는 실제 디렉토리 구조와 노트 frontmatter 에서 뽑아낸 **초안**이다. 특히 공개/비공개 판정은 이름 기반 추정이 섞이므로, 저장 전에 `⚠️ 공개 여부 확인 필요` 로 표시된 항목을 직접 확인해야 한다.

```
=== 분류 초안 ===
  디렉토리                   type       naming        공개     건수
  backend                    analysis   kebab         shared   46
  personal/analysis          analysis   date-compact  PRIVATE  20
  ...
⚠️  공개 여부 확인 필요 (7곳). 추정이므로 그대로 믿지 마세요.
```

### 직접 작성

```bash
cp kb.config.example.json kb.config.json
# kb.root 를 본인 KB 경로로, taxonomy/boundary 를 본인 디렉토리 구조로 수정
mkdir -p ~/notes   # KB 루트는 config.Load 가 존재만 검증(자동 생성 안 함) → 미리 생성
```

> **taxonomy 에 없는 디렉토리는 fail-safe 로 검색에서 빠진다.** 새 폴더를 만들었는데 검색이 안 된다면 십중팔구 등록을 빠뜨린 것이다. `make doctor` 가 그 디렉토리를 이름으로 지목해 준다.

실제 `kb.config.json` 은 개인 경로/구조를 담으므로 gitignore 된다. 저장소에는 제네릭 템플릿 `kb.config.example.json` 만 커밋된다. `~/memo` 등 작성자 환경에 특화된 디렉토리 규약은 어디까지나 예시이며, 사용자는 자신의 KB 구조를 config 로 정의한다.

> **경로 지정·디렉토리 스캐폴딩 규칙 전문은 [docs/configuration.md](./docs/configuration.md) 참고.** KB 루트(`kb.root`)와 config 파일 경로(`GRIMOIRE_CONFIG`)의 두 계층 구분, taxonomy 폴더 선언 규칙, 미등록 디렉토리 fail-safe private, 하드가드(`hard_locked_dirs`) 설정, 초기 셋업 절차를 코드 근거와 함께 정리했다.

## 빌드 / 실행

```bash
make build     # 모든 실행 파일 빌드 (bin/)
make index     # full 재인덱싱 + 검증 통계 (인덱스 손상 시 복구 경로)
make doctor    # 설정·커버리지 진단
make lint      # KB 건강검진
make check     # fmt + vet + test
```

`make` 없이 직접 쓰려면:

```bash
go build -o bin/grimoire ./cmd/grimoire   # (각 cmd/ 별로 동일)

./bin/reindex kb.config.json              # full 재인덱싱
./bin/grimoire kb.config.json             # MCP 서버 (stdio)
```

> 시작 인덱싱은 **mtime 기반 증분 동기화**다. 인덱스를 디스크에 보존하고 변경된 노트만 갱신(삭제 노트는 스윕 제거)하므로 시작 시간·idle 메모리를 절감한다. 시작 후에도 `index.sync_interval_seconds`(기본 60초) 주기로 백그라운드 증분 동기화가 돌아, Obsidian 등으로 세션 중 추가·수정·사적전환(`ai_access:private`)된 노트를 재시작 없이 반영한다(`write_note`와 같은 mutex로 직렬화; 음수 = 주기 동기화 비활성). 전체 재색인이 필요하면 `reindex` CLI 를 쓴다.

## Claude Code 등록

```bash
claude mcp add grimoire -- ~/tools/grimoire/bin/grimoire ~/tools/grimoire/kb.config.json
```

config **파일** 경로는 CLI 인자 또는 `GRIMOIRE_CONFIG` 환경변수로 지정한다(인자 우선). 노트 **루트**는 그 config 안 `kb.root` 로 지정한다. 두 경로 계층의 차이는 [docs/configuration.md §2](./docs/configuration.md#2-두-개의-경로-계층-핵심-구분) 참고.

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
| `lint` | 건강검진(frontmatter 결손/dangling link/미분류 누락), Ollama 불필요 | 제외 |
| `suggest_frontmatter` | Ollama frontmatter 백필 제안(apply 시 누락 키만 기록) | 제외/거부 |
| `new_study` | 학습노트 스캐폴딩(자료 종류별 README + notes/ + deep-dive/) | 쓰기 허용 |

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

### 5) 학습노트: 주제 1개 = 디렉토리 1개 (new_study)

학습 정리가 흐지부지되는 이유는 대개 의지가 아니라 구조다. 매번 폴더 구성과 적을 내용을 다시 정하다 보면 노트마다 형식이 달라지고 나중에 검색도 안 된다. `new_study` 는 같은 모양의 학습 공간을 한 번에 만든다.

```
"Raft 알고리즘 학습노트 만들어줘. AI한테 물어보며 공부할 거야"
  → new_study({topic:"Raft 합의 알고리즘", kind:"ai", source:"Claude"})
     → personal/study/raft-합의-알고리즘/
          README.md      # 메타 + 고정 관점 + 출처 확인 + 진행 체크리스트
          notes/         # 요약
          deep-dive/     # 자료 밖으로 직접 판 주제
```

규칙 두 가지가 핵심이다.

- **요약(`notes/`)과 직접 판 심화(`deep-dive/`)를 섞지 않는다.** 섞으면 나중에 "자료가 말한 것"과 "내가 판단한 것"이 구분되지 않는다.
- **모든 요약은 고정 관점(렌즈)을 빠짐없이 거친다.** 자료가 짚지 않았으면 빈 칸 대신 "해당 없음" 사유를 남긴다.

#### 자료 종류(`kind`)

학습 자료는 강의만이 아니다. 강의 전제로 틀을 만들면 나머지는 "플랫폼/강사" 같은 빈 칸을 안고 시작하게 되고, 실제로 그런 노트는 메타가 통째로 비어 버린다. 종류를 지정하면 메타 항목과 하위 구조가 그에 맞게 바뀐다.

| kind | 자료 | 메타 | notes 구조 |
|---|---|---|---|
| `course` | 강의 | 플랫폼, 강사 | `<NN-섹션>/<NN-강의>.md` |
| `book` | 기술서적 | 출판사, 저자 | `<NN-장>/<NN-절>.md` |
| `ai` | AI 대화 기반 | 사용 도구/모델 | `<주제>.md` |
| `docs` | 공식 문서 | 문서, **대상 버전** | `<주제>.md` |
| `self` (기본) | 자가 탐구 | 참고 자료 | `<주제>.md` |

`kind` 를 생략하면 `self` 다. 강의가 아닌 학습이 더 흔하기 때문이다.

**`ai` 는 '출처 확인' 표가 추가된다.** AI 답변은 그럴듯하게 틀릴 수 있어서, 1차 자료로 확인한 것만 사실로 남기고 나머지는 "미검증"으로 두게 한다. 틀린 것으로 판명된 항목도 지우지 않고 남겨 같은 착각을 반복하지 않게 한다.

관점은 사람마다 다르므로 config 로 교체한다(미설정 시 정의 → 적용 → 운영 3단계가 기본):

```jsonc
"study": {
  "dir": "personal/study",          // 생략 시 taxonomy 의 type:study 를 찾는다
  "lenses": [
    { "name": "개념 정의", "detail": "용어를 정확히 정의하고 예시로 고정한다." },
    { "name": "실무 적용", "detail": "코드·설정·절차 수준으로 옮겨 적는다." },
    { "name": "운영과 성능", "detail": "부하·장애·관측 관점에서 무엇이 달라지는지 본다." }
  ]
}
```

이미 있는 디렉토리는 덮어쓰지 않고 거부한다(기존 학습 기록 보호).

### 6) 진단: 왜 내 문서가 검색되지 않는가 (doctor)

가장 흔한 사고는 **새 폴더를 만들고 taxonomy 등록을 빠뜨리는 것**이다. 미등록 디렉토리는 fail-safe 로 private 취급되어 인덱스에서 조용히 빠지므로, 경고가 없으면 알아챌 방법이 없다.

```bash
make doctor
```

```
=== 커버리지 (검색 가능한 문서) ===
  마크다운 총계:        391
  인덱싱됨:             287
  제외(glob):           1
  제외(차단·의도):      103     # locked_dirs 등, 정상 동작
  제외(미분류·사고):    0       # taxonomy 미등록, 0 이어야 한다
```

doctor 가 잡는 것: 미등록 디렉토리(원인 경로를 이름으로 지목), taxonomy 경로 오타, `ai_access` 오타, enum 밖 값, `deny_value` 누락(사적 보호가 통째로 꺼진 상태), 인덱스와 설정의 불일치. 종료코드는 정상 0 / error 1 / 실행 실패 2 라 CI 에 걸 수 있다.

`reindex` 와 MCP 서버 시작 시에도 같은 경고가 뜬다. 세션 중 새 폴더를 만들면 주기 동기화가 그 시점에 알린다.

### 7) 건강검진 & frontmatter 백필 (컴파일러)

KB가 커지면 frontmatter 결손·깨진 링크가 쌓인다. `lint`로 점검하고, 선택적으로 Ollama가 frontmatter를 제안한다.

```bash
# 구조 건강검진 (Ollama 불필요): frontmatter 결손/dangling link 보고
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

동작하는 기능: RAG-lite 4툴(`get_index`/`search`/`read_note`/`links`) + `write_note`(분류규약 저장) + 증분 인덱싱 + `get_context`/`get_runbook`(작업 경로·런북 라우팅) + `lint`/`suggest_frontmatter`(건강검진·Ollama 백필) + `new_study`(학습노트 스캐폴딩).

온보딩·운영 도구: `grimoire-init`(KB 스캔 → config 초안), `grimoire-doctor`(설정·커버리지 진단), Makefile(`setup`/`build`/`index`/`doctor`/`register`).

향후(옵션): 문서가 수만 규모로 커지고 탐색형 질문이 잦아질 때 임베딩 레이어 추가.

## 라이선스

미정 (개인 프로젝트).
