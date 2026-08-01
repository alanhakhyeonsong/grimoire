package index

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/boundary"
	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/frontmatter"
	"github.com/bmatcuk/doublestar/v4"
)

// Stats 는 인덱싱 회계다.
// Updated/Unchanged/Deleted 는 증분 동기화(Sync)에서만 채워진다(full Reindex 는 0).
type Stats struct {
	Scanned      int `json:"scanned"`
	Indexed      int `json:"indexed"`
	ExcludedGlob int `json:"excludedGlob"`

	// ExcludedPrivate 는 차단으로 제외된 총합이다(하위호환 유지).
	// 아래 두 값의 합이며, 원인이 다르므로 반드시 나눠서 읽어야 한다.
	ExcludedPrivate int `json:"excludedPrivate"`

	// ExcludedByPolicy 는 의도된 차단이다.
	// locked_dirs / hard_locked_dirs 소속이거나 frontmatter 에 ai_access:private
	// 를 명시한 노트. 정상 동작이므로 줄이려 할 대상이 아니다.
	ExcludedByPolicy int `json:"excludedByPolicy"`

	// ExcludedUnclassified 는 taxonomy 미등록 디렉토리라서 fail-safe private
	// 으로 걸러진 노트다. 대개 "새 폴더를 만들고 분류 규약을 갱신하지 않은 사고"이며,
	// 사용자는 검색이 안 되는 이유를 알 방법이 없다. 0 이 아니면 경고 대상이다.
	ExcludedUnclassified int `json:"excludedUnclassified"`

	// UnclassifiedDirs 는 ExcludedUnclassified 를 유발한 디렉토리 후보다(정렬·중복제거).
	// "6건이 빠졌다"보다 "skills/ 가 taxonomy 에 없다"가 조치 가능한 정보이므로
	// 건수와 함께 원인 경로를 돌려준다.
	UnclassifiedDirs []string `json:"unclassifiedDirs,omitempty"`

	ParseErrors int `json:"parseErrors"`
	Inferred    int `json:"inferred"`
	Updated     int `json:"updated,omitempty"`
	Unchanged   int `json:"unchanged,omitempty"`
	Deleted     int `json:"deleted,omitempty"`
}

// unclassifiedTracker 는 미등록 디렉토리 후보를 중복 없이 모은다.
type unclassifiedTracker map[string]bool

// suggestDirKey 는 미등록 노트 경로에서 taxonomy 에 등록할 후보 키를 고른다.
//
// DirMetaFor 가 최장 prefix 매칭이므로 상위 디렉토리 하나만 등록하면 하위가 전부
// 커버된다. 따라서 가능한 한 짧은 키를 제안하되, 그 키가 이미 등록된 다른 키의
// 상위인 경우(예: personal 은 personal/study 의 상위)에는 성격이 다른 형제
// 디렉토리를 싸잡아 등록하게 되므로 한 단계 더 구체화한다.
func suggestDirKey(rel string, c *config.Config) string {
	dir := path.Dir(rel)
	if dir == "." || dir == "/" {
		return "" // KB 루트 직속 파일은 디렉토리 등록으로 해결되지 않는다
	}
	segs := strings.Split(dir, "/")
	for i := 1; i <= len(segs); i++ {
		cand := strings.Join(segs[:i], "/")
		if isAncestorOfRegistered(cand, c) {
			continue
		}
		return cand
	}
	return dir
}

// isAncestorOfRegistered 는 cand 가 이미 등록된 taxonomy 키의 상위 경로인지 본다.
func isAncestorOfRegistered(cand string, c *config.Config) bool {
	for key := range c.Taxonomy.Directories {
		if strings.HasPrefix(key, cand+"/") {
			return true
		}
	}
	return false
}

// finish 는 수집된 미등록 디렉토리를 정렬해 Stats 에 확정한다.
func (t unclassifiedTracker) finish(st *Stats) {
	if len(t) == 0 {
		return
	}
	dirs := make([]string, 0, len(t))
	for d := range t {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	st.UnclassifiedDirs = dirs
}

// recordExcluded 는 ai_access 차단 1건을 원인별로 회계한다.
func recordExcluded(st *Stats, tracker unclassifiedTracker, note frontmatter.Note, rel string, c *config.Config) {
	st.ExcludedPrivate++
	if note.Unclassified {
		st.ExcludedUnclassified++
		if key := suggestDirKey(rel, c); key != "" {
			tracker[key] = true
		}
		return
	}
	st.ExcludedByPolicy++
}

// UnclassifiedScan 은 미등록 디렉토리 스캔 결과다.
// 같은 순회에서 전체 회계도 함께 채워, 호출측이 "KB 안의 문서 중 실제로 검색
// 가능한 비율"을 별도 스캔 없이 계산할 수 있게 한다.
type UnclassifiedScan struct {
	Files int            `json:"files"`           // 미분류로 인덱스에서 빠진 노트 수
	Dirs  []string       `json:"dirs"`            // 등록 후보 디렉토리(정렬)
	ByDir map[string]int `json:"byDir,omitempty"` // 디렉토리별 누락 건수

	TotalMarkdown    int `json:"totalMarkdown"`    // KB 안의 .md 총수
	ExcludedGlob     int `json:"excludedGlob"`     // kb.exclude 로 걸러진 수
	ExcludedByPolicy int `json:"excludedByPolicy"` // 차단 경로 + ai_access:private 명시
}

// ScanUnclassified 는 인덱스와 무관하게 KB 를 훑어 미등록 디렉토리를 찾는다.
//
// Lint 는 db.AllNotePaths() 즉 "이미 인덱싱된 노트"만 보므로, 미등록이라 인덱스에
// 들어가지도 못한 노트는 구조적으로 볼 수 없다. 정작 가장 알려야 할 사각지대가
// 검진에서 빠지는 셈이라, 파일시스템을 직접 훑는 별도 경로가 필요하다.
// 정책상 차단 경로(locked_dirs 등)는 의도된 비공개이므로 대상에서 제외한다.
func ScanUnclassified(c *config.Config) (UnclassifiedScan, error) {
	root := c.KB.Root
	out := UnclassifiedScan{ByDir: map[string]int{}}

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
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		out.TotalMarkdown++

		for _, pat := range c.KB.Exclude {
			if ok, _ := doublestar.Match(pat, rel); ok {
				out.ExcludedGlob++
				return nil
			}
		}
		if boundary.IsPrivateDir(rel, c) {
			out.ExcludedByPolicy++
			return nil // 의도된 차단은 사고가 아니다
		}

		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		note, _ := frontmatter.Parse(string(raw), rel, info.ModTime().UnixMilli(), c)

		// 미등록이어도 frontmatter 에 ai_access: shared 를 명시했다면 노출되므로
		// 사각지대가 아니다. 실제로 배제된 것만 센다.
		if !boundary.IsPrivateAccess(note.AIAccess, c) {
			return nil
		}
		if !note.Unclassified {
			out.ExcludedByPolicy++ // ai_access:private 를 노트가 직접 선언한 경우
			return nil
		}
		out.Files++
		if key := suggestDirKey(rel, c); key != "" {
			out.ByDir[key]++
		}
		return nil
	})

	dirs := make([]string, 0, len(out.ByDir))
	for d := range out.ByDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	out.Dirs = dirs
	return out, walkErr
}

// Reindex 는 KB 루트의 *.md 를 재귀 스캔해 인덱스를 재생성한다.
// 차단 경로는 인덱스에 제목/요약조차 들어가지 않는다.
func Reindex(c *config.Config) (Stats, *DB, error) {
	root := c.KB.Root
	db, err := Open(filepath.Join(root, c.KB.IndexPath))
	if err != nil {
		return Stats{}, nil, err
	}
	if err := db.Clear(); err != nil {
		return Stats{}, nil, err
	}

	var st Stats
	tracker := unclassifiedTracker{}
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// dot 디렉토리(.git/.obsidian/.grimoire 등)는 통째로 스킵
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		st.Scanned++

		// 1) exclude glob
		for _, pat := range c.KB.Exclude {
			if ok, _ := doublestar.Match(pat, rel); ok {
				st.ExcludedGlob++
				return nil
			}
		}

		// 2) 차단 디렉토리 (코드 하드 가드 포함) — 언제나 의도된 정책 차단이다
		if boundary.IsPrivateDir(rel, c) {
			st.ExcludedPrivate++
			st.ExcludedByPolicy++
			return nil
		}

		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			st.ParseErrors++
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			st.ParseErrors++
			return nil
		}

		note, _ := frontmatter.Parse(string(raw), rel, info.ModTime().UnixMilli(), c)

		// 3) ai_access:private 배제(명시값 + 미등록 dir fail-safe 추론값 동일 기준)
		//    배제는 같지만 원인은 다르다 → 의도/사고를 나눠 회계한다.
		if boundary.IsPrivateAccess(note.AIAccess, c) {
			recordExcluded(&st, tracker, note, rel, c)
			return nil
		}

		if err := db.Upsert(note); err != nil {
			st.ParseErrors++
			return nil
		}
		st.Indexed++
		if note.Inferred {
			st.Inferred++
		}
		return nil
	})

	tracker.finish(&st)
	return st, db, walkErr
}

// Sync 는 mtime 기반 증분 동기화다. 기존 인덱스를 보존한 채
// 파일 mtime 이 인덱스 기록과 다른 노트만 갱신하고, 사라진(또는 더 이상
// 적격이 아닌) 노트는 인덱스에서 제거한다. 시작 시 전체 재인덱싱을 없애
// idle 메모리·시작 시간을 줄이는 핵심 레버다.
// 인덱스가 비어 있으면(최초 실행) 사실상 전체 인덱싱과 동일하게 동작한다.
func Sync(c *config.Config) (Stats, *DB, error) {
	root := c.KB.Root
	db, err := Open(filepath.Join(root, c.KB.IndexPath))
	if err != nil {
		return Stats{}, nil, err
	}
	st, err := SyncWith(c, db)
	return st, db, err
}

// SyncWith 는 이미 열린 DB 핸들로 증분 동기화를 수행한다(Sync 의 본체).
// 시작 후 주기적 백그라운드 동기화가 같은 핸들을 재사용해, 세션 중
// 추가/수정/사적전환된 노트를 재시작 없이 반영하기 위한 진입점이다.
// 동시 호출은 호출측(예: write_note 와 같은 mutex)이 직렬화해야 한다.
func SyncWith(c *config.Config, db *DB) (Stats, error) {
	root := c.KB.Root

	existing, err := db.PathMtimes()
	if err != nil {
		return Stats{}, err
	}
	seen := make(map[string]bool, len(existing))

	var st Stats
	tracker := unclassifiedTracker{}
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

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		st.Scanned++

		// 1) exclude glob
		for _, pat := range c.KB.Exclude {
			if ok, _ := doublestar.Match(pat, rel); ok {
				st.ExcludedGlob++
				return nil
			}
		}
		// 2) 차단 디렉토리 — 언제나 의도된 정책 차단이다
		if boundary.IsPrivateDir(rel, c) {
			st.ExcludedPrivate++
			st.ExcludedByPolicy++
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			st.ParseErrors++
			return nil
		}
		mtime := info.ModTime().UnixMilli()

		// 변경 없음: mtime 일치 → 읽기·파싱 생략(증분 절감의 핵심)
		if prev, ok := existing[rel]; ok && prev == mtime {
			seen[rel] = true
			st.Unchanged++
			return nil
		}

		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			st.ParseErrors++
			return nil
		}
		note, _ := frontmatter.Parse(string(raw), rel, mtime, c)

		// 3) ai_access:private 배제(명시값 + 미등록 dir fail-safe 추론값 동일 기준)
		//    → 인덱스에서 배제. 이전에 적재돼 있었다면(공유→사적 전환) 흔적을 제거한다.
		if boundary.IsPrivateAccess(note.AIAccess, c) {
			if _, ok := existing[rel]; ok {
				_ = db.DeletePath(rel)
			}
			recordExcluded(&st, tracker, note, rel, c)
			return nil
		}

		// FTS/links 중복 방지를 위해 Upsert 전에 기존 흔적 제거
		if err := db.DeletePath(rel); err != nil {
			st.ParseErrors++
			return nil
		}
		if err := db.Upsert(note); err != nil {
			st.ParseErrors++
			return nil
		}
		seen[rel] = true
		st.Updated++
		if note.Inferred {
			st.Inferred++
		}
		return nil
	})

	// 삭제 스윕: 인덱스에 있으나 이번 스캔에서 보지 못한 경로
	// (파일 삭제, 또는 glob/차단으로 더 이상 적격 아님) → 제거
	for path := range existing {
		if !seen[path] {
			if err := db.DeletePath(path); err == nil {
				st.Deleted++
			}
		}
	}

	tracker.finish(&st)
	st.Indexed = db.Total()
	return st, walkErr
}
