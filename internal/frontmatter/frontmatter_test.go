package frontmatter_test

import (
	"testing"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
	"github.com/alanhakhyeonsong/grimoire/internal/frontmatter"
)

func cfg() *config.Config {
	c := &config.Config{}
	c.Frontmatter.Defaults = map[string]string{"ai_access": "shared", "status": "active"}
	c.Taxonomy.Directories = map[string]config.DirMeta{
		"backend":         {Type: "analysis", Domain: "backend", AIAccess: "shared"},
		"personal/career": {Type: "career", Domain: "career", AIAccess: "private"},
	}
	return c
}

// 이슈 1 핵심: taxonomy 미등록 dir + ai_access 미기재 → fail-safe private.
func TestUnregisteredDirDefaultsPrivate(t *testing.T) {
	c := cfg()
	n, _ := frontmatter.Parse("# 일기\n오늘 있었던 일", "personal/diary/2026-05-31.md", 0, c)
	if n.AIAccess != "private" {
		t.Fatalf("미등록 dir 은 기본 private 여야 함, got %q", n.AIAccess)
	}
}

// 미등록 dir 이라도 ai_access: shared 를 명시하면 노출(opt-in).
func TestUnregisteredDirOptInShared(t *testing.T) {
	c := cfg()
	raw := "---\nai_access: shared\n---\n# 공개 메모"
	n, _ := frontmatter.Parse(raw, "personal/diary/public.md", 0, c)
	if n.AIAccess != "shared" {
		t.Fatalf("ai_access: shared 명시 시 노출되어야 함, got %q", n.AIAccess)
	}
}

// 등록된 shared dir 은 기존대로 shared.
func TestRegisteredSharedDir(t *testing.T) {
	c := cfg()
	n, _ := frontmatter.Parse("# 분석", "backend/foo.md", 0, c)
	if n.AIAccess != "shared" {
		t.Fatalf("등록 shared dir, got %q", n.AIAccess)
	}
}

// 등록된 private dir 은 기존대로 private.
func TestRegisteredPrivateDir(t *testing.T) {
	c := cfg()
	n, _ := frontmatter.Parse("# 커리어", "personal/career/promo.md", 0, c)
	if n.AIAccess != "private" {
		t.Fatalf("등록 private dir, got %q", n.AIAccess)
	}
}

// 미등록 dir 노트가 frontmatter 에서 명시적으로 private 를 둔 경우도 private.
func TestUnregisteredDirExplicitPrivate(t *testing.T) {
	c := cfg()
	raw := "---\nai_access: private\n---\n# 비공개"
	n, _ := frontmatter.Parse(raw, "scratch/secret.md", 0, c)
	if n.AIAccess != "private" {
		t.Fatalf("명시 private, got %q", n.AIAccess)
	}
}
