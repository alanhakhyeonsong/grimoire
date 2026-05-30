// Package writer 는 write_note(Phase 1)의 핵심 로직이다.
//
// taxonomy 역매핑(type/domain -> dir), 파일명 컨벤션, frontmatter 생성,
// redact 스캔, atomic write(temp -> rename)를 담당한다.
// 인덱스 갱신과 경계(push) 판정은 호출측(main)이 수행한다.
package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Request 는 write_note 입력이다.
type Request struct {
	Title     string
	Content   string
	Type      string
	Domain    string
	Dir       string
	Tags      []string
	Status    string
	Slug      string
	Date      string
	Overwrite bool
}

// Result 는 저장 결과다.
type Result struct {
	Path          string `json:"path"`
	Dir           string `json:"dir"`
	File          string `json:"file"`
	Type          string `json:"type"`
	AIAccess      string `json:"ai_access"`
	RedactScanned bool   `json:"redact_scanned"`
}

// ClassifyError 는 taxonomy 분류 미매칭/모호 시 반환된다(후보 dir 동반).
type ClassifyError struct {
	Msg        string
	Candidates []string
}

func (e *ClassifyError) Error() string {
	return e.Msg + " 후보 dir: " + strings.Join(e.Candidates, ", ")
}

// RedactError 는 redact 대상 dir 에서 사내 식별자 후보가 발견됐을 때 반환된다.
type RedactError struct {
	Dir     string
	Matches []string
}

func (e *RedactError) Error() string {
	return fmt.Sprintf("redact 대상(%s)에서 사내 식별자 후보 발견: %s — 치환 후 다시 시도하세요.",
		e.Dir, strings.Join(e.Matches, ", "))
}

// ExistsError 는 대상 파일이 이미 있고 overwrite 가 아닐 때 반환된다.
type ExistsError struct{ Path string }

func (e *ExistsError) Error() string {
	return fmt.Sprintf("이미 존재합니다: %s (overwrite=true 또는 다른 slug 지정)", e.Path)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func allDirs(c *config.Config) []string {
	out := make([]string, 0, len(c.Taxonomy.Directories))
	for d := range c.Taxonomy.Directories {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// slugify 는 제목/슬러그를 kebab 으로 정규화한다(유니코드 문자는 보존, 한글 포함).
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteRune('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// resolveDir 는 dir 을 확정한다. 미매칭/모호하면 ClassifyError(후보 동반)를 반환한다.
func resolveDir(c *config.Config, req Request) (string, *ClassifyError) {
	if req.Dir != "" {
		if _, ok := c.Taxonomy.Directories[req.Dir]; ok {
			return req.Dir, nil
		}
		return "", &ClassifyError{Msg: fmt.Sprintf("알 수 없는 dir=%q.", req.Dir), Candidates: allDirs(c)}
	}
	if req.Type == "" {
		return "", &ClassifyError{Msg: "dir 또는 type 중 하나는 필요합니다.", Candidates: allDirs(c)}
	}
	var matches []string
	for d, m := range c.Taxonomy.Directories {
		if m.Type != req.Type {
			continue
		}
		if req.Domain != "" && m.Domain != req.Domain {
			continue
		}
		matches = append(matches, d)
	}
	sort.Strings(matches)
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", &ClassifyError{
			Msg:        fmt.Sprintf("type=%q domain=%q 에 매칭되는 dir 가 없습니다.", req.Type, req.Domain),
			Candidates: allDirs(c),
		}
	default:
		return "", &ClassifyError{
			Msg:        fmt.Sprintf("type=%q 가 여러 dir 에 매칭됩니다(domain 으로 좁히거나 dir 를 명시).", req.Type),
			Candidates: matches,
		}
	}
}

// buildFilename 은 naming 규칙에 따라 파일명을 만든다(미지원 규칙은 kebab fallback).
func buildFilename(naming, slug, date string) string {
	switch naming {
	case "date-compact":
		return strings.ReplaceAll(date, "-", "") + "-" + slug + ".md"
	case "date-done":
		return date + "-" + slug + "-done.md"
	default: // kebab, mixed, worklog(특수)·미지정 → kebab
		return slug + ".md"
	}
}

func scanRedact(c *config.Config, text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, pat := range c.Redact.Patterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			continue // 잘못된 패턴은 건너뛴다
		}
		for _, m := range re.FindAllString(text, -1) {
			if m != "" && !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

func escapeYAML(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func buildContent(title, date, typ string, tags []string, status, access, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: \"" + escapeYAML(title) + "\"\n")
	b.WriteString("date: " + date + "\n")
	b.WriteString("type: " + typ + "\n")
	b.WriteString("tags: [" + strings.Join(tags, ", ") + "]\n")
	b.WriteString("status: " + status + "\n")
	b.WriteString("ai_access: " + access + "\n")
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	return b.String()
}

// atomicWrite 는 같은 디렉토리에 temp 로 쓴 뒤 rename 한다(Obsidian 동시편집 대비).
func atomicWrite(abs string, data []byte) error {
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".grim-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 성공 시 no-op
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, abs)
}

// Write 는 분류 → 파일명 → redact → frontmatter → atomic write 를 수행한다.
func Write(c *config.Config, req Request, now time.Time) (*Result, error) {
	dir, cerr := resolveDir(c, req)
	if cerr != nil {
		return nil, cerr
	}
	meta := c.Taxonomy.Directories[dir]

	slug := slugify(firstNonEmpty(req.Slug, req.Title))
	if slug == "" {
		return nil, fmt.Errorf("title 또는 slug 가 필요합니다(파일명 생성 불가)")
	}

	date := req.Date
	if date == "" {
		date = now.Format("2006-01-02")
	} else if !dateRe.MatchString(date) {
		return nil, fmt.Errorf("date 형식은 YYYY-MM-DD 여야 합니다: %q", req.Date)
	}

	file := buildFilename(meta.Naming, slug, date)
	rel := filepath.ToSlash(filepath.Clean(dir + "/" + file))
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("잘못된 경로입니다: %s", rel)
	}

	redactScanned := false
	if meta.Redact {
		redactScanned = true
		if matches := scanRedact(c, req.Title+"\n"+req.Content); len(matches) > 0 {
			return nil, &RedactError{Dir: dir, Matches: matches}
		}
	}

	typ := firstNonEmpty(req.Type, meta.Type)
	tags := req.Tags
	if len(tags) == 0 && meta.Domain != "" {
		tags = []string{meta.Domain}
	}
	status := firstNonEmpty(req.Status, c.Frontmatter.Defaults["status"], "active")
	access := firstNonEmpty(meta.AIAccess, c.Frontmatter.Defaults["ai_access"], "shared")

	abs := filepath.Join(c.KB.Root, rel)
	if _, err := os.Stat(abs); err == nil && !req.Overwrite {
		return nil, &ExistsError{Path: rel}
	}

	content := buildContent(req.Title, date, typ, tags, status, access, req.Content)
	if err := atomicWrite(abs, []byte(content)); err != nil {
		return nil, err
	}

	return &Result{
		Path: rel, Dir: dir, File: file, Type: typ,
		AIAccess: access, RedactScanned: redactScanned,
	}, nil
}
