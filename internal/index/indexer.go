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
type Stats struct {
	Scanned         int `json:"scanned"`
	Indexed         int `json:"indexed"`
	ExcludedGlob    int `json:"excludedGlob"`
	ExcludedPrivate int `json:"excludedPrivate"`
	ParseErrors     int `json:"parseErrors"`
	Inferred        int `json:"inferred"`
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

		note, fm := frontmatter.Parse(string(raw), rel, info.ModTime().UnixMilli(), c)

		// 3) frontmatter ai_access:private 오버라이드
		if boundary.IsPrivateByFrontmatter(fm, c) {
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
