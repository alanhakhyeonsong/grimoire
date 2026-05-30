// grimoire-context 는 세션 시작 시 작업 경로 컨텍스트를 주입하기 위한 헬퍼 CLI 다.
//
// config 와 cwd 로 project 를 역추론하고, 관련 런북·노트 후보를 주입용
// 마크다운으로 stdout 에 출력한다. Claude Code 의 SessionStart 훅이 이 출력을
// additionalContext 로 주입하는 용도다(선택 기능). 매칭이 없으면 아무것도
// 출력하지 않아 훅 노이즈를 만들지 않는다.
//
// 사용: grimoire-context [config경로] [cwd]
//   - config 기본: ./kb.config.json (GRIMOIRE_CONFIG 환경변수도 인식)
//   - cwd    기본: 현재 작업 디렉토리
//
// 주의: 이 CLI 는 인덱스를 갱신하지 않는다(기존 인덱스만 읽음). 신선도는
// grimoire MCP 서버의 시작 시 Sync 가 담당한다.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/contextsig"
	"github.com/alanhakhyeonsong/grimoire/internal/index"
)

func main() {
	cfgPath := "kb.config.json"
	if v := os.Getenv("GRIMOIRE_CONFIG"); v != "" {
		cfgPath = v
	}
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cwd := ""
	if len(os.Args) > 2 {
		cwd = os.Args[2]
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	c, err := config.Load(cfgPath)
	if err != nil {
		// 조용히 종료: 훅이 설정 없이 호출돼도 세션을 방해하지 않는다.
		return
	}

	res := contextsig.Resolve(c, cwd)
	if !res.Enabled || res.Project == "" {
		return // 신호원 비활성 또는 매칭 없음 → 주입할 것 없음
	}

	db, err := index.Open(filepath.Join(c.KB.Root, c.KB.IndexPath))
	if err != nil {
		return
	}
	defer db.Close()

	runbooks, notes, err := db.ContextCandidates(res.Project, 5)
	if err != nil || (len(runbooks) == 0 && len(notes) == 0) {
		return
	}

	fmt.Printf("## Grimoire 컨텍스트 — project: %s\n", res.Project)
	fmt.Printf("작업 경로(`%s`)에서 추론한 관련 KB. 필요한 페이지는 grimoire `read_note`/`get_runbook` 으로 확인하세요.\n", res.ProjectPath)
	if len(runbooks) > 0 {
		fmt.Println("\n**런북(반복 절차):**")
		for _, e := range runbooks {
			fmt.Printf("- `%s` — %s\n", e.Path, e.Title)
		}
	}
	if len(notes) > 0 {
		fmt.Println("\n**관련 노트:**")
		for _, e := range notes {
			fmt.Printf("- `%s` — %s\n", e.Path, e.Title)
		}
	}
}
