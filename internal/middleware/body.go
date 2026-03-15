package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/fxwio/go-llm-gateway/internal/response"
)

const (
	BodyContextKey             contextKey = "body_ctx"
	DefaultMaxRequestBodyBytes int64      = 4 << 20 // 4 MiB
)

// RequestBodyContext 保存请求体的两个视图：
// 1. RawBody: 客户端原始请求体，供路由、缓存等逻辑使用
// 2. UpstreamBody: 发往上游 provider 的请求体，允许在网关侧做受控增强
type RequestBodyContext struct {
	RawBody               []byte
	UpstreamBody          []byte
	IsStream              bool
	StreamOptionsInjected bool
}

// BodyContextMiddleware 统一完成四件事：
// 1. 对请求体做大小限制
// 2. 一次性读取 body
// 3. 把原始 body / upstream body 放进 context，供后续中间件复用
// 4. 提前完成 stream_options.include_usage 注入，避免 proxy 再次读取 body
func BodyContextMiddleware(maxBodyBytes int64, next http.Handler) http.Handler {
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxRequestBodyBytes
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			response.WriteOpenAIError(
				w,
				http.StatusBadRequest,
				"Request body is required.",
				"invalid_request_error",
				nil,
				response.Ptr("missing_request_body"),
			)
			return
		}

		limitedBody := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		defer limitedBody.Close()

		rawBody, err := io.ReadAll(limitedBody)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				response.WriteOpenAIError(
					w,
					http.StatusRequestEntityTooLarge,
					fmt.Sprintf("Request body too large. Max allowed size is %d bytes.", maxBodyBytes),
					"invalid_request_error",
					nil,
					response.Ptr("request_body_too_large"),
				)
				return
			}

			response.WriteOpenAIError(
				w,
				http.StatusBadRequest,
				"Failed to read request body.",
				"invalid_request_error",
				nil,
				response.Ptr("request_body_read_failed"),
			)
			return
		}

		isStream := isStreamRequestBody(rawBody)
		upstreamBody, injected := buildUpstreamBody(rawBody, isStream)

		bodyCtx := &RequestBodyContext{
			RawBody:               rawBody,
			UpstreamBody:          upstreamBody,
			IsStream:              isStream,
			StreamOptionsInjected: injected,
		}

		applyRequestBody(r, upstreamBody)

		ctx := context.WithValue(r.Context(), BodyContextKey, bodyCtx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GetRequestBodyContext(r *http.Request) (*RequestBodyContext, bool) {
	ctxVal := r.Context().Value(BodyContextKey)
	if ctxVal == nil {
		return nil, false
	}

	bodyCtx, ok := ctxVal.(*RequestBodyContext)
	if !ok || bodyCtx == nil {
		return nil, false
	}

	return bodyCtx, true
}

func buildUpstreamBody(rawBody []byte, isStream bool) ([]byte, bool) {
	if len(rawBody) == 0 || !isStream {
		return rawBody, false
	}

	var jsonBody map[string]interface{}
	if err := json.Unmarshal(rawBody, &jsonBody); err != nil {
		return rawBody, false
	}

	streamOptions, ok := jsonBody["stream_options"].(map[string]interface{})
	if !ok || streamOptions == nil {
		streamOptions = make(map[string]interface{})
	}

	// 已显式传入 include_usage 时，不覆盖客户端意图。
	if _, exists := streamOptions["include_usage"]; exists {
		return rawBody, false
	}

	streamOptions["include_usage"] = true
	jsonBody["stream_options"] = streamOptions

	newBody, err := json.Marshal(jsonBody)
	if err != nil {
		return rawBody, false
	}

	return newBody, true
}

func applyRequestBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	if r.Header != nil {
		r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
}

func isStreamRequestBody(body []byte) bool {
	return bytes.Contains(body, []byte(`"stream":true`)) ||
		bytes.Contains(body, []byte(`"stream": true`))
}
