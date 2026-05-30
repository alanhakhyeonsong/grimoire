// lint 는 KB 건강검진 리포트를 출력하는 CLI 다(Ollama 불필요).
//
// frontmatter 결손, 누락 코어필드, dangling [[wikilink]] 을 노트별로 보고한다.
// 검진 전 증분 Sync 로 인덱스를 최신화한다.
//
// 사용: lint [config경로] [--all]
//   --all : findings 전체 출력(기본은 상위 50건)
package main

import (
	"fmt"
	"os"

	"github.com/alanhakhyeonsong/grimoire/internal/compiler"
	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/index"
)

func main() {
	cfgPath := "kb.config.json"
	limit := 50
	for _, a := range os.Args[1:] {
		if a == "--all" {
			limit = 0
		} else {
			cfgPath = a
		}
	}

	c, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "설정 로드 실패:", err)
		os.Exit(1)
	}

	st, db, err := index.Sync(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "동기화 실패:", err)
		os.Exit(1)
	}
	defer db.Close()

	rep, err := compiler.Lint(c, db, limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lint 실패:", err)
		os.Exit(1)
	}

	fmt.Printf("🩺 Grimoire 건강검진: %s\n\n", c.KB.Root)
	fmt.Printf("동기화: 총 %d건(갱신 %d, 변경없음 %d, 삭제 %d)\n\n", st.Indexed, st.Updated, st.Unchanged, st.Deleted)
	fmt.Println("=== 집계 ===")
	fmt.Printf("  노트: %d\n", rep.Notes)
	fmt.Printf("  frontmatter 없음:     %d\n", rep.NoFrontmatter)
	fmt.Printf("  코어필드 누락(>=1):   %d\n", rep.MissingCoreFields)
	fmt.Printf("  dangling wikilink:    %d\n", rep.DanglingLinks)

	if len(rep.Findings) == 0 {
		fmt.Println("\n✓ 지적사항 없음")
		return
	}
	fmt.Printf("\n=== findings (%d건%s) ===\n", len(rep.Findings), map[bool]string{true: ", --all", false: ""}[limit == 0])
	for _, f := range rep.Findings {
		fmt.Printf("  %s\n", f.Path)
		for _, is := range f.Issues {
			fmt.Printf("     - %s\n", is)
		}
	}
}
