package study_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/study"
)

// newCfg 는 임시 KB 를 만들고 study 디렉토리를 taxonomy 에 등록한 설정을 준다.
func newCfg(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "personal", "study"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{}
	c.KB.Root = root
	c.Taxonomy.Directories = map[string]config.DirMeta{
		"personal/study": {Type: "study", Domain: "study", Naming: "kebab", AIAccess: "shared"},
	}
	c.Frontmatter.Defaults = map[string]string{"ai_access": "shared", "status": "active"}
	return c
}

var now = time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

func TestScaffoldCreatesStructure(t *testing.T) {
	c := newCfg(t)
	res, err := study.Scaffold(c, study.Request{
		Course:   "고성능 JPA와 Hibernate",
		Platform: "인프런",
		Sections: []string{"JDBC 기본", "커넥션 관리"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	if res.Dir != "personal/study/고성능-jpa와-hibernate" {
		t.Errorf("예상과 다른 디렉토리: %s", res.Dir)
	}
	for _, sub := range []string{"notes", "deep-dive"} {
		p := filepath.Join(c.KB.Root, filepath.FromSlash(res.Dir), sub)
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			t.Errorf("하위 디렉토리가 없음: %s", sub)
		}
	}

	raw, err := os.ReadFile(filepath.Join(c.KB.Root, filepath.FromSlash(res.Index)))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	// 인덱싱되려면 frontmatter 가 정상이어야 한다.
	for _, want := range []string{"type: study", "ai_access: shared", "# 고성능 JPA와 Hibernate", "인프런"} {
		if !strings.Contains(body, want) {
			t.Errorf("README 에 %q 가 없음", want)
		}
	}
	// 전달한 섹션이 체크리스트로 들어가야 한다.
	if !strings.Contains(body, "01 - JDBC 기본") || !strings.Contains(body, "02 - 커넥션 관리") {
		t.Error("섹션 인덱스가 반영되지 않음")
	}
	// 기본 렌즈가 모두 박혀야 한다.
	for _, l := range config.DefaultLenses {
		if !strings.Contains(body, l.Name) {
			t.Errorf("기본 관점 %q 가 없음", l.Name)
		}
	}
}

// 관점은 사람마다 다르므로 config 로 갈아끼울 수 있어야 한다.
func TestScaffoldUsesConfiguredLenses(t *testing.T) {
	c := newCfg(t)
	c.Study.Lenses = []config.StudyLens{
		{Name: "수식 유도", Detail: "증명을 직접 따라 적는다."},
		{Name: "반례 탐색", Detail: "성립하지 않는 경우를 찾는다."},
	}
	res, err := study.Scaffold(c, study.Request{Course: "선형대수"}, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(c.KB.Root, filepath.FromSlash(res.Index)))
	body := string(raw)

	if !strings.Contains(body, "수식 유도") || !strings.Contains(body, "반례 탐색") {
		t.Error("설정한 관점이 반영되지 않음")
	}
	if strings.Contains(body, "운영과 성능") {
		t.Error("설정을 지정했는데 기본 관점이 남아 있음")
	}
	if len(res.Lenses) != 2 {
		t.Errorf("적용된 관점 수가 다름: %v", res.Lenses)
	}
}

// 기존 학습 기록을 덮어써 날리는 일은 없어야 한다.
func TestScaffoldRefusesExisting(t *testing.T) {
	c := newCfg(t)
	req := study.Request{Course: "Kafka"}
	if _, err := study.Scaffold(c, req, now); err != nil {
		t.Fatal(err)
	}
	_, err := study.Scaffold(c, req, now)
	if err == nil {
		t.Fatal("중복 생성이 거부되지 않음")
	}
	var ee *study.ExistsError
	if !asExists(err, &ee) {
		t.Errorf("ExistsError 가 아님: %v", err)
	}
}

func asExists(err error, target **study.ExistsError) bool {
	e, ok := err.(*study.ExistsError)
	if ok {
		*target = e
	}
	return ok
}

// study.dir 미설정 시 taxonomy 에서 type:study 를 찾아야 한다.
func TestStudyDirFallsBackToTaxonomy(t *testing.T) {
	c := newCfg(t)
	dir, err := study.StudyDir(c)
	if err != nil {
		t.Fatal(err)
	}
	if dir != "personal/study" {
		t.Errorf("예상과 다름: %s", dir)
	}
}

// type:study 가 여러 개면 사용자가 골라야 한다(임의 선택 금지).
func TestStudyDirAmbiguous(t *testing.T) {
	c := newCfg(t)
	c.Taxonomy.Directories["learning"] = config.DirMeta{Type: "study"}
	if _, err := study.StudyDir(c); err == nil {
		t.Fatal("모호한 상태인데 오류가 나지 않음")
	}
}

func TestScaffoldRequiresTopic(t *testing.T) {
	c := newCfg(t)
	if _, err := study.Scaffold(c, study.Request{}, now); err == nil {
		t.Fatal("topic 없이 생성됨")
	}
}

// readIndex 는 생성된 README 본문을 돌려준다.
func readIndex(t *testing.T, c *config.Config, res *study.Result) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.KB.Root, filepath.FromSlash(res.Index)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// kind 미지정은 자가 학습이다. 강의가 아닌 학습이 더 흔하므로,
// 쓰지도 않을 플랫폼·강사 칸이 붙어서는 안 된다.
func TestScaffoldDefaultsToSelfStudy(t *testing.T) {
	c := newCfg(t)
	res, err := study.Scaffold(c, study.Request{Topic: "Valkey 클러스터 동작"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "self" {
		t.Errorf("기본 kind 가 self 가 아님: %s", res.Kind)
	}
	body := readIndex(t, c, res)
	for _, unwanted := range []string{"**플랫폼**", "**강사**", "**출판사**", "**저자**"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("자가 학습에 %s 칸이 붙음", unwanted)
		}
	}
	if !strings.Contains(body, "자가 학습") {
		t.Error("자료 종류가 표시되지 않음")
	}
}

func TestScaffoldCourseKind(t *testing.T) {
	c := newCfg(t)
	res, err := study.Scaffold(c, study.Request{
		Topic: "고성능 JPA", Kind: "course",
		Source: "인프런", Author: "홍길동", Units: []string{"JDBC 기본"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	body := readIndex(t, c, res)
	for _, want := range []string{"**플랫폼**: 인프런", "**강사**: 홍길동", "섹션 인덱스", "01 - JDBC 기본"} {
		if !strings.Contains(body, want) {
			t.Errorf("강의 노트에 %q 가 없음", want)
		}
	}
	// 강의는 섹션 → 강의 2단 구조를 쓴다.
	if !strings.Contains(body, "notes/<NN-섹션>/<NN-강의>.md") {
		t.Error("강의의 2단 구조 안내가 없음")
	}
}

func TestScaffoldBookKind(t *testing.T) {
	c := newCfg(t)
	res, err := study.Scaffold(c, study.Request{
		Topic: "High-Performance Java Persistence", Kind: "book",
		Source: "Vlad Mihalcea", Author: "Vlad Mihalcea",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	body := readIndex(t, c, res)
	for _, want := range []string{"기술서적", "**출판사**", "**저자**", "목차", "notes/<NN-장>/<NN-절>.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("책 노트에 %q 가 없음", want)
		}
	}
}

// AI 학습은 검증이 본체다. 출처 확인 절이 없으면 틀린 답이 그대로 굳는다.
func TestScaffoldAIKindHasVerificationSection(t *testing.T) {
	c := newCfg(t)
	res, err := study.Scaffold(c, study.Request{
		Topic: "Raft 합의 알고리즘", Kind: "ai", Source: "Claude",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	body := readIndex(t, c, res)
	for _, want := range []string{"출처 확인", "미검증", "1차 자료", "그럴듯하게 틀릴 수 있다"} {
		if !strings.Contains(body, want) {
			t.Errorf("AI 학습 노트에 %q 가 없음", want)
		}
	}
	// 강사·출판사 같은 무관한 칸은 없어야 한다.
	if strings.Contains(body, "**강사**") || strings.Contains(body, "**저자**") {
		t.Error("AI 학습에 강사/저자 칸이 붙음")
	}
}

// 문서는 버전이 곧 정확성이다.
func TestScaffoldDocsKindShowsVersion(t *testing.T) {
	c := newCfg(t)
	res, err := study.Scaffold(c, study.Request{
		Topic: "Kubernetes Gateway API", Kind: "docs", Version: "v1.2",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	body := readIndex(t, c, res)
	if !strings.Contains(body, "**대상 버전**: v1.2") {
		t.Error("문서 학습에 대상 버전이 없음")
	}
	if !strings.Contains(body, "버전에 따라 달라진다") {
		t.Error("버전 주의 문구가 없음")
	}
}

// v0.3.0 필드명(course/platform/instructor/sections)으로 호출해도 동작해야 한다.
func TestScaffoldLegacyFieldsStillWork(t *testing.T) {
	c := newCfg(t)
	res, err := study.Scaffold(c, study.Request{
		Course: "레거시 호출", Platform: "인프런",
		Instructor: "김강사", Sections: []string{"첫 섹션"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	body := readIndex(t, c, res)
	if !strings.Contains(body, "# 레거시 호출") {
		t.Error("course 필드가 topic 으로 접히지 않음")
	}
	if !strings.Contains(body, "인프런") || !strings.Contains(body, "김강사") {
		t.Error("platform/instructor 가 반영되지 않음")
	}
	if !strings.Contains(body, "01 - 첫 섹션") {
		t.Error("sections 가 units 로 접히지 않음")
	}
}

func TestNormalizeKind(t *testing.T) {
	cases := map[string]study.Kind{
		"": study.KindSelf, "self": study.KindSelf, "알 수 없는 값": study.KindSelf,
		"course": study.KindCourse, "강의": study.KindCourse,
		"book": study.KindBook, "책": study.KindBook,
		"ai": study.KindAI, "ChatGPT": study.KindAI,
		"docs": study.KindDocs, "공식문서": study.KindDocs,
	}
	for in, want := range cases {
		if got := study.NormalizeKind(in); got != want {
			t.Errorf("NormalizeKind(%q) = %q, want %q", in, got, want)
		}
	}
}
