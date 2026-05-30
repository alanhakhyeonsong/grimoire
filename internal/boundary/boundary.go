// Package boundary 는 AI 접근 경계 가드다.
//
// pull(검색/인덱싱/자동주입/컴파일) 차단, push(write)와 명시 단건 read 허용.
// 차단 디렉토리 목록은 config 가 소유한다(엔진에 사용자 경로를 하드코딩하지 않는다).
// 설정은 서버 시작 시 1회 로드되고 실행 중 프로세스는 config 를 다시 읽지 않으므로,
// 세션 도중 차단 목록을 무력화할 수 없다(런타임 보호).
package boundary

import (
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
)

func normalize(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	return strings.TrimPrefix(rel, "./")
}

// PrivateDirs 는 차단 디렉토리 집합을 반환한다.
// locked_dirs(기본·항상 차단)와 private_dirs(추가 차단)를 병합·정규화한다.
func PrivateDirs(c *config.Config) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(d string) {
		d = normalize(strings.TrimSpace(d))
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, d := range c.Boundary.LockedDirs {
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
