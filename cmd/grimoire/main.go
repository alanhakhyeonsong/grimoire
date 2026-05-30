// grimoire 는 stdio MCP 서버다.
//
// Phase 0 RAG-lite 툴: get_index / search / read_note / links.
// 시작 시 KB 를 인덱싱하고 stdin/stdout 으로 MCP 프로토콜을 처리한다.
// 주의: stdio 의 stdout 은 JSON-RPC 채널이므로 로깅은 stderr 로만 한다.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/boundary"
	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/index"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

	// 시작 시 인덱싱(신선도 보장). Phase 2 에서 증분/watch 로 대체 예정.
	st, db, err := index.Reindex(c)
	if err != nil {
		log.Fatalln("인덱싱 실패:", err)
	}
	defer db.Close()
	log.Printf("grimoire: 인덱싱 완료 (%d건, 차단경로 %d 제외)", st.Indexed, st.ExcludedPrivate)

	s := mcp.NewServer(&mcp.Implementation{Name: "grimoire", Version: "0.0.1"}, nil)

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

	log.Println("grimoire: stdio MCP 서버 시작")
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalln("서버 종료:", err)
	}
}
