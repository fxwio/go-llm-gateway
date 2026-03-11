package middleware

import (
	"net/http"
	"testing"
)

func TestExtractClientIP(t *testing.T) {
	// 1. 定义测试用例表
	tests := []struct {
		name       string            // 测试用例名称
		headers    map[string]string // 模拟的 HTTP 头
		remoteAddr string            // 模拟的底层 TCP 连接地址
		expectedIP string            // 我们期望提取出的纯净 IP
	}{
		{
			name:       "包含 X-Forwarded-For (经过多层代理)",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.195, 70.41.3.18"},
			remoteAddr: "127.0.0.1:8080",
			expectedIP: "203.0.113.195", // 应该提取第一个真实 IP
		},
		{
			name:       "包含 X-Real-IP (经过 Nginx)",
			headers:    map[string]string{"X-Real-IP": "198.51.100.1"},
			remoteAddr: "127.0.0.1:8080",
			expectedIP: "198.51.100.1",
		},
		{
			name:       "没有任何代理头，只有 RemoteAddr",
			headers:    map[string]string{},
			remoteAddr: "192.168.1.100:54321", // 带有端口号
			expectedIP: "192.168.1.100",       // 应该成功剥离端口
		},
		{
			name:       "异常的 RemoteAddr 格式兜底",
			headers:    map[string]string{},
			remoteAddr: "invalid-ip-format",
			expectedIP: "invalid-ip-format", // 解析失败时原样返回
		},
	}

	// 2. 遍历执行每一个测试用例
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造 HTTP 请求
			req, _ := http.NewRequest("GET", "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			req.RemoteAddr = tt.remoteAddr

			// 调用我们的业务函数
			ip := extractClientIP(req)

			// 断言验证结果
			if ip != tt.expectedIP {
				t.Errorf("extractClientIP() = %v, 期望得到 %v", ip, tt.expectedIP)
			}
		})
	}
}
