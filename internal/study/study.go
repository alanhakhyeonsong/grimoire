// Package study 는 학습노트 디렉토리를 스캐폴딩한다.
//
// 학습 정리가 흐지부지되는 이유는 대개 의지가 아니라 구조다. 매번 폴더를 어떻게
// 나눌지, 무엇을 적을지 다시 정하다 보면 노트마다 형식이 달라지고 나중에 검색도
// 안 된다. 여기서는 "학습 주제 1개 = 디렉토리 1개"와 고정 관점(렌즈)을 틀로 박아,
// 명령 한 줄로 같은 모양의 학습 공간이 생기게 한다.
//
// 학습 자료는 강의만이 아니다. 기술서적, 공식 문서, AI 와의 대화, 순수 자가 탐구가
// 모두 섞인다. 강의 전제로만 틀을 만들면 나머지는 "플랫폼/강사" 같은 빈 칸을 안고
// 시작하게 되고, 실제로 그런 노트는 메타가 통째로 비어 버린다. 그래서 자료 종류
// (Kind)에 따라 메타 항목과 하위 구조 문구를 바꾼다.
//
// 핵심 규칙 두 가지:
//   - 요약(notes/)과 스스로 판 심화(deep-dive/)를 섞지 않는다.
//   - 모든 요약은 config 로 정한 렌즈를 빠짐없이 거친다(자료가 안 짚었으면
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

// Kind 는 학습 자료의 종류다. 메타 항목과 하위 구조 문구를 결정한다.
type Kind string

const (
	KindCourse Kind = "course" // 인프런/Udemy 등 강의
	KindBook   Kind = "book"   // 기술서적
	KindAI     Kind = "ai"     // AI 와의 대화로 학습
	KindDocs   Kind = "docs"   // 공식 문서/스펙
	KindSelf   Kind = "self"   // 자가 탐구(실습·삽질 포함)
)

// NormalizeKind 는 입력값을 Kind 로 정규화한다.
// 미지정이면 KindSelf 다. 강의가 아닌 학습이 더 흔하므로 강의를 기본값으로
// 두면 대부분의 노트가 쓰지 않을 메타(플랫폼/강사)를 안고 시작하게 된다.
func NormalizeKind(s string) Kind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "course", "lecture", "강의", "인프런", "udemy":
		return KindCourse
	case "book", "책", "기술서적", "서적":
		return KindBook
	case "ai", "llm", "chatgpt", "claude", "gpt":
		return KindAI
	case "docs", "doc", "documentation", "spec", "공식문서", "문서":
		return KindDocs
	case "", "self", "self-study", "자가", "자가학습", "자가 학습":
		return KindSelf
	default:
		return KindSelf
	}
}

// Request 는 새 학습노트 생성 요청이다.
type Request struct {
	Topic string `json:"topic"` // 학습 주제/자료 제목 (필수)
	Kind  string `json:"kind,omitempty"`
	Slug  string `json:"slug,omitempty"` // 디렉토리명 (미지정 시 Topic 에서 생성)

	// Source 는 자료 출처다. 종류에 따라 의미가 다르다.
	// 강의=플랫폼, 책=출판사, AI=모델/도구, 문서=문서명, 자가=참고한 것.
	Source string `json:"source,omitempty"`
	// Author 는 강사/저자다(AI·자가 탐구에는 대개 없다).
	Author string `json:"author,omitempty"`

	URL     string `json:"url,omitempty"`
	Version string `json:"version,omitempty"` // 문서/도구 대상 버전
	Goal    string `json:"goal,omitempty"`    // 왜 배우는가

	// Units 는 커리큘럼 단위다. 강의=섹션, 책=장, 그 외=다룰 주제.
	Units []string `json:"units,omitempty"`

	// 아래는 v0.3.0 필드명 하위호환용이다. 신규 필드가 비었을 때만 쓰인다.
	Course     string   `json:"course,omitempty"`
	Platform   string   `json:"platform,omitempty"`
	Instructor string   `json:"instructor,omitempty"`
	Sections   []string `json:"sections,omitempty"`
}

// normalize 는 하위호환 필드를 신규 필드로 접는다.
func (r Request) normalize() Request {
	r.Topic = firstNonEmpty(r.Topic, r.Course)
	r.Source = firstNonEmpty(r.Source, r.Platform)
	r.Author = firstNonEmpty(r.Author, r.Instructor)
	if len(r.Units) == 0 {
		r.Units = r.Sections
	}

	// kind 를 지정하지 않았는데 강의 전용 필드(플랫폼/강사)가 왔다면 강의로 본다.
	// 그 칸을 채웠다는 것 자체가 강의라는 뜻이고, 기본값(self)으로 떨어뜨리면
	// 강사 항목이 표시되지 않아 입력한 정보가 조용히 사라진다.
	if strings.TrimSpace(r.Kind) == "" &&
		(strings.TrimSpace(r.Platform) != "" || strings.TrimSpace(r.Instructor) != "") {
		r.Kind = string(KindCourse)
	}
	return r
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// profile 은 Kind 별 표시 규칙이다.
type profile struct {
	Label       string // 자료 종류 표시명
	SourceLabel string // Source 필드의 이름
	AuthorLabel string // Author 필드의 이름(빈 값이면 표시하지 않음)
	OuterUnit   string // 상위 단위(섹션/장)
	InnerUnit   string // 하위 단위(강의/절). 빈 값이면 평면 구조
	UnitsTitle  string // 진행 인덱스 절 제목
	Caution     string // 이 종류에서 특히 주의할 점
}

func profileFor(k Kind) profile {
	switch k {
	case KindCourse:
		return profile{
			Label: "강의", SourceLabel: "플랫폼", AuthorLabel: "강사",
			OuterUnit: "섹션", InnerUnit: "강의", UnitsTitle: "섹션 인덱스",
		}
	case KindBook:
		return profile{
			Label: "기술서적", SourceLabel: "출판사", AuthorLabel: "저자",
			OuterUnit: "장", InnerUnit: "절", UnitsTitle: "목차",
			Caution: "책의 서술 순서를 그대로 옮기지 말고, 내 문제에 닿는 부분부터 정리한다.",
		}
	case KindAI:
		return profile{
			Label: "AI 대화 기반 학습", SourceLabel: "사용 도구/모델", AuthorLabel: "",
			OuterUnit: "주제", InnerUnit: "", UnitsTitle: "다룰 주제",
			Caution: "AI 답변은 그럴듯하게 틀릴 수 있다. 아래 '출처 확인'에서 1차 자료로 검증한 항목만 사실로 남긴다.",
		}
	case KindDocs:
		return profile{
			Label: "공식 문서", SourceLabel: "문서", AuthorLabel: "",
			OuterUnit: "주제", InnerUnit: "", UnitsTitle: "다룰 주제",
			Caution: "문서는 버전에 따라 달라진다. 대상 버전을 적고, 나중에 볼 때 버전을 먼저 확인한다.",
		}
	default:
		return profile{
			Label: "자가 학습", SourceLabel: "참고 자료", AuthorLabel: "",
			OuterUnit: "주제", InnerUnit: "", UnitsTitle: "다룰 주제",
			Caution: "직접 확인한 것과 추측을 구분해 적는다. 실습으로 확인했으면 그 사실을 남긴다.",
		}
	}
}

// Result 는 생성 결과다.
type Result struct {
	Dir     string   `json:"dir"`     // KB 루트 기준 상대경로
	Index   string   `json:"index"`   // 생성된 README 경로
	Created []string `json:"created"` // 만든 경로들
	Lenses  []string `json:"lenses"`  // 적용된 관점
	Kind    string   `json:"kind"`    // 적용된 자료 종류
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
	req = req.normalize()
	if strings.TrimSpace(req.Topic) == "" {
		return nil, fmt.Errorf("topic(학습 주제/자료 제목)은 필수입니다")
	}

	base, err := StudyDir(c)
	if err != nil {
		return nil, err
	}

	slug := nonEmpty(req.Slug, slugify(req.Topic))
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

	kind := NormalizeKind(req.Kind)
	res.Kind = string(kind)

	content := renderIndex(c, req, kind, notesName, deepName, lenses, now)
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

// renderIndex 는 학습 인덱스(README.md) 본문을 만든다.
func renderIndex(c *config.Config, req Request, kind Kind, notesName, deepName string, lenses []config.StudyLens, now time.Time) string {
	p := profileFor(kind)
	var b strings.Builder

	access := c.Frontmatter.Defaults["ai_access"]
	if access == "" {
		access = "shared"
	}

	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: \"%s\"\n", yamlEscape(req.Topic))
	b.WriteString("type: study\n")
	fmt.Fprintf(&b, "tags: [study, %s]\n", kind)
	b.WriteString("status: active\n")
	fmt.Fprintf(&b, "ai_access: %s\n", access)
	fmt.Fprintf(&b, "date: %s\n", now.Format("2006-01-02"))
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", req.Topic)

	// 메타: 종류에 따라 의미 없는 항목은 아예 넣지 않는다.
	// 빈 칸이 많은 템플릿은 결국 아무도 채우지 않는다.
	b.WriteString("## 메타\n\n")
	fmt.Fprintf(&b, "- **학습 자료**: %s\n", p.Label)
	fmt.Fprintf(&b, "- **%s**: %s\n", p.SourceLabel, nonEmpty(req.Source, "(미정)"))
	if p.AuthorLabel != "" {
		fmt.Fprintf(&b, "- **%s**: %s\n", p.AuthorLabel, nonEmpty(req.Author, "(미정)"))
	}
	if req.URL != "" {
		fmt.Fprintf(&b, "- **URL**: %s\n", req.URL)
	}
	if kind == KindDocs || req.Version != "" {
		fmt.Fprintf(&b, "- **대상 버전**: %s\n", nonEmpty(req.Version, "(확인 필요)"))
	}
	fmt.Fprintf(&b, "- **학습 목표**: %s\n", nonEmpty(req.Goal, "(왜 배우는가, 무엇을 얻고 싶은가)"))
	fmt.Fprintf(&b, "- **시작일**: %s\n", now.Format("2006-01-02"))
	b.WriteString("- **진행률**: 0%\n")
	b.WriteString("- **총평**: (마무리 시점에 작성)\n\n")

	// 구조 규칙
	b.WriteString("## 구조 규칙\n\n")
	if p.InnerUnit != "" {
		fmt.Fprintf(&b, "- `%s/<NN-%s>/<NN-%s>.md` : %s 1개 = 파일 1개. %s은 디렉토리로 묶는다.\n",
			notesName, p.OuterUnit, p.InnerUnit, p.InnerUnit, p.OuterUnit)
	} else {
		fmt.Fprintf(&b, "- `%s/<%s>.md` : %s 1개 = 파일 1개. 순서가 있으면 `NN-` 접두사를 붙인다.\n",
			notesName, p.OuterUnit, p.OuterUnit)
	}
	fmt.Fprintf(&b, "- `%s/<주제>.md` : 자료 범위를 넘어 직접 판 주제. 요약과 섞지 않는다.\n", deepName)
	fmt.Fprintf(&b, "- 각 노트 끝에 추가학습 후보를 적어두고, 실제로 파면 `%s/` 로 승격한다.\n", deepName)
	if p.Caution != "" {
		fmt.Fprintf(&b, "\n> %s\n", p.Caution)
	}
	b.WriteString("\n")

	// 고정 관점
	fmt.Fprintf(&b, "### 노트 작성 고정 관점 (%d개, 항상 포함)\n\n", len(lenses))
	b.WriteString("모든 요약은 아래 관점을 빠짐없이 거친다. 자료가 짚지 않았어도 빈 칸으로 두지 말고 \"해당 없음\" 사유라도 남긴다.\n\n")
	for i, l := range lenses {
		fmt.Fprintf(&b, "%d. **%s**: %s\n", i+1, l.Name, l.Detail)
	}
	b.WriteString("\n> 매 노트의 take-away 절에서 위 관점이 한 번씩은 드러나야 한다.\n\n")

	// AI 학습은 검증이 본체다. 확인 안 된 내용을 사실로 굳히지 않게 별도 절을 둔다.
	if kind == KindAI {
		b.WriteString("## 출처 확인 (AI 학습 필수)\n\n")
		b.WriteString("AI 가 알려준 내용 중 **1차 자료로 확인한 것만** 사실로 남긴다. 확인 전에는 \"미검증\"으로 표시한다.\n\n")
		b.WriteString("| 주장 | 확인 방법(공식 문서·소스·실습) | 상태 |\n")
		b.WriteString("|---|---|---|\n")
		b.WriteString("| <AI 가 말한 것> | <무엇으로 확인했는가> | 미검증 / 확인됨 / 틀림 |\n\n")
		b.WriteString("> \"틀림\"으로 판명된 항목도 지우지 말고 남긴다. 같은 착각을 반복하지 않기 위한 기록이다.\n\n")
	}

	// 진행 인덱스
	fmt.Fprintf(&b, "## %s (%s/)\n\n", p.UnitsTitle, notesName)
	if len(req.Units) > 0 {
		for i, s := range req.Units {
			fmt.Fprintf(&b, "- [ ] %02d - %s\n", i+1, s)
		}
	} else {
		b.WriteString("학습하며 한 줄씩 채운다.\n\n")
		if p.InnerUnit != "" {
			fmt.Fprintf(&b, "- [ ] 01 - <%s명>\n", p.OuterUnit)
			fmt.Fprintf(&b, "  - [ ] 01 - <%s명> → `%s/01-<%s>/01-....md`\n", p.InnerUnit, notesName, p.OuterUnit)
		} else {
			fmt.Fprintf(&b, "- [ ] <%s> → `%s/....md`\n", p.OuterUnit, notesName)
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## 추가로 판 것 (%s/)\n\n", deepName)
	b.WriteString("자료가 다루지 않았거나 더 깊게 확인하려고 직접 판 주제만 적는다.\n\n")
	fmt.Fprintf(&b, "- [ ] <주제> → `%s/....md`\n\n", deepName)

	b.WriteString("## 한 줄 회고\n\n")
	b.WriteString("- (마무리 또는 중단 시점에 배운 것 / 계속할 것 / 문제 / 다음 시도)\n")

	return b.String()
}
