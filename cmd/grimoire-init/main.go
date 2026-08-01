// grimoire-init 은 기존 마크다운 KB 를 훑어 kb.config.json 초안을 만든다.
//
//	grimoire-init ~/notes                 # 초안을 화면에 출력(검토용)
//	grimoire-init ~/notes -o kb.config.json   # 파일로 저장(기존 파일은 덮지 않음)
//
// 분류는 실제 디렉토리 구조와 노트 frontmatter 에서 추론한 "초안"이다.
// 특히 공개/비공개 판정은 저장 전에 사람이 확인해야 한다.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/initcfg"
)

func main() {
	out := flag.String("o", "", "저장할 config 경로 (미지정 시 화면 출력)")
	name := flag.String("name", "", "KB 이름 (기본: 루트 디렉토리 이름)")
	force := flag.Bool("force", false, "-o 대상 파일이 이미 있어도 덮어쓴다")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "사용법: grimoire-init <KB루트> [-o kb.config.json] [-name 이름] [-force]")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	// Go flag 는 위치 인자 뒤의 플래그를 파싱하지 않는다.
	// `grimoire-init ~/notes -o kb.config.json` 이 더 자연스러운 순서이므로
	// 첫 위치 인자를 걷어내고 나머지를 한 번 더 파싱한다.
	rest := flag.Args()
	if len(rest) > 1 {
		if err := flag.CommandLine.Parse(rest[1:]); err != nil {
			os.Exit(2)
		}
	}

	root := config.ExpandTilde(rest[0])
	abs, err := filepath.Abs(root)
	if err == nil {
		root = abs
	}

	plan, err := initcfg.Scan(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "KB 루트를 읽을 수 없습니다: %s (%v)\n", root, err)
		os.Exit(1)
	}
	if plan.TotalFiles == 0 {
		fmt.Fprintf(os.Stderr, "마크다운(.md)을 찾지 못했습니다: %s\n", root)
		os.Exit(1)
	}

	kbName := *name
	if kbName == "" {
		kbName = filepath.Base(root)
	}

	data, err := initcfg.Render(plan, kbName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config 생성 실패:", err)
		os.Exit(1)
	}

	// 요약은 stderr 로 보낸다. stdout 은 config 본문만 담아야
	// `grimoire-init ~/notes > kb.config.json` 이 그대로 동작한다.
	printSummary(plan)

	if *out == "" {
		fmt.Println(string(data))
		fmt.Fprintln(os.Stderr, "\n검토 후 저장하려면: grimoire-init <루트> -o kb.config.json")
		return
	}

	target := config.ExpandTilde(*out)
	if _, statErr := os.Stat(target); statErr == nil && !*force {
		fmt.Fprintf(os.Stderr, "\n이미 존재하는 파일입니다: %s\n덮어쓰려면 -force 를 쓰세요.\n", target)
		os.Exit(1)
	}
	if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "저장 실패:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\n저장했습니다: %s\n", target)
	fmt.Fprintln(os.Stderr, "다음: ./bin/reindex "+target+" 으로 인덱싱 결과를 확인하세요.")
}

func printSummary(p *initcfg.Plan) {
	w := os.Stderr
	fmt.Fprintf(w, "🔮 KB 스캔: %s\n", p.Root)
	fmt.Fprintf(w, "   마크다운 %d건, 디렉토리 %d곳\n\n", p.TotalFiles, len(p.Dirs))

	fmt.Fprintln(w, "=== 분류 초안 ===")
	fmt.Fprintf(w, "  %-26s %-10s %-13s %-8s %s\n", "디렉토리", "type", "naming", "공개", "건수")
	for _, d := range p.Dirs {
		access := "shared"
		if d.AIAccess == "private" {
			access = "PRIVATE"
		}
		fmt.Fprintf(w, "  %-26s %-10s %-13s %-8s %d\n", trunc(d.Path, 26), d.Type, d.Naming, access, d.Files)
	}

	if rv := p.NeedsReview(); len(rv) > 0 {
		fmt.Fprintf(w, "\n⚠️  공개 여부 확인 필요 (%d곳) — 추정이므로 그대로 믿지 마세요.\n", len(rv))
		for _, d := range rv {
			label := "shared 로 제안"
			if d.AIAccess == "private" {
				label = "PRIVATE 로 제안"
			}
			fmt.Fprintf(w, "     - %-24s %s: %s\n", d.Path, label, d.Reason)
		}
		fmt.Fprintln(w, "   · 사적인데 shared 로 잡혔다면: ai_access 를 private 로 바꾸고 locked_dirs 에 추가")
		fmt.Fprintln(w, "   · 공개해도 되는데 PRIVATE 면: ai_access 를 shared 로 바꾸고 locked_dirs 에서 제거")
	}

	if p.RootFiles > 0 {
		fmt.Fprintf(w, "\n참고: KB 루트 직속 마크다운 %d건은 디렉토리 분류 대상이 아닙니다.\n", p.RootFiles)
		fmt.Fprintln(w, "   검색에 넣으려면 각 파일 frontmatter 에 ai_access: shared 를 적으세요.")
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + strings.Repeat("…", 1)
}
