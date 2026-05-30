// Package boundary 는 AI 접근 경계 가드다.
//
// pull(검색/인덱싱/자동주입/컴파일) 차단, push(write)와 명시 단건 read 허용.
// hardPrivateDirs 는 config 로도 해제할 수 없는 코드 레벨 최후 방어선이다.
package boundary

import (
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
)

// hardPrivateDirs 는 config 로 해제 불가능한 기본 차단 디렉토리(배포 환경별 하드 디폴트).
// 다른 KB 에 맞게 빌드 시 조정하거나, 비워두고 config 의 private_dirs 로만 운용할 수 있다.
// (확장성 TODO: 이 기본값을 별도 locked_dirs config 키로 외재화 검토 — design.md §11)
var hardPrivateDirs = []string{"personal/career", "personal/analysis", "docs/career"}

func normalize(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	return strings.TrimPrefix(rel, "./")
}

// PrivateDirs 는 하드 디폴트 + config private_dirs 를 병합한다(해제 불가, 추가만 가능).
func PrivateDirs(c *config.Config) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, d := range hardPrivateDirs {
		add(d)
	}
	for _, d := range c.Boundary.PrivateDirs {
		add(d)
	}
	return out
}

// IsPrivateDir 는 경로가 차단 디렉토리에 속하는지 판정한다.
func IsPrivateDir(rel string, c *config.Config) bool {
	n := normalize(rel)
	for _, d := range PrivateDirs(c) {
		if n == d || strings.HasPrefix(n, d+"/") {
			return true
		}
	}
	return false
}

// IsPrivateByFrontmatter 는 frontmatter ai_access 오버라이드로 차단되는지 판정한다.
func IsPrivateByFrontmatter(fm map[string]any, c *config.Config) bool {
	v, ok := fm[c.Boundary.PrivateFrontmatter.Key]
	if !ok {
		return false
	}
	s, _ := v.(string)
	return s == c.Boundary.PrivateFrontmatter.DenyValue
}
