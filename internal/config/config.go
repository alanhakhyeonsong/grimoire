// Package config 는 KB 설정(kb.config.json) 로딩과 검증을 담당한다.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DirMeta 는 taxonomy 디렉토리 카탈로그의 1개 항목이다.
type DirMeta struct {
	Type     string `json:"type"`
	Domain   string `json:"domain"`
	Naming   string `json:"naming"`
	Tone     string `json:"tone"`
	AIAccess string `json:"ai_access"`
	Nested   bool   `json:"nested,omitempty"`
	Redact   bool   `json:"redact,omitempty"`
}

// Config 는 kb.config.json 전체 구조다. 모르는 키($comment 등)는 무시된다.
type Config struct {
	KB struct {
		Name      string   `json:"name"`
		Root      string   `json:"root"`
		IndexPath string   `json:"indexPath"`
		Exclude   []string `json:"exclude"`
	} `json:"kb"`

	Boundary struct {
		LockedDirs         []string `json:"locked_dirs"`
		PrivateDirs        []string `json:"private_dirs"`
		PrivateFrontmatter struct {
			Key       string `json:"key"`
			DenyValue string `json:"deny_value"`
		} `json:"private_frontmatter"`
		WriteAllowedInPrivate      bool `json:"write_allowed_in_private"`
		SingleReadAllowedInPrivate bool `json:"single_read_allowed_in_private"`
	} `json:"boundary"`

	Frontmatter struct {
		Core     []string          `json:"core"`
		Custom   string            `json:"custom"`
		Defaults map[string]string `json:"defaults"`
	} `json:"frontmatter"`

	Taxonomy struct {
		Directories    map[string]DirMeta `json:"directories"`
		NamingPatterns map[string]string  `json:"naming_patterns"`
	} `json:"taxonomy"`

	ContextSignals struct {
		JumpRegistry      string `json:"jump_registry"`
		CwdProjectMapping bool   `json:"cwd_project_mapping"`
	} `json:"context_signals"`

	Ollama struct {
		Enabled bool   `json:"enabled"`
		Host    string `json:"host"`
		Model   string `json:"model"`
	} `json:"ollama"`
}

func expandTilde(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if len(p) >= 2 && p[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Load 는 설정을 읽고 KB 루트 존재를 검증한다.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("설정 파일을 찾을 수 없습니다: %s", path)
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("설정 파싱 실패: %w", err)
	}

	c.KB.Root = expandTilde(c.KB.Root)

	info, err := os.Stat(c.KB.Root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("KB 루트 경로가 유효하지 않습니다: %s", c.KB.Root)
	}
	if len(c.Taxonomy.Directories) == 0 {
		return nil, fmt.Errorf("taxonomy.directories 설정이 없습니다")
	}

	return &c, nil
}
