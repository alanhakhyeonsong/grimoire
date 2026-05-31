package boundary_test

import (
	"testing"

	"github.com/alanhakhyeonsong/grimoire/internal/boundary"
	"github.com/alanhakhyeonsong/grimoire/internal/config"
)

// cfgEmptyLocked 는 locked_dirs/private_dirs 가 비어 있는 설정이다.
// 이 상태에서도 HardLockedDirs(코드 하드 가드)가 동작하는지 검증한다(이슈 3).
func cfgEmptyLocked() *config.Config {
	c := &config.Config{}
	c.Boundary.LockedDirs = nil
	c.Boundary.PrivateDirs = nil
	c.Boundary.PrivateFrontmatter.Key = "ai_access"
	c.Boundary.PrivateFrontmatter.DenyValue = "private"
	return c
}

func TestHardLockedDirsAlwaysBlocked(t *testing.T) {
	c := cfgEmptyLocked() // config 로는 아무것도 차단하지 않음
	blocked := []string{
		"personal/career/promo-2026.md",
		"personal/analysis/self-review.md",
		"docs/career/20260101-plan.md",
		"personal/career", // 디렉토리 자체
	}
	for _, p := range blocked {
		if !boundary.IsPrivateDir(p, c) {
			t.Errorf("하드 가드 차단 기대했으나 통과됨: %s", p)
		}
	}
}

func TestNonPrivateDirNotBlocked(t *testing.T) {
	c := cfgEmptyLocked()
	allowed := []string{
		"backend/x.md",
		"personal/work-logs/tasks_api_2026-05-31.md",
		"personal/careerful/not-career.md", // career prefix 오탐 방지(career/ 만 매칭)
	}
	for _, p := range allowed {
		if boundary.IsPrivateDir(p, c) {
			t.Errorf("비차단 경로를 오판함: %s", p)
		}
	}
}

func TestConfigLockedDirsMergeWithHardGuard(t *testing.T) {
	c := cfgEmptyLocked()
	c.Boundary.LockedDirs = []string{"private/journal"}
	if !boundary.IsPrivateDir("private/journal/2026.md", c) {
		t.Error("config locked_dirs 가 병합되지 않음")
	}
	if !boundary.IsPrivateDir("personal/career/x.md", c) {
		t.Error("config 추가 후에도 하드 가드는 유지되어야 함")
	}
}

func TestIsPrivateAccess(t *testing.T) {
	c := cfgEmptyLocked()
	if !boundary.IsPrivateAccess("private", c) {
		t.Error("deny_value=private 인데 private 가 차단되지 않음")
	}
	if boundary.IsPrivateAccess("shared", c) {
		t.Error("shared 가 차단됨")
	}
}

// deny_value 가 비면 빈 access 를 과잉 차단하지 않아야 한다(설정 누락 방어).
func TestIsPrivateAccessEmptyDenyValue(t *testing.T) {
	c := cfgEmptyLocked()
	c.Boundary.PrivateFrontmatter.DenyValue = ""
	if boundary.IsPrivateAccess("", c) {
		t.Error("deny_value 가 비면 빈 access 도 false 여야 함")
	}
	if boundary.IsPrivateAccess("private", c) {
		t.Error("deny_value 가 비면 어떤 값도 차단하지 않아야 함")
	}
}
