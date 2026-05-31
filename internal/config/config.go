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

	Redact struct {
		Patterns []string `json:"patterns"`
	} `json:"redact"`

	Index struct {
		// SyncIntervalSeconds 는 시작 시 1회 동기화에 더해 수행하는 주기 증분
		// 동기화 간격(초)이다. 0/미설정 → 기본 defaultSyncIntervalSeconds,
		// 음수 → 주기 동기화 비활성(시작 시 1회만).
		SyncIntervalSeconds int `json:"sync_interval_seconds"`
	} `json:"index"`
}

// defaultSyncIntervalSeconds 는 index.sync_interval_seconds 미설정 시 기본값이다.
const defaultSyncIntervalSeconds = 60

// ExpandTilde 는 선행 ~ 를 사용자 홈 디렉토리로 확장한다(컨텍스트 신호원 등에서 재사용).
func ExpandTilde(p string) string { return expandTilde(p) }

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

	// 0(미설정)만 기본값으로 채운다. 음수는 "주기 동기화 끔" 의도라 보존한다.
	if c.Index.SyncIntervalSeconds == 0 {
		c.Index.SyncIntervalSeconds = defaultSyncIntervalSeconds
	}

	return &c, nil
}
