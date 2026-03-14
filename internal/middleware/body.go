package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const (
	BodyContextKey             contextKey = "body_ctx"
	DefaultMaxRequestBodyBytes int64      = 4 << 20 // 4 MiB
)

// RequestBodyContext 保存已经读取并复用的请求体。
// 这一层是“只读一次 body”的基础设施。
type RequestBodyContext struct {
	RawBody  []byte
	IsStream bool
}

// BodyContextMiddleware 统一完成三件事：
// 1. 对请求体做大小限制
// 2. 一次性读取 body
// 3. 把原始 body 放进 context，供后续中间件复用
func BodyContextMiddleware(maxBodyBytes int64, next http.Handler) http.Handler {
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxRequestBodyBytes
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			http.Error(w, "Request body is required", http.StatusBadRequest)
			return
		}

		limitedBody := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		defer limitedBody.Close()

		bodyBytes, err := io.ReadAll(limitedBody)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(
					w,
					fmt.Sprintf("Request body too large. Max allowed size is %d bytes.", maxBodyBytes),
					http.StatusRequestEntityTooLarge,
				)
				return
			}

			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}

		bodyCtx := &RequestBodyContext{
			RawBody:  bodyBytes,
			IsStream: isStreamRequestBody(bodyBytes),
		}

		// 仍然把 body 塞回去，确保下游代理层还能继续使用
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		r.ContentLength = int64(len(bodyBytes))

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

func isStreamRequestBody(body []byte) bool {
	return bytes.Contains(body, []byte(`"stream":true`)) ||
		bytes.Contains(body, []byte(`"stream": true`))
}
