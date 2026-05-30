// Package contextsig 는 작업 경로 인지 컨텍스트 신호원이다.
//
// cwd(현재 작업 디렉토리)를 jump 레지스트리(별칭→절대경로 플랫 저장소)로
// 역매핑해 project 별칭을 추론한다. 이 신호원은 config.context_signals 로
// 외재화되며, jump_registry 는 그 한 구현일 뿐이다(다른 유저는 zoxide 등으로
// 교체하거나 cwd_project_mapping:false 로 비활성). 신호원이 없으면 라우팅은
// 명시 scope 인자에만 의존한다.
package contextsig

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
)

// Resolution 은 cwd → project 역매핑 결과다.
type Resolution struct {
	Enabled     bool   `json:"enabled"`               // context_signals 활성 여부
	Project     string `json:"project,omitempty"`     // 추론된 별칭("" = 매칭 없음)
	ProjectPath string `json:"projectPath,omitempty"` // 별칭이 가리키는 절대경로
	Note        string `json:"note,omitempty"`        // 비활성/오류 등 진단 메모
}

// Resolve 는 cwd 를 jump 레지스트리로 역매핑해 project 별칭을 추론한다.
// 가장 긴(가장 구체적인) 경로 매칭을 채택한다.
func Resolve(c *config.Config, cwd string) Resolution {
	if !c.ContextSignals.CwdProjectMapping || c.ContextSignals.JumpRegistry == "" {
		return Resolution{Enabled: false, Note: "context_signals 비활성 — scope 인자를 직접 지정하세요."}
	}
	reg := config.ExpandTilde(c.ContextSignals.JumpRegistry)
	entries, err := os.ReadDir(reg)
	if err != nil {
		return Resolution{Enabled: true, Note: "jump 레지스트리를 읽을 수 없습니다: " + reg}
	}

	cwd = filepath.Clean(cwd)
	var bestAlias, bestPath string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(reg, e.Name()))
		if rerr != nil {
			continue
		}
		target := strings.TrimSpace(string(data))
		if target == "" {
			continue
		}
		target = filepath.Clean(config.ExpandTilde(target))
		if cwd == target || strings.HasPrefix(cwd, target+string(filepath.Separator)) {
			if len(target) > len(bestPath) {
				bestPath = target
				bestAlias = e.Name()
			}
		}
	}

	r := Resolution{Enabled: true, Project: bestAlias, ProjectPath: bestPath}
	if bestAlias == "" {
		r.Note = "cwd 가 jump 레지스트리와 매칭되지 않음."
	}
	return r
}
