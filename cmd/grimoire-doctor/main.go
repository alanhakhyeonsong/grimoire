// grimoire-doctor 는 설정과 인덱스 상태를 진단한다.
//
//	grimoire-doctor kb.config.json
//
// "왜 내 문서가 검색되지 않는가"를 사용자가 스스로 확인하기 위한 도구다.
// 종료코드: 0=정상, 1=error 있음, 2=실행 실패.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/doctor"
	"github.com/alanhakhyeonsong/grimoire/internal/index"
)

func main() {
	cfgPath := os.Getenv("GRIMOIRE_CONFIG")
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	if cfgPath == "" {
		fmt.Fprintln(os.Stderr, "사용법: grimoire-doctor <kb.config.json>")
		fmt.Fprintln(os.Stderr, "        (또는 GRIMOIRE_CONFIG 환경변수)")
		os.Exit(2)
	}

	c, err := config.Load(config.ExpandTilde(cfgPath))
	if err != nil {
		// 설정 자체를 못 읽으면 나머지 진단이 무의미하다.
		fmt.Fprintln(os.Stderr, "❌ 설정을 읽을 수 없습니다:", err)
		fmt.Fprintln(os.Stderr, "   새 KB 라면 grimoire-init 으로 초안을 만드세요.")
		os.Exit(2)
	}

	// 인덱스는 없을 수도 있다(최초 실행). 없으면 인덱스 대조만 건너뛴다.
	var db *index.DB
	if d, oErr := index.Open(filepath.Join(c.KB.Root, c.KB.IndexPath)); oErr == nil {
		db = d
		defer db.Close()
	}

	rep, err := doctor.Run(c, db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "진단 실패:", err)
		os.Exit(2)
	}

	fmt.Printf("🩺 Grimoire 진단: %s\n", rep.Root)
	fmt.Printf("   설정: %s\n\n", cfgPath)

	cov := rep.Coverage
	fmt.Println("=== 커버리지 (검색 가능한 문서) ===")
	fmt.Printf("  마크다운 총계:        %d\n", cov.MarkdownFiles)
	fmt.Printf("  인덱싱됨:             %d\n", cov.Indexed)
	fmt.Printf("  제외(glob):           %d\n", cov.ExcludedGlob)
	fmt.Printf("  제외(차단·의도):      %d\n", cov.ByPolicy)
	fmt.Printf("  제외(미분류·사고):    %d\n", cov.Unclassified)
	if cov.MarkdownFiles > 0 {
		reachable := cov.MarkdownFiles - cov.ExcludedGlob - cov.ByPolicy
		if reachable > 0 {
			// 분류 기준으로 검색 대상이어야 하는 건수. 실제 인덱스 적재 여부는
			// 아래 진단(index 영역)에서 따로 대조한다.
			fmt.Printf("  → 공개 대상 %d건 중 %d건이 분류상 검색 대상 (%.1f%%)\n",
				reachable, reachable-cov.Unclassified,
				float64(reachable-cov.Unclassified)/float64(reachable)*100)
		}
	}

	if len(rep.Issues) == 0 {
		fmt.Println("\n✓ 문제 없음")
		return
	}

	errs, warns := rep.Counts()
	fmt.Printf("\n=== 진단 (%d건: error %d, warn %d) ===\n", len(rep.Issues), errs, warns)
	for _, is := range rep.Issues {
		fmt.Printf("\n  [%s] %s: %s\n", mark(is.Severity), is.Area, is.Message)
		if is.Fix != "" {
			fmt.Printf("      → %s\n", is.Fix)
		}
	}

	if errs > 0 {
		os.Exit(1)
	}
}

func mark(s doctor.Severity) string {
	switch s {
	case doctor.SevError:
		return "ERROR"
	case doctor.SevWarn:
		return "WARN "
	default:
		return "INFO "
	}
}
