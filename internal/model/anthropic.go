package model

// AnthropicRequest 代表 Anthropic (Claude) 原生的请求结构
type AnthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []AnthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float32            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	System      string             `json:"system,omitempty"` // Claude 的 System prompt 是独立字段
}

type AnthropicMessage struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}
