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

type ProviderRoute struct {
	Name            string
	BaseURL         string
	APIKey          string
	Priority        int
	MaxRetries      int
	HealthCheckPath string
}

// GatewayContext 是贯穿网关中间件链的核心结构。
// 在 M2 中它不仅保存当前命中的 provider，还保存整个 failover 候选集。
type GatewayContext struct {
	TargetProvider     string
	TargetModel        string
	APIKey             string
	BaseURL            string
	CandidateProviders []ProviderRoute
	AttemptedProviders []string
	FailoverCount      int
}

func (g *GatewayContext) SetActiveProvider(provider ProviderRoute) {
	g.TargetProvider = provider.Name
	g.APIKey = provider.APIKey
	g.BaseURL = provider.BaseURL
}

// OpenAIResponse 代表标准的大模型非流式返回结果。
type OpenAIResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

// Usage 计费字段。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
