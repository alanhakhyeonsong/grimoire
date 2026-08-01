// Package initcfg 는 기존 마크다운 KB 를 훑어 kb.config.json 초안을 만든다.
//
// grimoire 도입의 실제 병목은 엔진이 아니라 "taxonomy 를 손으로 다 적어야 한다"는
// 점이다. 디렉토리가 20개면 20줄을 규약에 맞게 써야 하고, 한 곳이라도 빠뜨리면
// 그 폴더는 fail-safe private 으로 조용히 검색에서 빠진다.
// 여기서는 실제 디렉토리 구조와 노트의 frontmatter 를 근거로 분류를 추론해
// 초안을 만들고, 확신이 낮은 항목은 검토 대상으로 표시한다.
//
// 자동 확정이 아니라 초안 생성이다. 특히 공개/비공개 판정은 사람이 확인해야 한다.
package initcfg

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	fmBlockRe     = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---`)
	fmTypeRe      = regexp.MustCompile(`(?m)^type:\s*"?([A-Za-z0-9_-]+)"?\s*$`)
	dateCompactRe = regexp.MustCompile(`^\d{8}-`)
	dateISORe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
)

// DirProposal 은 디렉토리 1개에 대한 분류 제안이다.
type DirProposal struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	Domain   string `json:"domain"`
	Naming   string `json:"naming"`
	Tone     string `json:"tone"`
	AIAccess string `json:"ai_access"`

	Files  int    `json:"files"`
	Reason string `json:"reason"` // 판정 근거(사용자 검토용)
	Review bool   `json:"review"` // 사람이 반드시 확인해야 하는 항목인지
}

// Plan 은 스캔 결과 전체다.
type Plan struct {
	Root       string        `json:"root"`
	Dirs       []DirProposal `json:"dirs"`
	TotalFiles int           `json:"totalFiles"`
	RootFiles  int           `json:"rootFiles"`
}

// PrivateDirs 는 비공개로 제안된 디렉토리 경로다(locked_dirs 후보).
func (p *Plan) PrivateDirs() []string {
	var out []string
	for _, d := range p.Dirs {
		if d.AIAccess == "private" {
			out = append(out, d.Path)
		}
	}
	return out
}

// NeedsReview 는 사람이 확인해야 하는 제안들이다.
func (p *Plan) NeedsReview() []DirProposal {
	var out []DirProposal
	for _, d := range p.Dirs {
		if d.Review {
			out = append(out, d)
		}
	}
	return out
}

// privateHints 는 비공개 후보로 볼 디렉토리 이름 조각이다.
// 오탐(공개인데 비공개로 제안)은 사용자가 되돌리면 그만이지만, 미탐(사적 문서를
// 공개로 제안)은 곧바로 유출이므로 넓게 잡는다.
// 매칭 대상 문자열은 경로 구분자를 '-' 로 정규화한 값이다("personal/analysis"
// 가 "personal-analysis" 힌트에 걸리도록).
var privateHints = []string{
	"career", "resume", "cv", "interview", "job",
	"diary", "journal", "private", "secret", "personal-analysis",
	"salary", "1on1", "therapy", "health", "finance",
	"retro", "reflection", "self",
}

// personalContainers 는 "사적일 수도 있는" 상위 컨테이너다.
// 이 아래는 성격이 섞이기 쉬워(work-logs 는 공개, analysis 는 사적) 이름만으로
// 단정할 수 없다. 공개로 제안하더라도 사람이 반드시 확인하도록 표시한다.
var personalContainers = []string{"personal", "private", "me", "self"}

// typeByName 은 디렉토리 이름에서 type 을 추론한다.
var typeByName = []struct {
	hints []string
	typ   string
}{
	{[]string{"runbook"}, "runbook"},
	{[]string{"guide", "howto", "manual", "sre"}, "guide"},
	{[]string{"blog", "post", "article"}, "blog"},
	{[]string{"reference", "external", "clipping"}, "reference"},
	{[]string{"design", "architecture", "rfc", "adr"}, "design"},
	{[]string{"analysis", "research", "report"}, "analysis"},
	{[]string{"study", "learn", "course", "note"}, "study"},
	{[]string{"work-log", "worklog", "log", "journal", "daily", "weekly"}, "log"},
	{[]string{"ops", "incident", "postmortem"}, "ops"},
	{[]string{"talk", "presentation", "slide"}, "talk"},
	{[]string{"skill", "prompt", "recipe"}, "guide"},
	{[]string{"career", "resume", "interview"}, "career"},
	{[]string{"diary"}, "log"},
	{[]string{"retro", "reflection"}, "reflection"},
}

// toneByType 은 type 에 어울리는 서술 톤이다.
var toneByType = map[string]string{
	"analysis":   "report",
	"design":     "design",
	"guide":      "guide",
	"runbook":    "guide",
	"reference":  "report",
	"blog":       "1st-person",
	"log":        "1st-person",
	"ops":        "1st-person",
	"career":     "1st-person",
	"reflection": "1st-person",
	"study":      "study",
	"talk":       "presentation",
	"note":       "free",
}

func containsAny(s string, hints []string) bool {
	for _, h := range hints {
		if strings.Contains(s, h) {
			return true
		}
	}
	return false
}

// Scan 은 KB 루트를 훑어 분류 초안을 만든다.
func Scan(root string) (*Plan, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, os.ErrNotExist
	}

	// 그룹키 → 파일 목록
	groups := map[string][]string{}
	plan := &Plan{Root: root}

	// 1) top-level 디렉토리별로 "직속 md 가 있는지 / 하위 디렉토리가 몇 개인지"를 본다.
	//    DirMetaFor 가 최장 prefix 매칭이므로 상위 한 줄만 등록해도 하위가 커버된다.
	//    다만 personal/ 처럼 성격이 다른 형제가 모인 컨테이너를 통째로 등록하면
	//    공개/비공개가 뒤섞이므로, 그 경우만 한 단계 더 내려간다.
	directMD := map[string]bool{}
	subDirs := map[string]map[string]bool{}

	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, rErr := filepath.Rel(root, p)
		if rErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		plan.TotalFiles++

		dir := path.Dir(rel)
		if dir == "." {
			plan.RootFiles++
			return nil
		}
		segs := strings.Split(dir, "/")
		top := segs[0]
		if len(segs) == 1 {
			directMD[top] = true
		} else {
			if subDirs[top] == nil {
				subDirs[top] = map[string]bool{}
			}
			subDirs[top][segs[1]] = true
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	// 2) 그룹 키 확정
	keyFor := func(rel string) string {
		segs := strings.Split(path.Dir(rel), "/")
		top := segs[0]
		// 직속 md 가 없고 하위 디렉토리가 여러 개면 컨테이너로 보고 2단계로 분해
		if !directMD[top] && len(subDirs[top]) > 1 && len(segs) > 1 {
			return top + "/" + segs[1]
		}
		return top
	}

	// 3) 파일을 그룹에 배치
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, rErr := filepath.Rel(root, p)
		if rErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if path.Dir(rel) == "." {
			return nil
		}
		k := keyFor(rel)
		groups[k] = append(groups[k], rel)
		return nil
	})

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		plan.Dirs = append(plan.Dirs, propose(root, k, groups[k]))
	}
	return plan, nil
}

// propose 는 그룹 1개의 분류를 추론한다.
func propose(root, key string, files []string) DirProposal {
	lower := strings.ToLower(key)
	base := path.Base(lower)

	p := DirProposal{Path: key, Files: len(files)}

	// (1) type: 노트가 실제로 쓰고 있는 frontmatter type 이 가장 강한 근거다.
	typeCount := map[string]int{}
	scanned := 0
	for _, rel := range files {
		if scanned >= 40 { // 큰 디렉토리는 표본만 본다
			break
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		scanned++
		m := fmBlockRe.FindSubmatch(raw)
		if m == nil {
			continue
		}
		if t := fmTypeRe.FindSubmatch(m[1]); t != nil {
			typeCount[strings.ToLower(string(t[1]))]++
		}
	}
	bestType, bestN := "", 0
	for t, n := range typeCount {
		if n > bestN || (n == bestN && t < bestType) {
			bestType, bestN = t, n
		}
	}

	switch {
	case bestN >= 2:
		p.Type = bestType
		p.Reason = "노트 frontmatter 의 우세 type"
	default:
		p.Type = "note"
		p.Reason = "디렉토리 이름 기준 추정"
		for _, r := range typeByName {
			if containsAny(lower, r.hints) {
				p.Type = r.typ
				break
			}
		}
		if bestN == 1 {
			p.Type = bestType
			p.Reason = "노트 frontmatter type(표본 1건)"
		}
	}

	// (2) naming: 실제 파일명이 날짜로 시작하는 비율을 본다.
	dated := 0
	for _, rel := range files {
		name := path.Base(rel)
		if dateCompactRe.MatchString(name) || dateISORe.MatchString(name) {
			dated++
		}
	}
	// writer 는 date-compact/date-done 외에는 kebab 으로 처리하므로
	// 초안에는 이 둘만 쓴다(해석 불가한 규칙을 만들지 않는다).
	if len(files) > 0 && dated*2 > len(files) {
		p.Naming = "date-compact"
	} else {
		p.Naming = "kebab"
	}

	// (3) domain / tone
	p.Domain = strings.ReplaceAll(base, "_", "-")
	p.Tone = toneByType[p.Type]
	if p.Tone == "" {
		p.Tone = "free"
	}

	// (4) ai_access: 사적 폴더를 공개로 제안하면 곧바로 유출이므로 넓게 잡고,
	//     걸린 항목은 사람이 반드시 확인하도록 표시한다.
	//     경로 구분자를 '-' 로 정규화해 "personal/analysis" 같은 2단계 경로도
	//     "personal-analysis" 힌트에 걸리게 한다.
	norm := strings.ReplaceAll(lower, "/", "-")
	switch {
	case containsAny(norm, privateHints):
		p.AIAccess = "private"
		p.Review = true
		p.Reason += " / 이름에 사적 키워드가 있어 비공개로 제안"
	default:
		p.AIAccess = "shared"
		// personal/ 아래는 공개(work-logs)와 사적(analysis)이 섞이기 쉽다.
		// 공개로 제안하되 확인을 요구한다.
		if top := strings.Split(lower, "/")[0]; len(strings.Split(lower, "/")) > 1 &&
			containsAny(top, personalContainers) {
			p.Review = true
			p.Reason += " / 사적 컨테이너 하위라 공개 여부 확인 필요"
		}
	}
	return p
}

// Render 는 Plan 을 kb.config.json 바이트로 직렬화한다.
func Render(p *Plan, kbName string) ([]byte, error) {
	dirs := map[string]any{}
	for _, d := range p.Dirs {
		e := map[string]any{
			"type":      d.Type,
			"domain":    d.Domain,
			"naming":    d.Naming,
			"tone":      d.Tone,
			"ai_access": d.AIAccess,
		}
		dirs[d.Path] = e
	}

	locked := p.PrivateDirs()
	if locked == nil {
		locked = []string{}
	}

	cfg := map[string]any{
		"$comment": "grimoire-init 이 생성한 초안입니다. 특히 boundary(공개/비공개)를 반드시 검토하세요.",
		"kb": map[string]any{
			"name":      kbName,
			"root":      p.Root,
			"indexPath": ".grimoire/index",
			"exclude": []string{
				"**/_TEMPLATE/**", ".obsidian/**", ".git/**", ".grimoire/**",
				"**/*.png", "**/*.jpg", "**/*.jpeg", "**/*.pdf", "**/*.pptx",
			},
		},
		"boundary": map[string]any{
			"$comment":                       "pull(검색/인덱싱/자동주입) 차단, push(write)와 명시 단건 read 는 허용. hard_locked_dirs 는 config 가 잘못돼도 유지되는 최후 안전망이다.",
			"hard_locked_dirs":               locked,
			"locked_dirs":                    locked,
			"private_dirs":                   []string{},
			"private_frontmatter":            map[string]any{"key": "ai_access", "deny_value": "private"},
			"write_allowed_in_private":       true,
			"single_read_allowed_in_private": true,
		},
		"frontmatter": map[string]any{
			"core":     []string{"title", "type", "tags", "status", "ai_access"},
			"custom":   "allow",
			"defaults": map[string]any{"ai_access": "shared", "status": "active"},
			"enums": map[string]any{
				"type":      collectTypes(p),
				"status":    []string{"draft", "active", "done", "archived"},
				"ai_access": []string{"shared", "private"},
			},
		},
		"taxonomy": map[string]any{
			"directories": dirs,
			"naming_patterns": map[string]any{
				"kebab":        "<topic>-<detail>.md",
				"date-compact": "<YYYYMMDD>-<title>.md",
			},
		},
		"context_signals": map[string]any{
			"$comment":            "cwd -> project 역매핑 신호원(선택). 안 쓰면 false 로 둔다.",
			"cwd_project_mapping": false,
		},
		"ollama": map[string]any{
			"$comment": "임베딩이 아니다. frontmatter 백필 제안용 옵션. 기본 비활성.",
			"enabled":  false,
			"host":     "http://localhost:11434",
			"model":    "qwen3:8b",
		},
		"redact": map[string]any{
			"$comment": "redact:true 인 디렉토리에 저장할 때 본문을 검사할 패턴(사내 식별자 등).",
			"patterns": []string{},
		},
		"index": map[string]any{"sync_interval_seconds": 60},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// collectTypes 는 초안에 등장한 type 들을 enum 으로 모은다.
func collectTypes(p *Plan) []string {
	seen := map[string]bool{}
	for _, d := range p.Dirs {
		seen[d.Type] = true
	}
	for _, t := range []string{"analysis", "design", "guide", "runbook", "reference", "note", "log"} {
		seen[t] = true
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
