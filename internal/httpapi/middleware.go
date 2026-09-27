package httpapi

import (
	"context"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"zonenan-backend/internal/store"
)

type ctxKey string

const ctxUserID ctxKey = "uid"

// authMiddleware 校验 Bearer 令牌,将 user_id 注入 context;失败 401。
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			Fail(w, http.StatusUnauthorized, "缺少登录令牌")
			return
		}
		uid, sessionID, err := s.tokens.ParseSession(strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			Fail(w, http.StatusUnauthorized, "登录已过期，请重新登录")
			return
		}
		if sessionID != "" && !s.webSessions.Active(r.Context(), sessionID, uid) {
			Fail(w, http.StatusUnauthorized, "登录会话已撤销，请重新登录")
			return
		}
		switch s.users.CheckStatus(r.Context(), uid) {
		case store.UserNotFound:
			Fail(w, http.StatusUnauthorized, "账号不存在，请重新登录")
			return
		case store.UserBanned:
			Fail(w, http.StatusForbidden, "账号已被封禁")
			return
		}
		if err := s.users.TouchLastOnline(r.Context(), uid); err != nil {
			// 在线时间仅用于管理展示，不能因记录失败阻断已通过鉴权的业务请求。
			log.Printf("touch user last online failed uid=%d: %v", uid, err)
		}
		ctx := context.WithValue(r.Context(), ctxUserID, uid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(s.cfg.WebAllowedOrigins))
	for _, origin := range s.cfg.WebAllowedOrigins {
		allowed[origin] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !allowed[origin] {
			if r.Method == http.MethodOptions {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-ZoneNaN-Map-Upload-Token")
		w.Header().Set("Access-Control-Expose-Headers", "X-ZoneNaN-Map-Upload-Token, X-ZoneNaN-Map-Cache")
		w.Header().Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// optionalAuthMiddleware 尝试解析令牌但不强制:有效且用户存在则注入 user_id,
// 无/无效/用户已删除则放行(user_id=0)。
// 用于 cas-login:带令牌=绑到当前账号,不带=自动开户/登入。
func (s *Server) optionalAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer ") {
			if uid, err := s.tokens.Parse(strings.TrimPrefix(h, "Bearer ")); err == nil {
				if s.users.CheckStatus(r.Context(), uid) == store.UserOK {
					if err := s.users.TouchLastOnline(r.Context(), uid); err != nil {
						log.Printf("touch user last online failed uid=%d: %v", uid, err)
					}
					r = r.WithContext(context.WithValue(r.Context(), ctxUserID, uid))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// userIDFrom 从 context 取当前登录用户 id(经 authMiddleware 后必有;optional 时可能为 0)。
func userIDFrom(r *http.Request) int64 {
	if v, ok := r.Context().Value(ctxUserID).(int64); ok {
		return v
	}
	return 0
}

// trustedRealIPMiddleware only accepts forwarding headers from a loopback/private reverse proxy.
func trustedRealIPMiddleware(next http.Handler) http.Handler {
	forwarded := chimiddleware.RealIP(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = strings.Trim(r.RemoteAddr, "[]")
		}
		ip := net.ParseIP(host)
		if ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
			forwarded.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimiter 是简单的按 key 固定窗口限流器(进程内)。
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{hits: make(map[string][]time.Time), limit: limit, window: window}
	go rl.sweepLoop()
	return rl
}

// allow 记录一次命中,返回是否在限额内。
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-rl.window)
	kept := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= rl.limit {
		rl.hits[key] = kept
		return false
	}
	rl.hits[key] = append(kept, now)
	return true
}

// sweepLoop 定期清理窗口已全过期的 key,防 map 无界增长(M3)。
func (rl *rateLimiter) sweepLoop() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-rl.window)
		rl.mu.Lock()
		for key, ts := range rl.hits {
			fresh := false
			for _, t := range ts {
				if t.After(cutoff) {
					fresh = true
					break
				}
			}
			if !fresh {
				delete(rl.hits, key)
			}
		}
		rl.mu.Unlock()
	}
}

// limit 中间件:按客户端 IP 限流。
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !rl.allow(ip) {
			Fail(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// middlewareByUser 用于已经通过 authMiddleware 的写接口。它避免校园
// NAT 下多个正常用户共享同一个 IP 配额；没有用户上下文时才回退到 IP。
func (rl *rateLimiter) middlewareByUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := userIDFrom(r)
		key := clientIP(r)
		if uid > 0 {
			key = "user:" + strconv.FormatInt(uid, 10)
		}
		if !rl.allow(key) {
			Fail(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP 取客户端 IP。用 RemoteAddr(chi 的 RealIP 中间件已据可信头填好),
// 不再直接信任原始 XFF —— 否则攻击者每请求伪造不同 XFF 即可绕过限流并撑爆 map(M3)。
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]") // 去掉 IPv6 方括号
}
