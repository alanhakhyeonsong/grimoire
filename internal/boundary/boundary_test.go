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

// hard_locked_dirs 키를 생략하면 기본 하드가드가 그대로 적용돼야 한다(하위호환).
func TestHardLockedDirsDefaultsWhenUnset(t *testing.T) {
	c := cfgEmptyLocked()
	if c.Boundary.HardLockedDirs != nil {
		t.Fatal("전제 위반: 이 설정에는 hard_locked_dirs 가 없어야 한다")
	}
	if !boundary.IsPrivateDir("personal/career/x.md", c) {
		t.Error("키 미설정 시 DefaultHardLockedDirs 가 적용되어야 함")
	}
}

// 다른 사용자가 자기 사적 폴더를 소스 수정 없이 하드가드에 넣을 수 있어야 한다.
func TestHardLockedDirsConfigOverride(t *testing.T) {
	c := cfgEmptyLocked()
	c.Boundary.HardLockedDirs = []string{"private/journal", "secrets"}

	if !boundary.IsPrivateDir("private/journal/2026.md", c) {
		t.Error("설정한 하드가드 경로가 차단되지 않음")
	}
	if !boundary.IsPrivateDir("secrets/keys.md", c) {
		t.Error("설정한 하드가드 경로가 차단되지 않음")
	}
	// 명시했으면 작성자 개인 경로(기본값)는 더 이상 강제되지 않는다.
	if boundary.IsPrivateDir("personal/career/x.md", c) {
		t.Error("hard_locked_dirs 명시 시 기본값은 대체되어야 함")
	}
}

// 빈 배열은 "하드가드를 끄겠다"는 사용자의 명시적 선택이다.
func TestHardLockedDirsExplicitEmpty(t *testing.T) {
	c := cfgEmptyLocked()
	c.Boundary.HardLockedDirs = []string{}

	if boundary.IsPrivateDir("personal/career/x.md", c) {
		t.Error("빈 배열을 명시하면 하드가드가 없어야 함")
	}
	// 하드가드를 껐어도 config locked_dirs 는 정상 동작해야 한다.
	c.Boundary.LockedDirs = []string{"private/journal"}
	if !boundary.IsPrivateDir("private/journal/a.md", c) {
		t.Error("하드가드를 꺼도 locked_dirs 는 유지되어야 함")
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
