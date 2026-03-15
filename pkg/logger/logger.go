package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log = zap.NewNop()

// InitLogger 初始化全局的 Zap 日志实例
func InitLogger() {
	// 生产环境配置：输出 JSON 格式，适合对接到 ELK 或 Fluentd
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.Lock(os.Stdout),
		zap.InfoLevel,
	)

	Log = zap.New(core, zap.AddCaller())
	// 替换全局的 zap 实例
	zap.ReplaceGlobals(Log)
}

// Sync 优雅退出时清理缓冲区
func Sync() {
	if Log != nil {
		_ = Log.Sync()
	}
}
