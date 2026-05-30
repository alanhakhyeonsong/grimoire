// reindex 는 인덱싱을 실행하고 검증 통계를 출력하는 CLI 다.
//
// 사용: reindex [config경로]   (기본: ./kb.config.json)
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/alanhakhyeonsong/grimoire/internal/boundary"
	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/index"
)

func main() {
	cfgPath := "kb.config.json"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	c, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "설정 로드 실패:", err)
		os.Exit(1)
	}
	fmt.Printf("🔮 Grimoire 인덱싱 시작: %s\n\n", c.KB.Root)

	t0 := time.Now()
	st, db, err := index.Reindex(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "인덱싱 실패:", err)
		os.Exit(1)
	}
	defer db.Close()
	ms := time.Since(t0).Milliseconds()

	fmt.Println("=== 인덱싱 통계 ===")
	fmt.Printf("  스캔: %d  인덱싱: %d  (%dms)\n", st.Scanned, st.Indexed, ms)
	fmt.Printf("  제외(glob): %d  제외(차단경로): %d  파싱오류: %d\n", st.ExcludedGlob, st.ExcludedPrivate, st.ParseErrors)
	fmt.Printf("  frontmatter 추론(fallback): %d / %d\n", st.Inferred, st.Indexed)
	fmt.Printf("  wikilinks: %d\n", db.LinkCount())

	fmt.Println("\n=== type 분포 ===")
	tc, _ := db.TypeCounts()
	for _, r := range tc {
		fmt.Printf("  %-12s %d\n", r.Type, r.Count)
	}

	fmt.Println("\n=== 경계 검증: 차단경로 인덱스 누출 (모두 0이어야 함) ===")
	leak := false
	for _, d := range boundary.PrivateDirs(c) {
		n := db.CountUnder(d + "/")
		fmt.Printf("  %-22s %d\n", d, n)
		if n > 0 {
			leak = true
		}
	}
	if leak {
		fmt.Println("  ❌ 누출 발견!")
	} else {
		fmt.Println("  ✓ 누출 없음")
	}

	fmt.Println("\n=== 구조 검증 ===")
	fmt.Printf("  personal/study (중첩): %d건\n", db.CountUnder("personal/study/"))
	fmt.Printf("  personal/work-logs:    %d건\n", db.CountUnder("personal/work-logs/"))

	fmt.Println("\n=== 샘플 검색 (FTS5) ===")
	for _, q := range []string{"kubernetes", "redis", "design", "guide"} {
		hits, err := db.Search(q, nil, "", 3)
		if err != nil {
			fmt.Printf("  %q -> 오류: %v\n", q, err)
			continue
		}
		fmt.Printf("  %q -> %d건\n", q, len(hits))
		for _, h := range hits {
			fmt.Printf("     - [%s] %s\n", h.Type, h.Path)
		}
	}
}
