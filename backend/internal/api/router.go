package api

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rolingg/fizzra/backend/internal/config"
)

// RouterOptions 配置 HTTP 层
type RouterOptions struct {
	Config *config.Config
	// StaticFS 提供构建好的前端。纯命令行部署时可以为 nil
	StaticFS fs.FS
}

// NewRouter 构建 Gin 引擎并注册所有路由
//
// 路由布局
//
//	/healthz                    存活探针
//	/api/v1/...                 JSON 接口
//	/                           单页应用，或一段说明用的占位页
func NewRouter(h *Handler, opts RouterOptions) *gin.Engine {
	if opts.Config.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else if opts.Config.Server.Mode == "test" {
		gin.SetMode(gin.TestMode)
	}

	// 用 gin.New() 而不是 gin.Default()，因为需要自定义的结构化日志，
	// 以及一个不会把堆栈泄露给客户端的 panic 处理器
	r := gin.New()
	r.Use(requestLogger(h.Log), recoveryMiddleware(h.Log), corsMiddleware(opts.Config.Server.CORSOrigins))

	r.GET("/healthz", h.healthz)

	v1 := r.Group("/api/v1")
	{
		v1.GET("/system/info", h.systemInfo)
		v1.GET("/analyzers", h.listAnalyzers)
		// 离线模式可运行时切换，正确答案取决于机器当下的连通性，无需重启
		v1.PUT("/system/offline", h.setOffline)
		// 逐分析器设置，对照分析器自己声明的结构校验
		v1.PUT("/system/analyzers/:name/options", h.setAnalyzerOptions)

		projects := v1.Group("/projects")
		{
			projects.GET("", h.listProjects)
			projects.POST("", h.createProject)
			// 批量删除注册在 /:id 之前。Gin 的路由树里静态段优先于参数段，顺序不影响
			// 匹配，但集合级操作排在前面读起来更清楚
			projects.DELETE("", h.deleteProjects)
			projects.GET("/:id", h.getProject)
			projects.PATCH("/:id", h.updateProject)
			projects.DELETE("/:id", h.deleteProject)
			projects.GET("/:id/overview", h.projectOverview)
			projects.GET("/:id/analyses", h.listAnalyses)
			projects.GET("/:id/trend", h.projectTrend)
			projects.GET("/:id/gate", h.getGate)
			projects.PUT("/:id/gate", h.saveGate)
			// 扫描前检查，面板在点「立即扫描」之前调用，据此决定要不要先弹确认
			projects.GET("/:id/preflight", h.scanPreflight)
			// 报告挂在项目下而不是分析下，导出的是「这个项目的这次扫描」，项目名要进
			// 文件名，挂在 /analyses 下还得再查一次
			projects.GET("/:id/report/:analysisID", h.exportReport)
		}

		analyses := v1.Group("/analyses")
		{
			analyses.POST("", h.startAnalysis)
			analyses.GET("/:id", h.getAnalysis)
			analyses.GET("/:id/issues", h.listIssues)
			analyses.GET("/:id/stats", h.analysisStats)
		}

		v1.GET("/events", h.streamEvents)

		issues := v1.Group("/issues")
		{
			issues.GET("/:id", h.getIssue)
			issues.PATCH("/:id", h.updateIssueStatus)
		}

		rules := v1.Group("/rules")
		{
			rules.GET("", h.listRules)
			rules.POST("", h.createRule)
			rules.PUT("/:id", h.updateRule)
			rules.DELETE("/:id", h.deleteRule)
			rules.POST("/:id/toggle", h.toggleRule)
		}
	}

	registerStatic(r, opts.StaticFS, h.Log)

	return r
}

// registerStatic 提供内嵌的单页应用
//
// 同一个二进制有两种工作方式，带前端构建产物时提供真正的应用，没有时给一个说明
// 如何构建的页面。命令行和 CI 接入必须在完全没有前端产物时也能运行
func registerStatic(r *gin.Engine, staticFS fs.FS, log interface{ Info(string, ...any) }) {
	if staticFS == nil {
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.JSON(http.StatusNotFound, Response{
					Error: &ErrorBody{Code: "not_found", Message: "no such endpoint"},
				})
				return
			}
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, placeholderPage)
		})
		return
	}

	fileServer := http.FileServer(http.FS(staticFS))

	r.NoRoute(func(c *gin.Context) {
		path := strings.TrimPrefix(c.Request.URL.Path, "/")

		if strings.HasPrefix(path, "api/") {
			c.JSON(http.StatusNotFound, Response{
				Error: &ErrorBody{Code: "not_found", Message: "no such endpoint"},
			})
			return
		}

		// 资源存在就返回它，否则回落到 index.html，客户端路由（如 /projects/prj_x）
		// 在硬刷新时才能正常工作
		if path != "" {
			if f, err := staticFS.Open(path); err == nil {
				f.Close()
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}

		index, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			log.Info("frontend assets missing from the binary", "err", err)
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, placeholderPage)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}

// placeholderPage 在二进制未带前端构建产物时显示
const placeholderPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Fizzra</title>
<style>
  body { font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
         max-width: 46rem; margin: 4rem auto; padding: 0 1.5rem; line-height: 1.6;
         color: #1f2937; }
  h1 { font-size: 1.5rem; margin-bottom: .25rem; }
  p  { color: #4b5563; }
  code { background: #f3f4f6; padding: .15rem .4rem; border-radius: 4px;
         font-size: .875em; }
  pre  { background: #f9fafb; border: 1px solid #e5e7eb; border-radius: 8px;
         padding: 1rem; overflow-x: auto; }
  .pill { display:inline-block; background:#ecfdf5; color:#047857; border-radius:999px;
          padding:.1rem .6rem; font-size:.75rem; font-weight:600; }
</style>
</head>
<body>
<h1>Fizzra <span class="pill">API only</span></h1>
<p>The server is running, but this binary was built without the web dashboard.</p>
<p>Build the frontend, then rebuild the binary to serve it from the same process:</p>
<pre>cd frontend &amp;&amp; npm install &amp;&amp; npm run build
cd .. &amp;&amp; go build -o fizzra ./backend/cmd/fizzra</pre>
<p>Or run the dashboard in development mode against this API:</p>
<pre>cd frontend &amp;&amp; npm run dev</pre>
<p>The JSON API is available under <code>/api/v1</code>. Try
   <a href="/api/v1/system/info">/api/v1/system/info</a>.</p>
</body>
</html>
`

// ---------------------------------------------------------------------------
// 中间件
// ---------------------------------------------------------------------------

// logger 是从 slog 需要的最小接口，刻意做窄，测试可以传桩实现
type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// requestLogger 为每个请求记录一行结构化日志
func requestLogger(log logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 健康检查不记日志，在容器里它会淹掉其余输出
		if c.Request.URL.Path == "/healthz" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()
		duration := time.Since(start)

		args := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration", duration.Round(time.Millisecond),
			"ip", c.ClientIP(),
		}
		if q := c.Request.URL.RawQuery; q != "" {
			args = append(args, "query", q)
		}

		switch {
		case c.Writer.Status() >= 500:
			log.Error("request", args...)
		case c.Writer.Status() >= 400:
			log.Warn("request", args...)
		default:
			log.Info("request", args...)
		}
	}
}

// recoveryMiddleware 把 panic 转成 500，同时保住服务器
func recoveryMiddleware(log logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered",
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
					"panic", r,
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, Response{
					Error: &ErrorBody{
						Code:    "internal_error",
						Message: "the server encountered an unexpected condition",
					},
				})
			}
		}()
		c.Next()
	}
}

// corsMiddleware 允许 Vite 开发服务器访问接口
//
// 只放行配置里列出的来源。生产构建同源提供，不需要 CORS
func corsMiddleware(origins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[strings.TrimSpace(o)] = true
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (allowed[origin] || allowed["*"]) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
			c.Header("Access-Control-Max-Age", "600")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
