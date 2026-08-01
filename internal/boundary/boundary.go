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

// DefaultHardLockedDirs 는 boundary.hard_locked_dirs 를 설정하지 않았을 때
// 적용되는 기본 하드가드다.
//
// 이 목록은 작성자 KB(~/memo) 기준이라 다른 사용자에게는 대개 존재하지 않는
// 경로다(없는 경로를 차단하는 것은 무해하다). 자신의 사적 폴더를 최후 안전망에
// 넣으려면 소스를 고치지 말고 kb.config.json 의 boundary.hard_locked_dirs 에
// 명시한다.
var DefaultHardLockedDirs = []string{
	"personal/career",
	"personal/analysis",
	"personal/diary",
	"docs/career",
}

// HardLockedDirs 는 이 KB 에 적용할 하드가드 목록을 반환한다.
//
// config 에 hard_locked_dirs 키가 없으면 기본값을 쓰고(하위호환), 명시했다면
// 그 값을 그대로 쓴다. 빈 배열(`[]`)을 명시하면 하드가드를 끄겠다는 사용자의
// 명시적 선택으로 간주한다. 키 부재와 빈 배열을 구분하기 위해 nil 검사를 쓴다.
//
// 이 목록이 config 로 열려도 런타임 보호는 유지된다. 설정은 서버 시작 시 1회만
// 로드되므로 세션 도중 차단을 무력화할 수 없다(위협 모델은 외부 침입자가 아니라
// 세션 안 AI 의 자동 행동 통제다).
func HardLockedDirs(c *config.Config) []string {
	if c == nil || c.Boundary.HardLockedDirs == nil {
		return DefaultHardLockedDirs
	}
	return c.Boundary.HardLockedDirs
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
	for _, d := range HardLockedDirs(c) {
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
