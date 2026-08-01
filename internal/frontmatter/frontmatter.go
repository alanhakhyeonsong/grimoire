// Package frontmatter 는 마크다운 frontmatter 파싱과 fallback 추론을 담당한다.
//
// frontmatter 가 있으면 활용하고, 없거나 필드가 누락되면
// 경로(taxonomy 카탈로그) / 파일명(날짜) / 본문(첫 H1)에서 추론한다.
package frontmatter

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"gopkg.in/yaml.v3"
)

const summaryMax = 200

// Note 는 인덱싱된 노트 1건이다(Path 는 KB 루트 기준 상대경로).
type Note struct {
	Path     string
	Title    string
	Type     string
	Domain   string
	Tags     []string
	Status   string
	AIAccess string
	Date     string
	Summary  string
	Body     string
	Mtime    int64
	Inferred bool // frontmatter 가 없어 추론했는지
	Links    []string

	// Unclassified 는 이 노트가 taxonomy 미등록 디렉토리에 있는지를 뜻한다.
	// 미등록 = fail-safe private 이므로 인덱스에서 배제되는데, 그 배제가
	// "의도된 차단(locked_dirs / ai_access:private 명시)"인지 "분류 규약을
	// 갱신하지 않아 생긴 사고"인지 호출측이 구분하려면 이 근거가 필요하다.
	// (미등록이어도 frontmatter 에 ai_access: shared 를 명시하면 노출되므로
	//  Unclassified 는 "배제됐다"가 아니라 "분류 미상"만을 뜻한다.)
	Unclassified bool
}

var (
	dateRe       = regexp.MustCompile(`(\d{4})[-_]?(\d{2})[-_]?(\d{2})`) // YYYYMMDD 또는 YYYY-MM-DD
	ymRe         = regexp.MustCompile(`(\d{4})[-_](\d{2})`)              // YYYY-MM (구분자 필수)
	fmRe         = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n?`)
	h1Re         = regexp.MustCompile(`(?m)^#\s+(.+)$`)
	linkRe       = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	codeRe       = regexp.MustCompile("(?s)```.*?```")
	inlineCodeRe = regexp.MustCompile("`[^`\n]+`")
	headRe       = regexp.MustCompile(`(?m)^#.*$`)
	wsRe         = regexp.MustCompile(`\s+`)
)

// DirMetaFor 는 경로에 해당하는 디렉토리 메타를 반환한다(가장 긴 매칭 우선).
func DirMetaFor(rel string, c *config.Config) (config.DirMeta, bool) {
	n := strings.ReplaceAll(rel, "\\", "/")
	dirs := make([]string, 0, len(c.Taxonomy.Directories))
	for d := range c.Taxonomy.Directories {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		if n == d || strings.HasPrefix(n, d+"/") {
			return c.Taxonomy.Directories[d], true
		}
	}
	return config.DirMeta{}, false
}

// ExtractDateFromName 은 파일명에서 날짜를 추출한다(YYYYMMDD / YYYY-MM-DD / YYYY-MM).
func ExtractDateFromName(name string) string {
	if m := dateRe.FindStringSubmatch(name); m != nil {
		return m[1] + "-" + m[2] + "-" + m[3]
	}
	if m := ymRe.FindStringSubmatch(name); m != nil {
		return m[1] + "-" + m[2]
	}
	return ""
}

func getStr(m map[string]any, k string) string {
	v, ok := m[k]
	if !ok {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case fmt.Stringer:
		return s.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func getTags(m map[string]any) []string {
	v, ok := m["tags"]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, fmt.Sprintf("%v", e))
	}
	return out
}

func firstHeading(body string) string {
	if m := h1Re.FindStringSubmatch(body); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func buildSummary(body string) string {
	s := headRe.ReplaceAllString(body, "")
	s = codeRe.ReplaceAllString(s, "")
	s = wsRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > summaryMax {
		r = r[:summaryMax]
	}
	return string(r)
}

func extractLinks(body string) []string {
	// 코드블록(펜스 ```...``` / 인라인 `...`) 안의 [[ ]] 는 위키링크가 아니라
	// bash `[[ -d $dir ]]` 같은 테스트 구문일 수 있으므로 제거 후 추출한다.
	cleaned := codeRe.ReplaceAllString(body, "")
	cleaned = inlineCodeRe.ReplaceAllString(cleaned, "")
	ms := linkRe.FindAllStringSubmatch(cleaned, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		t := m[1]
		t = strings.SplitN(t, "|", 2)[0]
		t = strings.SplitN(t, "#", 2)[0]
		out = append(out, strings.TrimSpace(t))
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Parse 는 raw 마크다운을 파싱해 Note 와 원본 frontmatter 맵을 반환한다.
func Parse(raw, rel string, mtime int64, c *config.Config) (Note, map[string]any) {
	fm := map[string]any{}
	body := raw
	if m := fmRe.FindStringSubmatch(raw); m != nil {
		_ = yaml.Unmarshal([]byte(m[1]), &fm)
		body = raw[len(m[0]):]
	}
	hasFm := len(fm) > 0

	dir, hasDir := DirMetaFor(rel, c)
	name := strings.TrimSuffix(filepath.Base(rel), ".md")

	dirType, dirDomain, dirAccess := "note", "misc", ""
	if hasDir {
		dirType, dirDomain, dirAccess = dir.Type, dir.Domain, dir.AIAccess
	} else {
		// taxonomy 미등록 디렉토리 = 분류 미상. fail-safe 로 기본 private 취급한다.
		// 새 폴더를 추가하며 ai_access 를 명시하지 않아도 검색/인덱스에 새지 않는다.
		// 노출하려면 노트 frontmatter 에 ai_access: shared 를 명시(opt-in)한다.
		dirAccess = "private"
	}

	tags := getTags(fm)
	if tags == nil {
		if hasDir {
			tags = []string{dir.Domain}
		} else {
			tags = []string{}
		}
	}

	date := getStr(fm, "date")
	if len(date) > 10 {
		date = date[:10]
	}
	if date == "" {
		date = ExtractDateFromName(name)
	}

	return Note{
		Path:     rel,
		Title:    firstNonEmpty(getStr(fm, "title"), firstHeading(body), name),
		Type:     firstNonEmpty(getStr(fm, "type"), dirType),
		Domain:   firstNonEmpty(getStr(fm, "domain"), dirDomain),
		Tags:     tags,
		Status:   firstNonEmpty(getStr(fm, "status"), c.Frontmatter.Defaults["status"], "active"),
		AIAccess: firstNonEmpty(getStr(fm, "ai_access"), dirAccess, c.Frontmatter.Defaults["ai_access"], "shared"),
		Date:     date,
		Summary:  buildSummary(body),
		Body:     body,
		Mtime:    mtime,
		Inferred: !hasFm,
		Links:    extractLinks(body),

		Unclassified: !hasDir,
	}, fm
}
