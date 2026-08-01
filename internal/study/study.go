// Package study 는 강의 학습노트 디렉토리를 스캐폴딩한다.
//
// 학습 정리가 흐지부지되는 이유는 대개 의지가 아니라 구조다. 매번 폴더를 어떻게
// 나눌지, 무엇을 적을지 다시 정하다 보면 노트마다 형식이 달라지고 나중에 검색도
// 안 된다. 여기서는 "강의 1개 = 디렉토리 1개"와 고정 관점(렌즈)을 틀로 박아,
// 명령 한 줄로 같은 모양의 학습 공간이 생기게 한다.
//
// 핵심 규칙 두 가지:
//   - 강의 요약(notes/)과 스스로 판 심화(deep-dive/)를 섞지 않는다.
//   - 모든 요약은 config 로 정한 렌즈를 빠짐없이 거친다(강의가 안 짚었으면
//     "해당 없음" 사유라도 남긴다).
package study

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/writer"
)

// Request 는 새 학습노트 생성 요청이다.
type Request struct {
	Course     string   `json:"course"`               // 강의/주제 제목 (필수)
	Slug       string   `json:"slug,omitempty"`       // 디렉토리명 (미지정 시 Course 에서 생성)
	Platform   string   `json:"platform,omitempty"`   // 인프런/Udemy/자가 학습 등
	Instructor string   `json:"instructor,omitempty"` // 강사
	URL        string   `json:"url,omitempty"`
	Goal       string   `json:"goal,omitempty"`     // 왜 듣는가
	Sections   []string `json:"sections,omitempty"` // 알고 있다면 섹션 목록
}

// Result 는 생성 결과다.
type Result struct {
	Dir     string   `json:"dir"`     // KB 루트 기준 상대경로
	Index   string   `json:"index"`   // 생성된 README 경로
	Created []string `json:"created"` // 만든 경로들
	Lenses  []string `json:"lenses"`  // 적용된 관점
}

// ExistsError 는 같은 이름의 학습 디렉토리가 이미 있을 때다.
type ExistsError struct{ Dir string }

func (e *ExistsError) Error() string {
	return fmt.Sprintf("이미 존재하는 학습 디렉토리입니다: %s", e.Dir)
}

// slugify 는 제목을 디렉토리명으로 바꾼다(한글은 보존한다).
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteRune('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// StudyDir 은 학습노트 루트를 정한다.
// config 의 study.dir 이 우선이고, 없으면 taxonomy 에서 type:study 를 찾는다.
func StudyDir(c *config.Config) (string, error) {
	if d := strings.TrimSpace(c.Study.Dir); d != "" {
		return d, nil
	}
	var found []string
	for dir, meta := range c.Taxonomy.Directories {
		if meta.Type == "study" {
			found = append(found, dir)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("학습노트 디렉토리를 찾을 수 없습니다. config 의 study.dir 을 지정하거나 taxonomy 에 type:study 디렉토리를 두세요")
	default:
		return "", fmt.Errorf("type:study 디렉토리가 여러 개입니다(%s). config 의 study.dir 로 하나를 지정하세요", strings.Join(found, ", "))
	}
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// Scaffold 는 학습 디렉토리와 인덱스 문서를 만든다.
func Scaffold(c *config.Config, req Request, now time.Time) (*Result, error) {
	if strings.TrimSpace(req.Course) == "" {
		return nil, fmt.Errorf("course(강의/주제 제목)는 필수입니다")
	}

	base, err := StudyDir(c)
	if err != nil {
		return nil, err
	}

	slug := nonEmpty(req.Slug, slugify(req.Course))
	if slug == "" {
		return nil, fmt.Errorf("slug 를 만들 수 없습니다. slug 를 직접 지정하세요")
	}

	relDir := filepath.ToSlash(filepath.Join(base, slug))
	absDir := filepath.Join(c.KB.Root, filepath.FromSlash(relDir))
	if _, statErr := os.Stat(absDir); statErr == nil {
		return nil, &ExistsError{Dir: relDir}
	}

	notesName := nonEmpty(c.Study.NotesDir, "notes")
	deepName := nonEmpty(c.Study.DeepDiveDir, "deep-dive")

	res := &Result{Dir: relDir}
	for _, sub := range []string{notesName, deepName} {
		p := filepath.Join(absDir, sub)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
		res.Created = append(res.Created, filepath.ToSlash(filepath.Join(relDir, sub)))
	}

	lenses := c.StudyLenses()
	for _, l := range lenses {
		res.Lenses = append(res.Lenses, l.Name)
	}

	content := renderIndex(c, req, notesName, deepName, lenses, now)
	indexRel := filepath.ToSlash(filepath.Join(relDir, "README.md"))
	if err := writer.AtomicWrite(filepath.Join(absDir, "README.md"), []byte(content)); err != nil {
		return nil, err
	}
	res.Index = indexRel
	res.Created = append(res.Created, indexRel)
	return res, nil
}

func yamlEscape(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// renderIndex 는 강의 인덱스(README.md) 본문을 만든다.
func renderIndex(c *config.Config, req Request, notesName, deepName string, lenses []config.StudyLens, now time.Time) string {
	var b strings.Builder

	access := c.Frontmatter.Defaults["ai_access"]
	if access == "" {
		access = "shared"
	}

	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: \"%s\"\n", yamlEscape(req.Course))
	b.WriteString("type: study\n")
	b.WriteString("tags: [study]\n")
	b.WriteString("status: active\n")
	fmt.Fprintf(&b, "ai_access: %s\n", access)
	fmt.Fprintf(&b, "date: %s\n", now.Format("2006-01-02"))
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", req.Course)

	b.WriteString("## 메타\n\n")
	fmt.Fprintf(&b, "- **플랫폼**: %s\n", nonEmpty(req.Platform, "(미정)"))
	fmt.Fprintf(&b, "- **강사**: %s\n", nonEmpty(req.Instructor, "(미정)"))
	fmt.Fprintf(&b, "- **URL**: %s\n", nonEmpty(req.URL, "(없음)"))
	fmt.Fprintf(&b, "- **학습 목표**: %s\n", nonEmpty(req.Goal, "(왜 듣는가, 무엇을 얻고 싶은가)"))
	fmt.Fprintf(&b, "- **시작일**: %s\n", now.Format("2006-01-02"))
	b.WriteString("- **진행률**: 0%\n")
	b.WriteString("- **총평**: (완강 후 작성)\n\n")

	b.WriteString("## 구조 규칙\n\n")
	fmt.Fprintf(&b, "- `%s/<NN-섹션>/<NN-강의>.md` : 강의 1개 = 파일 1개. 섹션은 디렉토리로 묶는다.\n", notesName)
	fmt.Fprintf(&b, "- `%s/<주제>.md` : 강의 범위를 넘어 직접 판 주제. 강의 요약과 섞지 않는다.\n", deepName)
	fmt.Fprintf(&b, "- 각 강의 노트 끝에 추가학습 후보를 적어두고, 실제로 파면 `%s/` 로 승격한다.\n\n", deepName)

	fmt.Fprintf(&b, "### 노트 작성 고정 관점 (%d개, 항상 포함)\n\n", len(lenses))
	b.WriteString("모든 강의 요약은 아래 관점을 빠짐없이 거친다. 강의가 짚지 않았어도 빈 칸으로 두지 말고 \"해당 없음\" 사유라도 남긴다.\n\n")
	for i, l := range lenses {
		fmt.Fprintf(&b, "%d. **%s**: %s\n", i+1, l.Name, l.Detail)
	}
	b.WriteString("\n> 매 강의 노트의 take-away 절에서 위 관점이 한 번씩은 드러나야 한다.\n\n")

	fmt.Fprintf(&b, "## 섹션 인덱스 (%s/)\n\n", notesName)
	if len(req.Sections) > 0 {
		for i, s := range req.Sections {
			fmt.Fprintf(&b, "- [ ] %02d - %s\n", i+1, s)
		}
	} else {
		b.WriteString("학습하며 한 줄씩 채운다.\n\n")
		b.WriteString("- [ ] 01 - <섹션명>\n")
		fmt.Fprintf(&b, "  - [ ] 01 - <강의명> → `%s/01-<섹션>/01-....md`\n", notesName)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## 추가로 판 것 (%s/)\n\n", deepName)
	b.WriteString("강의가 다루지 않았거나 더 깊게 확인하려고 직접 판 주제만 적는다.\n\n")
	fmt.Fprintf(&b, "- [ ] <주제> → `%s/....md`\n\n", deepName)

	b.WriteString("## 한 줄 회고\n\n")
	b.WriteString("- (완강 또는 중단 시점에 배운 것 / 계속할 것 / 문제 / 다음 시도)\n")

	return b.String()
}
