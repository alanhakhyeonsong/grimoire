// Package config 는 KB 설정(kb.config.json) 로딩과 검증을 담당한다.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StudyLens 는 학습노트가 항상 거쳐야 하는 관점 1개다.
type StudyLens struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// DefaultLenses 는 study.lenses 미설정 시 쓰이는 기본 관점이다.
//
// "정의 → 적용 → 운영" 순서로, 강의가 짚지 않은 층까지 스스로 메우게 하는 것이
// 목적이다. 도메인에 맞게 config 로 갈아끼우는 것을 전제한다.
var DefaultLenses = []StudyLens{
	{
		Name:   "개념 정의",
		Detail: "강의에 나온 용어를 정확히 정의하고 예시로 고정한다. 정의가 흐리면 뒤가 다 흔들린다.",
	},
	{
		Name:   "실무 적용",
		Detail: "내 일에서 어디에 쓰이는지 코드·설정·절차 수준으로 옮겨 적는다.",
	},
	{
		Name:   "운영과 성능",
		Detail: "부하·장애·관측(metrics/logs/tracing) 관점에서 무엇이 달라지는지 본다.",
	},
}

// StudyLenses 는 이 KB 에 적용할 학습 관점을 반환한다.
func (c *Config) StudyLenses() []StudyLens {
	if len(c.Study.Lenses) == 0 {
		return DefaultLenses
	}
	return c.Study.Lenses
}

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
		// HardLockedDirs 는 최후 안전망 목록이다. 키를 생략하면(nil)
		// boundary.DefaultHardLockedDirs 가 적용되고, 명시하면 그 값이 쓰인다.
		// 빈 배열은 "하드가드 없음"이라는 명시적 선택으로 해석된다.
		HardLockedDirs []string `json:"hard_locked_dirs"`

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

		// Enums 는 필드별 허용값이다(type/status/ai_access).
		// 엔진은 이 값을 강제하지 않지만, doctor 가 taxonomy 와의 불일치를
		// 잡아내는 근거로 쓴다(특히 ai_access 오타는 의도치 않은 공개가 된다).
		Enums map[string][]string `json:"enums"`
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

	// Study 는 학습노트 스캐폴딩(new_study) 설정이다.
	// 강의 1개 = 디렉토리 1개로 두고, 모든 강의 요약이 같은 관점(렌즈)을 거치게
	// 강제하는 것이 목적이다. 관점은 사람마다 다르므로 config 로 교체할 수 있다.
	Study struct {
		// Dir 은 학습노트 루트다. 비우면 taxonomy 에서 type:study 인
		// 디렉토리를 찾아 쓴다.
		Dir string `json:"dir"`

		// Lenses 는 모든 강의 노트가 반드시 다뤄야 하는 고정 관점이다.
		// 비우면 DefaultLenses 가 쓰인다.
		Lenses []StudyLens `json:"lenses"`

		// NotesDir/DeepDiveDir 은 하위 구조 이름이다(기본 notes / deep-dive).
		NotesDir    string `json:"notes_dir"`
		DeepDiveDir string `json:"deep_dive_dir"`
	} `json:"study"`

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
