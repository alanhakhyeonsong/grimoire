// Package ollama 는 선택적 컴파일러 백엔드의 최소 클라이언트다.
//
// 임베딩이 아니라 frontmatter 백필 제안·요약·lint 보조용 텍스트 생성 전용이다.
// config.ollama.enabled 가 false 면 호출측이 아예 생성하지 않으며, 서버가
// 미가동이어도 엔진의 나머지 기능(RAG-lite/write/lint 구조검진)은 그대로 동작한다.
// 차단 경로 파일은 호출측(compiler)이 인덱스 기반으로 걸러 절대 전달하지 않는다.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alanhakhyeonsong/grimoire/internal/config"
)

// Client 는 Ollama HTTP 클라이언트다(생성 전용).
type Client struct {
	host  string
	model string
	http  *http.Client
}

// New 는 config.ollama 로 클라이언트를 만든다. enabled=false 면 (nil,err).
func New(c *config.Config) (*Client, error) {
	if !c.Ollama.Enabled {
		return nil, fmt.Errorf("ollama 가 비활성입니다(config ollama.enabled=false)")
	}
	host := strings.TrimRight(c.Ollama.Host, "/")
	if host == "" {
		host = "http://localhost:11434"
	}
	model := c.Ollama.Model
	if model == "" {
		model = "qwen2.5"
	}
	return &Client{
		host:  host,
		model: model,
		http:  &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Model 은 사용 모델명을 반환한다.
func (c *Client) Model() string { return c.model }

// Available 은 서버 가동 여부를 빠르게 확인한다(짧은 타임아웃).
func (c *Client) Available(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type generateRequest struct {
	Model  string  `json:"model"`
	Prompt string  `json:"prompt"`
	System string  `json:"system,omitempty"`
	Stream bool    `json:"stream"`
	Format string  `json:"format,omitempty"`
	Options options `json:"options,omitempty"`
}

type options struct {
	Temperature float64 `json:"temperature"`
}

type generateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// Generate 는 단발 프롬프트로 텍스트를 생성한다(stream 비활성).
// jsonFormat=true 면 Ollama 에 JSON 출력 모드를 요청한다(format:"json").
func (c *Client) Generate(ctx context.Context, system, prompt string, jsonFormat bool) (string, error) {
	body := generateRequest{
		Model:   c.model,
		Prompt:  prompt,
		System:  system,
		Stream:  false,
		Options: options{Temperature: 0},
	}
	if jsonFormat {
		body.Format = "json"
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/generate", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama 요청 실패(서버 미가동?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama 응답 오류: %s", resp.Status)
	}
	var gr generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return "", fmt.Errorf("ollama 응답 파싱 실패: %w", err)
	}
	return strings.TrimSpace(gr.Response), nil
}
