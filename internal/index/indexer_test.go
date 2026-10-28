package index_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/index"
)

func TestUnclassifiedNoticeEmptyWhenNothingMissing(t *testing.T) {
	if got := index.UnclassifiedNotice(0, nil); got != "" {
		t.Errorf("누락 0건이면 빈 문자열 기대, got %q", got)
	}
}

func TestUnclassifiedNoticeListsDirsAndRemedy(t *testing.T) {
	got := index.UnclassifiedNotice(3, []string{"projects", "skills"})
	for _, want := range []string{"3건", "projects, skills", "taxonomy.directories", "boundary.locked_dirs"} {
		if !strings.Contains(got, want) {
			t.Errorf("안내에 %q 가 없음: %s", want, got)
		}
	}
}

// 루트 직속 파일은 디렉토리 후보가 없다. 빈 목록 대신 원인을 밝혀야 한다.
func TestUnclassifiedNoticeRootFilesWithoutDirs(t *testing.T) {
	got := index.UnclassifiedNotice(1, nil)
	if !strings.Contains(got, "KB 루트 직속 파일") {
		t.Errorf("디렉토리 후보가 없을 때 루트 직속 안내 기대: %s", got)
	}
}

// syncCfg 는 projects/ 하나를 공개 등록한 KB 를 만든다.
func syncCfg(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects", "a.md"), []byte("# 노트\nkafka 분석\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{}
	c.KB.Root = root
	c.KB.IndexPath = ".grimoire"
	c.Boundary.PrivateFrontmatter.Key = "ai_access"
	c.Boundary.PrivateFrontmatter.DenyValue = "private"
	c.Taxonomy.Directories = map[string]config.DirMeta{
		"projects": {Type: "note", Domain: "misc", AIAccess: "shared"},
	}
	return c
}

// 설정이 그대로면 mtime 단축 경로로 재파싱을 건너뛴다.
func TestSyncSkipsUnchangedWhenConfigSame(t *testing.T) {
	c := syncCfg(t)
	_, db, err := index.Sync(c)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := index.SyncWith(c, db)
	if err != nil {
		t.Fatal(err)
	}
	if st.Unchanged != 1 || st.Updated != 0 {
		t.Errorf("설정이 같으면 변경없음 1건 기대, got unchanged=%d updated=%d", st.Unchanged, st.Updated)
	}
}

// taxonomy 키를 지우면 파일 mtime 이 그대로여도 fail-safe private 으로 빠져야 한다.
func TestSyncReclassifiesWhenTaxonomyKeyRemoved(t *testing.T) {
	c := syncCfg(t)
	_, db, err := index.Sync(c)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if hits, _ := db.Search("kafka", nil, "", 5); len(hits) != 1 {
		t.Fatalf("등록 상태에서는 검색 1건 기대, got %d", len(hits))
	}

	delete(c.Taxonomy.Directories, "projects")
	c.Taxonomy.Directories["backend"] = config.DirMeta{Type: "analysis", Domain: "backend", AIAccess: "shared"}

	st, err := index.SyncWith(c, db)
	if err != nil {
		t.Fatal(err)
	}
	if hits, _ := db.Search("kafka", nil, "", 5); len(hits) != 0 {
		t.Errorf("taxonomy 에서 지운 디렉토리의 노트가 여전히 검색됨: %d건", len(hits))
	}
	if st.ExcludedUnclassified != 1 {
		t.Errorf("미분류 누락 1건 기대, got %d", st.ExcludedUnclassified)
	}

	// 새 지문이 기록돼, 다음 동기화부터는 다시 단축 경로를 쓴다.
	st, err = index.SyncWith(c, db)
	if err != nil {
		t.Fatal(err)
	}
	if st.ExcludedUnclassified != 1 {
		t.Errorf("재동기화에서도 미분류 1건 유지 기대, got %d", st.ExcludedUnclassified)
	}
}
