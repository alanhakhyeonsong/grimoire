package index

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/boundary"
	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/frontmatter"
	"github.com/bmatcuk/doublestar/v4"
)

// Stats 는 인덱싱 회계다.
// Updated/Unchanged/Deleted 는 증분 동기화(Sync)에서만 채워진다(full Reindex 는 0).
type Stats struct {
	Scanned         int `json:"scanned"`
	Indexed         int `json:"indexed"`
	ExcludedGlob    int `json:"excludedGlob"`
	ExcludedPrivate int `json:"excludedPrivate"`
	ParseErrors     int `json:"parseErrors"`
	Inferred        int `json:"inferred"`
	Updated         int `json:"updated,omitempty"`
	Unchanged       int `json:"unchanged,omitempty"`
	Deleted         int `json:"deleted,omitempty"`
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

		// 2) 차단 디렉토리 (코드 하드 가드 포함)
		if boundary.IsPrivateDir(rel, c) {
			st.ExcludedPrivate++
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
		if boundary.IsPrivateAccess(note.AIAccess, c) {
			st.ExcludedPrivate++
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
		// 2) 차단 디렉토리
		if boundary.IsPrivateDir(rel, c) {
			st.ExcludedPrivate++
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
			st.ExcludedPrivate++
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

	st.Indexed = db.Total()
	return st, walkErr
}
