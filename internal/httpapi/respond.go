package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxBodyBytes 限制请求体大小,防内存耗尽(M4)。
const maxBodyBytes = 1 << 20 // 1 MiB

// Envelope 是统一响应信封 {ok, message, data}。
type Envelope struct {
	OK      bool        `json:"ok"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, env Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

// OK 返回 200 + data。
func OK(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, Envelope{OK: true, Data: data})
}

// Fail 返回指定状态码 + 错误信息。
func Fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, Envelope{OK: false, Message: message})
}

// decodeJSON 解析请求体 JSON 到 dst,失败返回 false 并已写出 400。体积超限也 400。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		Fail(w, http.StatusBadRequest, "请求体解析失败")
		return false
	}
	return true
}

// decodeJSONOptional 用于请求体可选的端点:空体(EOF)视为零值,不报错、不写响应。
// 返回 false 仅当体存在但格式错误(已写出 400)。
func decodeJSONOptional(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	err := json.NewDecoder(r.Body).Decode(dst)
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	Fail(w, http.StatusBadRequest, "请求体解析失败")
	return false
}
