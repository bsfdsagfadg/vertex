package cli

import (
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rivo/uniseg"
)

// ReqState 记录单个活跃请求的状态。
type ReqState struct {
	ID         string
	Model      string
	State      string
	Color      string // ANSI 颜色代码
	WinnerNode string // 胜出的代理节点
	Detail     string
	StartTime  time.Time
}

const (
	minTermWidth = 60 // 最小终端宽度
	maxLogs      = 10 // 日志窗口固定显示的行数
)

var (
	//nolint:gochecknoglobals // Internal CLI state
	mu sync.Mutex
	//nolint:gochecknoglobals // Internal CLI state
	activeReqs = make(map[string]*ReqState)
	//nolint:gochecknoglobals // Internal CLI state
	enabled bool
	//nolint:gochecknoglobals // Internal CLI state
	osStdout = os.Stdout

	// 环形日志缓冲区，存放原始文本（渲染时动态换行）
	//nolint:gochecknoglobals // Internal CLI state
	logBuffer = []string{}

	// 软件版本与平台常驻信息
	//nolint:gochecknoglobals // Internal CLI state
	appVersion = "dev"
	//nolint:gochecknoglobals // Internal CLI state
	buildInfo = "Build: unknown / unknown"
	//nolint:gochecknoglobals // Internal CLI state
	platformInfo = "Platform: unknown"

	//nolint:gochecknoglobals // Internal CLI state
	spinners = []rune(`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`)
	//nolint:gochecknoglobals // Internal CLI state
	spinnerIdx int

	// 动态终端尺寸
	//nolint:gochecknoglobals // Internal CLI state
	terminalWidth int = 80

	// 记录上一帧渲染的实际行数，用于精准擦除
	//nolint:gochecknoglobals // Internal CLI state
	lastHeight int

	//nolint:gochecknoglobals // Internal CLI state
	needsRedraw bool
)

// SetAppInfo 供 main.go 初始化时传入版本及编译属性。
func SetAppInfo(ver, commit, bTime, goos, goarch string) {
	mu.Lock()
	defer mu.Unlock()
	appVersion = ver
	buildInfo = fmt.Sprintf("Build: %s / %s", commit, bTime)
	platformInfo = fmt.Sprintf("Platform: %s/%s", goos, goarch)

	logBuffer = append(logBuffer,
		fmt.Sprintf("[vproxy] 启动成功: Version=%s, Commit=%s, Built=%s", ver, commit, bTime))
	logBuffer = append(logBuffer,
		fmt.Sprintf("[vproxy] 运行平台: %s/%s", goos, goarch))
}

// getTerminalWidth 检测终端当前宽度，至少返回 minTermWidth。
func getTerminalWidth() int {
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if w, err := strconv.Atoi(cols); err == nil && w > 0 {
			if w < minTermWidth {
				return minTermWidth
			}
			return w
		}
	}
	if w := getTerminalWidthOS(); w > 0 {
		if w < minTermWidth {
			return minTermWidth
		}
		return w
	}
	return minTermWidth
}

// boxWidth 返回外层边框的总目标宽度。
func boxWidth() int { return terminalWidth }

// boxInnerWidth 返回边框内部可填充文本的净宽度。
func boxInnerWidth() int {
	w := terminalWidth - 4
	if w < minTermWidth-4 {
		w = minTermWidth - 4
	}
	return w
}

// ─── 字符宽度计算 ───

func stringWidth(s string) int {
	return uniseg.StringWidth(s)
}

func padOrTrunc(s string, maxCol int) string {
	w := uniseg.StringWidth(s)
	if w <= maxCol {
		return s + strings.Repeat(" ", maxCol-w)
	}
	if maxCol <= 2 {
		return ".."
	}
	var sb strings.Builder
	cur := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		gw := g.Width()
		if cur+gw > maxCol-2 {
			break
		}
		sb.WriteString(g.Str())
		cur += gw
	}
	sb.WriteString("..")
	cur += 2
	if cur < maxCol {
		sb.WriteString(strings.Repeat(" ", maxCol-cur))
	}
	return sb.String()
}

func wordWrap(text string, maxCol int) []string {
	if maxCol <= 0 {
		return []string{text}
	}
	w := uniseg.StringWidth(text)
	if w <= maxCol {
		return []string{text + strings.Repeat(" ", maxCol-w)}
	}

	g := uniseg.NewGraphemes(text)
	type graphemeItem struct {
		str   string
		width int
	}
	var items []graphemeItem
	for g.Next() {
		items = append(items, graphemeItem{
			str:   g.Str(),
			width: g.Width(),
		})
	}

	var lines []string
	var cur strings.Builder
	curW := 0

	i := 0
	for i < len(items) {
		item := items[i]
		gw := item.width

		if item.str == " " && curW > 0 {
			nextW := 0
			j := i + 1
			for j < len(items) && items[j].str != " " {
				nextW += items[j].width
				j++
			}
			if curW+1+nextW > maxCol {
				lines = append(lines, cur.String()+strings.Repeat(" ", maxCol-curW))
				cur.Reset()
				curW = 0
				i++
				continue
			}
		}

		if curW+gw > maxCol {
			lines = append(lines, cur.String()+strings.Repeat(" ", maxCol-curW))
			cur.Reset()
			curW = 0
		}

		cur.WriteString(item.str)
		curW += gw
		i++
	}

	if cur.Len() > 0 {
		lines = append(lines, cur.String()+strings.Repeat(" ", maxCol-curW))
	}
	if len(lines) == 0 {
		lines = []string{strings.Repeat(" ", maxCol)}
	}
	return lines
}

func dashBar(w int) string {
	if w <= 0 {
		return ""
	}
	return strings.Repeat("─", w)
}

// ─── 日志拦截 ───

//nolint:gochecknoglobals // Log writer for interceptor
var additionalLogWriter io.Writer

type logInterceptor struct{}

func (logInterceptor) Write(p []byte) (int, error) {
	if additionalLogWriter != nil {
		_, _ = additionalLogWriter.Write(p)
	}
	if enabled {
		addLogLine(string(p))
	} else {
		_, _ = os.Stderr.Write(p)
	}
	return len(p), nil
}

func addLogLine(text string) {
	lines := strings.Split(text, "\n")
	mu.Lock()
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		logBuffer = append(logBuffer, line)
	}
	for len(logBuffer) > maxLogs*3 {
		logBuffer = logBuffer[1:]
	}
	mu.Unlock()

	if enabled {
		mu.Lock()
		drawTUI()
		mu.Unlock()
	}
}

// ─── 公共 API ───

func InitTracker(fileLogger io.Writer) {
	additionalLogWriter = fileLogger
	fileInfo, err := osStdout.Stat()
	if err == nil && (fileInfo.Mode()&os.ModeCharDevice) != 0 {
		enabled = true
		terminalWidth = getTerminalWidth()
		needsRedraw = true

		log.SetOutput(logInterceptor{})

		onResizeOS(func() {
			mu.Lock()
			newW := getTerminalWidth()
			if newW != terminalWidth {
				terminalWidth = newW
				needsRedraw = true
			}
			mu.Unlock()
		})

		go func() {
			ticker := time.NewTicker(120 * time.Millisecond)
			defer ticker.Stop()
			for range ticker.C {
				mu.Lock()
				spinnerIdx = (spinnerIdx + 1) % len(spinners)
				if envW := getTerminalWidth(); envW != terminalWidth {
					terminalWidth = envW
					needsRedraw = true
				}
				if needsRedraw || len(activeReqs) > 0 {
					drawTUI()
				}
				mu.Unlock()
			}
		}()

		printBanner()
	} else {
		// Non-TTY environment (e.g. Docker without -it), TUI is disabled.
		// We still need to write logs to both os.Stderr and the fileLogger.
		if fileLogger != nil {
			log.SetOutput(io.MultiWriter(os.Stderr, fileLogger))
		}
	}
}

func StartReq(id string) {
	mu.Lock()
	defer mu.Unlock()
	activeReqs[id] = &ReqState{ //nolint:exhaustruct
		ID:        id,
		Model:     "连接中...",
		State:     "🔗 连接中",
		Color:     "\033[90m",
		StartTime: time.Now(),
	}
	if enabled {
		drawTUI()
	}
}

func UpdateReqModel(id, model string) {
	mu.Lock()
	defer mu.Unlock()
	if req, ok := activeReqs[id]; ok {
		req.Model = model
	}
}

func UpdateReqState(id, state, color, detail string) {
	mu.Lock()
	defer mu.Unlock()
	if req, ok := activeReqs[id]; ok {
		req.State = state
		req.Color = color
		if detail != "" {
			req.Detail = detail
		}
	}
}

func UpdateReqWinner(id, nodeName string) {
	mu.Lock()
	defer mu.Unlock()
	if req, ok := activeReqs[id]; ok {
		req.WinnerNode = nodeName
	}
}

func FinishReq(id string) {
	mu.Lock()
	defer mu.Unlock()
	delete(activeReqs, id)
	if enabled {
		drawTUI()
	}
}

// printBanner 在 TUI 启动时一次性打印顶部版权/版本面板（黄色框），
// 不会在后续的 drawTUI 帧刷新中被擦除或重绘。
func printBanner() {
	bw := boxWidth()
	biw := boxInnerWidth()

	var sb strings.Builder

	bottomBorder := func() string {
		return "╰" + dashBar(bw-2) + "╯\n"
	}

	prefix := "╭── 📢 Vertex AI Proxy "
	pw := stringWidth(prefix)
	d := bw - pw - 1
	if d < 0 {
		d = 0
	}
	fmt.Fprintf(&sb, "\033[33m%s%s╮\033[0m\n", prefix, dashBar(d))

	line1 := fmt.Sprintf("Version: %s | %s", appVersion, platformInfo)
	fmt.Fprintf(&sb, "\033[33m│\033[0m %s \033[33m│\033[0m\n", padOrTrunc(line1, biw))
	fmt.Fprintf(&sb, "\033[33m│\033[0m %s \033[33m│\033[0m\n", padOrTrunc(buildInfo, biw))

	warn := "⚠️  本软件完全免费！付费即被骗，请退款。"
	fmt.Fprintf(&sb, "\033[33m│\033[0m \033[31m%s\033[0m \033[33m│\033[0m\n", padOrTrunc(warn, biw))
	sb.WriteString("\033[33m" + bottomBorder() + "\033[0m")

	fmt.Fprint(osStdout, sb.String())
}

// ─── 渲染核心 ───

func drawTUI() {
	needsRedraw = false

	bw := boxWidth()
	biw := boxInnerWidth()

	var sb strings.Builder

	// 底边生成函数：确保总长度严格等于 bw
	bottomBorder := func() string {
		return "╰" + dashBar(bw-2) + "╯\n"
	}

	// ── 2. 最近系统日志 (蓝色框) ──
	{
		prefix := "╭── 📝 最近系统日志 "
		pw := stringWidth(prefix)
		d := bw - pw - 1
		if d < 0 {
			d = 0
		}
		fmt.Fprintf(&sb, "\033[36m%s%s╮\033[0m\n", prefix, dashBar(d))

		var visualLines []string
		for i := len(logBuffer) - 1; i >= 0 && len(visualLines) < maxLogs*5; i-- {
			wrapped := wordWrap(logBuffer[i], biw)
			visualLines = append(wrapped, visualLines...)
		}
		if len(visualLines) > maxLogs {
			visualLines = visualLines[len(visualLines)-maxLogs:]
		}
		for i := 0; i < maxLogs; i++ {
			if i < len(visualLines) {
				fmt.Fprintf(&sb, "\033[36m│\033[0m %s \033[36m│\033[0m\n", visualLines[i])
			} else {
				fmt.Fprintf(&sb, "\033[36m│\033[0m %s \033[36m│\033[0m\n", strings.Repeat(" ", biw))
			}
		}
		sb.WriteString("\033[36m" + bottomBorder() + "\033[0m")
	}

	// ── 3. 请求追踪器 (青色框) ──
	if len(activeReqs) > 0 {
		var reqs []*ReqState
		for _, r := range activeReqs {
			reqs = append(reqs, r)
		}
		sort.Slice(reqs, func(i, j int) bool {
			return reqs[i].StartTime.Before(reqs[j].StartTime)
		})

		prefix := "╭── 🚀 请求追踪器 "
		pw := stringWidth(prefix)
		d := bw - pw - 1
		if d < 0 {
			d = 0
		}
		fmt.Fprintf(&sb, "\033[36m%s%s╮\033[0m\n", prefix, dashBar(d))

		// 字符间距计算：表头包含 6 个边框字符和 10 个内边距空格，总计 16 像素固定开销
		const separatorOverhead = 16
		totalColsWidth := bw - separatorOverhead

		idW := 8
		timeW := 6
		remaining := totalColsWidth - idW - timeW
		if remaining < 20 {
			remaining = 20
		}
		modelW := remaining * 25 / 100
		if modelW < 8 {
			modelW = 8
		}
		stateW := remaining * 20 / 100
		if stateW < 6 {
			stateW = 6
		}
		detailW := remaining - modelW - stateW
		if detailW < 6 {
			detailW = 6
		}

		// 表头 (每列左右两侧必须带 1 个空格边距)
		fmt.Fprintf(&sb, "\033[36m│\033[0m %-*s \033[36m│\033[0m %-*s \033[36m│\033[0m %-*s \033[36m│\033[0m %-*s \033[36m│\033[0m %-*s \033[36m│\033[0m\n",
			idW, "ID", modelW, "Model", stateW, "State", timeW, "Time", detailW, "Details")

		// 分隔线：宽度加上两边的内边距空格(各+2)从而保持对齐
		sep := fmt.Sprintf("\033[36m├%s┼%s┼%s┼%s┼%s┤\033[0m\n",
			dashBar(idW+2), dashBar(modelW+2), dashBar(stateW+2), dashBar(timeW+2), dashBar(detailW+2))
		sb.WriteString(sep)

		for _, r := range reqs {
			elapsed := time.Since(r.StartTime).Seconds()
			idVal := r.ID
			if len(idVal) > idW-2 {
				idVal = idVal[:idW-2]
			}

			detailStr := r.Detail
			if r.WinnerNode != "" {
				detailStr = "🏆 " + r.WinnerNode
				if r.Detail != "" {
					detailStr += " | " + r.Detail
				}
			}

			idCol := padOrTrunc(idVal, idW-2)
			modelCol := padOrTrunc(r.Model, modelW)
			stateCol := padOrTrunc(r.State, stateW)
			timeCol := fmt.Sprintf("%4.1fs", elapsed)
			timeCol = padOrTrunc(timeCol, timeW)
			detailCol := padOrTrunc(detailStr, detailW)

			fmt.Fprintf(&sb,
				"\033[36m│\033[0m \033[36m%c\033[0m %s \033[36m│\033[0m %s \033[36m│\033[0m %s%s\033[0m \033[36m│\033[0m %s \033[36m│\033[0m \033[90m%s\033[0m \033[36m│\033[0m\n",
				spinners[spinnerIdx], idCol, modelCol, r.Color, stateCol, timeCol, detailCol,
			)
		}
		sb.WriteString("\033[36m" + bottomBorder() + "\033[0m")
	}

	tuiContent := sb.String()
	newHeight := strings.Count(tuiContent, "\n")

	// ─── 使用原生底层 ANSI 擦除上一帧并重绘 ───
	if lastHeight > 0 {
		// \033[%dF 向上移动 N 行至行首
		// \033[J 清除当前光标到屏幕末尾的所有字符（完美根治重影）
		_, _ = fmt.Fprintf(osStdout, "\033[%dA\r\033[J", lastHeight)
	}
	_, _ = fmt.Fprint(osStdout, tuiContent)

	lastHeight = newHeight
}
