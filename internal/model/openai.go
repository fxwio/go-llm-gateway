package model

type ChatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float32   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type Message struct {
	Role    string `json:"role"` // system, user, assistant
	Content string `json:"content"`
}

// GatewayContext 是贯穿我们网关中间件链的核心结构
// 用于在鉴权、路由、限流等中间件之间传递状态
type GatewayContext struct {
	TargetProvider string // 目标厂商 (例如: openai, anthropic)
	TargetModel    string // 目标厂商的实际模型名 (例如: claude-3-opus-20240229)
	APIKey         string // 用于访问该厂商的 Key
	BaseURL        string // 该厂商的网关地址 (例如: https://api.openai.com)
}

// OpenAIResponse 代表标准的大模型非流式返回结果
type OpenAIResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

// Usage 计费字段
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
