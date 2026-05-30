// grimoire 는 stdio MCP 서버다.
//
// 툴: get_index / search / read_note / links / write_note / get_context /
// get_runbook / lint / suggest_frontmatter.
// 시작 시 KB 를 인덱싱하고 stdin/stdout 으로 MCP 프로토콜을 처리한다.
// 주의: stdio 의 stdout 은 JSON-RPC 채널이므로 로깅은 stderr 로만 한다.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alanhakhyeonsong/grimoire/internal/boundary"
	"github.com/alanhakhyeonsong/grimoire/internal/compiler"
	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/contextsig"
	"github.com/alanhakhyeonsong/grimoire/internal/frontmatter"
	"github.com/alanhakhyeonsong/grimoire/internal/index"
	"github.com/alanhakhyeonsong/grimoire/internal/ollama"
	"github.com/alanhakhyeonsong/grimoire/internal/writer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// writeMu 는 write_note 의 파일·인덱스 갱신을 직렬화한다.
var writeMu sync.Mutex

type getIndexInput struct {
	Type   string `json:"type,omitempty" jsonschema:"필터: 문서 타입(analysis, guide, runbook, log 등)"`
	Domain string `json:"domain,omitempty" jsonschema:"필터: 도메인(backend, sre, infra-network 등)"`
	Dir    string `json:"dir,omitempty" jsonschema:"필터: 디렉토리 prefix(예: sre-guides)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"최대 결과 수(기본 50)"`
}

type searchInput struct {
	Query string   `json:"query" jsonschema:"검색어(FTS5 키워드)"`
	Tags  []string `json:"tags,omitempty" jsonschema:"태그 필터(AND)"`
	Dir   string   `json:"dir,omitempty" jsonschema:"디렉토리 prefix 필터"`
	Limit int      `json:"limit,omitempty" jsonschema:"최대 결과 수(기본 5)"`
}

type readNoteInput struct {
	Path string `json:"path" jsonschema:"KB 루트 기준 상대경로(예: sre-guides/dev-sre-install-guide.md)"`
}

type linksInput struct {
	Path string `json:"path" jsonschema:"KB 루트 기준 상대경로"`
}

type lintInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"findings 최대 수(기본 50, 0=무제한)"`
}

type suggestFrontmatterInput struct {
	Path  string `json:"path" jsonschema:"KB 루트 기준 상대경로. Ollama 가 frontmatter 후보를 제안한다(차단경로 거부)"`
	Apply bool   `json:"apply,omitempty" jsonschema:"true 면 누락된 frontmatter 키만 기록(기존 키는 절대 덮어쓰지 않음). 기본 false=제안만"`
}

type getContextInput struct {
	Cwd   string `json:"cwd" jsonschema:"현재 작업 디렉토리 절대경로. jump 레지스트리 역매핑으로 project 를 추론한다"`
	Limit int    `json:"limit,omitempty" jsonschema:"런북/노트 후보 각 최대 수(기본 10)"`
}

type getRunbookInput struct {
	Name  string `json:"name,omitempty" jsonschema:"런북 이름/키워드(title·path·tags 매칭). 생략 시 전체 런북 목록만 반환"`
	Limit int    `json:"limit,omitempty" jsonschema:"최대 결과 수(기본 20)"`
}

type writeNoteInput struct {
	Title     string   `json:"title" jsonschema:"문서 제목(H1/frontmatter title)"`
	Content   string   `json:"content" jsonschema:"마크다운 본문(frontmatter 제외; 엔진이 frontmatter 를 생성해 앞에 붙인다)"`
	Type      string   `json:"type,omitempty" jsonschema:"문서 타입(analysis/guide/runbook/reflection/blog 등). dir 미지정 시 분류에 사용"`
	Domain    string   `json:"domain,omitempty" jsonschema:"도메인(backend/sre/infra-network 등). type 와 함께 dir 역매핑을 좁힌다"`
	Dir       string   `json:"dir,omitempty" jsonschema:"저장 디렉토리(taxonomy 키) 직접 지정. 주면 type/domain 역매핑 생략"`
	Tags      []string `json:"tags,omitempty" jsonschema:"태그 목록(생략 시 도메인 기본값)"`
	Status    string   `json:"status,omitempty" jsonschema:"draft/active/done/archived(기본 active)"`
	Slug      string   `json:"slug,omitempty" jsonschema:"파일명 핵심부(kebab). 생략 시 title 에서 생성"`
	Date      string   `json:"date,omitempty" jsonschema:"YYYY-MM-DD. 날짜기반 네이밍 dir 에 사용(기본 오늘)"`
	Overwrite bool     `json:"overwrite,omitempty" jsonschema:"기존 파일 덮어쓰기 허용(기본 false)"`
}

func textResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}, nil, nil
}

func main() {
	cfgPath := "kb.config.json"
	if v := os.Getenv("GRIMOIRE_CONFIG"); v != "" {
		cfgPath = v
	}
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	c, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalln("설정 로드 실패:", err)
	}

	// 시작 시 mtime 기반 증분 동기화. 전체 재인덱싱을 없애
	// idle 메모리·시작 시간을 절감한다. 인덱스 손상 시 reindex CLI 로 전체 복구.
	st, db, err := index.Sync(c)
	if err != nil {
		log.Fatalln("인덱싱 실패:", err)
	}
	defer db.Close()
	log.Printf("grimoire: 동기화 완료 (총 %d건 / 갱신 %d, 변경없음 %d, 삭제 %d, 차단 %d 제외)",
		st.Indexed, st.Updated, st.Unchanged, st.Deleted, st.ExcludedPrivate)

	s := mcp.NewServer(&mcp.Implementation{Name: "grimoire", Version: "0.1.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_index",
		Description: "KB 목차 조회(제목·태그·요약·경로). Claude가 읽을 페이지를 고르는 라우팅용. 차단 경로는 제외됨.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in getIndexInput) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		entries, err := db.GetIndex(in.Type, in.Domain, in.Dir, limit)
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]any{"count": len(entries), "notes": entries})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search",
		Description: "KB 전문검색(FTS5 키워드 + 태그/디렉토리 필터). 차단 경로는 제외됨.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, fmt.Errorf("query 가 비어 있습니다")
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 5
		}
		hits, err := db.Search(in.Query, in.Tags, in.Dir, limit)
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]any{"count": len(hits), "hits": hits})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "read_note",
		Description: "노트 본문 읽기(경로 명시 단건). 차단 경로도 명시 지목 시 허용되나 사적 자료 경고가 붙는다.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in readNoteInput) (*mcp.CallToolResult, any, error) {
		rel := filepath.ToSlash(filepath.Clean(in.Path))
		if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return nil, nil, fmt.Errorf("잘못된 경로입니다: %s", in.Path)
		}
		isPrivate := boundary.IsPrivateDir(rel, c)
		if isPrivate && !c.Boundary.SingleReadAllowedInPrivate {
			return nil, nil, fmt.Errorf("차단 경로입니다(읽기 비허용): %s", rel)
		}
		abs := filepath.Join(c.KB.Root, rel)
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, nil, fmt.Errorf("노트를 읽을 수 없습니다: %s", rel)
		}
		result := map[string]any{"path": rel, "content": string(data)}
		if isPrivate {
			result["warning"] = "이 문서는 지식베이스에서 제외된 사적 자료입니다. 다른 작업의 판단 근거로 사용하지 마세요."
		}
		return textResult(result)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "links",
		Description: "노트의 [[wikilink]] 그래프 조회(outgoing/incoming).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in linksInput) (*mcp.CallToolResult, any, error) {
		rel := filepath.ToSlash(filepath.Clean(in.Path))
		out, inc, err := db.LinksOf(rel)
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]any{"path": rel, "outgoing": out, "incoming": inc})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_context",
		Description: "작업 경로 인지 컨텍스트. cwd 를 jump 레지스트리로 역매핑해 project 를 추론하고, 관련 런북·노트 후보를 좁혀 반환한다. 사용자가 프로젝트를 말하지 않아도 맥락을 확보하는 라우팅용. 신호원 미설정 시 enabled:false.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in getContextInput) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 10
		}
		res := contextsig.Resolve(c, in.Cwd)
		runbooks, notes, err := db.ContextCandidates(res.Project, limit)
		if err != nil {
			return nil, nil, err
		}
		hint := res.Note
		if res.Project != "" {
			hint = "project '" + res.Project + "' 추론됨. 아래 런북/노트를 read_note 로 확인하세요."
		} else if res.Enabled && hint == "" {
			hint = "project 미추론 — 사용 가능한 런북 목록만 반환."
		}
		return textResult(map[string]any{
			"cwd": in.Cwd, "enabled": res.Enabled, "project": res.Project,
			"projectPath": res.ProjectPath, "runbooks": runbooks, "notes": notes,
			"hint": hint,
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_runbook",
		Description: "반복 작업 절차(type:runbook) 반환. name 지정 시 가장 잘 맞는 런북 본문을 바로 반환(배포·클러스터 접속 등), 생략 시 전체 런북 목록. 차단 경로는 제외.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in getRunbookInput) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		list, err := db.Runbooks(in.Name, limit)
		if err != nil {
			return nil, nil, err
		}
		if in.Name == "" {
			return textResult(map[string]any{"count": len(list), "runbooks": list})
		}
		if len(list) == 0 {
			return textResult(map[string]any{"count": 0, "message": "일치하는 런북이 없습니다: " + in.Name})
		}
		// best match: 파일명(확장자 제외) 또는 title 이 name 과 정확히 일치하면 우선, 아니면 최신순 1번째
		best := list[0]
		for _, e := range list {
			base := strings.TrimSuffix(filepath.Base(e.Path), ".md")
			if base == in.Name || e.Title == in.Name {
				best = e
				break
			}
		}
		abs := filepath.Join(c.KB.Root, best.Path)
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			return nil, nil, fmt.Errorf("런북 본문을 읽을 수 없습니다: %s", best.Path)
		}
		others := make([]string, 0, len(list))
		for _, e := range list {
			if e.Path != best.Path {
				others = append(others, e.Path)
			}
		}
		return textResult(map[string]any{
			"matched": best.Path, "title": best.Title, "content": string(data),
			"others": others,
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "lint",
		Description: "KB 건강검진(Ollama 불필요). frontmatter 결손/누락 코어필드/dangling [[wikilink]] 을 노트별로 보고한다. 차단 경로는 제외.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in lintInput) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit == 0 {
			limit = 50
		}
		rep, err := compiler.Lint(c, db, limit)
		if err != nil {
			return nil, nil, err
		}
		return textResult(rep)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "suggest_frontmatter",
		Description: "Ollama 컴파일러(옵션). 노트 본문을 읽고 frontmatter 후보(title/type/tags/summary)를 제안한다. apply:true 면 누락된 코어 키만 기록(기존 키 덮어쓰기 금지, atomic). config ollama.enabled=false 거나 서버 미가동이면 ok:false. 차단 경로 거부.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in suggestFrontmatterInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Path) == "" {
			return nil, nil, fmt.Errorf("path 가 필요합니다")
		}
		oll, oerr := ollama.New(c)
		if oerr != nil {
			return textResult(map[string]any{"ok": false, "reason": "ollama-disabled", "message": oerr.Error()})
		}
		if !oll.Available(ctx) {
			return textResult(map[string]any{"ok": false, "reason": "ollama-unreachable",
				"message": "Ollama 서버에 연결할 수 없습니다(ollama serve 실행 확인)."})
		}
		prop, err := compiler.Suggest(ctx, c, oll, in.Path)
		if err != nil {
			return textResult(map[string]any{"ok": false, "reason": "suggest-failed", "message": err.Error()})
		}
		out := map[string]any{"ok": true, "path": in.Path, "model": oll.Model(), "proposal": prop, "applied": false}
		if !in.Apply {
			out["note"] = "제안만 반환(apply:false). 검수 후 apply:true 로 누락 키만 기록하세요."
			return textResult(out)
		}

		// apply: 파일·인덱스 변경을 write_note 와 같은 mutex 로 직렬화
		writeMu.Lock()
		defer writeMu.Unlock()
		fields := compiler.BuildFields(c, in.Path, prop)
		ar, aerr := compiler.Apply(c, in.Path, fields)
		if aerr != nil {
			return textResult(map[string]any{"ok": false, "reason": "apply-failed", "message": aerr.Error(), "proposal": prop})
		}
		out["applied"] = true
		out["apply_result"] = ar
		// 인덱스 증분 갱신(차단/사적은 제외 유지)
		if !boundary.IsPrivateDir(ar.Path, c) {
			abs := filepath.Join(c.KB.Root, ar.Path)
			if raw, rerr := os.ReadFile(abs); rerr == nil {
				mtime := time.Now().UnixMilli()
				if info, serr := os.Stat(abs); serr == nil {
					mtime = info.ModTime().UnixMilli()
				}
				note, fm := frontmatter.Parse(string(raw), ar.Path, mtime, c)
				if !boundary.IsPrivateByFrontmatter(fm, c) {
					_ = db.DeletePath(ar.Path)
					_ = db.Upsert(note)
				}
			}
		}
		return textResult(out)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "write_note",
		Description: "분류 규약에 맞춰 KB 에 노트 저장. type/domain 으로 dir 자동결정(또는 dir 명시), 파일명·frontmatter 자동생성, atomic write. 분류 모호 시 후보 dir 와 함께 ok:false 반환. redact 대상 dir 은 사내 식별자 발견 시 저장 거부.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in writeNoteInput) (*mcp.CallToolResult, any, error) {
		// write 는 파일 stat/rename + 인덱스 갱신이 얽혀 있어 직렬화한다
		// (동시 호출 시 exists 판정·쓰기 레이스 방지).
		writeMu.Lock()
		defer writeMu.Unlock()
		if strings.TrimSpace(in.Title) == "" && strings.TrimSpace(in.Slug) == "" {
			return nil, nil, fmt.Errorf("title 또는 slug 가 필요합니다")
		}
		res, err := writer.Write(c, writer.Request{
			Title: in.Title, Content: in.Content, Type: in.Type, Domain: in.Domain,
			Dir: in.Dir, Tags: in.Tags, Status: in.Status, Slug: in.Slug,
			Date: in.Date, Overwrite: in.Overwrite,
		}, time.Now())
		if err != nil {
			var ce *writer.ClassifyError
			var re *writer.RedactError
			var ee *writer.ExistsError
			switch {
			case errors.As(err, &ce):
				return textResult(map[string]any{"ok": false, "reason": "classify", "message": ce.Msg, "candidates": ce.Candidates})
			case errors.As(err, &re):
				return textResult(map[string]any{"ok": false, "reason": "redact", "dir": re.Dir, "matches": re.Matches, "message": re.Error()})
			case errors.As(err, &ee):
				return textResult(map[string]any{"ok": false, "reason": "exists", "path": ee.Path, "message": ee.Error()})
			default:
				return nil, nil, err
			}
		}

		// 인덱스 증분 갱신 (차단/사적 경로는 인덱스에서 제외 유지)
		indexed := false
		indexErr := ""
		private := boundary.IsPrivateDir(res.Path, c)
		if !private {
			abs := filepath.Join(c.KB.Root, res.Path)
			raw, rerr := os.ReadFile(abs)
			if rerr != nil {
				indexErr = "read: " + rerr.Error()
			} else {
				// 실제 파일 mtime 으로 인덱싱해야 다음 시작의 Sync 가
				// 이 노트를 변경됨으로 오인(중복 재인덱싱)하지 않는다.
				mtime := time.Now().UnixMilli()
				if info, serr := os.Stat(abs); serr == nil {
					mtime = info.ModTime().UnixMilli()
				}
				note, fm := frontmatter.Parse(string(raw), res.Path, mtime, c)
				if boundary.IsPrivateByFrontmatter(fm, c) {
					private = true
				} else if derr := db.DeletePath(res.Path); derr != nil {
					indexErr = "delete: " + derr.Error()
				} else if uerr := db.Upsert(note); uerr != nil {
					indexErr = "upsert: " + uerr.Error()
				} else {
					indexed = true
				}
			}
		}

		warn := ""
		if private {
			warn = "차단/사적 경로에 저장됨 — 인덱스/검색에서 제외(push 허용)."
		}
		if indexErr != "" {
			warn = strings.TrimSpace(warn + " 인덱스 갱신 실패(" + indexErr + ") — reindex 로 복구 가능.")
		}
		if res.RedactScanned && len(c.Redact.Patterns) == 0 {
			warn = strings.TrimSpace(warn + " redact 대상 dir 이나 redact.patterns 가 비어 스캔이 사실상 비활성입니다.")
		}
		return textResult(map[string]any{
			"ok": true, "path": res.Path, "dir": res.Dir, "file": res.File,
			"type": res.Type, "ai_access": res.AIAccess, "indexed": indexed,
			"private": private, "warning": warn,
		})
	})

	log.Println("grimoire: stdio MCP 서버 시작")
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalln("서버 종료:", err)
	}
}
