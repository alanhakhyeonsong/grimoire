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
3. AI 접근 경계 = pull 차단 / push 허용. 차단 경로(config `private_dirs` 로 지정)는 검색/인덱싱/자동주입에서 제외하되 쓰기와 명시 단건 읽기는 허용한다.
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
    grimoire/           # stdio MCP 서버 (get_index/search/read_note/links)
    reindex/            # 인덱싱 CLI (검증/수동 재색인)
  internal/
    config/             # kb.config.json 로딩·검증
    boundary/           # AI 접근 경계 가드 (pull 차단/push 허용)
    frontmatter/        # frontmatter 파싱 + fallback 추론
    index/              # SQLite FTS5 인덱스 + 인덱서
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
go build -o bin/grimoire ./cmd/grimoire
go build -o bin/reindex  ./cmd/reindex

# 인덱싱 + 검증 통계 출력
./bin/reindex kb.config.json

# MCP 서버 (stdio) 직접 실행
./bin/grimoire kb.config.json
```

## Claude Code 등록

```bash
claude mcp add grimoire -- ~/tools/grimoire/bin/grimoire ~/tools/grimoire/kb.config.json
```

config 경로는 인자 또는 `GRIMOIRE_CONFIG` 환경변수로 지정한다.

## MCP 툴 (단계별)

| 툴 | 기능 | Phase |
|---|---|---|
| `get_index` | 목차(제목·태그·요약·경로) | 0 (완료) |
| `search` | FTS5 키워드+태그 | 0 (완료) |
| `read_note` | 본문 읽기 (차단경로 명시 단건만) | 0 (완료) |
| `links` | `[[링크]]` 그래프 추적 | 0 (완료) |
| `write_note` | 분류규약 적용 저장 | 1 |
| `get_runbook` / `get_context` | 작업 런북 / cwd→project 라우팅 | 2 |

## 현재 상태

**Phase 0 (RAG-lite) 완료** — `get_index` / `search` / `read_note` / `links` 동작. 다음 단계는 `write_note`(Phase 1).

## 라이선스

미정 (개인 프로젝트).
