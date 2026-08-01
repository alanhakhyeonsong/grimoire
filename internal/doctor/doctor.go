// Package doctor 는 설정과 인덱스 상태를 진단한다.
//
// 사용자가 가장 자주 겪는 문제는 "왜 내 문서가 검색되지 않는가"인데, 지금까지는
// 이를 스스로 확인할 방법이 없었다. config.Load 는 KB 루트 존재와 taxonomy 가
// 비었는지만 보므로 오타난 경로나 enum 위반은 조용히 통과한다.
// doctor 는 그 사각지대를 문제 목록으로 바꾼다.
package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/boundary"
	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/index"
)

// Severity 는 문제의 심각도다.
type Severity string

const (
	// SevError 는 기능이 실제로 깨지는 문제다.
	SevError Severity = "error"
	// SevWarn 은 의도와 다르게 동작할 수 있는 문제다.
	SevWarn Severity = "warn"
	// SevInfo 는 참고 사항이다.
	SevInfo Severity = "info"
)

// Issue 는 진단 결과 1건이다. Fix 는 사용자가 취할 구체적 조치를 담는다.
type Issue struct {
	Severity Severity `json:"severity"`
	Area     string   `json:"area"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
}

// Report 는 진단 전체 결과다.
type Report struct {
	Root     string  `json:"root"`
	Issues   []Issue `json:"issues"`
	Coverage struct {
		MarkdownFiles int `json:"markdownFiles"` // KB 안의 .md 총수
		Indexed       int `json:"indexed"`
		ExcludedGlob  int `json:"excludedGlob"`
		ByPolicy      int `json:"byPolicy"`
		Unclassified  int `json:"unclassified"`
	} `json:"coverage"`
	UnclassifiedDirs []string `json:"unclassifiedDirs,omitempty"`
}

func (r *Report) add(sev Severity, area, msg, fix string) {
	r.Issues = append(r.Issues, Issue{Severity: sev, Area: area, Message: msg, Fix: fix})
}

// Counts 는 심각도별 건수를 반환한다.
func (r *Report) Counts() (errors, warns int) {
	for _, i := range r.Issues {
		switch i.Severity {
		case SevError:
			errors++
		case SevWarn:
			warns++
		}
	}
	return
}

// Run 은 설정과 KB 를 진단한다. db 는 nil 이어도 되며, 그때는 인덱스 대조를 건너뛴다.
func Run(c *config.Config, db *index.DB) (*Report, error) {
	rep := &Report{Root: c.KB.Root}

	checkTaxonomy(c, rep)
	checkBoundary(c, rep)
	checkFrontmatterEnums(c, rep)
	checkCoverage(c, db, rep)

	return rep, nil
}

// checkTaxonomy 는 taxonomy 에 적힌 디렉토리가 실제로 존재하는지 본다.
// 오타난 키는 아무것도 매칭하지 못한 채 조용히 무시되므로, 해당 폴더 전체가
// 미분류(=검색 제외) 상태가 된다.
func checkTaxonomy(c *config.Config, rep *Report) {
	keys := make([]string, 0, len(c.Taxonomy.Directories))
	for k := range c.Taxonomy.Directories {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	knownNaming := map[string]bool{}
	for n := range c.Taxonomy.NamingPatterns {
		knownNaming[n] = true
	}

	for _, k := range keys {
		meta := c.Taxonomy.Directories[k]
		abs := filepath.Join(c.KB.Root, filepath.FromSlash(k))
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			rep.add(SevWarn, "taxonomy",
				fmt.Sprintf("등록된 디렉토리가 KB 에 없습니다: %s", k),
				"경로 오타이거나 이미 지운 폴더입니다. taxonomy.directories 에서 제거하거나 경로를 고치세요.")
		}
		if meta.Type == "" {
			rep.add(SevError, "taxonomy",
				fmt.Sprintf("%s 에 type 이 없습니다", k),
				"type 을 지정하세요. write_note 의 역매핑이 이 값을 씁니다.")
		}
		if meta.Naming != "" && len(knownNaming) > 0 && !knownNaming[meta.Naming] {
			rep.add(SevInfo, "taxonomy",
				fmt.Sprintf("%s 의 naming=%q 이 naming_patterns 에 설명이 없습니다", k, meta.Naming),
				"동작에는 지장이 없지만(미지원 규칙은 kebab 으로 처리) 설명을 추가해두면 규약이 명확해집니다.")
		}
	}
}

// checkBoundary 는 차단 설정의 정합성을 본다.
func checkBoundary(c *config.Config, rep *Report) {
	// deny_value 가 비면 IsPrivateAccess 가 항상 false 가 되어,
	// frontmatter 로 private 를 선언한 노트도, taxonomy 미등록 디렉토리의
	// fail-safe 도 전부 무력화된다(locked_dirs 경로 차단만 남는다).
	// 보호 장치가 통째로 꺼지는 것이므로 error 다.
	if c.Boundary.PrivateFrontmatter.DenyValue == "" {
		rep.add(SevError, "boundary",
			"boundary.private_frontmatter.deny_value 가 비어 있습니다(사적 보호 대부분이 꺼진 상태)",
			`"private" 를 지정하세요. 이 값이 없으면 ai_access: private 선언과 미등록 디렉토리 fail-safe 가 모두 무시되고, locked_dirs 경로 차단만 동작합니다.`)
	}
	if c.Boundary.PrivateFrontmatter.Key == "" {
		rep.add(SevWarn, "boundary",
			"boundary.private_frontmatter.key 가 비어 있습니다",
			`관례상 "ai_access" 를 씁니다.`)
	}

	for _, d := range boundary.PrivateDirs(c) {
		abs := filepath.Join(c.KB.Root, filepath.FromSlash(d))
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			rep.add(SevInfo, "boundary",
				fmt.Sprintf("차단 경로가 KB 에 없습니다: %s", d),
				"없는 경로를 차단하는 것은 무해하지만, 오타라면 실제 사적 폴더가 보호되지 않습니다.")
		}
	}

	if c.Boundary.HardLockedDirs != nil && len(c.Boundary.HardLockedDirs) == 0 {
		rep.add(SevWarn, "boundary",
			"hard_locked_dirs 가 빈 배열입니다(최후 안전망 없음)",
			"의도한 설정이면 무시하세요. 사적 폴더가 있다면 여기에 넣는 편이 안전합니다.")
	}
}

// checkFrontmatterEnums 는 taxonomy 가 쓰는 값이 enum 에 있는지 본다.
func checkFrontmatterEnums(c *config.Config, rep *Report) {
	enums := c.Frontmatter.Enums
	if len(enums) == 0 {
		return
	}
	allowedType := map[string]bool{}
	for _, t := range enums["type"] {
		allowedType[t] = true
	}
	allowedAccess := map[string]bool{}
	for _, a := range enums["ai_access"] {
		allowedAccess[a] = true
	}

	keys := make([]string, 0, len(c.Taxonomy.Directories))
	for k := range c.Taxonomy.Directories {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		m := c.Taxonomy.Directories[k]
		if len(allowedType) > 0 && m.Type != "" && !allowedType[m.Type] {
			rep.add(SevWarn, "enum",
				fmt.Sprintf("%s 의 type=%q 이 frontmatter.enums.type 에 없습니다", k, m.Type),
				"enums.type 에 추가하거나 등록된 값으로 바꾸세요.")
		}
		if len(allowedAccess) > 0 && m.AIAccess != "" && !allowedAccess[m.AIAccess] {
			rep.add(SevError, "enum",
				fmt.Sprintf("%s 의 ai_access=%q 이 허용값이 아닙니다", k, m.AIAccess),
				`"shared" 또는 "private" 를 쓰세요. 오타는 의도치 않은 공개로 이어집니다.`)
		}
	}
}

// checkCoverage 는 "KB 에 있는 문서 중 실제로 검색 가능한 비율"을 계산한다.
func checkCoverage(c *config.Config, db *index.DB, rep *Report) {
	scan, err := index.ScanUnclassified(c)
	if err == nil {
		rep.Coverage.Unclassified = scan.Files
		rep.Coverage.MarkdownFiles = scan.TotalMarkdown
		rep.Coverage.ExcludedGlob = scan.ExcludedGlob
		rep.Coverage.ByPolicy = scan.ExcludedByPolicy
		rep.UnclassifiedDirs = scan.Dirs
		if scan.Files > 0 {
			rep.add(SevError, "coverage",
				fmt.Sprintf("taxonomy 미등록 디렉토리 %d곳의 노트 %d건이 검색에서 빠져 있습니다: %s",
					len(scan.Dirs), scan.Files, strings.Join(scan.Dirs, ", ")),
				"공개하려면 taxonomy.directories 에 등록하고, 비공개 의도라면 boundary.locked_dirs 에 넣으세요.")
		}
	}

	if db == nil {
		rep.add(SevWarn, "index",
			"인덱스를 열 수 없습니다(아직 만들지 않았을 수 있습니다)",
			"reindex 를 한 번 실행하세요.")
		return
	}

	rep.Coverage.Indexed = db.Total()

	// 분류상 검색 대상이어야 할 건수와 실제 인덱스 건수를 대조한다.
	// 어긋나면 인덱스가 낡은 것이다. 특히 증분 동기화는 파일 mtime 이 그대로면
	// 다시 파싱하지 않으므로, "파일은 그대로 두고 config 만 고친" 변경은
	// 곧바로 반영되지 않는다(전체 재인덱싱이 필요하다).
	expected := rep.Coverage.MarkdownFiles - rep.Coverage.ExcludedGlob -
		rep.Coverage.ByPolicy - rep.Coverage.Unclassified
	switch {
	case rep.Coverage.Indexed == 0 && expected > 0:
		rep.add(SevError, "index",
			fmt.Sprintf("인덱스가 비어 있습니다(검색 대상 %d건인데 0건 적재)", expected),
			"reindex 를 실행하세요.")
	case rep.Coverage.Indexed != expected:
		rep.add(SevWarn, "index",
			fmt.Sprintf("인덱스 건수(%d)가 설정 기준 예상치(%d)와 다릅니다", rep.Coverage.Indexed, expected),
			"config 를 고친 뒤 재색인하지 않았을 가능성이 큽니다. reindex 로 전체 재구축하세요.")
	}
}
