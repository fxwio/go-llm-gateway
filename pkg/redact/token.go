package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// NormalizeBearerToken 把 Authorization 头或裸 token 统一归一化为纯 token。
func NormalizeBearerToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	parts := strings.Fields(raw)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}

	return raw
}

// TokenFingerprint 返回稳定且不可逆的 token 指纹。
// 输出示例：sha256:1a2b3c4d5e6f7788:len=32
func TokenFingerprint(raw string) string {
	token := NormalizeBearerToken(raw)
	if token == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:8]) + ":len=" + strconv.Itoa(len(token))
}
