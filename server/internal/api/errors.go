package api

import (
	"encoding/json"
	"net/http"
)

// 错误码（03 文档第 7 节）。NOT_IMPLEMENTED 为开发期占位。
const (
	CodeUnauthorized   = "UNAUTHORIZED"
	CodeNotFound       = "NOT_FOUND"
	CodeForbidden      = "FORBIDDEN"
	CodeBadPath        = "BAD_PATH"
	CodeConflict       = "CONFLICT"
	CodeTooLarge       = "TOO_LARGE"
	CodeShareOffline   = "SHARE_OFFLINE"
	CodeLocked         = "LOCKED"   // 配对锁定（01 文档 3.2）
	CodeInternal       = "INTERNAL" // 服务端内部错误
	CodeNotImplemented = "NOT_IMPLEMENTED"
)

var codeStatus = map[string]int{
	CodeUnauthorized:   http.StatusUnauthorized,
	CodeNotFound:       http.StatusNotFound,
	CodeForbidden:      http.StatusForbidden,
	CodeBadPath:        http.StatusBadRequest,
	CodeConflict:       http.StatusConflict,
	CodeTooLarge:       http.StatusRequestEntityTooLarge,
	CodeShareOffline:   http.StatusServiceUnavailable,
	CodeLocked:         http.StatusLocked,
	CodeInternal:       http.StatusInternalServerError,
	CodeNotImplemented: http.StatusNotImplemented,
}

// writeError 输出 {"error":{"code":"...","message":"..."}}，HTTP 状态由错误码决定。
func writeError(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(codeStatus[code])
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	writeError(w, CodeNotImplemented, "接口尚未实现")
}
