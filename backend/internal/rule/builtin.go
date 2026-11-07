package rule

import "github.com/rolingg/fizzra/backend/internal/model"

// builtinDef 与 YAML 的结构对应，规则库因此可以是纯 Go 数据，并能通过
// `fizzra rules export` 导出成 fizzra.yaml
type builtinDef struct {
	Name     string
	Severity model.Severity
	Message  string
	Advice   string
	Patterns []string
	Paths    []string
	Exclude  []string
	// Fix 是修改前后对照示例，凡是正确改法确定的规则都填了它。只报问题不给改法，
	// 会让读者自己去琢磨这条建议在他的代码里指什么
	Fix model.FixExample
	// RequireLoop 与 ForbidInLoop 补充按行正则表达不了的结构性上下文，见
	// rule.matchContext
	RequireLoop  bool
	ForbidInLoop bool
}

// goGlobs 是 Go 源码类规则的默认作用域
var goGlobs = []string{"**/*.go"}

// goGeneratedSkip 把生成物、依赖和测试夹具挡在结果之外
var goGeneratedSkip = []string{
	"**/vendor/**",
	"**/*.pb.go",
	"**/*_gen.go",
	"**/*.gen.go",
	"**/mock_*.go",
	"**/mocks/**",
	"**/node_modules/**",
	"**/testdata/**",
	"**/wailsjs/**",
}

// goExclude 是那些描述线上代码缺陷的规则的默认作用域
//
// 它排除测试文件。测试套件合法地做生产代码不该做的事，故意 panic 走 recover 路径、
// 构造注入载荷、硬编码示例凭据、在初始化里调 context.Background()。规则在这些地方
// 触发，找到的不是 bug 而是测试，误报一多只会教人彻底忽略这个工具
//
// 少数在测试里也确实有意义的规则（比如未处理的 TODO）改用 goTestIncluded
func goExclude() []string {
	return append(append([]string{}, goGeneratedSkip...), "**/*_test.go")
}

// goTestIncluded 是同样适用于测试代码的规则的作用域
func goTestIncluded() []string { return goGeneratedSkip }

// BuiltinRules 是随发布一起提供的规则集
//
// 有两条设计原则约束这份列表
//
//   - 规则不得命中它自己的定义、消息或注释。引擎在全局层面压制注释和字符串字面量里的
//     匹配，这些模式写得连本文件里的定义文本都不会触发它们
//   - 宁可要一个没有误报的窄模式，也不要带少量误报的宽模式。总喊狼来了的审查工具会
//     被关掉
//
// 这个集合有意与 golangci-lint 略有重叠，目的是提供一个零依赖的基线，在任何外部工具
// 安装之前就能用，并承载现成 linter 都不具备的 Go/Wails 知识
func BuiltinRules() []builtinDef {

	// panic 检测
	//
	// 只在调用位置匹配 `panic(`，引擎已经排除了注释、字符串字面量和测试。
	// must 这类辅助函数合法地会 panic，所以那些名字在它们所在处通过 Exclude
	// 排除
	noPanicAvoid := append(goExclude(),
		"**/must.go", "**/must_*.go", "**/*_must.go",
	)

	return []builtinDef{
		// ------------------------------------------------------------------
		// 正确性与可靠性
		// ------------------------------------------------------------------
		{
			Name:     "no-panic",
			Severity: model.SeverityError,
			Message:  "panic() called outside initialisation",
			Advice:   "Return an error instead of panicking. Reserve panic for genuinely unrecoverable startup failures in must-style helpers.",
			// panic 在 init() 里、以及校验编程错误的构造函数里（比如注册表条目重复）
			// 是合法的。正则看不到外层函数，所以那些位置写在 Exclude 列表里，而不是
			// 靠结构检测。该模式还要求语句边界，`x.panic(` 因此不会命中
			Patterns: []string{`(^|[^\w.])panic\s*\(`},
			Paths:    goGlobs,
			Exclude:  noPanicAvoid,
			Fix: model.FixExample{
				Before: `func LoadConfig(path string) Config {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return parse(data)
}`,
				After: `func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	return parse(data)
}`,
				Note:     "Returning the error lets the caller decide. Wrapping it with %w keeps the original cause available to errors.Is and errors.As.",
				Language: "go",
			},
		},
		{
			// 这个正则刻意要求有赋值，而不只是 `defer`。引擎随后确认匹配位于循环
			// 体内，那才是累积真正成为问题的唯一情况
			Name:     "defer-in-loop",
			Severity: model.SeverityWarning,
			Message:  "defer inside a loop defers cleanup until the function returns",
			Advice:   "Extract the loop body into its own function, or close the resource explicitly at the end of each iteration. With a large iteration count this leaks file handles or connections.",
			// 只标记明显的资源清理。循环里的 `defer wg.Done()` 和
			// `defer func() { <-sem }()` 是正确的，不该被报出来，所以模式点名了
			// 具体的清理动词
			Patterns:    []string{`^\s*defer\s+(\w+\.)?(Close|CloseFile|CloseBody|Release|Unlock|RUnlock|Stop|Cancel|Rollback|Disconnect|Flush|Remove|RemoveAll|Cleanup)\s*\(`},
			Paths:       goGlobs,
			Exclude:     goExclude(),
			RequireLoop: true,
			Fix: model.FixExample{
				Before: `func readAll(paths []string) error {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close() // runs only when readAll returns
		if err := process(f); err != nil {
			return err
		}
	}
	return nil
}`,
				After: `func readAll(paths []string) error {
	for _, p := range paths {
		if err := readOne(p); err != nil {
			return err
		}
	}
	return nil
}

// readOne closes its own file: the defer runs once per call.
func readOne(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return process(f)
}`,
				Note:     "Extracting the body is usually cleaner than an explicit Close, because every error path keeps working. One loop over 10,000 files would otherwise hold 10,000 open handles.",
				Language: "go",
			},
		},
		{
			Name:     "context-background-in-request-path",
			Severity: model.SeverityWarning,
			Message:  "context.Background()/TODO() used where a request context should flow",
			Advice:   "Thread the incoming request context through the call chain so cancellation and deadlines propagate. Use context.Background() only at the true root of a process.",
			Patterns: []string{`context\.(TODO|Background)\s*\(\s*\)`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `func (h *Handler) List(c *gin.Context) {
	// The background context is not cancelled when the client disconnects,
	// so this query keeps running after the request is gone.
	items, err := h.store.List(context.Background())
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, items)
}`,
				After: `func (h *Handler) List(c *gin.Context) {
	// The request context carries the client's deadline and cancellation.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	items, err := h.store.List(ctx)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, items)
}`,
				Note:     "Deriving from c.Request.Context() means a client disconnect cancels the work. This is the difference between a query that dies with the request and one that keeps holding a connection.",
				Language: "go",
			},
		},
		{
			// 按引用捕获循环变量的 `go func() {}()` 是 Go 的经典 bug。笼统的
			// 「循环里的 goroutine」噪音太大，所以这条用 RequireLoop 限定在循环里
			Name:        "goroutine-captures-loopvar",
			Severity:    model.SeverityWarning,
			Message:     "goroutine started inside a loop may capture the loop variable",
			Advice:      "Pass the loop variable as a parameter to the closure (`go func(v T) {...}(v)`). Go 1.22+ scopes per-iteration variables correctly, but passing explicitly documents the intent and is safe on every version.",
			Patterns:    []string{`^\s*go\s+func\s*\(\s*\)`},
			Paths:       goGlobs,
			Exclude:     goExclude(),
			RequireLoop: true,
			Fix: model.FixExample{
				Before: `func processAll(items []Item) {
	for _, item := range items {
		go func() {
			process(item) // captures the loop variable
		}()
	}
}`,
				After: `func processAll(items []Item) {
	for _, item := range items {
		go func(it Item) {
			process(it) // receives its own copy
		}(item)
	}
}`,
				Note:     "Go 1.22+ scopes loop variables per iteration, but passing explicitly documents the intent and is correct on every version.",
				Language: "go",
			},
		},
		{
			// 复制锁是真 bug，但锁通常声明为**字段**，那是正确的。限定到真正发生
			// 值复制的位置，函数参数或结构体字面量赋值。go vet 的 copylocks 才是
			// 权威版本，这条只抓最扎眼的那些
			Name:     "mutex-by-value",
			Severity: model.SeverityError,
			Message:  "a lock or WaitGroup is passed or stored by value",
			Advice:   "Locks must not be copied. Hold them behind a pointer field, and never pass the containing struct by value once it embeds one.",
			Patterns: []string{
				`func\s+\w+\s*\([^)]*\b\w*\s+sync\.(Mutex|RWMutex|WaitGroup)\b`,
			},
			Paths:   goGlobs,
			Exclude: goExclude(),
			Fix: model.FixExample{
				Before: `// The lock is copied with the struct, so Lock() protects nothing.
func update(cfg Config) {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	cfg.Value = 1
}`,
				After: `func update(cfg *Config) {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	cfg.Value = 1
}`,
				Note:     "Locks must never be copied. Pass the struct by pointer once it embeds one, and hold it behind a pointer field.",
				Language: "go",
			},
		},
		{
			// 循环里的 time.Sleep 是忙等或轮询的坏味道，不是错误，但值得在审查时提出
			Name:        "sleep-in-loop",
			Severity:    model.SeverityInfo,
			Message:     "time.Sleep inside a loop indicates polling",
			Advice:      "Prefer a ticker, a channel receive, or context-aware waiting. Polling loops with a fixed sleep add latency and are hard to cancel.",
			Patterns:    []string{`\btime\.Sleep\s*\(`},
			Paths:       goGlobs,
			Exclude:     goExclude(),
			RequireLoop: true,
			Fix: model.FixExample{
				Before: `for {
	if ready() {
		break
	}
	time.Sleep(time.Second) // polls forever, cannot be cancelled
}`,
				After: `ticker := time.NewTicker(time.Second)
defer ticker.Stop()
for {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ticker.C:
		if ready() {
			return nil
		}
	}
}`,
				Note:     "A ticker inside a select can be cancelled and does not drift. It also makes the wait interruptible.",
				Language: "go",
			},
		},

		// ------------------------------------------------------------------
		// 错误处理
		// ------------------------------------------------------------------
		{
			// 只标记丢弃了可能失败调用的错误。`_ = x.Close()` 和
			// `_ = fmt.Fprintf(...)` 很常见且通常是有意的，所以模式显式点名了
			// 有风险的几类
			Name:     "discarded-error",
			Severity: model.SeverityWarning,
			Message:  "error return from a fallible call is discarded",
			Advice:   "Assign the error and handle it, or document with a comment why the failure is safe to ignore.",
			Patterns: []string{
				`^\s*_\s*=\s*\w+\.(Exec|Query|QueryRow|Write|WriteString|Encode|Decode|Save|Create|Update|Delete|Open|Close|Flush|Sync|Marshal|Unmarshal|Scan|Parse|ReadFile|WriteFile)\w*\s*\(`,
				`^\s*_\s*,\s*_\s*:?=\s*\w+\.(Unmarshal|Decode|Read|Write|Encode|Scan)\w*\s*\(`,
			},
			Paths:   goGlobs,
			Exclude: goExclude(),
			Fix: model.FixExample{
				Before: `func save(db *sql.DB, u User) {
	// The write may fail silently.
	_ = db.QueryRow("INSERT INTO users(name) VALUES(?)", u.Name)
}`,
				After: `func save(db *sql.DB, u User) error {
	if _, err := db.QueryRow("INSERT INTO users(name) VALUES(?)", u.Name); err != nil {
		return fmt.Errorf("insert user %q: %w", u.Name, err)
	}
	return nil
}`,
				Note:     "A discarded error from a fallible call hides a real failure. Either handle it or document with a comment why the failure is safe to ignore.",
				Language: "go",
			},
		},
		{
			Name:     "error-shadowed-by-nil",
			Severity: model.SeverityWarning,
			Message:  "error variable is overwritten with nil before it is checked",
			Advice:   "Check the error before reusing the variable. Overwriting it hides the original failure and produces a nil dereference later.",
			Patterns: []string{`\berr\s*=\s*nil\b`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `var err error
data, err := fetch()
if err != nil {
	return err
}
err = nil // the check above already passed; this resets state for no reason
if err := save(data); err != nil {
	return err
}`,
				After: `data, err := fetch()
if err != nil {
	return fmt.Errorf("fetch: %w", err)
}
// Reuse err only when you intend to check it again immediately.
if err := save(data); err != nil {
	return fmt.Errorf("save: %w", err)
}`,
				Note:     "Overwriting err with nil before checking it hides the original failure. Prefer a fresh := in the next check.",
				Language: "go",
			},
		},

		// ------------------------------------------------------------------
		// 安全卫生
		//
		// gosec 才是权威且全面得多的那个。这几条规则的存在，是为了让一个什么
		// 工具都没装的用户也能抓到明显的情况
		// ------------------------------------------------------------------
		{
			Name:     "command-injection",
			Severity: model.SeverityCritical,
			Message:  "command executed through a shell or built by concatenation",
			Advice:   "Use exec.Command with discrete arguments. Never route a value through sh -c or build the command string by concatenation — both open a command injection path.",
			Patterns: []string{
				`exec\.Command\s*\(\s*"(sh|bash|cmd|powershell|zsh)"`,
				`exec\.Command\s*\([^)]*\+`,
			},
			Paths:   goGlobs,
			Exclude: goExclude(),
			Fix: model.FixExample{
				Before: `func run(userInput string) error {
	// The shell interprets whatever the user supplied.
	return exec.Command("sh", "-c", "convert "+userInput+" out.png").Run()
}`,
				After: `func run(userInput string) error {
	// Arguments are passed as a list; no shell is involved, so nothing
	// in userInput can be interpreted as a command.
	return exec.Command("convert", userInput, "out.png").Run()
}`,
				Note:     "Pass discrete arguments and never route input through sh -c. A shell turns a filename into a command.",
				Language: "go",
			},
		},
		{
			// 注意是精确到包的
			// crypto/rand 是**正确**的选择，绝不能标记，而安全场景下的 math/rand 才是
			Name:     "weak-random",
			Severity: model.SeverityError,
			Message:  "math/rand used where unpredictability matters",
			Advice:   "Use crypto/rand for tokens, nonces, identifiers and anything an attacker could predict. math/rand is fine only for jitter, sampling and test data.",
			// 只匹配导入。匹配 `rand.X(` 会连 crypto/rand 一起命中，而那是正确的
			// 选择，不能标记。引擎已经忽略注释和字符串字面量，像 gosec 的 G404
			// 描述那样的文档提及也因此被覆盖了
			Patterns: []string{`^\s*(\w+\s+)?"math/rand(/v2)?"\s*$`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `// Tokens from math/rand are predictable and can be guessed.
func newToken() string {
	return fmt.Sprintf("%d", rand.Int63())
}`,
				After: `func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil { // crypto/rand
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}`,
				Note:     "math/rand is fine for jitter, sampling and test data. Anything an attacker could predict needs crypto/rand.",
				Language: "go",
			},
		},
		{
			Name:     "broken-hash",
			Severity: model.SeverityError,
			Message:  "broken or deprecated hash algorithm",
			Advice:   "Use SHA-256/SHA-512 for integrity, AES-GCM or ChaCha20-Poly1305 for encryption, and bcrypt/argon2id for passwords. MD5 and SHA-1 are collision-broken.",
			Patterns: []string{
				`\b(md5|sha1)\.(New|Sum|New512|Sum512)\s*\(`,
				`"crypto/(md5|sha1|des|rc4)"`,
			},
			Paths:   goGlobs,
			Exclude: goExclude(),
			Fix: model.FixExample{
				Before: `func checksum(data []byte) string {
	sum := md5.Sum(data) // collision-broken
	return hex.EncodeToString(sum[:])
}`,
				After: `func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}`,
				Note:     "MD5 and SHA-1 are collision-broken. For passwords use bcrypt or argon2id — SHA-256 is also too fast for that purpose.",
				Language: "go",
			},
		},
		{
			Name:     "insecure-tls",
			Severity: model.SeverityCritical,
			Message:  "TLS certificate verification disabled",
			Advice:   "Remove InsecureSkipVerify. For a private CA, inject a custom RootCAs pool instead of disabling verification.",
			Patterns: []string{`InsecureSkipVerify\s*:\s*true`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `client := &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}`,
				After: `// A private CA needs its certificate in the pool, not verification disabled.
pool := x509.NewCertPool()
pool.AppendCertsFromPEM(caPEM)

client := &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	},
}`,
				Note:     "Disabling verification accepts any certificate, including an attacker's. Configure RootCAs for a private CA instead.",
				Language: "go",
			},
		},
		{
			Name:     "hardcoded-credential",
			Severity: model.SeverityCritical,
			Message:  "possible hard-coded credential",
			Advice:   "Load secrets from the environment or a secret manager, and rotate anything already committed.",
			// 一串占位符，或明显是样例的取值，会给 changeme、test 这类拼写留出
			// 余地，测试夹具因此不会被读成泄露
			Patterns: []string{
				`(?i)\b(password|passwd|secret|api[_-]?key|apikey|access[_-]?key|private[_-]?key|auth[_-]?token)\s*[:=]\s*"[^"\s]{8,}"`,
			},
			Paths:   goGlobs,
			Exclude: append(goExclude(), "**/config*.go", "**/*_example.go"),
			Fix: model.FixExample{
				Before: `const apiKey = "hardcoded-api-key-value"

func call() {
	req.Header.Set("Authorization", "Bearer "+apiKey)
}`,
				After: `func call() error {
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return errors.New("API_KEY is not set")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return nil
}`,
				Note:     "Load secrets from the environment or a secret manager, and rotate anything already committed — it stays in the git history.",
				Language: "go",
			},
		},
		{
			Name:     "sql-injection",
			Severity: model.SeverityCritical,
			Message:  "SQL statement built by concatenation or Sprintf",
			Advice:   "Use parameterised queries and pass values as bind arguments. Never interpolate a value into SQL text.",
			Patterns: []string{
				`(?i)"(select|insert\s+into|update|delete\s+from)[^"]*"\s*\+`,
				`(?i)fmt\.Sprintf\s*\(\s*"(select|insert|update|delete)`,
			},
			Paths:   goGlobs,
			Exclude: goExclude(),
			Fix: model.FixExample{
				Before: `query := "SELECT * FROM users WHERE name = '" + name + "'"
rows, err := db.Query(query)`,
				After: `query := "SELECT * FROM users WHERE name = ?"
rows, err := db.Query(query, name)`,
				Note:     "Values go in as bind arguments, never as SQL text. The placeholder differs by driver: ? for MySQL/SQLite, $1 for PostgreSQL.",
				Language: "go",
			},
		},
		{
			Name:     "unsafe-file-permissions",
			Severity: model.SeverityWarning,
			Message:  "world-writable file or directory permission",
			Advice:   "Use 0600 for files holding data and 0755 for executables and directories. 0777 and 0666 expose the file to every local user.",
			Patterns: []string{`0o?777\b`, `0o?666\b`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `// 0777 lets any local user write to this file.
if err := os.WriteFile("secrets.json", data, 0o777); err != nil {
	return err
}`,
				After: `// 0600: readable and writable only by the owner.
if err := os.WriteFile("secrets.json", data, 0o600); err != nil {
	return err
}`,
				Note:     "Use 0600 for data files and 0755 for executables and directories. 0666/0777 exposes the file to every local account.",
				Language: "go",
			},
		},
		{
			Name:     "unsafe-pointer",
			Severity: model.SeverityWarning,
			Message:  "unsafe package usage",
			Advice:   "unsafe bypasses the type system and is not covered by the compatibility promise. Confirm there is no safer alternative and document the invariant being relied on.",
			Patterns: []string{`\bunsafe\.(Pointer|Sizeof|Offsetof|String|StringData|Slice)\b`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `// Relies on struct layout, which is not part of the compatibility promise.
p := (*int)(unsafe.Pointer(uintptr(unsafe.Pointer(&s)) + unsafe.Offsetof(s.Field)))`,
				After: `// Use the field directly; the compiler is already optimal here.
p := &s.Field`,
				Note:     "unsafe bypasses the type system and is not covered by Go's compatibility guarantee. If it is genuinely required, document the invariant being relied on.",
				Language: "go",
			},
		},

		// ------------------------------------------------------------------
		// Wails 专项
		//
		// 这几条承载通用 linter 不具备的知识，也正是 Wails 分析器要与
		// golangci-lint 并存的原因
		// ------------------------------------------------------------------
		{
			// 每次迭代都发送事件会淹没 JS 桥。限定在循环里，批量发送的正确用法
			// 因此不会被标记
			Name:        "wails-events-emit-in-loop",
			Severity:    model.SeverityWarning,
			Message:     "runtime.EventsEmit called inside a loop",
			Advice:      "Accumulate the payload and emit once after the loop. Per-iteration events queue up on the JS bridge and stall the UI on large data sets.",
			Patterns:    []string{`runtime\.EventsEmit\s*\(`},
			Paths:       goGlobs,
			Exclude:     goExclude(),
			RequireLoop: true,
			Fix: model.FixExample{
				Before: `func (a *App) Stream(items []Item) {
	for _, item := range items {
		runtime.EventsEmit(a.ctx, "item", item)
	}
}`,
				After: `func (a *App) Stream(items []Item) {
	// One emission per batch: the JS bridge is the bottleneck, not the loop.
	runtime.EventsEmit(a.ctx, "items", items)
}`,
				Note:     "Emitting per iteration queues one bridge call per item and stalls the UI on large data sets. Batch the payload.",
				Language: "go",
			},
		},
		{
			Name:     "wails-dialog-unchecked",
			Severity: model.SeverityWarning,
			Message:  "runtime dialog result is discarded",
			Advice:   "Dialog calls return (result, error). Check the error and honour the user's answer — treating a cancelled dialog as confirmation is a real bug.",
			Patterns: []string{`^\s*runtime\.(MessageDialog|QuestionDialog|ErrorDialog|InfoDialog|WarningDialog|OpenFileDialog|SaveFileDialog|OpenDirectoryDialog)\s*\(`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `func (a *App) ConfirmDelete() {
	runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Title:   "Delete?",
		Message: "This cannot be undone.",
	})
	// The user may have clicked Cancel: a cancelled dialog returns "".
	a.delete()
}`,
				After: `func (a *App) ConfirmDelete() error {
	answer, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Title:   "Delete?",
		Message: "This cannot be undone.",
	})
	if err != nil {
		return fmt.Errorf("show dialog: %w", err)
	}
	if answer != "Yes" {
		return nil // the user declined
	}
	a.delete()
	return nil
}`,
				Note:     "Dialogs return (result, error). Treating a cancelled dialog as confirmation is a real bug, not a style issue.",
				Language: "go",
			},
		},
		{
			Name:     "wails-browser-open-url",
			Severity: model.SeverityWarning,
			Message:  "runtime.BrowserOpenURL opens an external URL",
			Advice:   "Parse the URL and allow only http/https. file: and custom schemes reach local handlers, which turns an attacker-supplied link into local code execution.",
			Patterns: []string{`runtime\.BrowserOpenURL\s*\(`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `func (a *App) OpenLink(raw string) {
	runtime.BrowserOpenURL(a.ctx, raw) // raw may be file:// or a custom scheme
}`,
				After: `func (a *App) OpenLink(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("refusing to open scheme %q", parsed.Scheme)
	}
	runtime.BrowserOpenURL(a.ctx, parsed.String())
	return nil
}`,
				Note:     "file:// and custom schemes reach local handlers, so an attacker-supplied link becomes local code execution. Allow only http and https.",
				Language: "go",
			},
		},
		{
			Name:     "wails-frontend-hardcoded-path",
			Severity: model.SeverityInfo,
			Message:  "frontend asset path hard-coded in Go",
			Advice:   "Wails 2 serves the frontend through assetserver. Use the embedded FS rather than a filesystem path, otherwise production builds cannot find the assets.",
			Patterns: []string{`"frontend/(dist|src)/`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `// Breaks in a production build: the assets are embedded, not on disk.
f, err := os.Open("frontend/dist/index.html")`,
				After: `// Wails 2 serves the frontend through assetserver.
//go:embed all:frontend/dist
var assets embed.FS

func indexHTML() ([]byte, error) {
	return assets.ReadFile("frontend/dist/index.html")
}`,
				Note:     "Use the embedded filesystem so the production build can find its assets.",
				Language: "go",
			},
		},
		{
			Name:     "wails-wailsjson-legacy-fields",
			Severity: model.SeverityInfo,
			Message:  "wails.json appears to use Wails v1 field names",
			Advice:   "Wails 2 renamed several fields. Compare against a fresh `wails init` output and update the build commands.",
			Patterns: []string{`"(frontend:dir|frontend:install|frontend:build|bridge|wails:version)"\s*:`},
			Paths:    []string{"**/wails.json"},
			Fix: model.FixExample{
				Before: `{
  "frontend:dir": "frontend",
  "wails:version": "1"
}`,
				After: `{
  "name": "my-app",
  "frontend:install": "npm install",
  "frontend:build": "npm run build"
}`,
				Note:     "Wails 2 renamed several fields. Compare against a fresh `wails init` output.",
				Language: "go",
			},
		},

		// ------------------------------------------------------------------
		// 可维护性
		// ------------------------------------------------------------------
		{
			Name:     "unresolved-marker",
			Severity: model.SeverityInfo,
			Message:  "TODO/FIXME/HACK marker left in the source",
			Advice:   "Link the marker to a tracked issue, or resolve it before merging. Markers without an owner tend to outlive the context that created them.",
			Patterns: []string{`(?i)//\s*(TODO|FIXME|HACK|XXX)\b`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `// TODO: handle the timeout case
func fetch(url string) (*Response, error) {`,
				After: `// fetch returns ErrTimeout when the server does not respond within the
// configured deadline. See issue #482 for the retry policy.
func fetch(url string) (*Response, error) {`,
				Note:     "Link the marker to a tracked issue, or resolve it. Markers without an owner outlive the context that created them.",
				Language: "go",
			},
		},
		{
			// 被注释掉的一块代码是审查者无法评估的累赘。引擎已经忽略 `//` 注释行，
			// 所以这条规则要靠显式匹配标记文本来主动启用
			Name:     "commented-out-code",
			Severity: model.SeverityInfo,
			Message:  "large block of commented-out code",
			Advice:   "Delete it. Version control already remembers it, and commented-out code drifts out of sync with the APIs it references.",
			Patterns: []string{`(?i)//\s*(disable|removed|deprecated|old version|previous implementation)\b`},
			Paths:    goGlobs,
			Exclude:  goExclude(),
			Fix: model.FixExample{
				Before: `// old version:
// func parseV1(data []byte) (Config, error) {
//     return Config{Version: 1}, nil
// }
func parseV2(data []byte) (Config, error) {`,
				After:    `func parseV2(data []byte) (Config, error) {`,
				Note:     "Version control already remembers it. Commented-out code drifts out of sync with the APIs it references.",
				Language: "go",
			},
		},
		{
			Name:     "debug-output-left",
			Severity: model.SeverityInfo,
			Message:  "debug print left in the source",
			Advice:   "Use the project's structured logger, or remove the print. Bare prints bypass log levels and cannot be filtered in production.",
			Patterns: []string{`^\s*fmt\.Print(ln|f)?\s*\(`},
			Paths:    goGlobs,
			Exclude:  append(goExclude(), "**/main.go", "**/cmd/**"),
			Fix: model.FixExample{
				Before: `func process(u User) {
	fmt.Println("processing user", u.ID) // bypasses log levels
	doWork(u)
}`,
				After: `func process(u User) {
	logger.Info("processing user", "user_id", u.ID)
	doWork(u)
}`,
				Note:     "A bare print cannot be filtered by level and is not structured. Use the project logger.",
				Language: "go",
			},
		},
	}
}

// BuiltinRulesAsModels 把规则库转成可持久化的规则记录
func BuiltinRulesAsModels() []model.Rule {
	defs := BuiltinRules()
	out := make([]model.Rule, 0, len(defs))
	for _, d := range defs {
		out = append(out, model.Rule{
			ID:           model.NewID("rul"),
			Name:         d.Name,
			Analyzer:     "custom",
			Severity:     d.Severity,
			Message:      d.Message,
			Advice:       d.Advice,
			Patterns:     d.Patterns,
			Paths:        d.Paths,
			Exclude:      d.Exclude,
			Fix:          d.Fix,
			RequireLoop:  d.RequireLoop,
			ForbidInLoop: d.ForbidInLoop,
			Enabled:      true,
			Builtin:      true,
		})
	}
	return out
}
