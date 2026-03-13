package adapter

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/fxwio/go-llm-gateway/internal/model"
	"github.com/fxwio/go-llm-gateway/pkg/logger"
	"go.uber.org/zap"
)

// TranslateOpenAIToAnthropic 将 OpenAI 格式的 HTTP 请求体实时转换为 Anthropic 格式
func TranslateOpenAIToAnthropic(req *http.Request) error {
	// 1. 读取原始请求体
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}

	// 2. 解析为 OpenAI 格式
	var openAIReq model.ChatCompletionRequest
	if err := json.Unmarshal(bodyBytes, &openAIReq); err != nil {
		return err
	}

	// 3. 构建 Anthropic 请求体
	anthropicReq := model.AnthropicRequest{
		Model:       openAIReq.Model, // 假设配置里已经把名字映射好了
		MaxTokens:   openAIReq.MaxTokens,
		Temperature: openAIReq.Temperature,
		Stream:      openAIReq.Stream,
	}

	// 如果客户端没有传 max_tokens，Claude API 强制要求必须传，我们做个容错兜底
	if anthropicReq.MaxTokens == 0 {
		anthropicReq.MaxTokens = 4096
	}

	// 4. 转换 Messages (极其关键：Claude 把 System prompt 提出来了)
	for _, msg := range openAIReq.Messages {
		if msg.Role == "system" {
			anthropicReq.System = msg.Content
		} else {
			anthropicReq.Messages = append(anthropicReq.Messages, model.AnthropicMessage{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
	}

	// 5. 序列化为全新的 JSON
	newBodyBytes, err := json.Marshal(anthropicReq)
	if err != nil {
		return err
	}

	// 6. 【资深细节】重写 HTTP 请求体和相关的 Header
	req.Body = io.NopCloser(bytes.NewBuffer(newBodyBytes))
	req.ContentLength = int64(len(newBodyBytes))

	// 为了安全，删除可能导致下游解析错误的头
	req.Header.Del("Content-Encoding")

	logger.Log.Info("Protocol Translation executed", zap.String("from", "openai"), zap.String("to", "anthropic"))

	return nil
}
