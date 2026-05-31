// Package boundary 는 AI 접근 경계 가드다.
//
// pull(검색/인덱싱/자동주입/컴파일) 차단, push(write)와 명시 단건 read 허용.
// 차단 디렉토리는 config(locked_dirs/private_dirs)가 주로 소유하되, 핵심 사적
// 디렉토리(career/self-analysis)는 config 가 비거나 잘못 편집돼도 새지 않도록
// 코드 하드 가드(HardLockedDirs)로 이중화한다(fail-safe).
// 설정은 서버 시작 시 1회 로드되고 실행 중 프로세스는 config 를 다시 읽지 않으므로,
// 세션 도중 차단 목록을 무력화할 수 없다(런타임 보호).
package boundary

import (
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
)

// HardLockedDirs 는 config 와 무관하게 항상 차단되는 최후 안전망이다.
// kb.config.json 의 locked_dirs 가 비거나 오타로 빠져도 이 디렉토리들은
// 인덱싱·검색·컴파일·자동주입에서 제외된다(명시 단건 read 만 경고와 함께 허용).
// 다른 사용자/KB 로 이식할 때만 이 목록을 조정한다.
var HardLockedDirs = []string{
	"personal/career",
	"personal/analysis",
	"docs/career",
}

func normalize(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	return strings.TrimPrefix(rel, "./")
}

// PrivateDirs 는 차단 디렉토리 집합을 반환한다.
// HardLockedDirs(코드 하드 가드) + locked_dirs(설정 기본) + private_dirs(설정 추가)를
// 병합·정규화한다.
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
	for _, d := range HardLockedDirs {
		add(d)
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

// IsPrivateAccess 는 ai_access 값이 차단(deny) 값인지 판정한다.
// frontmatter 명시값과 taxonomy/fallback 추론값을 동일 기준으로 처리하기 위해,
// 호출측은 frontmatter.Parse 가 산출한 Note.AIAccess 를 그대로 넘긴다.
// (미등록 디렉토리는 Parse 가 fail-safe 로 "private" 를 채우므로 여기서 차단된다.)
// deny_value 가 비어 있으면(설정 누락) 빈 access 를 과잉 차단하지 않도록 항상 false.
func IsPrivateAccess(access string, c *config.Config) bool {
	deny := c.Boundary.PrivateFrontmatter.DenyValue
	return deny != "" && access == deny
}
