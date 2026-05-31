// Package compiler 는 선택적 위키 컴파일러다.
//
// 두 축으로 나뉜다.
//   - Lint: Ollama 없이 동작하는 구조 건강검진(frontmatter 결손, fallback 추론,
//     dangling [[wikilink]], 날짜 누락). 엔진 기본 기능으로 항상 쓸 수 있다.
//   - Suggest/Apply: Ollama 백필. frontmatter 값을 "제안"하고(Suggest),
//     기존 키는 절대 덮어쓰지 않고 누락분만 "기록"한다(Apply, gap-fill).
//
// 경계 준수: Suggest/Apply/Lint 는 모두 인덱스에 적재된(=차단경로가 이미 제외된)
// 노트만 다룬다. 추가로 boundary 가드를 한 번 더 적용해 차단 파일을 Ollama 에
// 전달하거나 수정하지 않는다(design §11).
package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/boundary"
	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/frontmatter"
	"github.com/alanhakhyeonsong/grimoire/internal/index"
	"github.com/alanhakhyeonsong/grimoire/internal/ollama"
	"github.com/alanhakhyeonsong/grimoire/internal/writer"
)

var (
	fmBlockRe = regexp.MustCompile(`(?s)^(---\r?\n)(.*?)(\r?\n---\r?\n?)`)
	fmKeyRe   = regexp.MustCompile(`(?m)^([A-Za-z0-9_-]+)\s*:`)
)

// defaultCore 는 config.frontmatter.core 가 비었을 때의 코어 필드다.
var defaultCore = []string{"title", "date", "type", "tags", "status", "ai_access"}

func coreFields(c *config.Config) []string {
	if len(c.Frontmatter.Core) > 0 {
		return c.Frontmatter.Core
	}
	return defaultCore
}

// ---------- Lint (Ollama-free) ----------

// LintFinding 은 한 노트의 건강검진 결과다.
type LintFinding struct {
	Path   string   `json:"path"`
	Issues []string `json:"issues"`
}

// LintReport 는 전체 건강검진 집계다.
type LintReport struct {
	Notes             int           `json:"notes"`
	NoFrontmatter     int           `json:"noFrontmatter"`
	MissingCoreFields int           `json:"missingCoreFields"`
	DanglingLinks     int           `json:"danglingLinks"`
	Findings          []LintFinding `json:"findings"`
}

// Lint 는 인덱스의 모든 노트를 구조적으로 점검한다(생성형 모델 불필요).
func Lint(c *config.Config, db *index.DB, limit int) (*LintReport, error) {
	paths, err := db.AllNotePaths()
	if err != nil {
		return nil, err
	}
	links, err := db.AllLinks()
	if err != nil {
		return nil, err
	}

	// 알려진 링크 타깃 집합(전체 경로 + 확장자 제외 basename + 경로에서 .md 제거)
	known := make(map[string]bool, len(paths)*2)
	for _, p := range paths {
		known[p] = true
		known[strings.TrimSuffix(p, ".md")] = true
		known[strings.TrimSuffix(filepath.Base(p), ".md")] = true
	}
	dangBySrc := make(map[string][]string)
	for _, e := range links {
		dst := strings.TrimSpace(e[1])
		if dst == "" || known[dst] || known[strings.TrimSuffix(dst, ".md")] {
			continue
		}
		dangBySrc[e[0]] = append(dangBySrc[e[0]], dst)
	}

	core := coreFields(c)
	rep := &LintReport{Notes: len(paths)}
	for _, rel := range paths {
		if boundary.IsPrivateDir(rel, c) {
			continue // 안전망: 차단경로는 검진 대상에서도 제외
		}
		var issues []string

		raw, rerr := os.ReadFile(filepath.Join(c.KB.Root, rel))
		if rerr == nil {
			_, fm := frontmatter.Parse(string(raw), rel, 0, c)
			if len(fm) == 0 {
				issues = append(issues, "no-frontmatter")
				rep.NoFrontmatter++
			}
			missing := 0
			for _, k := range core {
				if _, ok := fm[k]; !ok {
					issues = append(issues, "missing:"+k)
					missing++
				}
			}
			if missing > 0 {
				rep.MissingCoreFields++
			}
		} else {
			issues = append(issues, "unreadable")
		}

		for _, d := range dangBySrc[rel] {
			issues = append(issues, "dangling-link:"+d)
			rep.DanglingLinks++
		}

		if len(issues) > 0 && (limit <= 0 || len(rep.Findings) < limit) {
			rep.Findings = append(rep.Findings, LintFinding{Path: rel, Issues: issues})
		}
	}
	return rep, nil
}

// ---------- Suggest (Ollama 제안) ----------

// Proposal 은 Ollama 가 제안한 frontmatter 후보다(기록 전, 검수 대상).
type Proposal struct {
	Title   string   `json:"title"`
	Type    string   `json:"type"`
	Tags    []string `json:"tags"`
	Summary string   `json:"summary"`
}

const suggestSystem = "당신은 마크다운 지식베이스의 메타데이터 사서다. " +
	"주어진 노트 본문을 읽고 frontmatter 후보를 JSON 으로만 제안한다. " +
	"설명/코드펜스 없이 JSON 객체 하나만 출력한다."

// Suggest 는 노트 본문을 Ollama 에 보내 frontmatter 후보를 제안받는다.
// 파일을 수정하지 않는다("제안" 단계). 차단 경로는 거부한다.
func Suggest(ctx context.Context, c *config.Config, oll *ollama.Client, rel string) (*Proposal, error) {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("잘못된 경로입니다: %s", rel)
	}
	if boundary.IsPrivateDir(rel, c) {
		return nil, fmt.Errorf("차단 경로는 컴파일러에 전달하지 않습니다: %s", rel)
	}
	raw, err := os.ReadFile(filepath.Join(c.KB.Root, rel))
	if err != nil {
		return nil, fmt.Errorf("노트를 읽을 수 없습니다: %s", rel)
	}

	enum := strings.Join(typeEnum(c), ", ")
	body := string(raw)
	if len(body) > 4000 {
		body = body[:4000]
	}
	prompt := fmt.Sprintf(
		"다음 노트의 frontmatter 후보를 제안하라.\n"+
			"- title: 본문 핵심을 담은 짧은 제목\n"+
			"- type: 다음 중 하나만 [%s]\n"+
			"- tags: 소문자 kebab 키워드 2~5개 배열\n"+
			"- summary: 한 문장 요약\n"+
			"출력은 {\"title\":\"\",\"type\":\"\",\"tags\":[],\"summary\":\"\"} 형식의 JSON 하나.\n\n"+
			"=== 노트(%s) ===\n%s",
		enum, rel, body)

	out, err := oll.Generate(ctx, suggestSystem, prompt, true)
	if err != nil {
		return nil, err
	}
	var p Proposal
	if err := json.Unmarshal([]byte(stripFence(out)), &p); err != nil {
		return nil, fmt.Errorf("ollama 제안 JSON 파싱 실패: %w (원문: %.120s)", err, out)
	}
	return &p, nil
}

func typeEnum(c *config.Config) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range c.Taxonomy.Directories {
		if m.Type != "" && !seen[m.Type] {
			seen[m.Type] = true
			out = append(out, m.Type)
		}
	}
	return out
}

// stripFence 는 혹시 모를 ```json ... ``` 코드펜스를 벗긴다.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

// ---------- Apply (gap-fill 기록) ----------

// Field 는 frontmatter 에 채울 한 줄(키/이미 포맷된 YAML 값)이다.
type Field struct {
	Key   string
	Value string // 예: `"제목"`, `[a, b]`, `2026-05-30`
}

// ApplyResult 는 백필 기록 결과다.
type ApplyResult struct {
	Path         string   `json:"path"`
	Added        []string `json:"added"`
	Skipped      []string `json:"skipped"`
	CreatedBlock bool     `json:"createdBlock"`
}

// BuildFields 는 제안 + 파생 기본값으로 코어 frontmatter 후보를 만든다.
// 값은 YAML 스칼라로 포맷된다. 실제 삽입은 Apply 가 "누락분만" 수행한다.
func BuildFields(c *config.Config, rel string, p *Proposal) []Field {
	meta, hasDir := frontmatter.DirMetaFor(rel, c)
	name := strings.TrimSuffix(filepath.Base(rel), ".md")

	var fields []Field
	add := func(k, v string) {
		if v != "" {
			fields = append(fields, Field{Key: k, Value: v})
		}
	}

	if p != nil && p.Title != "" {
		add("title", yamlString(p.Title))
	}
	if d := frontmatter.ExtractDateFromName(name); d != "" {
		add("date", d) // 파일명에서 유추 가능할 때만(날짜를 임의 생성하지 않음)
	}
	typ := meta.Type
	if p != nil && p.Type != "" {
		typ = p.Type
	}
	add("type", typ)
	if p != nil && len(p.Tags) > 0 {
		add("tags", "["+strings.Join(p.Tags, ", ")+"]")
	} else if meta.Domain != "" {
		add("tags", "["+meta.Domain+"]")
	}
	status := c.Frontmatter.Defaults["status"]
	if status == "" {
		status = "active"
	}
	add("status", status)
	access := meta.AIAccess
	if access == "" {
		if hasDir {
			access = c.Frontmatter.Defaults["ai_access"]
			if access == "" {
				access = "shared"
			}
		} else {
			// taxonomy 미등록 dir = fail-safe: 백필로 shared 를 박지 않는다.
			access = "private"
		}
	}
	add("ai_access", access)
	return fields
}

func yamlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// Apply 는 기존 frontmatter 키를 절대 덮어쓰지 않고 누락된 키만 삽입한다.
// frontmatter 블록이 없으면 새로 만들어 본문 앞에 붙인다. atomic write 사용.
func Apply(c *config.Config, rel string, fields []Field) (*ApplyResult, error) {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("잘못된 경로입니다: %s", rel)
	}
	if boundary.IsPrivateDir(rel, c) {
		return nil, fmt.Errorf("차단 경로는 백필 대상이 아닙니다: %s", rel)
	}
	abs := filepath.Join(c.KB.Root, rel)
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("노트를 읽을 수 없습니다: %s", rel)
	}
	text := string(raw)
	res := &ApplyResult{Path: rel}

	if m := fmBlockRe.FindStringSubmatch(text); m != nil {
		// 기존 블록: 현재 키 집합 파악 후 누락 키만 닫는 --- 직전에 삽입
		present := map[string]bool{}
		for _, km := range fmKeyRe.FindAllStringSubmatch(m[2], -1) {
			present[km[1]] = true
		}
		var insert strings.Builder
		for _, f := range fields {
			if present[f.Key] {
				res.Skipped = append(res.Skipped, f.Key)
				continue
			}
			insert.WriteString(f.Key + ": " + f.Value + "\n")
			res.Added = append(res.Added, f.Key)
		}
		if len(res.Added) == 0 {
			return res, nil // 변경 없음 — 파일 미수정
		}
		open, content, close := m[1], m[2], m[3]
		newText := open + content + "\n" + strings.TrimRight(insert.String(), "\n") + close + text[len(m[0]):]
		if err := writer.AtomicWrite(abs, []byte(newText)); err != nil {
			return nil, err
		}
		return res, nil
	}

	// 블록 없음: 새 frontmatter 블록 생성(순수 추가)
	if len(fields) == 0 {
		return res, nil
	}
	var b strings.Builder
	b.WriteString("---\n")
	for _, f := range fields {
		b.WriteString(f.Key + ": " + f.Value + "\n")
		res.Added = append(res.Added, f.Key)
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimLeft(text, "\n"))
	res.CreatedBlock = true
	if err := writer.AtomicWrite(abs, []byte(b.String())); err != nil {
		return nil, err
	}
	return res, nil
}
